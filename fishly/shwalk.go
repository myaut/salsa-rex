package fishly

import (
	"reflect"

	"strconv"
	"strings"

	"fmt"

	"bytes"
)

type cmdTokenWalker struct {
	// All parsers tokens
	tokens []cmdToken

	// Currently handled command
	command    *cmdCommand
	tokenRange cmdTokenRange
}
type cmdBlockTokenWalker struct {
	cmdTokenWalker
}
type cmdCommandTokenWalker struct {
	cmdTokenWalker

	// Indexes in command value for currently handled command
	redirIndex, blockIndex int
}
type cmdRedirTokenWalker struct {
	cmdCommandTokenWalker
}

type cmdOptionInterpolate func(s string) string

type cmdOption struct {
	field      reflect.StructField
	fieldValue reflect.Value

	specified bool
	flags     []string
}

type cmdOptionStructDescriptor struct {
	options []cmdOption
	optMap  map[string]int

	args []cmdOption
}

type cmdArgumentParser struct {
	LastError error

	// interpolate function is used to interpolate raw and double quoted arguments
	interpolate cmdOptionInterpolate

	// Argument state and their index
	command string
	args    []cmdToken
	index   int

	doValidate bool
}

func (parser *cmdTokenParser) createRootWalker() *cmdBlockTokenWalker {
	walker := new(cmdBlockTokenWalker)
	walker.tokens = parser.Tokens
	walker.tokenRange = cmdTokenRange{start: 0, end: len(walker.tokens)}

	if len(walker.tokens) > 0 {
		walker.command = walker.tokens[0].command
	}
	return walker
}

func (walker *cmdTokenWalker) set(tokens []cmdToken,
	command *cmdCommand, tokenRange cmdTokenRange) {
	walker.tokens = tokens
	walker.command = command
	walker.tokenRange = tokenRange
}

func (walker *cmdBlockTokenWalker) advanceCommand(tokenRange cmdTokenRange) {
	walker.command = nil

	// Find next command -- may need to omit command separator
	for nextIndex := tokenRange.end + 1; nextIndex <= walker.tokenRange.end; nextIndex++ {
		if len(walker.tokens) > nextIndex {
			token := walker.tokens[nextIndex]
			if token.tokenType == tCommandSeparator {
				continue
			}

			walker.command = token.command
		}

		break
	}
}

// Picks next command from subblock / global walker
func (blockWalker *cmdBlockTokenWalker) nextCommand() *cmdCommandTokenWalker {
	command := blockWalker.command
	if command == nil {
		return nil
	}

	blockWalker.advanceCommand(command.command)

	walker := new(cmdCommandTokenWalker)
	walker.set(blockWalker.tokens, command, command.command)
	return walker
}

func (walker *cmdCommandTokenWalker) getFirstToken() cmdToken {
	return walker.tokens[walker.tokenRange.start]
}

// Slices arguments for currently handled command/redir and returns it
func (walker *cmdCommandTokenWalker) getArguments() []cmdToken {
	command := walker.command
	if command == nil {
		return nil
	}

	// If arguments were not found, zero index is kept for start. Otherwise,
	// we got inclusive interval which we want slice
	if command.args.start == 0 {
		return nil
	}
	return walker.tokens[command.args.start : command.args.end+1]
}

func (parentWalker *cmdCommandTokenWalker) nextRedirection() *cmdRedirTokenWalker {
	command := parentWalker.command
	if command == nil {
		return nil
	}
	if parentWalker.redirIndex >= len(command.redirections) {
		return nil
	}

	// Pick redirection command definition (which may have subblocks)
	redirRange := command.redirections[parentWalker.redirIndex]
	redirection := parentWalker.tokens[redirRange.start]

	parentWalker.redirIndex++

	// Return copy of myself, but with altered command now pointing ot redirection
	walker := new(cmdRedirTokenWalker)
	walker.set(parentWalker.tokens, redirection.command, redirRange)
	return walker
}

func (parentWalker *cmdCommandTokenWalker) nextBlock() *cmdBlockTokenWalker {
	command := parentWalker.command
	if command == nil {
		return nil
	}
	if parentWalker.blockIndex >= len(command.blocks) {
		return nil
	}

	blockRange := command.blocks[parentWalker.blockIndex]
	parentWalker.blockIndex++

	// Setup command pointer to first command in block similar to createRootParser
	// We do not care about command after last as tBlockEnd will have nil pointer
	command = nil
	commandIndex := blockRange.start + 1
	if commandIndex <= blockRange.end {
		command = parentWalker.tokens[commandIndex].command
	}

	walker := new(cmdBlockTokenWalker)
	walker.set(parentWalker.tokens, command, blockRange)
	return walker
}

// Reassembles command lines corresponding to a token and its command and
// returns their content and position at which updated token is located
// (indexing starts at last line location)
func (walker *cmdCommandTokenWalker) reassembleLines(index int) (lines []string, startPos, endPos int) {
	lineBuf := bytes.NewBuffer([]byte{})

	startIndex := walker.command.command.start
	prevToken := walker.tokens[startIndex]
	currentLineNumber := 0

	// Find last index to show some context for tokeens
	lastIndex := index
	for lastIndex <= walker.command.command.end {
		lastToken := walker.tokens[lastIndex]
		if lastToken.tokenType == tBlockBegin {
			break
		}
		lastIndex++
	}

	for tokenIndex := startIndex; tokenIndex <= lastIndex; tokenIndex++ {
		if tokenIndex >= len(walker.tokens) {
			break
		}

		token := walker.tokens[tokenIndex]
		if currentLineNumber > 0 && token.line > currentLineNumber {
			lines = append(lines, lineBuf.String())
			lineBuf.Reset()
		}
		currentLineNumber = token.line

		// Now convert token back to its original representation
		if tokenIndex == index {
			startPos = lineBuf.Len()
		}

		var prefix, suffix, text string
		text = token.token
		switch token.tokenType {
		case tCommandSeparator:
			text = ";"
			fallthrough
		case tBlockBegin, tBlockEnd:
			prefix = " "
			suffix = " "
		case tOption:
			prefix = " -"
		case tSingleQuotedArgument, tDoubleQuotedArgument:
			switch token.tokenType {
			case tSingleQuotedArgument:
				prefix = `'`
			case tDoubleQuotedArgument:
				prefix = `"`
			}
			suffix = prefix
			fallthrough
		case tRawArgument:
			if prevToken.argIndex < token.argIndex {
				prefix = " " + prefix
			}
		case tRedirection:
			prefix = " | "
		case tFileRedirection:
			prefix = " > "
		case tShellRedirection:
			prefix = " ! "
		}

		lineBuf.WriteString(prefix)
		lineBuf.WriteString(text)
		lineBuf.WriteString(suffix)

		if tokenIndex == index {
			endPos = lineBuf.Len()
		}
		prevToken = token
	}

	lines = append(lines, lineBuf.String())
	return
}

func (walker *cmdCommandTokenWalker) parseArgs(optStruct interface{},
	interpolate cmdOptionInterpolate) *cmdArgumentParser {

	parser := &cmdArgumentParser{
		command:     walker.getFirstToken().token,
		args:        walker.getArguments(),
		interpolate: interpolate,
		doValidate:  true,
	}

	parser.LastError = parser.parse(optStruct)
	return parser
}

func (parser *cmdArgumentParser) parse(optStruct interface{}) (err error) {
	if reflect.TypeOf(optStruct).Kind() != reflect.Ptr {
		return fmt.Errorf("invalid type of options struct, pointer expected")
	}
	if reflect.ValueOf(optStruct).IsNil() {
		if len(parser.args) == 0 {
			return nil
		}
		return fmt.Errorf("arguments and options are not accepted in this context")
	}

	// Base argument index where arguments (not optvals) start
	baseArgIndex := 1
	// Have we parsed arguments already and no longer accept options?
	optMode := true

	descriptor := generateOptionStructDescriptor(optStruct, parser.command)
	for parser.index = 0; parser.index < len(parser.args); parser.index++ {
		token := parser.args[parser.index]
		var opt *cmdOption
		switch token.tokenType {
		case tOption:
			if !optMode {
				return fmt.Errorf("unexpected option after arguments")
			}
			if optIndex, ok := descriptor.optMap[token.token]; ok {
				opt = &descriptor.options[optIndex]
			} else {
				return fmt.Errorf("unknown option")
			}

			if !opt.setSpecified() {
				// This option requires an argument
				optTokenIndex := parser.index
				parser.index++
				value := parser.assembleArgument()
				if parser.LastError != nil {
					parser.index = optTokenIndex
					return parser.LastError
				}

				err = opt.setValue(value)
				if err != nil {
					return
				}

				baseArgIndex++
			}
		case tRawArgument, tSingleQuotedArgument, tDoubleQuotedArgument:
			if len(descriptor.args) == 0 {
				return fmt.Errorf("unexpected argument")
			}

			optMode = false
			argIndex := token.argIndex - baseArgIndex
			if argIndex >= len(descriptor.args) {
				// Try to append to last argument (if it is slice), if it is not,
				// we will fail on specified check
				argIndex = len(descriptor.args) - 1
			}
			if argIndex < 0 {
				return fmt.Errorf("invalid argument index %d", argIndex)
			}
			opt = &descriptor.args[argIndex]

			value := parser.assembleArgument()
			err = opt.setValue(value)
		default:
			err = fmt.Errorf("unexpected token, only options and arguments expected")
		}

		if err != nil {
			break
		}
	}

	if parser.doValidate {
		parser.validate(descriptor)
	}
	return
}

// Assembles multiple tokens of argument types with same argIndex into
// single string and returns it. Also performs error checking if no
// arguments/tokens is specified
func (parser *cmdArgumentParser) assembleArgument() string {
	if parser.index >= len(parser.args) {
		parser.LastError = fmt.Errorf("argument is expected, got EOL")
		return ""
	}

	argIndex := parser.args[parser.index].argIndex
	buf := bytes.NewBuffer([]byte{})
	numTokens := 0

loop:
	for parser.index < len(parser.args) {
		token := parser.args[parser.index]

		// TODO: support for "raw" (non-interpolable) arguments
		arg := token.token
		switch token.tokenType {
		case tRawArgument, tDoubleQuotedArgument:
			arg = parser.interpolate(arg)
			fallthrough
		case tSingleQuotedArgument:
			if token.argIndex != argIndex {
				break loop
			}
			buf.WriteString(arg)
			numTokens++
		default:
			break loop
		}

		parser.index++
	}

	parser.index--
	if numTokens == 0 {
		parser.LastError = fmt.Errorf("argument is expected")
		return ""
	}
	return buf.String()
}

func generateOptionStructDescriptor(optStruct interface{}, command string) cmdOptionStructDescriptor {
	var descriptor cmdOptionStructDescriptor
	descriptor.optMap = make(map[string]int)

	optionsType := reflect.TypeOf(optStruct).Elem()
	optionsVal := reflect.ValueOf(optStruct).Elem()
	descriptor.generateOptionStructDescriptor(optionsType, optionsVal, command)

	return descriptor
}

func (descriptor *cmdOptionStructDescriptor) generateOptionStructDescriptor(
	optionsType reflect.Type, optionsVal reflect.Value, command string) {

	for fieldIdx := 0; fieldIdx < optionsType.NumField(); fieldIdx++ {
		var opt *cmdOption

		fieldOpt := cmdOption{
			field:      optionsType.Field(fieldIdx),
			fieldValue: optionsVal.Field(fieldIdx),
		}

		if optTag := fieldOpt.field.Tag.Get("opt"); len(optTag) > 0 {
			flags := descriptor.parseFlags(optTag, command)
			if len(flags) == 0 {
				continue
			}

			// Insert options into structure
			index := len(descriptor.options)
			descriptor.options = append(descriptor.options, fieldOpt)
			opt = &descriptor.options[index]

			// Save index pointer to an option
			aliases := strings.Split(flags[0], "|")
			for _, optAlias := range aliases {
				descriptor.optMap[optAlias] = index
			}

			opt.flags = flags[1:]
		} else if argTag := fieldOpt.field.Tag.Get("arg"); len(argTag) > 0 {
			flags := descriptor.parseFlags(argTag, command)
			if len(flags) == 0 {
				continue
			}

			argIndex, _ := strconv.Atoi(flags[0])

			// Fill arguments with stubs
			for argIndex > len(descriptor.args) {
				descriptor.args = append(descriptor.args, cmdOption{})
			}

			// And then fill one field with acquired data
			opt := &descriptor.args[argIndex-1]
			*opt = fieldOpt
			opt.flags = flags[1:]
		} else if fieldOpt.field.Type.Kind() == reflect.Struct {
			// Recurse into embedded types
			descriptor.generateOptionStructDescriptor(fieldOpt.field.Type,
				fieldOpt.fieldValue, command)
		}
	}
}

func (descriptor *cmdOptionStructDescriptor) parseFlags(tag, command string) []string {
	// Parses option tag in following format:
	// 		tag := flags [';' flags]...
	// 		flags := [command '=']  flag [',' flag]...
	// and pick proper set of flags based on command name

	flagSets := strings.Split(tag, ";")
	for _, flagSet := range flagSets {
		eqPos := strings.Index(flagSet, "=")
		if eqPos > 0 {
			if command != flagSet[:eqPos] {
				continue // this flag set is for a different command
			}
			flagSet = flagSet[eqPos+1:]
		}

		return strings.Split(flagSet, ",")
	}

	// Not found matching flagset
	return nil
}

// Checks that all necessary options were specified
func (parser *cmdArgumentParser) validate(descriptor cmdOptionStructDescriptor) (err error) {
	for optIndex, opt := range descriptor.options {
		if opt.isMissing() {
			aliases := descriptor.getOptionAliases(optIndex)
			return fmt.Errorf("missing one of the options: %s",
				strings.Join(aliases, "|"))
		}
	}

	for argIndex, arg := range descriptor.args {
		if arg.isMissing() {
			return fmt.Errorf("missing argument #%d", argIndex+1)
		}
	}

	return
}

// We rarely need list of options (help and error checking), so we do not cache
// them and instead recover list of option aliases from map
func (descriptor cmdOptionStructDescriptor) getOptionAliases(optIndex int) []string {
	aliases := make([]string, 0, 3)
	for alias, index := range descriptor.optMap {
		if index == optIndex {
			aliases = append(aliases, alias)
		}
	}
	return aliases
}

func (opt *cmdOption) hasFlag(flag string) bool {
	for _, flag2 := range opt.flags {
		if flag2 == flag {
			return true
		}
	}
	return false
}

func (opt *cmdOption) isMissing() bool {
	return !opt.specified && !opt.hasFlag("opt")
}

// For options -- sets as specified and returns true if options doesn't need
// option value
func (opt *cmdOption) setSpecified() bool {
	kind := opt.field.Type.Kind()
	switch kind {
	case reflect.Bool:
		opt.fieldValue.SetBool(true)
	case reflect.Int:
		if !opt.hasFlag("count") {
			return false
		}
		opt.fieldValue.SetInt(opt.fieldValue.Int() + 1)
	default:
		return false
	}

	opt.specified = true
	return true
}

// Sets value of structure using reflect. Also supports adding to slice
// values (if opt is slice)
func (opt *cmdOption) setValue(value string) error {
	fieldType := opt.field.Type
	var isSlice bool
	if fieldType.Kind() == reflect.Slice {
		isSlice = true
		fieldType = fieldType.Elem()
	} else {
		if opt.specified {
			return fmt.Errorf("option or argument already specified")
		}
	}

	switch fieldType.Kind() {
	case reflect.String:
		if isSlice {
			opt.fieldValue.Set(reflect.Append(opt.fieldValue, reflect.ValueOf(value)))
		} else {
			opt.fieldValue.SetString(value)
		}

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		intVal, err := strconv.ParseInt(value, 0, fieldType.Bits())
		if err != nil {
			return fmt.Errorf("integer is required, %s", err)
		}

		if isSlice {
			v := reflect.ValueOf(intVal)
			switch fieldType.Kind() {
			case reflect.Int8:
				v = reflect.ValueOf(int8(intVal))
			case reflect.Int16:
				v = reflect.ValueOf(int16(intVal))
			case reflect.Int32:
				v = reflect.ValueOf(int32(intVal))
			case reflect.Int:
				v = reflect.ValueOf(int(intVal))
			}

			opt.fieldValue.Set(reflect.Append(opt.fieldValue, v))
		} else {
			opt.fieldValue.SetInt(intVal)
		}
	case reflect.Uint, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		uintVal, err := strconv.ParseUint(value, 0, fieldType.Bits())
		if err != nil {
			return fmt.Errorf("unsigned integer is required, %s", err)
		}

		if isSlice {
			v := reflect.ValueOf(uintVal)
			switch fieldType.Kind() {
			case reflect.Uint8:
				v = reflect.ValueOf(uint8(uintVal))
			case reflect.Uint16:
				v = reflect.ValueOf(uint16(uintVal))
			case reflect.Uint32:
				v = reflect.ValueOf(uint32(uintVal))
			case reflect.Uint:
				v = reflect.ValueOf(uint(uintVal))
			}

			opt.fieldValue.Set(reflect.Append(opt.fieldValue, v))
		} else {
			opt.fieldValue.SetUint(uintVal)
		}
	case reflect.Float32, reflect.Float64:
		floatVal, err := strconv.ParseFloat(value, fieldType.Bits())
		if err != nil {
			return fmt.Errorf("floating point is required, %s", err)
		}

		if isSlice {
			v := reflect.ValueOf(floatVal)
			switch fieldType.Kind() {
			case reflect.Float32:
				v = reflect.ValueOf(float32(floatVal))
			case reflect.Float64:
				v = reflect.ValueOf(float64(floatVal))
			}

			opt.fieldValue.Set(reflect.Append(opt.fieldValue, v))
		} else {
			opt.fieldValue.SetFloat(floatVal)
		}
	default:
		return fmt.Errorf("unsupported argument type %s", fieldType.Name())
	}

	opt.specified = true
	return nil
}
