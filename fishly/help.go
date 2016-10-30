package fishly 

import (
	"fmt"
	"strings"
	
	"reflect"
)

type helpCmd struct {
	
}

type helpOpt struct {
	Handler string `arg:"1,opt"`
	
	Builtin bool `opt:"builtin,opt"`
}

type helpRq struct {
	ctx *Context
	ioh *IOHandle
	
	verbose bool
	
	lastGroup string
}

func (*helpCmd) IsApplicable(ctx *Context) bool {
	return true
}
func (*helpCmd) NewOptions() interface{} {
	return new(helpOpt)
}
func (*helpCmd) Complete(ctx *Context, option string) []string {
	// TODO
	return []string{}
}

func (cmd *helpCmd) Execute(ctx *Context, rq *Request) (err error) {
	var descriptor *handlerDescriptor
	var builtin string
	options := rq.Options.(*helpOpt)
	
	// If argument is provided, search for descriptor in the tables
	if len(options.Handler) > 0 {
		if options.Builtin {
			if isBuiltin(options.Handler) {
				builtin = options.Handler
			} else {
				return fmt.Errorf("Builtin '%s' is unknown", options.Handler)		
			}
		} else if commandDescriptor, ok := ctx.availableCommands[options.Handler]; ok {
			descriptor = commandDescriptor
		} else if ioDescriptor, ok := ctx.cfg.ioHandlers[options.Handler]; ok {
			descriptor = ioDescriptor
		} else {
			return fmt.Errorf("Handler '%s' is unknown", options.Handler)
		}
	}
	
	// Start output
	ioh, err := rq.StartOutput(ctx, false)
	if err != nil {
		return
	}
	defer ioh.CloseOutput()
	
	// Create request and load help files
	hrq := helpRq {
		ctx: ctx,
		ioh: ioh,
	}
	
	if descriptor != nil {
		hrq.verbose = true
		hrq.writeHandler(descriptor)
	} else if len(builtin) > 0 {
		hrq.verbose = true
		hrq.writeBuiltin(builtin)
	} else {
		for index, _ := range ctx.cfg.handlers {
			descriptor := &ctx.cfg.handlers[index]
			hrq.writeHandler(descriptor)
		}
		for _, builtin := range builtins {
			if strings.HasPrefix(builtin, "-") {
				continue
			}
			
			hrq.writeBuiltin(builtin)
		}
	}
	
	hrq.groupDone()
	return
}

func (rq *helpRq) writeBuiltin(name string) {
	ioh := rq.ioh
	
	if "builtin" != rq.lastGroup {
		rq.groupDone()
  		rq.lastGroup = "builtin"
  		
		ioh.StartObject("handlerGroup")
		ioh.WriteFormattedValue("name", "Builtin", rq.lastGroup)
		ioh.WriteString("type", "commands")
		
		ioh.StartArray("handlers")
	}
	
	basePath := fmt.Sprintf("_builtins.%s", name)
	help := rq.ctx.help.Section(basePath)
	
	ioh.StartObject("handler")
	ioh.WriteString("name", name)
	ioh.WriteString("usage", help.Key("Usage").String())
	
	if rq.verbose {
		ioh.WriteString("help", help.Key("Text").String())
	}
	
	ioh.EndObject()
}

func (rq *helpRq) writeHandler(descriptor *handlerDescriptor) {
	ioh := rq.ioh
	
  	if descriptor.group != rq.lastGroup {
  		// If descriptor has a new group specified, 
  		rq.groupDone()
  		
		ioh.StartObject("handlerGroup")
		ioh.WriteFormattedValue("name", strings.Title(descriptor.group), descriptor.group)
		
		switch descriptor.handlerLocalType {
			case hdlCommand:
				ioh.WriteString("type", "commands")
			default:
				ioh.WriteString("type", "I/O handlers")
		}
		
		ioh.StartArray("handlers")
		rq.lastGroup = descriptor.group
	}
  	
  	basePath := fmt.Sprintf("%s.%s", descriptor.group, descriptor.name)
	options := generateOptionDescriptors(rq.ctx.cfg.createOptionsForHandler(descriptor))
	
	ioh.StartObject("handler")
	ioh.WriteString("name", descriptor.name)
	rq.printUsage(options)
	
	if rq.verbose {
		help := rq.ctx.help.Section(basePath)
		ioh.WriteString("help", help.Key("Text").String())
		
		// Dump options & arguments help
		ioh.StartArray("options")
		for _, od := range options {
			if od.undocumented {
				continue
			}
			
			var optionPath string
			
			ioh.StartObject("option")
			if od.argIndex > 0 {
				ioh.WriteRawValue("index", od.argIndex)
				optionPath = fmt.Sprintf("%s@%d", basePath, od.argIndex)
			} else {
				for _, opt := range od.options {
					ioh.WriteString("alias", fmt.Sprintf("-%s", opt))					
				}
				
				// aliases are sorted, so we use first (usually shortest) option 
				optionPath = fmt.Sprintf("%s-%d", basePath, od.options[0])
			}
			
			if len(od.argName) > 0 {
				// Use argument name from 
				argName := help.Key("ArgName").String()
				if len(argName) == 0 {
					argName = od.argName
				}
				
				ioh.WriteString("arg", od.argName)	
			}
			
			if od.kind == reflect.Slice {
				ioh.WriteFormattedValue("slice", "...", true)
			}
			if od.optional {
				ioh.WriteFormattedValue("optional", "(opt)", true)
			}
			
			ioh.WriteString("defaultValue", fmt.Sprintf("%s", od.defaultVal))
			
			help := rq.ctx.help.Section(optionPath)
			ioh.WriteString("help", help.Key("Text").String())
			
			ioh.EndObject()			
		}
		
		ioh.EndArray()
	}
	
	ioh.EndObject() // /handler
}

func (rq *helpRq) printUsage(optionDescriptors []optionDescriptor) {
	options := make([]string, len(optionDescriptors))
	
	for index, od := range optionDescriptors {
		if od.undocumented {
			continue
		}
		
		var option string
		
		if od.argIndex > 0 {
			option = od.argName
			
			if od.kind == reflect.Slice {
				option = fmt.Sprintf("%s...", option)
			}
		} else {
			option = "-" + strings.Join(od.options, "|-")
			
			if len(od.argName) > 0 {
				option = fmt.Sprintf("%s %s", option, strings.ToUpper(od.argName))	
			}
		}
		
		if od.optional {
			option = fmt.Sprintf("[%s]", option)
		}
		
		options[index] = option
	}
	
	rq.ioh.WriteString("usage", strings.Join(options, " "))
}

func (rq *helpRq) groupDone() {
	if len(rq.lastGroup) > 0 {
		rq.ioh.EndArray()
		rq.ioh.EndObject()
	}
}

