package fishly

import (
	"io"
	"sync"
	"sync/atomic"
	
	"fmt"
	"strings"
)

const (
	ioChannelCapacity = 20
)

type OutputTokenType int
const (
	// End of file -- stops channels
	EOF = iota
	
	// Any other token containing text and/or value 
	Value
	
	// Object/Object start/end tags 
	ObjectStart
	ArrayStart
	ObjectEnd
)

type Token struct {
	// For special tokens like EOF, NewLine
	TokenType OutputTokenType
	
	// Formatted value for Text and Attribute
	Text string
	
	// Raw value for filters and 
	RawValue interface{}
	
	// Tag for tagged output (such as table)
	Tag string
}

// TokenPath is an accumulator for current token path
type TokenPath struct {
	path []string
	
	lastValue bool
	objectEnded bool
}

type IOChannel chan Token

type IOHandle struct {
	// Channels to send commands output. First is the channel for command. 
	// Last channel is for writing to shell, command line or file. The rest
	// are filters, converters, etc
	channels []IOChannel 
	
	// Sink is a last entity in the output chain
	sink io.WriteCloser
	
	// Wait group for all pipes and formatters
	wg sync.WaitGroup
	
	// Last error returned by I/O handlers
	err error
	hasError int32
}

// IOPipe takes two channels and filters/updates tokens
type IOPipe interface {
	Handler
	
	Run(ctx *Context, rq *IOPipeRequest)
}
type IOPipeRequest struct {
	Options interface{}
	
	Input IOChannel
	Output IOChannel
	
	pipe IOPipe
	
	rq *Request
}

// IOFormatter handles channels and dumps raw data into sinks
type IOFormatter interface {
	Handler
	Run(ctx *Context, rq *IOFormatterRequest) 
}
type IOFormatterRequest struct {
	Options interface{}
	
	Input IOChannel
	Output io.WriteCloser
	
	formatter IOFormatter
	
	rq *Request
}

// IOSink creates final output 
type IOSink interface {
	Handler
	
	IsTTY(*Context) bool
	
	NewSink(ctx *Context, rq *IOSinkRequest) (io.WriteCloser, error)
}
type IOSinkRequest struct {
	Options interface{}
	
	sink IOSink
}

func (rq *Request) StartOutput(ctx *Context, needPager bool) (*IOHandle, error) {
	ioh := new(IOHandle)
	
	// Spawn default formatter & sink request if none specified
	if rq.formatterRq == nil {
		if rq.sinkRq == nil || rq.sinkRq.sink.IsTTY(ctx) {
			rq.createIOFormatterRequest(ctx.cfg.DefaultRichTextFormatter)
		} else {
			rq.createIOFormatterRequest(ctx.cfg.DefaultTextFormatter)
		}
	}
	if rq.sinkRq == nil {
		if needPager {
			rq.createIOSinkRequest(ctx.cfg.DefaultPagerSink)
		} else {
			rq.createIOSinkRequest(ctx.cfg.DefaultSink)
		}
	}
	
	// Create sink or fail
	var err error
	ioh.sink, err = rq.sinkRq.sink.NewSink(ctx, rq.sinkRq)
	if err != nil {
		return nil, err
	}
	
	// Setup wait group for all pipe and formatter goroutine
	ioh.wg.Add(len(rq.pipeRqs) + 1)
	
	// Spawn channel pairs and run goroutines
	for pipeIndex := -1 ; pipeIndex < len(rq.pipeRqs) ; pipeIndex++ {
		ioh.channels = append(ioh.channels, make(IOChannel, ioChannelCapacity))
		if pipeIndex >= 0 {
			pipeRq := &rq.pipeRqs[pipeIndex]
			
			pipeRq.Input = ioh.channels[pipeIndex]
			pipeRq.Output = ioh.channels[pipeIndex+1]
			
			go pipeRq.pipe.Run(ctx, pipeRq)
		}
	}
	
	rq.formatterRq.Input = ioh.channels[len(ioh.channels)-1]
	rq.formatterRq.Output = ioh.sink
	go rq.formatterRq.formatter.Run(ctx, rq.formatterRq)
	
	rq.ioh = ioh
	return ioh, nil
}

// Closes front channel and waits for all wait groups to finish
func (ioh *IOHandle) CloseOutput() {
	ioh.closeChannel(ioh.channels[0])
	close(ioh.channels[0])
	
	ioh.wg.Wait()
}

func (ioh *IOHandle) setError(err error) {
	if err == nil {
		return
	}
	if atomic.SwapInt32(&ioh.hasError, 1) == 0 {
		ioh.err = err 
	}
}

// Closes output channel and notifies main thread
func (rq *IOPipeRequest) Close(err error) {
	rq.rq.ioh.setError(err)
	rq.rq.ioh.drainChannel(rq.Input)
	
	rq.rq.ioh.closeChannel(rq.Output)
	close(rq.Output)
	
	rq.rq.ioh.wg.Done()
}

// Closes sink and notifies main thread
func (rq* IOFormatterRequest) Close(err error) {
	rq.rq.ioh.setError(err)
	rq.rq.ioh.drainChannel(rq.Input)
	
	sinkError := rq.Output.Close()
	rq.rq.ioh.setError(sinkError)
	
	rq.rq.ioh.wg.Done()
}

func (rq* Request) createIOSinkRequest(sink IOSink) {
	rq.sinkRq = &IOSinkRequest{
		sink: sink,
		Options: sink.NewOptions(),			
	}
}
func (rq* Request) createIOFormatterRequest(formatter IOFormatter) {
	rq.formatterRq = &IOFormatterRequest{
		formatter: formatter,
		Options: formatter.NewOptions(),	
		rq: rq,		
	}
}
func (rq* Request) addIOPipeRequest(pipe IOPipe) {
	rq.pipeRqs = append(rq.pipeRqs, IOPipeRequest{
		pipe: pipe,
		Options: pipe.NewOptions(),
		rq: rq,			
	})
}

// Writes value that was pre-formatted
func (ioh *IOHandle) WriteFormattedValue(tag, text string, value interface{}) {
	if atomic.LoadInt32(&ioh.hasError) == 1 {
		return
	}
	
	ioh.channels[0] <- Token {
		TokenType: Value,
		Tag: tag,
		Text: text,
		RawValue: value,
	}
}

// Writes value and attempts to format it 
func (ioh *IOHandle) WriteRawValue(tag string, value interface{}) {
	text := fmt.Sprintf("%v", value)
	ioh.WriteFormattedValue(tag, text, value)
}

// Writes string as a value
func (ioh *IOHandle) WriteString(tag, text string) {
	ioh.WriteFormattedValue(tag, text, text)
}

// Writes untagged text
func (ioh *IOHandle) WriteText(text string) {
	ioh.WriteFormattedValue("", text, text)
}

func (ioh *IOHandle) StartObject(name string) {
	if atomic.LoadInt32(&ioh.hasError) == 1 {
		return
	}
	
	ioh.channels[0] <- Token {
		TokenType: ObjectStart,
		Tag: name,
	}
}

func (ioh *IOHandle) StartArray(name string) {
	if atomic.LoadInt32(&ioh.hasError) == 1 {
		return
	}
	
	ioh.channels[0] <- Token {
		TokenType: ArrayStart,
		Tag: name,
	}
}

func (ioh *IOHandle) EndObject() {
	if atomic.LoadInt32(&ioh.hasError) == 1 {
		return
	}
	
	ioh.channels[0] <- Token {
		TokenType: ObjectEnd,
	}
}

func (ioh *IOHandle) EndArray() {
	ioh.EndObject()
}

func (ioh *IOHandle) drainChannel(channel IOChannel) {
	// If we're closing due to error or some handler exited prematurely,
	// drain its input so sender won't hang
	for {
		select {
			case t := <- channel:
				if t.TokenType == EOF {
					return
				}
			default:
				return
		}
	}
}

func (ioh *IOHandle) closeChannel(channel IOChannel) {
	if atomic.LoadInt32(&ioh.hasError) == 1 {
		return
	}
	
	channel <- Token {
		TokenType: EOF,
	}
}

// Token path helper functions -- work for filters and formatters
// so they determine current output context

func (path *TokenPath) UpdatePath(token Token) {
	if path.lastValue {
		if token.TokenType == Value {
			// Fast path for one value going after another
			path.path[len(path.path)-1] = token.Tag
			return
		}
		
		path.path = path.path[:len(path.path)-1]
	}
	if path.objectEnded && len(path.path) > 0 {
		path.path = path.path[:len(path.path)-1]
		path.objectEnded = false
	}
	
	switch token.TokenType {
		case EOF:
			path.path = []string{}
		case ObjectEnd:
			// We are already stripped last component (value), but
			// the path should point to the current object, so 
			// we delay removing of path component until next time
			path.objectEnded = true
			path.lastValue = false
		case ObjectStart:
			path.path = append(path.path, token.Tag)
			path.lastValue = false
		case ArrayStart:
			path.path = append(path.path, fmt.Sprintf("(%s)", token.Tag))
			path.lastValue = false
		case Value:
			if len(token.Tag) > 0 {
				path.path = append(path.path, token.Tag)
				path.lastValue = true
			}
	}
}

func (path *TokenPath) GetPath() string {
	return strings.Join(path.path, ".")
}
