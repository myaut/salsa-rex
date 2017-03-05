package fishly

import (
	"io"
	"fmt"
	"log"
	
	"os"
		
	"strings"
	"strconv"
)

//
// loop -- main file in fishly which receives input lines from driver
// which would be either readline (terminal) or HTTP client (web), 
// starts cancel handler and interprets input command by command  
//

type ContextPromptFormatter func(ctx *Context) string

type ReadlineDriver interface {
	Close() error
	
	Stderr() io.Writer
	SetPrompt(prompt string) 
	
	Readline() (string, error)
}
type ReadlineDriverFactory interface {
	// Create readline driver or return error. May register sinks, etc.
	Create(ctx *Context) (ReadlineDriver, error)	
}

type CancelHandler interface {
	// Goroutines which wait for signal or reset handler
	Wait()
	Reset()
}
type CancelHandlerFactory interface {
	// Establish handler
	Create(ctx *Context, rq *Request) CancelHandler	
}

type UserConfig struct {
	// Command which would be automatically executed before running shell
	AutoExec string
	
	// Paths to schema files
	Schema []string
	
	// URL used as initial context state
	InitContextURL string
}

type Config struct {
	UserConfig
	
	// Sets default readline and cancel drivers
	Readline ReadlineDriverFactory
	Cancel CancelHandlerFactory
	
	// Sets program message of the day
	MOTD string
	
	// Sets base program's prompt (without context part)
	PromptProgram string
	PromptSuffix string
	
	// Lists of supported/registered handlers. Handlers are stateless
	// objects that handle requests and contexts
	handlers []handlerDescriptor
	commands []Command
	
	ioHandlers handlerTable
	pipes []IOPipe
	formatters []IOFormatter
	sinks []IOSink
	
	// Default sinks and formatters
	DefaultSink IOSink
	DefaultPagerSink IOSink
	DefaultRichTextFormatter IOFormatter
	DefaultTextFormatter IOFormatter
	
	// Data schema of outputs
	schema schemaRoot
}

func (cfg *Config) Run(extCtx ExternalContext) int {
	// runs main command loop
	ctx, err := newContext(cfg, extCtx)
	if err != nil {
		log.Fatalln(err)
	}
	defer ctx.rl.Close()
	
	if len(ctx.cfg.MOTD) > 0 {
		log.Println(ctx.cfg.MOTD)
	}
	ctx.runAutoExec()
	
	for ctx.exitCode < 0 {
		ctx.readNextCommand()
		if ctx.exitCode >= 0 {
			break	
		}
		
		ctx.processRequests()
	}
	
	return ctx.exitCode
}

// Reads next command from readline driver as a whole. If command is not yet 
// parseable (unclosed block or quoted string), continues reading. If ^D is 
// used, sets exit code to zero so loop can exit. If input was interrupted, 
// sets cmdParser to nil. Otherwise leaves cmdParser field for further handling
func (ctx *Context) readNextCommand() {
	ctx.rl.SetPrompt(ctx.FullPrompt)
	ctx.cmdParser = newParser()
	
	for ctx.cmdParser.ExpectMore {
		line, err := ctx.rl.Readline()
		if err != nil {
			if err.Error() == "Interrupt" {
				ctx.cmdParser = nil
				return
			} else if err == io.EOF {
				ctx.exitCode = 0
				return
			}
			log.Fatalln(err)
		}
		
		// Parse line and check if there is any input
		ctx.cmdParser.parseLine(line)
		if len(ctx.cmdParser.Tokens) == 0 {
			break
		}
		
		ctx.rl.SetPrompt("...")
	}
}

func (ctx *Context) parseAutoExec(line string) bool {
	parser := newParser()
	parser.parseLine(line)
	if parser.LastError != nil {
		ctx.dumpLastError(parser.Position, parser.Position, 0, []string{line}, 
			parser.LastError)
		return false
	}
	
	ctx.cmdParser = parser
	return true
}

// Processes incoming arguments from parser if there are some
func (ctx *Context) processRequests() (err *cmdProcessorError) {
	if ctx.cmdParser == nil {
		return		// nothing to process
	}
	
	return ctx.processBlock(ctx.cmdParser.createRootWalker())
}

// Processes block of arguments
func (ctx *Context) processBlock(block *cmdBlockTokenWalker) (err *cmdProcessorError) {
	for err == nil && ctx.exitCode < 0 {
		cmd := block.nextCommand()
		if cmd == nil {
			return
		}
		
		var rq *Request
		
		commandName := cmd.getFirstToken().token  
		switch commandName {
			case "cd":
				rq, err = ctx.tryProcessCD(cmd)
				if rq == nil {
					ctx.tick()
				}
			case "exit":
				err = ctx.tryProcessExit(cmd)
			case "schema":
				// TODO: implement as subcommands reload, etc...
				err = cmd.newCommandError(ctx.reloadSchema())
			default:
				rq, err = ctx.prepareCommandRequest(cmd)
		}
				
		if rq != nil && err == nil {
			err = cmd.newCommandError(ctx.processRequest(rq))
		}
		
		if err != nil {
			lines, startPos, endPos := cmd.reassembleLines(err.index)
			ctx.dumpLastError(startPos, endPos, cmd.getFirstToken().line, lines, err.err)
			return
		}
	}
	return
}

// Tries to interpret command as builtin cd. If we fail to do so, fall back to 
// normal type of request (external cd command)				
func (ctx *Context) tryProcessCD(cmd *cmdCommandTokenWalker) (rq *Request, err *cmdProcessorError) {
	args := cmd.getArguments()
	if len(args) == 0 {			// "cd"
		return nil, cmd.newCommandError(ctx.rewindStateRoot())
	}
	if len(args) == 1 && args[0].tokenType == tOption {
		if len(args[0].token) == 0 {	// "cd -"
			return nil, cmd.newArgumentError(ctx.rewindState(-1), 0)			
		} 
		if steps, convErr := strconv.Atoi(args[0].token); convErr == nil {	// "cd -N"
			return nil, cmd.newArgumentError(ctx.rewindState(-steps), 0)
		} 
	}
	
	return ctx.prepareCommandRequest(cmd)
}

func (ctx *Context) tryProcessExit(cmd *cmdCommandTokenWalker) (err *cmdProcessorError) {
	var exitArg struct {
		ExitCode int `arg:"1,opt"`
	}
	argParser := cmd.parseArgs(&exitArg, ctx.interpolateArgument)
	if argParser.LastError != nil {
		return cmd.newArgParserError(argParser)
	}
	
	ctx.exitCode = exitArg.ExitCode
	return
}

func (ctx *Context) runAutoExec() {
	if !ctx.parseAutoExec(ctx.cfg.UserConfig.AutoExec) {
		log.Fatalln("Parse error in autoexec command, exiting...")
	}
	
	err := ctx.processRequests()
	if err != nil {
		log.Fatalln("Error while executing in autoexec command, exiting...")
	}
	if ctx.exitCode >= 0 {
		os.Exit(ctx.exitCode)
	}	
}

// Prints syntax error & highlights token which caused failure
func (ctx *Context) dumpLastError(startPos, endPos, firstLineNo int, lines []string, err error) {
	for lineOff, line := range lines {
		fmt.Fprintf(ctx.rl.Stderr(), "%3d %s\n", firstLineNo+lineOff, line)
	}
	fmt.Fprintf(ctx.rl.Stderr(), "    %s%s error: %s\n", strings.Repeat(" ", startPos),
					strings.Repeat("^", endPos-startPos), err)	
}

