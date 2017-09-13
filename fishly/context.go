package fishly

import (
	"fmt"
	"log"

	"strings"

	"os"

	"net/url"
	"path/filepath"
)

//
// context -- context handling
//

const (
	PathSeparator = "/"
)

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

type ExternalContext interface {
	// Synchronizes state of the external context when internal
	// context is changed. If new context state fails validation,
	// returns an error
	Update(ctx *Context) error

	// Cancels blocked operations which are currently running in
	// corresponding request (cpu-greedy operations should check
	// rq.Cancelled flag)
	Cancel(rq *Request)
}

// Context is the overall state holder for currently executing
// fishly instance
type Context struct {
	// External context used by program currently executing
	External ExternalContext

	// Current prompt set by external context (contains context-
	// specific information such as formatted path). FullPrompt
	// contains full text to be used by readline driver
	Prompt     string
	FullPrompt string

	// Context states history. first is current state,
	// last is "root" state
	states []ContextState

	// Configuration of Context instance
	cfg *Config

	// Customizable ReadLine driver
	rl ReadlineDriver

	// Commands available in the current state
	availableCommands handlerTable

	// Requests state
	cmdParser *cmdTokenParser
	requestId int

	// Exit code. While context is running, set to -1
	exitCode int
}

// Creates new context from a config (and initializes config)
func newContext(cfg *Config, extCtx ExternalContext) (ctx *Context, err error) {
	// Create and setup context object
	ctx = new(Context)
	ctx.External = extCtx
	ctx.cfg = cfg
	ctx.exitCode = -1

	cfg.schema.init()
	ctx.cfg.registerBuiltinHandlers()

	// Initialize readline driver
	ctx.rl, err = ctx.cfg.Readline.Create(ctx)
	if err != nil {
		return nil, err
	}

	// Reload schema (requires rl.Stderr() for errors)
	err = ctx.reloadSchema()
	if err != nil {
		ctx.rl.Close()
		return nil, err
	}

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

// Sets current state of the context as root state
func (ctx *Context) GetCurrentState() *ContextState {
	return &ctx.states[len(ctx.states)-1]
}

// Creates new state which is the copy of the head context state
func (ctx *Context) PushState(isRoot bool) *ContextState {
	var currentState *ContextState
	if len(ctx.states) > 0 {
		currentState = ctx.GetCurrentState()
	} else {
		// First state
		currentState = &ContextState{
			Path:      []string{},
			Variables: map[string]string{},
		}
	}

	state := ContextState{
		Path:      make([]string, len(currentState.Path)),
		Variables: make(map[string]string),
		isNew:     true,
		isRoot:    isRoot,
	}

	// Copy values
	copy(state.Path, currentState.Path)
	for k, v := range currentState.Variables {
		state.Variables[k] = v
	}

	// TODO: cleanup history up to N values

	ctx.states = append(ctx.states, state)
	return ctx.GetCurrentState()
}

func (state *ContextState) URL() *url.URL {
	ctxUrl := new(url.URL)

	ctxUrl.Scheme = "ctx"
	ctxUrl.Path = "/" + filepath.Join(state.Path...)
	for key, value := range state.Variables {
		ctxUrl.Query().Add(key, value)
	}

	return ctxUrl
}

func (state *ContextState) Reset(newPath ...string) {
	state.Path = newPath
	state.Variables = make(map[string]string)
}

// Creates new state from context url. Raises error if URL is invalid
func (ctx *Context) PushStateFromURL(ctxUrl *url.URL, isRoot bool) error {
	if ctxUrl.Scheme != "ctx" {
		return fmt.Errorf("Invalid context state scheme: '%s'", ctxUrl.Scheme)
	}

	path := strings.Split(ctxUrl.Path, PathSeparator)
	if len(path) == 0 {
		return fmt.Errorf("Empty context state path: '%s'", ctxUrl.Path)
	}

	state := ContextState{
		Path:      path[1:],
		Variables: make(map[string]string),
		isNew:     true,
		isRoot:    isRoot,
	}
	for key, value := range ctxUrl.Query() {
		state.Variables[key] = strings.Join(value, ",")
	}

	ctx.states = append(ctx.states, state)
	return nil
}

// internal function that updates context after request has finished
func (ctx *Context) tick() {
	state := ctx.GetCurrentState()
	if !state.isNew {
		return
	}

	// Notify external context about state change with a possibility
	// to update prompt
	err := ctx.External.Update(ctx)
	for err != nil {
		if len(ctx.states) == 0 {
			state = ctx.PushState(true)
			break
		}

		log.Printf("Error in state '%s', rolling back: %s", state.URL().String(), err)

		ctx.states = ctx.states[:len(ctx.states)-1]
		state = ctx.GetCurrentState()
		state.isNew = true

		err = ctx.External.Update(ctx)
	}

	// Updates prompt
	ctx.FullPrompt = ctx.cfg.PromptProgram + " " + ctx.Prompt + ctx.cfg.PromptSuffix

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

// (Re-)loads schema
func (ctx *Context) reloadSchema() error {
	ctx.cfg.schema.reset()

	for _, schemaPath := range ctx.cfg.Schema {
		if !ctx.loadSchema(schemaPath) {
			return fmt.Errorf("Error loading schema file %s", schemaPath)
		}
	}
	return nil
}

func (ctx *Context) loadSchema(fpath string) bool {
	f, err := os.Open(fpath)
	if err != nil {
		log.Fatalln("Error loading schema file %s: %v", fpath, err)
	}
	defer f.Close()

	tokenParser := newParser()
	tokenParser.parseReader(f)
	if tokenParser.LastError != nil {
		// Re-read file to get required line contents and dump parse error
		ctx.dumpLastParserError(f, tokenParser)
		return false
	}

	parser := ctx.cfg.schema.parse(tokenParser)
	if parser.LastError != nil {
		// Re-assemble tokens and dump semantic error
		ctx.dumpLastProcessorError(parser.LastError)
		return false
	}
	return true
}

// Rewinds state steps states back (cd -N)
func (ctx *Context) RewindState(steps int) (err error) {
	index := len(ctx.states) - 1 + steps
	if index < 0 || index >= len(ctx.states) {
		return fmt.Errorf("Invalid context index #%d", index)
	}

	topState := ctx.states[index]
	topState.isNew = true
	if !topState.isRoot {
		ctx.states = append(ctx.states[:index], ctx.states[index+1:]...)
	}

	ctx.states = append(ctx.states, topState)
	return nil
}

// Rewinds states until it finds root state (cd)
func (ctx *Context) rewindStateRoot() (err error) {
	for steps := -1; steps >= -len(ctx.states); steps-- {
		topState := ctx.states[len(ctx.states)+steps-1]
		if topState.isRoot {
			return ctx.RewindState(steps)
		}
	}

	return fmt.Errorf("Cannot find root state")
}

func (ctx *Context) interpolateArgument(arg string) string {
	// TODO: implement argument interpolation
	return arg
}

//
// 'history' builtin command. shows history of context states,
// commands, etc.
//

type historyCmd struct {
}
type historyOpt struct {
	Contexts bool `opt:"c|ctx|contexts,opt"`
	Requests bool `opt:"r|rq|requests,opt"`
}

func (*historyCmd) IsApplicable(ctx *Context) bool {
	return true
}
func (*historyCmd) NewOptions(ctx *Context) interface{} {
	return new(historyOpt)
}
func (*historyCmd) Complete(ctx *Context, rq *CompleterRequest) {
}

func (cmd *historyCmd) Execute(ctx *Context, rq *Request) (err error) {
	options := rq.Options.(*historyOpt)

	ioh, err := rq.StartOutput(ctx, false)
	if err != nil {
		return
	}
	defer ioh.CloseOutput()

	ioh.StartObject("history")
	switch {
	case options.Contexts:
		// Contexts history
		ioh.StartObject("contexts")
		for index, state := range ctx.states {
			ioh.StartObject("context")

			steps := 1 - (len(ctx.states) - index)
			if steps == 0 {
				ioh.WriteFormattedValue("index", "=>", 0)
			} else {
				ioh.WriteRawValue("index", steps)
			}

			ioh.WriteString("url", state.URL().String())

			ioh.EndObject()
		}
		ioh.EndObject()
	case options.Requests:
		// Requests history
		return fmt.Errorf("Not implemented")
	default:
		// Readline history
		return fmt.Errorf("Not implemented")
	}
	ioh.EndObject()

	return nil
}
