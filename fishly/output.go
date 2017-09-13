package fishly

import (
	"bufio"
	"io"

	"sync"
	"sync/atomic"

	"fmt"
	"strings"
)

//
// output.go -- utilities for implementing command output in fishly. Note that it
// may referred to I/O in many contexts, while being only responsible for "O"
//
// Unlike traditional unix shells which praise text-only output and PowerShell which
// uses object output, fishly adopts semi-structured approach which allows to implement
// awk-like language more flexible, provide more than one formatter and dump data
// in machine-readable format without a change to original command source.
//
// In fishly commands are sending stream of tokens to an outgoing go channel where each
// token of type Value may have tag (to identify purpose of data), raw value (for raw
// formats such as JSON) and formatted user-readable string. There are also tokens of
// special types: EOF which denotes end of token stream, ObjectStart (with tag set) and
// ObjectEnd which denote object and sub-object boundaries.
//
// Output subsystem adds three new types of handlers, specifically:
//    * pipes -- goroutines that receive token stream over channel and put transformed
//		token stream to an output channel
//    * sinks -- handlers which spawn output WriteCloser which could be current terminal
//      window, pager or shell command which reads raw (unstructured) data
//    * formatters -- goroutines which connect commands with pipes to sinks by formatting
//      structured data into raw text
//

const (
	ioChannelCapacity = 20
)

type OutputTokenType int

const (
	// End of file -- stops channels
	EOF = iota

	// Any other token containing text and/or value
	Value

	// Object start/end tags
	ObjectStart
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

// TokenPath is an accumulator for current token path which maps path to schema.
//
// Lets S is structure, U is union and A(X) is array of X (structures or unions
// or arrays), and C = S | U | A(X) and V is variable inside structure or union
// with optional special value node, then the following transitions apply
//
// ObjectStart:
//		[]	-> 	[C]
//      [*, S] -> [*, S -> V, S'] if V.name = token.Tag && V.compoundType = S'
//		[*, U] -> [*, U -> V, S] if V.name = token.Tag && V.compoundType = S
//      [*, A(S)] -> [*, A(S), S] if S.name = token.Tag
//      [*, A(U)] -> [*, A(U) -> U -> V, S]
//
// Value:
//		[] -
//		[*, C] -> [*, C -> V]
//		[*, C -> V1] -> [*, C -> V2]
//
// ObjectEnd:
//		[*, C] -> [*]
//
type tokenPathElement struct {
	node        *schemaNode
	elementNode *schemaNode
	varNode     *schemaNode
	valueNode   *schemaNode

	count int
	old   bool
}
type TokenPath struct {
	// Pointer to schema root for identifying top-level schema type nodes
	schema *schemaRoot

	// Stack of compound types. Height contains current height of stack
	// after processing EndObject tags.
	stack  []tokenPathElement
	height int

	// TODO: deprecate this
	path []string

	lastError error
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
	err      error
	hasError int32
}

// IOPipe takes two channels and filters/updates tokens
type IOPipe interface {
	Handler

	Run(ctx *Context, rq *IOPipeRequest)
}
type IOPipeRequest struct {
	Options interface{}

	Input  IOChannel
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

	Input  IOChannel
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
			rq.createIOFormatterRequest(ctx, ctx.cfg.DefaultRichTextFormatter, nil)
		} else {
			rq.createIOFormatterRequest(ctx, ctx.cfg.DefaultTextFormatter, nil)
		}
	}
	if rq.sinkRq == nil {
		if needPager {
			rq.createIOSinkRequest(ctx, ctx.cfg.DefaultPagerSink, nil)
		} else {
			rq.createIOSinkRequest(ctx, ctx.cfg.DefaultSink, nil)
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
	for pipeIndex := -1; pipeIndex < len(rq.pipeRqs); pipeIndex++ {
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
func (rq *IOFormatterRequest) Close(err error) {
	rq.rq.ioh.setError(err)
	rq.rq.ioh.drainChannel(rq.Input)

	sinkError := rq.Output.Close()
	rq.rq.ioh.setError(sinkError)

	rq.rq.ioh.wg.Done()
}

func (rq *Request) createIOSinkRequest(ctx *Context, sink IOSink, redir *cmdRedirTokenWalker) (err *cmdProcessorError) {
	sinkRq := &IOSinkRequest{
		sink:    sink,
		Options: sink.NewOptions(ctx),
	}

	if redir != nil {
		argParser := redir.parseArgs(sinkRq.Options, ctx.interpolateArgument)
		if argParser.LastError != nil {
			return redir.newArgParserError(argParser)
		}
	}

	rq.sinkRq = sinkRq
	return
}
func (rq *Request) createIOFormatterRequest(ctx *Context, formatter IOFormatter,
	redir *cmdRedirTokenWalker) (err *cmdProcessorError) {
	formatterRq := &IOFormatterRequest{
		formatter: formatter,
		Options:   formatter.NewOptions(ctx),
		rq:        rq,
	}

	if redir != nil {
		argParser := redir.parseArgs(formatterRq.Options, ctx.interpolateArgument)
		if argParser.LastError != nil {
			return redir.newArgParserError(argParser)
		}
	}

	rq.formatterRq = formatterRq
	return
}
func (rq *Request) addIOPipeRequest(ctx *Context, pipe IOPipe,
	redir *cmdRedirTokenWalker) (err *cmdProcessorError) {
	pipeRq := IOPipeRequest{
		pipe:    pipe,
		Options: pipe.NewOptions(ctx),
		rq:      rq,
	}

	argParser := redir.parseArgs(pipeRq.Options, ctx.interpolateArgument)
	if argParser.LastError != nil {
		return redir.newArgParserError(argParser)
	}

	rq.pipeRqs = append(rq.pipeRqs, pipeRq)
	return
}

// Writes value that was pre-formatted
func (ioh *IOHandle) WriteFormattedValue(tag, text string, value interface{}) {
	if atomic.LoadInt32(&ioh.hasError) == 1 {
		return
	}

	ioh.channels[0] <- Token{
		TokenType: Value,
		Tag:       tag,
		Text:      text,
		RawValue:  value,
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

	ioh.channels[0] <- Token{
		TokenType: ObjectStart,
		Tag:       name,
	}
}

func (ioh *IOHandle) EndObject() {
	if atomic.LoadInt32(&ioh.hasError) == 1 {
		return
	}

	ioh.channels[0] <- Token{
		TokenType: ObjectEnd,
	}
}

func (ioh *IOHandle) drainChannel(channel IOChannel) {
	// If we're closing due to error or some handler exited prematurely,
	// drain its input so sender won't hang
	for {
		select {
		case t := <-channel:
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

	channel <- Token{
		TokenType: EOF,
	}
}

// Token path helper functions -- work for filters and formatters
// so they determine current output context
func (ctx *Context) NewTokenPath() (path *TokenPath) {
	return &TokenPath{
		schema: &ctx.cfg.schema,
	}
}

// Updates path of current token in correspondence to a specified schema
func (path *TokenPath) UpdatePath(token Token) {
	path.lastError = nil

	// fmt.Println(path.String(), token)

	if len(path.stack) == 0 {
		if token.TokenType == Value {
			return
		}

		if nodeId, ok := path.schema.types[token.Tag]; ok {
			node := path.schema.nodes[nodeId]
			path.addElement(node) // [] -> [C]
		}
		return
	}

	switch token.TokenType {
	case ObjectStart:
		top := path.getTopElement()
		if top.node == nil {
			// Uncharted territory -- just produce nil-pointers in path to match
			// corresponding ObjectEnd tags
			path.addElement(nil)
			return
		}

		switch top.node.data.(type) {
		case (schemaArray):
			array := top.node.data.(schemaArray)
			elementType := array.elementType
			switch elementType.data.(type) {
			case (schemaStruct):
				path.setArrayElement(top, elementType, token)
			default:
				path.lastError = fmt.Errorf("Unexpected array element: %s[%s], should be struct",
					top.node.name, elementType.name)
				path.addElement(elementType)
			}
		case (schemaStruct):
			// [*, C] -> [*, C -> V, S]
			compound := top.node.data.(schemaStruct)
			path.setVariableNode(top, compound, token, true)
		default:
			path.lastError = fmt.Errorf("Unexpected top-level schema node: %v", top)
			path.addElement(nil)
		}
	case Value:
		top := path.getTopElement()
		if top.node == nil {
			return
		}

		switch top.node.data.(type) {
		case (schemaArray):
			array := top.node.data.(schemaArray)
			elementType := array.elementType
			switch elementType.data.(type) {
			case (schemaStruct):
				compound := elementType.data.(schemaStruct)
				if !compound.isUnion {
					path.lastError = fmt.Errorf("Unexpected value in non-union schema node: %v", elementType)
				}
				path.setVariableNode(top, compound, token, false)
				top.elementNode = elementType
			}
		case (schemaStruct):
			compound := top.node.data.(schemaStruct)
			path.setVariableNode(top, compound, token, false)
		}
	case ObjectEnd:
		if path.height > 0 {
			path.height--

			if path.height > 0 {
				top := path.getTopElement()
				top.count++
				top.varNode = nil
				top.valueNode = nil
			}
		}
	}
}

func (path *TokenPath) addElement(node *schemaNode) {
	path.stack = append(path.stack[:path.height], tokenPathElement{
		node: node,
	})
	path.height++
}

func (path *TokenPath) setArrayElement(top *tokenPathElement, elementType *schemaNode, token Token) {
	compound := elementType.data.(schemaStruct)
	if compound.isUnion {
		// [*, A(U)] -> // [*, A(U) -> V, S]
		path.setVariableNode(top, compound, token, true)
		top.elementNode = elementType
	} else {
		// [*, A(S)] -> // [*, A(S) -> V, S]
		if elementType.name != token.Tag {
			path.lastError = fmt.Errorf("Unexpected array element: %s", token.Tag)
		}
		path.addElement(elementType)
	}
}

func (path *TokenPath) setVariableNode(top *tokenPathElement, compound schemaStruct,
	token Token, addCompound bool) bool {
	for _, varNode := range compound.variables {
		if varNode.name != token.Tag {
			continue
		}

		if top.varNode != nil {
			top.count++
		}
		top.varNode = varNode
		top.valueNode = nil

		variable := varNode.data.(schemaVariable)
		compoundType := variable.compoundType

		if addCompound {
			if compoundType == nil {
				path.lastError = fmt.Errorf("Variable '%s' expected to have compound type, but it doesn't",
					token.Tag)
				path.addElement(nil)
			} else {
				path.addElement(compoundType)
			}
		}

		// Now found value node (if it is possible)
		if compoundType != nil {
			switch compoundType.data.(type) {
			case (schemaEnum):
				enumeration := compoundType.data.(schemaEnum)
				if valueNode, ok := enumeration.values[token.Text]; ok {
					top.valueNode = valueNode
				}
			}
		} else if len(variable.values) > 0 {
			if valueNode, ok := variable.values[token.Text]; ok {
				top.valueNode = valueNode
			}
		}

		return true
	}

	path.lastError = fmt.Errorf("Undefined variable '%s'", token.Tag)
	top.varNode = nil
	if addCompound {
		path.addElement(nil)
	}
	return false
}

func (path *TokenPath) getNthElement(n int) *tokenPathElement {
	topIndex := path.height - n
	if topIndex < 0 || topIndex >= len(path.stack) {
		return nil
	}
	return &path.stack[topIndex]
}

func (path *TokenPath) getTopElement() *tokenPathElement {
	return path.getNthElement(1)
}

func (path *TokenPath) getVariableElement() *tokenPathElement {
	for i := 1; i <= 2; i++ {
		// FIXME: this doesn't work properly when top-level entry is supposed
		// to have variable, but left varNode == nil
		variable := path.getNthElement(i)
		if variable == nil {
			break
		}
		if variable.varNode != nil {
			return variable
		}
	}
	return nil
}

func (path *TokenPath) getClosedElement() *tokenPathElement {
	if path.height >= len(path.stack) {
		return nil
	}
	return &path.stack[path.height]
}

func (path *TokenPath) GetPath() string {
	var elements []string

	for _, el := range path.stack[:path.height] {
		elPath := []string{el.node.name}
		if el.elementNode != nil {
			elPath = append(elPath, fmt.Sprintf("union %s", el.elementNode.name))
		}
		if el.varNode != nil {
			elPath = append(elPath, fmt.Sprintf("var %s", el.varNode.name))
		}
		if el.valueNode != nil {
			elPath = append(elPath, "value")
		}

		elements = append(elements, strings.Join(elPath, " -> "))
	}
	return strings.Join(elements, ", ")
}

// For debugging
func (path *TokenPath) String() (str string) {
	if path.height == 0 {
		return
	}

	str = strings.Repeat(" .", path.height)

	top := path.getTopElement()
	if top.node == nil {
		str = fmt.Sprintf("%s -> <nil>", str)
		return
	}

	switch top.node.data.(type) {
	case (schemaArray):
		array := top.node.data.(schemaArray)
		elementType := array.elementType
		typeName := "?"
		switch elementType.data.(type) {
		case (schemaStruct):
			compound := elementType.data.(schemaStruct)
			typeName = "struct"
			if compound.isUnion {
				typeName = "union"
			}
		}
		str = fmt.Sprintf("%s -> %s array[%s %s] @%d", str,
			top.node.name, elementType.name, typeName, top.count)
	case (schemaStruct):
		compound := top.node.data.(schemaStruct)
		typeName := "struct"
		if compound.isUnion {
			typeName = "union"
		}
		str = fmt.Sprintf("%s -> %s %s @%d", str, top.node.name, typeName, top.count)
	default:
		str = fmt.Sprintf("%s -> ???")
	}

	if top.elementNode != nil {
		str = fmt.Sprintf("%s -> el %s", str, top.elementNode.name)
	}
	if top.varNode != nil {
		str = fmt.Sprintf("%s -> var %s", str, top.varNode.name)
	}

	closed := path.getClosedElement()
	if closed != nil {
		str = fmt.Sprintf("%s ... +", str)
	}
	return
}

//
// Trace formatter is a formatter for debugging token streams and TokenPath class
//

type traceFormatter struct {
	HandlerWithoutCompletion
	HandlerWithoutOptions
}

func (*traceFormatter) Run(ctx *Context, rq *IOFormatterRequest) {
	defer rq.Close(nil)

	path := ctx.NewTokenPath()
	w := bufio.NewWriter(rq.Output)
	defer w.Flush()

	for {
		token := <-rq.Input
		if token.TokenType == EOF {
			return
		}

		path.UpdatePath(token)

		tokenType := '?'
		switch token.TokenType {
		case Value:
			tokenType = 'V'
		case ObjectStart:
			tokenType = 'S'
		case ObjectEnd:
			tokenType = 'E'
		}

		w.WriteString(fmt.Sprintf("%c %16s %s\n", tokenType, token.Tag, path.String()))
	}
}
