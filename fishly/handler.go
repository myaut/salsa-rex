package fishly

import (
	"sort"

	"reflect"

	"strings"
)

type handlerType int

const (
	hdlCommand = iota
	hdlIOPipe
	hdlIOFormatter
	hdlIOSink
)

// Handler describes any handler: command, io pipes, formatters and sinks
type Handler interface {
	// Returns a command-option structure which should be filled in
	// before creating request to handle options and arguments
	// NOTE: it is used to generate help, so no side effects please
	NewOptions(ctx *Context) interface{}

	// Adds auto-complete strings for arguments and options into rq
	Complete(ctx *Context, rq *CompleterRequest)
}

// Helper mixins that can be embedded to avoid implementation of unsupported functions
type HandlerWithoutCompletion struct {
}
type HandlerWithoutOptions struct {
}

type handlerDescriptor struct {
	// Name of the handler
	name string

	// Name of the handler group and order to be shown in help
	group string

	// Index in global descriptor array
	handlerGlobalIndex int

	// Index in type-specific array and type of that array
	handlerLocalType  handlerType
	handlerLocalIndex int
}

type handlerTable map[string]*handlerDescriptor

type optionDescriptor struct {
	// Cached information from structure
	aliases  []string
	argIndex int
	argName  string

	// Lower-layer option & corresponding schema node (if found)
	option cmdOption
	node   *schemaNode
}
type optionDescriptorSlice []optionDescriptor

// Helper for Register* functions -- registers handler in global handler
// array and returns pointer to it
func (cfg *Config) registerHandler(hType handlerType, index int,
	group, name string) *handlerDescriptor {
	globalIndex := len(cfg.handlers)
	cfg.handlers = append(cfg.handlers, handlerDescriptor{
		name:               name,
		group:              group,
		handlerGlobalIndex: globalIndex,
		handlerLocalType:   hType,
		handlerLocalIndex:  index,
	})

	return &cfg.handlers[globalIndex]
}

// Helper for IO registrators -- also saves name of the handler to
// ioHandlers table
func (cfg *Config) registerIOHandler(hType handlerType, index int, group, name string) {
	hdl := cfg.registerHandler(hType, index, group, name)

	if cfg.ioHandlers == nil {
		cfg.ioHandlers = make(handlerTable)
	}
	cfg.ioHandlers[name] = hdl
}

func (cfg *Config) RegisterCommand(cmd Command, group, name string) {
	cfg.registerHandler(hdlCommand, len(cfg.commands), group, name)
	cfg.commands = append(cfg.commands, cmd)
}

func (cfg *Config) RegisterIOPipe(pipe IOPipe, name string) {
	cfg.registerIOHandler(hdlIOPipe, len(cfg.pipes), "pipe", name)
	cfg.pipes = append(cfg.pipes, pipe)
}

func (cfg *Config) RegisterIOFormatter(fmtr IOFormatter, name string) {
	cfg.registerIOHandler(hdlIOFormatter, len(cfg.formatters),
		"formatter", name)
	cfg.formatters = append(cfg.formatters, fmtr)
}

func (cfg *Config) RegisterIOSink(sink IOSink, name string) {
	cfg.registerIOHandler(hdlIOSink, len(cfg.sinks), "sink", name)
	cfg.sinks = append(cfg.sinks, sink)
}

// Registers handlers supplied by fishly
func (cfg *Config) registerBuiltinHandlers() {
	cfg.RegisterCommand(new(helpCmd), "common", "help")
	cfg.RegisterCommand(new(historyCmd), "common", "history")

	json := new(jsonFormatter)
	cfg.RegisterIOFormatter(json, "json")

	trace := new(traceFormatter)
	cfg.RegisterIOFormatter(trace, "_trace")

	cat := new(textFormatter)
	cfg.RegisterIOFormatter(cat, "cat")
	if cfg.DefaultTextFormatter == nil {
		cfg.DefaultTextFormatter = cat
	}

	cat.schema = newTextSchema()
	cfg.schema.handlers["text"] = cat.schema
}

// Finds pointer to the descriptor by name and group or returns nil
func (cfg *Config) findHandlerDescriptor(name, group string) *handlerDescriptor {
	for index, descriptor := range cfg.handlers {
		if descriptor.name == name && descriptor.group == group {
			return &cfg.handlers[index]
		}
	}

	return nil
}

// Finds command corresponding to a descriptor and returns it. If descriptor is not
// a command, returns nil
func (cfg *Config) getCommandFromDescriptor(descriptor *handlerDescriptor) Command {
	if descriptor.handlerLocalType != hdlCommand {
		return nil
	}

	return cfg.commands[descriptor.handlerLocalIndex]
}

// Returns handler object by its descriptor If descriptor
// is invalid, nil is returned
func (cfg *Config) getHandlerFromDescriptor(descriptor *handlerDescriptor) Handler {
	switch descriptor.handlerLocalType {
	case hdlCommand:
		return cfg.commands[descriptor.handlerLocalIndex]
	case hdlIOPipe:
		return cfg.pipes[descriptor.handlerLocalIndex]
	case hdlIOFormatter:
		return cfg.formatters[descriptor.handlerLocalIndex]
	case hdlIOSink:
		return cfg.sinks[descriptor.handlerLocalIndex]
	}

	return nil
}

// Returns options object corresponding to a descriptor. If descriptor
// is invalid or doesn't support options, nil is returned
func (ctx *Context) createOptionsForHandler(descriptor *handlerDescriptor) interface{} {
	handler := ctx.cfg.getHandlerFromDescriptor(descriptor)
	if handler != nil {
		return handler.NewOptions(ctx)
	}

	return nil
}

// Gathers extra info about options sort them and returns as help-friendly
// optionDescriptor slice
func generateOptionDescriptors(optStruct interface{}, command schemaCommand,
	cmdName string) optionDescriptorSlice {

	if optStruct == nil {
		return make(optionDescriptorSlice, 0)
	}

	descriptor := generateOptionStructDescriptor(optStruct, cmdName)
	numOpts, numArgs := len(descriptor.options), len(descriptor.args)

	options := make(optionDescriptorSlice, numOpts+numArgs)
	for optIndex, opt := range descriptor.options {
		options[optIndex].option = opt
		if !opt.hasFlag("count") && opt.field.Type.Kind() != reflect.Bool {
			options[optIndex].argName = strings.ToUpper(opt.field.Name)
		}
	}
	for alias, optIndex := range descriptor.optMap {
		options[optIndex].aliases = append(options[optIndex].aliases, alias)
		if node, ok := command.options[alias]; ok {
			options[optIndex].node = node
		}
	}
	for argIndex0, arg := range descriptor.args {
		var node *schemaNode
		if argIndex0 < len(command.args) {
			node = command.args[argIndex0]
		}

		options[numOpts+argIndex0] = optionDescriptor{
			option:   arg,
			argIndex: argIndex0 + 1,
			argName:  strings.ToUpper(arg.field.Name),
			node:     node,
		}
	}

	sort.Sort(options)
	return options
}

func (od optionDescriptor) findLongestAlias() (longestAlias string) {
	for _, alias := range od.aliases {
		if len(alias) > len(longestAlias) {
			longestAlias = alias
		}
	}
	return
}

func (ods optionDescriptorSlice) Len() int {
	return len(ods)
}

func (ods optionDescriptorSlice) Less(i, j int) bool {
	key1, key2 := ods[i].sortKey(), ods[j].sortKey()

	// Compare options lexicographically
	if key1 == key2 && key1 < 0 {
		return ods[i].aliases[0] < ods[j].aliases[0]
	}

	return key1 < key2
}

func (od optionDescriptor) sortKey() int {
	// Assign weight sort key to prioritize options/arguments:
	// Optional opts -> Required opts -> Args by number -> vararg
	if od.argIndex > 0 {
		if od.option.field.Type.Kind() == reflect.Slice {
			return 1000
		}

		return od.argIndex
	}

	if od.option.hasFlag("opt") {
		return -2
	}

	return -1
}

func (ods optionDescriptorSlice) Swap(i, j int) {
	ods[i], ods[j] = ods[j], ods[i]
}

func (ods optionDescriptorSlice) resolveSchemaNodes(command schemaCommand) {
	// TODO:
}

func (*HandlerWithoutCompletion) Complete(ctx *Context, rq *CompleterRequest) {
}
func (*HandlerWithoutOptions) NewOptions(ctx *Context) interface{} {
	var opt struct{}
	return &opt
}
