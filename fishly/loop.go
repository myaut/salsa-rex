package fishly

import (
	"io"
	"log"
	
	"strings"
	
	readline "gopkg.in/readline.v1"
)

type ContextPromptFormatter func(ctx *Context) string

type UserConfig struct {
	// As defined in readline.Config
	HistoryFile string
	HistoryLimit int
	DisableAutoSaveHistory bool
	VimMode bool
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
	
	// List of supported/registered commands
	Commands []Command
}

type cliInstance struct {
	cfg *Config
	rl *readline.Instance
	
	ctx Context
}

func Run(cfg *Config) {
	// runs main command loop
	ctx, err := newContext(cfg)
	if err != nil {
		log.Fatalln(err)
	}
	defer ctx.rl.Close()
	
	log.SetOutput(ctx.rl.Stderr())
	log.Println(ctx.cfg.MOTD)
	
	for {
		line, err := ctx.rl.Readline()
		if err == readline.ErrInterrupt {
			continue
		} else if err == io.EOF {
			break
		}
		
		line = strings.TrimSpace(line)
		tokens, err := ctx.parseLine(line)
		if err != nil {
			log.Printf("ERROR: %s", err)
			continue
		}
		
		dumpTokens(tokens)
		
		// If context has changed, perform necessary updates in context
		ctx.tick()
	}
}

func newContext(cfg *Config) (*Context, error) {
	rl, err := readline.NewEx(&readline.Config{
		HistoryFile: cfg.UserConfig.HistoryFile,
		HistoryLimit: cfg.UserConfig.HistoryLimit,
		DisableAutoSaveHistory: cfg.UserConfig.DisableAutoSaveHistory,
		VimMode: cfg.UserConfig.VimMode,
	})
	if err != nil {
		return nil, err
	}
	
	ctx := new(Context)
	ctx.cfg = cfg
	ctx.rl = rl
	
	// Create first root state
	ctx.states = make([]ContextState, 1)
	state := ctx.GetCurrentState() 
	state.Path = make([]string, 0)
	state.Variables = make(map[string]string)
	state.isNew = true
	state.isRoot = true
	
	// Initialize context
	ctx.tick()
	
	return ctx, nil
}

// For debugging
func dumpTokens(tokens []cmdToken) {
	for _, token := range tokens {
		log.Printf("%d..%d %d %s\n", token.startPos, token.endPos,
					 token.tokenType, token.token)			
	}
}
