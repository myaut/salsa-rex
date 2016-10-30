package fishly

import (
	"fmt"
	"bytes"
	
	"sort"
	
	"strconv"
	"strings"	
	"reflect"
)

type Request struct {
	// Options struct generated by command and filled-in by 
	// command line parser. For builtins -- only a single 
	// string may be saved to options 
	Options interface{}
	
	// Name of the command or builtin
	commandName string
	
	// Reference to command object. Set to nil for builtins 
	command Command
	
	// Request for outputs (without channels & sinks being setup)
	pipeRqs []IOPipeRequest 
	formatterRq *IOFormatterRequest
	sinkRq *IOSinkRequest
	
	// For output 
	ioh *IOHandle
}

type Command interface {
	Handler
	
	// Returns true if this command can be used in the current context
	// Note that you may create multiple commands but with varying command
	// contexts (they should be exclusive, though)
	IsApplicable(ctx *Context) bool
	
	// Execute command 
	Execute(ctx *Context, rq *Request) error
}

type cmdTokenProcessor struct {
	// Input data
	ctx *Context
	tokens []cmdToken
	
	// Public state
	Index int
	LastError error
	
	// Private state variables
	option string
	rq *Request
	optionDescriptors []optionDescriptor
	
	argIndex int
	providedOptions []string
	
	// Output data
	Requests []*Request
}

// Unlike regular commands, builtins don't have implementation as Command  
// interface and treated very specially
var builtins = []string{
	// Walks over context state if argument of special kind is provided
	"cd",
	
	// Executes script specified as argument
	"source",
	
	// Exits. Can have special optional argument
	"exit",
	
	// Reloads configuration files
	"_reload",
}

// Tries to process command tokens and returns array of requests or 
// error coupled with token where error has happened in cmdTokenProcessor
func (ctx *Context) processCommands(tokens []cmdToken) (*cmdTokenProcessor) {
	processor := new(cmdTokenProcessor)
	
	processor.Requests = make([]*Request, 0)
	processor.ctx = ctx
	processor.tokens = tokens
	
	for {
		token := processor.nextToken()
		if token == nil {
			break
		}
		
		switch token.tokenType {
			case tCommand:
				processor.newCommandRequest(token.token)	
			case tOption:
				processor.option = token.token
				
				if processor.handleOption(tOption, "") {
					// Option is boolean and taken care of, break here. Otherwise
					// next argument will be treated as option argument
					processor.option = ""
				}
			case tRawArgument, tSingleQuotedArgument, tDoubleQuotedArgument:
				// retry reading this token in assembleArgument() 
				processor.rewindToken()
				
				value := processor.assembleArgument(token.argIndex)
				if processor.LastError != nil {
					break
				}
				
				processor.handleOption(tRawArgument, value)
			case tCommandSeparator:
				processor.commandRequestDone()
				processor.rq = nil
			case tRedirection:
				processor.commandRequestDone()
				processor.newRedirectRequest(token.token)
				processor.optionDescriptors = generateOptionDescriptors(processor.getOptionsStruct())
			case tFileRedirection:
				processor.commandRequestDone()
				processor.newRedirectRequest("file")
				processor.optionDescriptors = generateOptionDescriptors(processor.getOptionsStruct())
				processor.handleOption(tRawArgument, token.token)
		}
	}
	
	if processor.LastError == nil {
		processor.commandRequestDone()
	}
	
	return processor
}

// Tries to get next token from token sequence. If fails to, returns nil
func (processor *cmdTokenProcessor) nextToken() (token *cmdToken) {
	if processor.LastError != nil {
		return
	}
	
	if processor.Index < len(processor.tokens) {
		token = &processor.tokens[processor.Index]
		processor.Index++
	}
	return 
}

// Similar to UnreadRune -- puts token back for nextToken
func (processor *cmdTokenProcessor) rewindToken() {
	if processor.Index <= 0 || processor.tokens[processor.Index-1].tokenType == tCommand {
		processor.LastError = fmt.Errorf("Unexpected request to rewind to command token")	
	} else {
		processor.Index--
	}
}

// Assembles argument
func (processor *cmdTokenProcessor) assembleArgument(argIndex int) string {
	buf := bytes.NewBuffer([]byte{})
	
	for {
		token := processor.nextToken()
		if token == nil {
			break
		}
		if token.argIndex != argIndex {
			processor.rewindToken()
			break
		}
		
		switch token.tokenType {
			case tRawArgument, tDoubleQuotedArgument:
				// TODO: support for argument interpolation
				buf.WriteString(token.token)
			case tSingleQuotedArgument:
				buf.WriteString(token.token)
			default:
				processor.rewindToken()
				return buf.String()
		} 
	}
	
	return buf.String()
}

func (processor *cmdTokenProcessor) newCommandRequest(name string) {
	rq := new(Request)
	rq.commandName = name
	
	if !isBuiltin(name) {
		if !processor.setupCommandRequest(rq) {
			// We failed to try to setup this command as builtin and this 
			// is not a real command, return in misery
			return
		}
	}
	
	processor.Requests = append(processor.Requests, rq)
	processor.rq = rq
	processor.resetCommandState()
}

func (processor *cmdTokenProcessor) setupCommandRequest(rq *Request) bool {
	descriptor, ok := processor.ctx.availableCommands[rq.commandName]
	if !ok {
		processor.LastError = fmt.Errorf("Command '%s' not found or not applicable", rq.commandName)
		return false
	}
	
	rq.command = processor.ctx.cfg.getCommandFromDescriptor(descriptor)
	rq.Options = rq.command.NewOptions()
	
	processor.optionDescriptors = generateOptionDescriptors(rq.Options)
	return true
}

// Checks if current token is a builtin and returns its string argument (if one exists)
func isBuiltin(name string) bool {
	for _, builtin := range builtins {
		if builtin == name {
			return true
		}
	}
	
	return false
}

func (processor *cmdTokenProcessor) resetCommandState() {
	processor.argIndex = 1
	processor.providedOptions = make([]string, 0)
}

func (processor *cmdTokenProcessor) commandRequestDone() {
	if processor.rq == nil {
		return
	}
	if len(processor.option) > 0 {
		processor.LastError = fmt.Errorf("Option '%s' expects an argument", 
			processor.option)
		return
	}
	
	// Find all required arguments/options and ensure all them were validated
	sort.Strings(processor.providedOptions)
	for _, descriptor := range processor.optionDescriptors {
		if descriptor.optional {
			continue
		}
		
		if descriptor.argIndex > 0 {
			if descriptor.argIndex >= processor.argIndex {
				processor.LastError = fmt.Errorf("Missing required argument #%d (%s)", 
						descriptor.argIndex, strings.ToLower(descriptor.argName))
				break
			}
			continue
		}
		 
		missingOption := true
		for _, opt := range descriptor.options {
			index := sort.SearchStrings(processor.providedOptions, opt)
			
			if processor.providedOptions[index] == opt {
				missingOption = false
				break
			}
		}
		
		if missingOption {
			processor.LastError = fmt.Errorf("Missing required option %s", strings.Join(descriptor.options, "|"))
			break
		}
	}
}

// Tries to handle builtin argument | value. Returns true if argument is taken care of or 
// last error is set. Return false if request is (promoted/already) a full command request
func (processor *cmdTokenProcessor) handleBuiltinArgument(tokenType cmdTokenType, value string) bool {
	if tokenType != tRawArgument {
		processor.LastError = fmt.Errorf("%s builtin doesn't support '%s' tokens", 
			processor.rq.commandName, tokenTypeStrings[tokenType])
		return true
	}
	if processor.rq.Options != nil {
		processor.LastError = fmt.Errorf("%s builtin option already specified", 
			processor.rq.commandName)
		return true
	}
	
	switch processor.rq.commandName {
		case "cd":
			if value != "-" {
				// This is not builtin variant of "cd", try to promote
				// it to the full-fledged cd
				return !processor.setupCommandRequest(processor.rq)
			}
			
			processor.rq.Options = value
		case "source":
			// TODO: load file and push it as subrequests
			return true
		case "exit":
			exitCode, err := strconv.Atoi(value)
			if err != nil {
				processor.LastError = fmt.Errorf("exit code should be numeric: %v", exitCode)
				return true
			}
			
			processor.rq.Options = exitCode
		default:
			processor.LastError = fmt.Errorf("%s builtin doesn't support any arguments", 
					processor.rq.commandName, tokenTypeStrings[tokenType])
	}
	
	
	return true
}

func (processor *cmdTokenProcessor) handleOption(tokenType cmdTokenType, value string) bool {
	if processor.rq == nil {
		processor.LastError = fmt.Errorf("Token '%s' requires command, but none specified",
				value)
		return false
	}
	if processor.rq.command == nil {
		// If we get a promoted request, good for us
		if processor.handleBuiltinArgument(tokenType, value) {
			return true
		} 
	}
	
	// Use reflect to find appropriate field
	options := processor.getOptionsStruct()
	maxArgIndex := 0
	
	// Find appropriate descriptor and check if we can use it
	for _, descriptor := range processor.optionDescriptors {
		optionsVal := reflect.ValueOf(options).Elem()
		field := optionsVal.Field(descriptor.fieldIndex)
		
		if len(processor.option) > 0 {
			if !descriptor.matchOption(processor.option) {
				continue
			}
			
			if descriptor.kind == reflect.Bool {
				// A special kind of option -- if it is present and structure
				// expect a boolean, just set it
				field.SetBool(true) 
				processor.option = ""
				return true
			} 
			
			if tokenType == tOption {
				// We are not yet have argument value, so wait for next tokens
				// being parsed as argument value 
				return false
			}
			
			processor.option = ""
		} else {
			if descriptor.argIndex == 0 {
				continue
			}
			
			if descriptor.kind == reflect.Slice {
				// A special kind of options argument -- a slice argument. If it 
				// is present, it vacuums all arguments
				field.Set(reflect.Append(field, reflect.ValueOf(value)))
				return true
			}
			
			// All arguments are numbered from 1
			if descriptor.argIndex != processor.argIndex {
				if descriptor.argIndex > maxArgIndex {
					maxArgIndex = descriptor.argIndex 
				}
				continue
			}
			
			processor.argIndex++
		}
		
		// Found an appropriate descriptor, now convert a data
		
		switch field.Kind() {
			// Boolean types shouldn't be handled by arguments and already
			// taken care of before, so reflect.Bool is ignored
			
			case reflect.String:
				field.SetString(value)
			
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				intVal, err := strconv.ParseInt(value, 0, field.Type().Bits())
				if err != nil {
					processor.LastError = fmt.Errorf("Integer is required, %s", err)
					return false
				}
				
				field.SetInt(intVal)
				
			case reflect.Uint, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				uintVal, err := strconv.ParseUint(value, 0, field.Type().Bits())
				if err != nil {
					processor.LastError = fmt.Errorf("Unsigned integer is required, %s", err)
					return false
				}
				
				field.SetUint(uintVal)
				
			default:
				processor.LastError = fmt.Errorf("unsupported options type %s", field.Type().Name())
				return false
		}
		
		return true 
	}
	
	// We shouldn't fall here, but if we did, something went wrong
	if len(processor.option) > 0 {
		processor.LastError = fmt.Errorf("Unexpected option '%s'", processor.option)
	} else {
		processor.LastError = fmt.Errorf("Unexpected argument #%d, %d argument(-s) allowed", 
				processor.argIndex, maxArgIndex)
	}
	return false
}

func (processor *cmdTokenProcessor) getOptionsStruct() interface{} {
	rq := processor.rq
	if rq == nil {
		if processor.LastError == nil {
			processor.LastError = fmt.Errorf("Unexpected option before command")
		}
		return nil
	}
	
	var options interface{}
	switch {
		case rq.sinkRq != nil:
			options = rq.sinkRq.Options
		case rq.formatterRq != nil:
			options = rq.formatterRq.Options
		case rq.pipeRqs != nil:
			options = rq.pipeRqs[len(rq.pipeRqs)-1]
		default:
			options = rq.Options		
	}
	
	if options == nil {
		// Current entity (io, command) doesn't support options
		return nil
	}
	
	if reflect.TypeOf(options).Kind() != reflect.Ptr {
		processor.LastError = fmt.Errorf("Invalid type of options struct, pointer expected")
		return nil
	}
	return options
}

func (processor *cmdTokenProcessor) newRedirectRequest(name string) {
	// Redirect goes as follows: pipe(-s) -> formatter -> sink, so 
	// if we already have one of formatter or sink, we couldn't create another
	// one or a pipe
	rq := processor.rq
	if rq.sinkRq != nil {
		processor.LastError = fmt.Errorf("Redirection cannot be used after sink")
		return
	}
	if rq.command == nil {
		processor.LastError = fmt.Errorf("Builtins cannot have redirection")
		return
	}
	if name == "" {
		name = "sh"
	}
	
	descriptor, ok := processor.ctx.cfg.ioHandlers[name]
	if !ok {
		processor.LastError = fmt.Errorf("Unknown redirection directive '%s'", name)
		return
	}
	
	switch descriptor.handlerLocalType {
		case hdlIOSink: 
			sink := processor.ctx.cfg.sinks[descriptor.handlerLocalIndex]
			rq.createIOSinkRequest(sink)
		case hdlIOFormatter:
			if rq.formatterRq != nil {
				processor.LastError = fmt.Errorf("Formatter already specified")
				return
			}
			
			formatter := processor.ctx.cfg.formatters[descriptor.handlerLocalIndex]
			rq.createIOFormatterRequest(formatter)
		case hdlIOPipe:
			if rq.formatterRq != nil {
				processor.LastError = fmt.Errorf("Cannot specify pipe after formatter")
				return
			}
			
			pipe := processor.ctx.cfg.pipes[descriptor.handlerLocalIndex]
			rq.addIOPipeRequest(pipe)
	}
}
