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
	Group   string `opt:"g|group,opt"`

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
func (*helpCmd) NewOptions(ctx *Context) interface{} {
	return new(helpOpt)
}
func (*helpCmd) Complete(ctx *Context, rq *CompleterRequest) {
	switch rq.ArgIndex {
	case 1:
		for _, handler := range ctx.cfg.handlers {
			rq.AddOption(handler.name)
		}
	}
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
		} else if len(options.Group) > 0 {
			descriptor = ctx.cfg.findHandlerDescriptor(options.Handler, options.Group)
			if descriptor == nil {
				return fmt.Errorf("Handler '%s.%s' is unknown", options.Group, options.Handler)
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
	hrq := helpRq{
		ctx: ctx,
		ioh: ioh,
	}

	ioh.StartObject("handlerGroups")
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
			if strings.HasPrefix(builtin, "_") {
				continue
			}

			hrq.writeBuiltin(builtin)
		}
	}
	hrq.groupDone()
	ioh.EndObject()

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

		ioh.StartObject("handlers")
	}

	schema := rq.ctx.cfg.schema.findCommand(name)
	if schema == nil {
		basePath := fmt.Sprintf("_builtins.%s", name)
		schema = rq.ctx.cfg.schema.findCommand(basePath)
	}

	ioh.StartObject("handler")
	ioh.WriteString("name", name)
	ioh.WriteString("usage", schema.toCommand().usage)

	if rq.verbose {
		ioh.WriteString("help", schema.getHelp())
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

		ioh.StartObject("handlers")
		rq.lastGroup = descriptor.group
	}

	schema := rq.ctx.cfg.schema.findCommand(descriptor.name)
	if schema == nil {
		basePath := fmt.Sprintf("%s.%s", descriptor.group, descriptor.name)
		schema = rq.ctx.cfg.schema.findCommand(basePath)
	}

	optDescriptors := generateOptionDescriptors(
		rq.ctx.createOptionsForHandler(descriptor), schema.toCommand(),
		descriptor.name)

	ioh.StartObject("handler")
	ioh.WriteString("name", descriptor.name)
	rq.printUsage(optDescriptors)

	if rq.verbose {
		ioh.WriteString("help", schema.getHelp())

		// Dump options & arguments help
		ioh.StartObject("options")

		for _, od := range optDescriptors {
			if od.option.hasFlag("undoc") {
				continue
			}

			ioh.StartObject("option")
			if od.argIndex > 0 {
				ioh.WriteRawValue("index", od.argIndex)
			} else {
				aliases := make([]string, len(od.aliases))
				for i, opt := range od.aliases {
					aliases[i] = fmt.Sprintf("-%s", opt)
				}
				ioh.WriteFormattedValue("aliases", strings.Join(aliases, "|"), od.aliases)
			}

			if len(od.argName) > 0 {
				argName := od.argName
				if od.node != nil {
					// Use argument redefined argument name from schema
					opt := od.node.toCommandOption()
					if len(opt.argName) > 0 {
						argName = opt.argName
					}
				}

				ioh.WriteString("argName", argName)
			}

			if od.option.field.Type.Kind() == reflect.Slice {
				ioh.WriteFormattedValue("slice", "...", true)
			}
			if od.option.hasFlag("opt") {
				ioh.WriteFormattedValue("optional", "(opt)", true)
			}
			if od.option.hasFlag("count") {
				ioh.WriteFormattedValue("counting", "*", true)
			}

			// Print default value but only if it is different than default initialization
			printDefaultVal := true
			switch od.option.field.Type.Kind() {
			case reflect.Slice, reflect.Array:
				printDefaultVal = (od.option.fieldValue.Len() > 0)
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				printDefaultVal = (od.option.fieldValue.Int() != 0)
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				printDefaultVal = (od.option.fieldValue.Uint() > 0)
			case reflect.Bool:
				printDefaultVal = od.option.fieldValue.Bool()
			}
			if printDefaultVal {
				ioh.WriteRawValue("defaultValue", od.option.fieldValue)
			}

			ioh.WriteString("help", od.node.getHelp())

			ioh.EndObject()
		}

		ioh.EndObject()
	}

	ioh.EndObject() // /handler
}

func (rq *helpRq) printUsage(optionDescriptors []optionDescriptor) {
	options := make([]string, len(optionDescriptors))

	for index, od := range optionDescriptors {
		if od.option.hasFlag("undoc") {
			continue
		}

		var option string

		if od.argIndex > 0 {
			option = od.argName

			if od.option.field.Type.Kind() == reflect.Slice {
				option = fmt.Sprintf("%s...", option)
			}
		} else {
			option = "-" + strings.Join(od.aliases, "|-")

			if len(od.argName) > 0 {
				option = fmt.Sprintf("%s %s", option, strings.ToUpper(od.argName))
			}
		}

		if od.option.hasFlag("opt") {
			option = fmt.Sprintf("[%s]", option)
		}
		if od.option.hasFlag("count") {
			option = fmt.Sprintf("%s *", option)
		}

		options[index] = option
	}

	rq.ioh.WriteString("usage", strings.Join(options, " "))
}

func (rq *helpRq) groupDone() {
	if len(rq.lastGroup) > 0 {
		rq.ioh.EndObject()
		rq.ioh.EndObject()
	}
}
