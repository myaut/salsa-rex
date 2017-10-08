package fishly

import (
	"io"
	"time"

	"strings"

	"reflect"
)

type Completer struct {
	ctx *Context
}

type completerRq struct {
	ctx    *Context
	parser *cmdTokenParser

	handler  *handlerDescriptor
	options  []string
	argIndex int

	startIndex int
	endIndex   int

	prefix  string
	newLine [][]rune
}

type CompleterRequest struct {
	// Unique id of request
	Id int

	// Argument index (if autocompleting argument, set to >=1, if
	// autocompleting option argument, set to 0)
	ArgIndex int

	// Longest option alias if auto-completing option
	Option string

	// Known user input (not applicable for arguments consisting of
	// multiple tokens)
	Prefix string

	rq *completerRq
}

// Adds option to a autocomplete request (it should be a full string, without
// cutting suggested prefix)
func (rq *CompleterRequest) AddOption(option string) {
	rq.rq.tryAddOption(option)
}

// Adds multiple options
func (rq *CompleterRequest) AddOptions(options ...string) {
	for _, option := range options {
		rq.rq.tryAddOption(option)
	}
}

// Tries to parse options that already specified (prior to completing option)
func (rq *CompleterRequest) GetExistingOptions() interface{} {
	state := rq.rq
	if state.handler == nil || state.startIndex >= state.endIndex {
		return nil
	}

	opt := state.handler.handler.NewOptions(state.ctx)
	if opt != nil {
		parser := &cmdArgumentParser{
			command:     state.handler.name,
			args:        state.parser.Tokens[state.startIndex:state.endIndex],
			interpolate: state.ctx.interpolateArgument,
		}
		parser.parse(opt)
	}

	return opt
}

// Returns deadline for auto-completer request
func (rq *CompleterRequest) GetDeadline() time.Time {
	return time.Now().Add(700 * time.Millisecond)
}

func (completer *Completer) Do(line []rune, pos int) (newLine [][]rune, length int) {
	if len(line) == 0 {
		// Special case when no token is specified
		root := completerRq{ctx: completer.ctx}
		return root.complete(cmdToken{
			tokenType: tCommand,
		})
	}

	// Parse line and ignore its errors. Find token we're trying to complete.
	// Also find handler which is specified on the left side of it
	parser := newParser()
	parser.parseLine(string(line))
	if parser.LastError == io.EOF {
		// If we're have unexpected EOF, complete with expected delimiter
		if parser.lastDelimiter == 0 {
			return [][]rune{}, 0
		}

		return [][]rune{[]rune{parser.lastDelimiter}}, 1
	}

	root := completerRq{
		ctx:    completer.ctx,
		parser: parser,
	}
	state := root
	for tokenIndex, token := range parser.Tokens {
		switch token.tokenType {
		case tCommandSeparator:
			state = root
		case tRedirection:
			state.handler, _ = completer.ctx.cfg.ioHandlers[token.token]
			state.startIndex = tokenIndex + 1
		case tCommand:
			state.handler, _ = completer.ctx.availableCommands[token.token]
			state.startIndex = tokenIndex + 1
		case tOption:
			state.options = append(state.options, token.token)
		case tRawArgument, tSingleQuotedArgument, tDoubleQuotedArgument:
			state.argIndex = token.argIndex
		default:
			// Not supported
			return
		}

		if token.startPos <= pos && pos <= token.endPos {
			// TODO: prefix for complex arguments is determined differently
			prefixLen := pos - token.startPos
			if prefixLen <= len(token.token) {
				token.token = token.token[:prefixLen]
			}
			state.endIndex = tokenIndex
			return state.complete(token)
		}
	}

	state.argIndex++
	state.endIndex = len(parser.Tokens)
	return state.complete(cmdToken{
		tokenType: tRawArgument,
		argIndex:  state.argIndex,
	})
}

// Tries to complete incomplete token if handler and list of options
// that are already specified is known
func (rq *completerRq) complete(token cmdToken) ([][]rune, int) {
	rq.prefix = token.token

	if rq.handler == nil {
		switch token.tokenType {
		case tCommand:
			rq.completeHandler(rq.ctx.availableCommands)
		case tRedirection:
			// TODO: if sink specified, we shouldn't perform completion
			rq.completeHandler(rq.ctx.cfg.ioHandlers)
		}
	} else {
		optionDescriptors := generateOptionDescriptors(
			rq.ctx.createOptionsForHandler(rq.handler), schemaCommand{},
			rq.handler.name)
		if token.tokenType == tOption {
			rq.completeOption(optionDescriptors)
		} else {
			rq.completeArgument(optionDescriptors)
		}
	}

	return rq.newLine, len(rq.prefix)
}

// Tries to add option to a list of completion possibilities if
// prefix matches
func (rq *completerRq) tryAddOption(option string) {
	if len(rq.prefix) > 0 {
		if !strings.HasPrefix(option, rq.prefix) {
			return
		}

		option = option[len(rq.prefix):]
	}

	// TODO: remove duplicates

	rq.newLine = append(rq.newLine, []rune(option))
}

// Walks over handler table and adds all handler names to the completer request
func (rq *completerRq) completeHandler(table handlerTable) {
	for name, _ := range table {
		rq.tryAddOption(name)
	}
}

// Completes option name (after '-')
func (rq *completerRq) completeOption(optionDescriptors []optionDescriptor) {
	for _, od := range optionDescriptors {
		if od.argIndex > 0 {
			continue
		}

		rq.tryAddOption(od.findLongestAlias())
	}
}

// Complete option or handler argument
func (rq *completerRq) completeArgument(optionDescriptors []optionDescriptor) {
	// Ignore argument that are already entered. We do not care
	// about order of arguments and user mistakes here. Also keep primary alias here
	optionsWithArgs := make(map[string]string)
	for _, od := range optionDescriptors {
		if od.argIndex > 0 || od.option.field.Type.Kind() == reflect.Bool {
			// This option doesn't require argument (or not an option)
			continue
		}

		for _, alias := range od.aliases {
			optionsWithArgs[alias] = od.findLongestAlias()
		}
	}

	argIndex := rq.argIndex
	for _, option := range rq.options {
		if _, ok := optionsWithArgs[option]; ok {
			// One of the arguments is used as option argument
			argIndex--
		}
	}

	crq := CompleterRequest{
		rq:     rq,
		Id:     rq.ctx.requestId,
		Prefix: rq.prefix,
	}
	rq.ctx.requestId++

	if argIndex == -1 || argIndex == 0 {
		// Auto-completing option argument, take last option (if
		// one specified)
		if len(rq.options) == 0 {
			return
		}

		if alias, ok := optionsWithArgs[rq.options[0]]; ok {
			crq.Option = alias
		} else {
			// Unknown option is specified
			return
		}
	} else if argIndex > 0 {
		crq.ArgIndex = argIndex
	} else {
		// Syntax error: not all options which required arguments matched to
		// arguments
		return
	}

	// Call handler's completer to find variants and add them to list
	handler := rq.ctx.cfg.getHandlerFromDescriptor(rq.handler)
	handler.Complete(rq.ctx, &crq)
}
