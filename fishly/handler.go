package fishly

import (
	"sort"
	
	"strings"
	"strconv"
	
	"reflect"
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
	NewOptions() interface{}
	
	// Adds auto-complete strings for arguments and options into rq
	Complete(ctx *Context, rq *CompleterRequest)
}

type handlerDescriptor struct {
	// Name of the handler
	name string
	
	// Name of the handler group and order to be shown in help
	group string
	
	// Index in global descriptor array 
	handlerGlobalIndex int
	
	// Index in type-specific array and type of that array
	handlerLocalType handlerType
	handlerLocalIndex int
}

type handlerTable map[string]*handlerDescriptor

type optionDescriptor struct {
	// Index of the field in structure 
	fieldIndex int
	
	options []string
	argIndex int
	kind reflect.Kind
	
	optional bool
	undocumented bool
	
	// Some helpers for help scrapped from structure
	argName string
	defaultVal reflect.Value
}
type optionDescriptorSlice []optionDescriptor

// Helper for Register* functions -- registers handler in global handler 
// array and returns pointer to it 
func (cfg *Config) registerHandler(hType handlerType, index int, 
			group, name string) *handlerDescriptor {
	globalIndex := len(cfg.handlers)
	cfg.handlers = append(cfg.handlers, handlerDescriptor{
		name: name,
		group: group,
		handlerGlobalIndex: globalIndex,
		handlerLocalType: hType,
		handlerLocalIndex: index,
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
	
	stdout := new(stdoutSink)
	cfg.RegisterIOSink(stdout, "stdout")
	if cfg.DefaultSink == nil {
		cfg.DefaultSink = stdout
	}
	
	pager := new(pagerSink)
	cfg.RegisterIOSink(pager, "pager")
	if cfg.DefaultPagerSink == nil {
		cfg.DefaultPagerSink = pager
	}
	
	cat := new(textFormatter)
	cfg.RegisterIOFormatter(cat, "cat")
	if cfg.DefaultTextFormatter == nil {
		cfg.DefaultTextFormatter = cat
	}
	
	color := new(textFormatter)
	color.richText = true
	cfg.RegisterIOFormatter(color, "color")
	if cfg.DefaultRichTextFormatter == nil {
		cfg.DefaultRichTextFormatter = color
	}
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
	
	return nil;
}

// Returns options object corresponding to a descriptor. If descriptor
// is invalid or doesn't support options, nil is returned
func (cfg *Config) createOptionsForHandler(descriptor *handlerDescriptor) interface{} {
	handler := cfg.getHandlerFromDescriptor(descriptor)
	if handler != nil {
		return handler.NewOptions()
	}
	
	return nil;
}

// Processes options structure using reflect and generates descriptors for it
func generateOptionDescriptors(options interface{}) []optionDescriptor {
	if options == nil {
		// No options supported by this handler
		return make([]optionDescriptor, 0)
	}
	
	optionsType := reflect.TypeOf(options).Elem()
	optionsVal := reflect.ValueOf(options).Elem()
	
	descriptors := make(optionDescriptorSlice, 0, optionsType.NumField())
	
	for fieldIdx := 0 ; fieldIdx < optionsType.NumField() ; fieldIdx++ {
		field := optionsType.Field(fieldIdx)
		
		var descriptor = &optionDescriptor{
			fieldIndex: fieldIdx,
			argIndex: 0,
			kind: field.Type.Kind(),
		}
		
		var flags []string
		
		if optTag := field.Tag.Get("opt"); len(optTag) > 0 {
			// This is option in format opt:"alias1|alias2,opt"
			flags = strings.Split(optTag, ",")
			descriptor.options = strings.Split(flags[0], "|")
			sort.Strings(descriptor.options)
			
			if descriptor.kind != reflect.Bool {
				descriptor.argName = strings.ToUpper(field.Name)
			}
			
			flags = flags[1:]
		} else if argTag := field.Tag.Get("arg"); len(argTag) > 0 {
			flags = strings.Split(argTag, ",")
			descriptor.argIndex, _ = strconv.Atoi(flags[0])
			descriptor.argName = strings.ToUpper(field.Name)
			
			flags = flags[1:]
		}
		
		for _, flag := range flags {
			switch flag {
				case "opt":
					descriptor.optional = true
				case "undoc":
					descriptor.undocumented = true
			}
		}
		
		descriptor.defaultVal = optionsVal.Field(fieldIdx)
		
		descriptors = append(descriptors, *descriptor)
	}
	
	sort.Sort(descriptors)
	return descriptors
}

func (od optionDescriptor) matchOption(opt string) bool { 
	if len(od.options) == 0{
		return false
	}
	
	// Check if one of the aliases in descriptor option matches
	for _, alias := range od.options {
		if alias == opt {
			return true
		}
	}
	return false
}

func (od optionDescriptor) findLongestAlias() (longestAlias string) { 
	for _, alias := range od.options {
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
		return ods[i].options[0] < ods[j].options[0] 
	}
	
	return key1 < key2
}

func (od optionDescriptor) sortKey() int { 
	// Assign weight sort key to prioritize options/arguments:
	// Optional opts -> Required opts -> Args by number -> vararg
	if od.argIndex > 0 {
		if od.kind == reflect.Slice {
			return 1000
		}
		
		return od.argIndex
	}
	if od.optional {
		return -2
	}
	
	return -1
}

func (ods optionDescriptorSlice) Swap(i, j int) { 
	ods[i], ods[j] = ods[j], ods[i]
}