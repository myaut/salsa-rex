package fishly

import (
	"io"
	"fmt"
	"log"
	
	"os"
		
	"strings"
	
	"github.com/go-ini/ini"	
	
	// readline "gopkg.in/readline.v1"
	readline "github.com/chzyer/readline"
)

//
// loop -- main file in fishly which  glues readline library
// and interprets inputs
//

type ContextPromptFormatter func(ctx *Context) string

type UserConfig struct {
	// As defined in readline.Config
	HistoryFile string
	HistoryLimit int
	DisableAutoSaveHistory bool
	VimMode bool
	
	// Program used as pager for long outputs. Should retain colorized outputs
	Pager string
	
	// Command which would be automatically executed before running shell
	AutoExec string
	
	// Help file paths
	Help []string
	
	// StyleSheet files used for formatted text
	StyleSheet []string
}

type Config struct {
	UserConfig
	
	// Sets program message of the day
	MOTD string
	
	// Sets base program's prompt (without context part)
	PromptProgram string
	PromptSuffix string
	
	// Formatter for prompt middle part
	PromptFormatter ContextPromptFormatter
	
	// Lists of supported/registered handlers. Handlers are stateless
	// objects that handle requests and contexts
	handlers []handlerDescriptor
	commands []Command
	
	ioHandlers handlerTable
	pipes []IOPipe
	formatters []IOFormatter
	sinks []IOSink
	
	// Default sink and formatters
	DefaultSink IOSink
	DefaultPagerSink IOSink
	DefaultRichTextFormatter IOFormatter
	DefaultTextFormatter IOFormatter
}

func (cfg *Config) Run() int {
	// runs main command loop
	ctx, err := newContext(cfg)
	if err != nil {
		log.Fatalln(err)
	}
	defer ctx.rl.Close()
	
	log.Println(ctx.cfg.MOTD)
	ctx.runAutoExec()
	
	for ctx.running {
		line, err := ctx.rl.Readline()
		if err == readline.ErrInterrupt {
			continue
		} else if err == io.EOF || strings.TrimSpace(line) == "exit" {
			break
		}
		
		ctx.parseRequests(line)
		for ctx.running && ctx.requestIndex < len(ctx.requests) {
			ctx.processRequest()
		}
		
	}
	
	return ctx.exitCode
}

func newContext(cfg *Config) (*Context, error) {
	// Create and setup context object
	ctx := new(Context)
	ctx.cfg = cfg
	ctx.reload()
	
	// Create readline instance
	rl, err := readline.NewEx(&readline.Config{
		HistoryFile: cfg.UserConfig.HistoryFile,
		HistoryLimit: cfg.UserConfig.HistoryLimit,
		DisableAutoSaveHistory: cfg.UserConfig.DisableAutoSaveHistory,
		VimMode: cfg.UserConfig.VimMode,
	})
	if err != nil {
		return nil, err
	}
	
	ctx.rl = rl
	ctx.running = true
	ctx.cfg.registerBuiltinHandlers()
	
	// Create first root state
	ctx.states = make([]ContextState, 0, 1)
	ctx.PushState(true)
	
	// Initialize context
	ctx.tick()
	
	// Some redirections 
	log.SetOutput(ctx.rl.Stderr())
	
	return ctx, nil
}

func (ctx *Context) loadHelp() (err error) {
	helpFiles := make([]interface{}, len(ctx.cfg.Help))
	for index, helpFile := range ctx.cfg.Help {
		helpFiles[index] = helpFile
	}
	
	ctx.help, err = ini.Load(helpFiles[0], helpFiles[1:]...)
	return 
}

func (ctx *Context) parseRequests(line string) {
	ctx.requestIndex = 0
	
	if len(line) == 0 {
		ctx.requests = []*Request{}
		return
	}
	
	parser := ctx.parseLine(line)
	if parser.LastError != nil {
		ctx.dumpLastError(parser.Position, parser.Position, line, "Parser", parser.LastError)
		ctx.requests = nil
		return
	}
	
	// dumpTokens(parser.Tokens)
	
	processor := ctx.processCommands(parser.Tokens)
	if processor.LastError != nil {
		token := &parser.Tokens[processor.Index-1]
		ctx.dumpLastError(token.startPos, token.endPos, line, "Syntax", processor.LastError)
		ctx.requests = nil
		return
	}
	
	// TODO: support for hanging requests (i.e. \, unclosed if/for) -- should 
	// enter multiline input
	
	ctx.requests = processor.Requests
}

func (ctx *Context) processRequest() (err error) {
	rq := ctx.requests[ctx.requestIndex]
	ctx.requestIndex++
	
	if rq.command == nil {
		err = ctx.executeBuiltinRequest(rq) 
	} else {
		cmdErr := ctx.executeRequest(rq)
		
		// Handle command & output errors
		if rq.ioh != nil && rq.ioh.err != nil {
			err = rq.ioh.err
			log.Printf("I/O handler exited with error: %s", err)
		}
		if cmdErr != nil {
			err = cmdErr
		}
	}
	
	if err == nil {
		// If context has changed, perform necessary updates in context
		ctx.tick()
	} else {
		log.Printf("Command '%s' exited with error: %s", rq.commandName, err)
	}
	
	// TODO: if '&&' is specified, we should rewind until the end of construct	
	
	return nil
}

func (ctx *Context) executeRequest(rq *Request) (error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Command caused a panic: %v", r)
		} 
	}()
	
	return rq.command.Execute(ctx, rq)
}

func (ctx *Context) executeBuiltinRequest(rq *Request) (error) {
	switch rq.commandName {
		case "cd":
			if rq.Options != nil {
				where := rq.Options.(string)
				ctx.rewindState(where)
			} else {
				ctx.rewindState("")
			}
		case "_reload":
			return ctx.reload()
		case "exit":
			if rq.Options != nil {
				ctx.exitCode = rq.Options.(int)
			}
			ctx.running = false
			return nil
	}
	return nil
}

func (ctx *Context) runAutoExec() {
	ctx.parseRequests(ctx.cfg.UserConfig.AutoExec)
	if ctx.requests == nil {
		log.Fatalln("Parse error in autoexec command, exiting...")
	}
	
	for ctx.running && ctx.requestIndex < len(ctx.requests) {
		err := ctx.processRequest()
		if err != nil {
			log.Fatalln("Error while executing in autoexec command, exiting...")
		}
		if !ctx.running {
			os.Exit(ctx.exitCode)
		}
	}	
}

func (ctx *Context) dumpLastError(startPos, endPos int, line string, errorClass string, err error) {
	// Print syntax error & highlight failed token
	fmt.Fprintf(ctx.rl.Stderr(), "   %s\n", line)
	fmt.Fprintf(ctx.rl.Stderr(), "   %s%s %s error: %s\n", strings.Repeat(" ", startPos),
					strings.Repeat("^", (endPos-startPos)+1), errorClass, err)	
}

// For debugging
func dumpTokens(tokens []cmdToken) {
	for _, token := range tokens {
		log.Printf("%d..%d #%d [%s] '%s'\n", token.startPos, token.endPos,
					 token.argIndex, tokenTypeStrings[token.tokenType],
					 token.token)			
	}
}
