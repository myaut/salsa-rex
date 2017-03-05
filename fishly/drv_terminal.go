package fishly

import (
	"os"
	"os/signal"
	
	"log"
	
	readline "github.com/chzyer/readline"
)

// drv_terminal -- driver for fishly for reading commands from terminal. Uses
// readline to read from terminal and SIGINT handler to interrupt commands

// cliInterruptHandler handles Ctrl-C while command is executing

type CLIInterruptHandlerFactory struct {
}

type cliInterruptHandler struct {
	ctx *Context
	rq *Request
	
	c chan os.Signal
}

func (*CLIInterruptHandlerFactory) Create(ctx *Context, rq *Request) CancelHandler {
	handler := &cliInterruptHandler {
		ctx: ctx,
		rq: rq,
		c: make(chan os.Signal, 1),
	}
	signal.Notify(handler.c, os.Interrupt)
	return handler
}

func (handler *cliInterruptHandler) Wait() {
	sigNum := <- handler.c
    if sigNum == os.Interrupt {
    	handler.rq.Cancelled = true
    	handler.ctx.External.Cancel(handler.rq)
    	log.Print("Interrupt")
    	
    	// Reset to default handler so following ^C will kill app 
    	signal.Reset(os.Interrupt)
    }
}

func (handler *cliInterruptHandler) Reset() {
	close(handler.c)
	signal.Reset(os.Interrupt)
}

// readline driver reads from terminal, allows to auto-complete, etc.

type ReadlineConfig struct {
	// As defined in readline.Config
	HistoryFile string
	HistoryLimit int
	DisableAutoSaveHistory bool
	VimMode bool
	
	// Program used as pager for long outputs. Should retain colorized outputs
	Pager string
}

type CLIReadlineFactory struct {
	Config ReadlineConfig
}

func (factory *CLIReadlineFactory) Create(ctx *Context) (ReadlineDriver, error) {
	// Create readline instance
	rl, err := readline.NewEx(&readline.Config{
		HistoryFile: factory.Config.HistoryFile,
		HistoryLimit: factory.Config.HistoryLimit,
		DisableAutoSaveHistory: factory.Config.DisableAutoSaveHistory,
		VimMode: factory.Config.VimMode,
		AutoComplete: &Completer{
			ctx: ctx,
		},
	})
	if err != nil {
		return nil, err
	}
	
	factory.registerTerminalIO(ctx.cfg)
	return rl, nil
}

func (factory *CLIReadlineFactory) registerTerminalIO(cfg *Config) {
	stdout := new(stdoutSink)
	cfg.RegisterIOSink(stdout, "stdout")
	if cfg.DefaultSink == nil {
		cfg.DefaultSink = stdout
	}
	
	pager := new(pagerSink)
	pager.command = factory.Config.Pager
	cfg.RegisterIOSink(pager, "pager")
	if cfg.DefaultPagerSink == nil {
		cfg.DefaultPagerSink = pager
	}
	
	text := new(textFormatter)
	text.richText = true
	cfg.RegisterIOFormatter(text, "text")
	if cfg.DefaultRichTextFormatter == nil {
		cfg.DefaultRichTextFormatter = text
	}
	
	text.schema = cfg.schema.handlers["text"].(*textSchema)
}

