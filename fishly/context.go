package fishly

import (
	"fmt"
	
	"github.com/go-ini/ini"	
	
	// readline "gopkg.in/readline.v1"
	readline "github.com/chzyer/readline"
)

//
// context -- context handling 
//


// Context state represents current execution state. It is very similar
// to path on filesystem, but may have set variables with names.
// Current conteext state determines availability of the commands
// and their behaviour
type ContextState struct {
	// Hierarchial components of the path
	Path []string
	// Extra context variables
	Variables map[string]string
	
	// Is this state a new state (should we recompute 
	// prompt & list of available commands)
	isNew bool
	
	// Root state are not evicted and handled specially when 'cd'
	// is issued
	isRoot bool
}

// Context is the overall state holder for currently executing 
// fishly instance
type Context struct {
	// Context states history. first is current state,
	// last is "root" state
	states []ContextState
	
	// Configuration of Context instance
	cfg *Config
	
	// ReadLine instance
	rl *readline.Instance
	
	// Help contents for help command
	help *ini.File
	
	// Stylesheet for text formatter
	style *textStyleNode
	
	// Commands available in the current state
	availableCommands handlerTable
	
	// List of requests that are currently handling
	requests []*Request
	requestIndex int
	
	// For exit
	running bool
	exitCode int
}


// Sets current state of the context as root state
func (ctx *Context) GetCurrentState() *ContextState {
	return &ctx.states[len(ctx.states)-1]
}

// Creates new state
func (ctx *Context) PushState(isRoot bool) *ContextState {
	var currentState *ContextState
	if len(ctx.states) > 0 {
		currentState = ctx.GetCurrentState()
	} else {
		// First state
		currentState = &ContextState{
			Path: []string{},
			Variables: map[string]string{},
		}
	}
	
	state := ContextState{
		Path: make([]string, 0, len(currentState.Path)),
		Variables: make(map[string]string),
		isNew: true,
		isRoot: isRoot,
	}
	ctx.states = append(ctx.states, state)
	
	// Copy values
	copy(state.Path, currentState.Path)
	for k, v := range currentState.Variables {
		state.Variables[k] = v
	}
	
	// TODO: cleanup history up to N values
	
	return ctx.GetCurrentState()
}

// internal function that updates context after request has finished
func (ctx *Context) tick() {
	state := ctx.GetCurrentState()
	if !state.isNew {
		return
	}
	
	// Updates prompt
	prompt := ""
	if len(state.Path) != 0 || len(state.Variables) != 0 {
		prompt = ctx.cfg.PromptFormatter(ctx)
	} 
	
	ctx.rl.SetPrompt(ctx.cfg.PromptProgram + " " + prompt + ctx.cfg.PromptSuffix)
	
	// Re-compute list of available commands
	ctx.availableCommands = make(handlerTable)
	for index, _ := range ctx.cfg.handlers {
		descriptor := &ctx.cfg.handlers[index]
		command := ctx.cfg.getCommandFromDescriptor(descriptor)
		
		if command == nil || !command.IsApplicable(ctx) {
			continue
		}
		
		ctx.availableCommands[descriptor.name] = descriptor
	}
	
	state.isNew = false
}

// (Re-)loads configuration files (help, style)
func (ctx *Context) reload() (err error) {
	// Load help files
	helpFiles := make([]interface{}, len(ctx.cfg.Help))
	for index, helpFile := range ctx.cfg.Help {
		helpFiles[index] = helpFile
	}
	
	ctx.help, err = ini.Load(helpFiles[0], helpFiles[1:]...)
	if err != nil {
		return 
	}
	
	// Create root node and load text styles
	ctx.style = newTextStyleNode()
	return LoadStyleSheet(ctx.cfg.StyleSheet, ctx.style)	
}

func (ctx *Context) rewindState(where string) (err error) {
	return fmt.Errorf("not implemented")
}