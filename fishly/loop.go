package fishly

import (
	"bufio"
	"fmt"
	"io"
	"log"

	"os"

	"strconv"
	"strings"
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
	Cancel   CancelHandlerFactory

	// Prevents fishly from redefining log outputs (good for tests)
	KeepLogOutput bool

	// Sets program message of the day
	MOTD string

	// Sets base program's prompt (without context part)
	PromptProgram string
	PromptSuffix  string

	// Lists of supported/registered handlers. Handlers are stateless
	// objects that handle requests and contexts
	handlers []handlerDescriptor
	commands []Command

	ioHandlers handlerTable
	pipes      []IOPipe
	formatters []IOFormatter
	sinks      []IOSink

	// Default sinks and formatters
	DefaultSink              IOSink
	DefaultPagerSink         IOSink
	DefaultRichTextFormatter IOFormatter
	DefaultTextFormatter     IOFormatter

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
		return // nothing to process
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
		case "source", ".":
			err = ctx.tryLoadScript(cmd)
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
	if len(args) == 0 { // "cd"
		return nil, cmd.newCommandError(ctx.rewindStateRoot())
	}
	if len(args) == 1 && args[0].tokenType == tOption {
		if len(args[0].token) == 0 { // "cd -"
			return nil, cmd.newArgumentError(ctx.RewindState(-1), 0)
		}
		if steps, convErr := strconv.Atoi(args[0].token); convErr == nil { // "cd -N"
			return nil, cmd.newArgumentError(ctx.RewindState(-steps), 0)
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

func (ctx *Context) tryLoadScript(cmd *cmdCommandTokenWalker) (err *cmdProcessorError) {
	// Process arguments: only name/path to script is expected
	var sourceArg struct {
		Path string `arg:"1"`
	}
	argParser := cmd.parseArgs(&sourceArg, ctx.interpolateArgument)
	if argParser.LastError != nil {
		return cmd.newArgParserError(argParser)
	}

	// Try to load script
	f, fileErr := os.Open(sourceArg.Path)
	if fileErr != nil {
		return cmd.newCommandError(fileErr)
	}
	defer f.Close()

	// Save and reset on exit command parser, use new one for processing script
	oldParser := ctx.cmdParser
	defer func() { ctx.cmdParser = oldParser }()
	ctx.cmdParser = newParser()

	// Now process file line by line
	ctx.cmdParser.parseReader(f)
	if ctx.cmdParser.LastError != nil {
		ctx.dumpLastParserError(f, ctx.cmdParser)
		return nil
	}

	return ctx.processRequests()
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

// Processes request from a next subblock command. If there is no block,
// error is returned. Call this from Execute() method of command
func (ctx *Context) ProcessBlock(rq *Request) (err error) {
	// In case we had changed context, update it
	ctx.tick()

	block := rq.walker.nextBlock()
	if block == nil {
		return fmt.Errorf("No subblock found")
	}

	ctx.processBlock(block)
	return nil
}

// Prints syntax error & highlights token which caused failure
func (ctx *Context) dumpLastError(startPos, endPos, firstLineNo int, lines []string, err error) {
	for lineOff, line := range lines {
		fmt.Fprintf(ctx.rl.Stderr(), "%3d %s\n", firstLineNo+lineOff, line)
	}
	fmt.Fprintf(ctx.rl.Stderr(), "    %s%s error: %s\n", strings.Repeat(" ", startPos),
		strings.Repeat("^", endPos-startPos), err)
}

func (ctx *Context) dumpLastProcessorError(lastError *cmdProcessorError) {
	lines, startPos, endPos := lastError.cmd.reassembleLines(lastError.index)
	ctx.dumpLastError(startPos, endPos, lastError.cmd.getFirstToken().line,
		lines, lastError.err)
}

func (ctx *Context) dumpLastParserError(rd io.ReadSeeker, tokenParser *cmdTokenParser) {
	// Re-read file to get required line contents and dump parse error
	var line string

	rd.Seek(0, io.SeekStart)
	lineReader := bufio.NewReader(rd)
	for l := 1; l <= tokenParser.Line; l++ {
		line, _ = lineReader.ReadString(byte('\n'))
	}

	ctx.dumpLastError(tokenParser.Position, tokenParser.Position,
		tokenParser.Line, []string{line}, tokenParser.LastError)
}
