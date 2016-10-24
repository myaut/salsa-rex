package fishly

import (
	readline "gopkg.in/readline.v1"
)

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

type Context struct {
	// Context states history. first is current state,
	// last is "root" state
	states []ContextState
	
	// Configuration of CLI instance
	cfg *Config
	rl *readline.Instance
	
	// Commands available in the current state
	availableCommands []Command
}

// Sets current state of the context as root state
func (ctx *Context) GetCurrentState() *ContextState {
	return &ctx.states[len(ctx.states)-1]
}

// Creates new state
func (ctx *Context) PushState(isRoot bool) *ContextState {
	currentState := ctx.GetCurrentState()
	
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
	ctx.availableCommands = make([]Command, 0, len(ctx.cfg.Commands))
	for _, command := range ctx.cfg.Commands {
		if command.IsApplicable(ctx) {
			ctx.availableCommands = append(ctx.availableCommands, command)
		}
	}
	
	state.isNew = false
}

