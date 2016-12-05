package fishly

import (
	"io"
	"fmt"
	"log"
	
	"os"
	"os/signal"
		
	"strings"
	"net/url"
	
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
	
	// URL used as initial context state
	InitContextURL string
}

type Config struct {
	UserConfig
	
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
	
	// Default sink and formatters
	DefaultSink IOSink
	DefaultPagerSink IOSink
	DefaultRichTextFormatter IOFormatter
	DefaultTextFormatter IOFormatter
}

func (cfg *Config) Run(extCtx ExternalContext) int {
	// runs main command loop
	ctx, err := newContext(cfg, extCtx)
	if ctx.rl != nil {
		defer ctx.rl.Close()	
	}
	if err != nil {
		log.Fatalln(err)
	}
	
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
		for ctx.running && ctx.hasMoreRequests {
			ctx.processRequest()
		}
		
	}
	
	return ctx.exitCode
}

func newContext(cfg *Config, extCtx ExternalContext) (*Context, error) {
	// Create and setup context object
	ctx := new(Context)
	ctx.External = extCtx
	ctx.cfg = cfg
	ctx.reload()
	
	// Create readline instance
	rl, err := readline.NewEx(&readline.Config{
		HistoryFile: cfg.UserConfig.HistoryFile,
		HistoryLimit: cfg.UserConfig.HistoryLimit,
		DisableAutoSaveHistory: cfg.UserConfig.DisableAutoSaveHistory,
		VimMode: cfg.UserConfig.VimMode,
		AutoComplete: &Completer{
			ctx: ctx,
		},
	})
	if err != nil {
		return nil, err
	}
	
	ctx.rl = rl
	ctx.running = true
	ctx.cfg.registerBuiltinHandlers()
	
	// Some redirections 
	log.SetOutput(ctx.rl.Stderr())
	
	// Create first root state
	ctx.states = make([]ContextState, 0, 1)
	ctx.PushState(true)
	
	if len(cfg.UserConfig.InitContextURL) > 0 {
		ctxUrl, err := url.Parse(cfg.UserConfig.InitContextURL) 
		if err != nil {
			return nil, err
		}
		err = ctx.PushStateFromURL(ctxUrl, true)
		if err != nil {
			return nil, err
		}
	}
	ctx.tick()
	
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

func (ctx *Context) parseRequests(line string) bool {
	if len(line) == 0 {
		return true
	}
	
	parser := ctx.parseLine(line)
	if parser.LastError != nil {
		ctx.dumpLastError(parser.Position, parser.Position, line, "Parser", parser.LastError)
		return false
	}
	
	// dumpTokens(parser.Tokens)
	
	ctx.cmdProcessor = ctx.newCommandProcessor(parser.Tokens)
	ctx.hasMoreRequests = true
	return true
}

func (ctx *Context) processRequest() (err error) {
	processor := ctx.cmdProcessor
	rq := processor.nextCommand()
	if processor.LastError != nil {
		line, basePos := processor.assembleLine()
		
		ctx.dumpLastError(basePos, len(line)-1, line, 
			"Syntax", processor.LastError)
		ctx.hasMoreRequests = false
		return
	}
	if rq == nil {
		ctx.hasMoreRequests = false
		return
	}
	
	// Assign request id	
	rq.Id = ctx.requestId
	ctx.requestId++
	
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
	
	// Setup ^C handler. 
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func(){
	    sigNum := <- c
	    if sigNum == os.Interrupt {
	    	rq.Cancelled = true
	    	ctx.External.Cancel(rq)
	    	log.Print("INTERRUPTED")
	    	
	    	// Reset to default handler so following ^C will kill app 
	    	signal.Reset(os.Interrupt)
	    }
	}()
	defer func() {
		close(c)
		signal.Reset(os.Interrupt)
	}()
	
	return rq.command.Execute(ctx, rq)
}

func (ctx *Context) executeBuiltinRequest(rq *Request) (error) {
	switch rq.commandName {
		case "cd":
			if rq.Options != nil {
				steps := rq.Options.(int)
				return ctx.rewindState(steps)
			}
			
			return ctx.rewindStateRoot()
		case "_reload":
			return ctx.reload()
		case "exit":
			if rq.Options != nil {
				ctx.exitCode = rq.Options.(int)
			}
			ctx.running = false
			return nil
	}
	return fmt.Errorf("Not implemented")
}

func (ctx *Context) runAutoExec() {
	if !ctx.parseRequests(ctx.cfg.UserConfig.AutoExec) {
		log.Fatalln("Parse error in autoexec command, exiting...")
	}
	
	for ctx.running && ctx.hasMoreRequests {
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
