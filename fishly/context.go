package fishly

import (
	"fmt"
	"log"

	"strings"

	"os"

	"reflect"

	"net/url"
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

	// Is new state incomplete (variables need to be re-synced from external
	// context). State is incomplete until reset is called
	isIncomplete bool

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
	if !cfg.KeepLogOutput {
		log.SetOutput(ctx.rl.Stderr())
	}

	// Create first root state
	ctx.states = make([]ContextState, 0, 1)
	ctx.PushState(true).Reset()

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

	// Always re-initialize external context first (save) before tick
	ctx.syncExternalVariables(false)
	ctx.tick()

	return ctx, nil
}

// Sets current state of the context as root state
func (ctx *Context) GetCurrentState() *ContextState {
	return &ctx.states[len(ctx.states)-1]
}

// Helper function which creates copy of the state on top of the current state
func (ctx *Context) pushStateCopy(isRoot, isIncomplete bool, oldState *ContextState) *ContextState {
	state := ContextState{
		Path:         make([]string, len(oldState.Path)),
		Variables:    make(map[string]string),
		isNew:        true,
		isRoot:       isRoot,
		isIncomplete: isIncomplete,
	}

	// Copy values
	copy(state.Path, oldState.Path)
	for k, v := range oldState.Variables {
		state.Variables[k] = v
	}

	// TODO: cleanup history up to N values

	ctx.states = append(ctx.states, state)
	return ctx.GetCurrentState()
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

	return ctx.pushStateCopy(isRoot, true, currentState)
}

func (state *ContextState) URL() *url.URL {
	ctxUrl := new(url.URL)

	ctxUrl.Scheme = "ctx"
	ctxUrl.Path = PathSeparator + strings.Join(state.Path, PathSeparator)

	queryValues := make(url.Values)
	for key, value := range state.Variables {
		queryValues.Add(key, value)
	}
	ctxUrl.RawQuery = queryValues.Encode()

	return ctxUrl
}

func (state *ContextState) Reset(newPath ...string) *ContextState {
	state.Path = newPath
	state.Variables = make(map[string]string)
	state.isIncomplete = false
	return state
}

// Creates new state from context url. Raises error if URL is invalid
func (ctx *Context) PushStateFromURL(ctxUrl *url.URL, isRoot bool) error {
	if ctxUrl.Scheme != "ctx" {
		return fmt.Errorf("Invalid context state scheme: '%s'", ctxUrl.Scheme)
	}
	if len(ctxUrl.Host) > 0 {
		return fmt.Errorf("Context state shouldn't have a host, got '%s'", ctxUrl.Host)
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

func (ctx *Context) syncExternalVariables(doLoad bool) {
	state := ctx.GetCurrentState()
	value := reflect.ValueOf(ctx.External)
	if value.Kind() == reflect.Ptr {
		value = reflect.Indirect(value)
	}

	extType := value.Type()
	var pathPrepend []string
fieldLoop:
	for fieldIdx := 0; fieldIdx < extType.NumField(); fieldIdx++ {
		// Check for ctxvar type tag defining context variable
		field := extType.Field(fieldIdx)
		fieldValue := value.Field(fieldIdx)
		varName, hasTag := field.Tag.Lookup("ctxvar")
		if !hasTag {
			pathName, hasPath := field.Tag.Lookup("ctxpath")
			if !hasPath {
				continue
			}

			pathElements := strings.Split(pathName, PathSeparator)
			if doLoad {
				if fieldValue.Bool() && len(pathElements) > len(pathPrepend) {
					pathPrepend = pathElements
				}
			} else {
				// If current path starts with with whatever specified in
				// ctxpath value, set corresponding boolean to true
				if len(pathElements) > len(state.Path) {
					fieldValue.SetBool(false)
					continue fieldLoop
				}

				for i, el := range pathElements {
					if el != state.Path[i] {
						fieldValue.SetBool(false)
						continue fieldLoop
					}
				}
				fieldValue.SetBool(true)
			}
			continue
		}

		// Check for default value (or use type-zero value)
		defaultStr := field.Tag.Get("default")
		mapValueInterface, hasMVI := state.Variables[varName]

		if doLoad {
			// Load variable from external context
			actualStr := fmt.Sprint(fieldValue.Interface())
			if len(defaultStr) == 0 {
				defaultStr = fmt.Sprint(reflect.Zero(field.Type).Interface())
			}

			if actualStr == defaultStr {
				// Delete variables that are set to default value in ext
				// context struct (if they are set)
				if hasMVI {
					delete(state.Variables, varName)
				}
			} else {
				// Update variables that are changed
				state.Variables[varName] = actualStr
			}
		} else {
			// Save variables into external context
			valueStr := defaultStr
			if hasMVI {
				valueStr = mapValueInterface
			}

			if len(valueStr) > 0 {
				fmt.Sscan(valueStr, fieldValue.Addr().Interface())
			} else {
				fieldValue.Set(reflect.Zero(field.Type))
			}
		}
	}

	if len(pathPrepend) > 0 {
		state.Path = append(pathPrepend, state.Path...)
	}
}

// internal function that updates context after request has finished
func (ctx *Context) tick() {
	state := ctx.GetCurrentState()
	if !state.isNew {
		return
	}

	// First, load/save variables from state. On incomplete states (left
	// after PushState() in Execute() of the command), load state variables
	// from external context
	if state.isIncomplete {
		ctx.syncExternalVariables(true)
	}

	// Notify external context about state change with a possibility
	// to update prompt
	err := ctx.External.Update(ctx)
	for err != nil {
		if len(ctx.states) == 0 {
			state = ctx.PushState(true).Reset()
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
	state.isIncomplete = false
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
		log.Fatalf("Error loading schema file %s: %v", fpath, err)
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

// Restores state saved earlier with GetCurrentState()
func (ctx *Context) RestoreState(state *ContextState) *ContextState {
	return ctx.pushStateCopy(state.isRoot, false, state)
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
	ctx.syncExternalVariables(false)
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
