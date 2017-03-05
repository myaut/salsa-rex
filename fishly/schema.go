package fishly

import (
	"fmt"
)

type schemaTypeClass int

const (
	varUnknown schemaTypeClass = iota
	
	varVariant
	varString
	varBoolean
	varInteger
	varFloat
	
	varArray
	varEnum
	varStruct
	varUnion
)
const varUserType = varArray

var typeClassNames []string = []string{
	"",
	"variant",
	"string",
	"bool",
	"int",
	"float",
	"array",
	"enum",
	"struct",
	"union",
}

type schemaNodeId int
type schemaNode struct {
	nodeId schemaNodeId
	
	name string 
	data interface{}
	
	// fast-path node for help text
	help string
}

type schemaValue struct {
	node *schemaNode
		
	value string
}

type schemaVariable struct {
	node *schemaNode
	
	typeClass schemaTypeClass
	compoundType *schemaNode
	
	// token provides slice of values as raw value
	isList bool
	
	// for special values -- parse them too
	values map[string]*schemaNode
}

type schemaStruct struct {
	node *schemaNode
	variables []*schemaNode
	
	isUnion bool
}

type schemaEnum struct {
	node *schemaNode
	values map[string]*schemaNode
}

type schemaArray struct {
	node *schemaNode
	elementType *schemaNode
}

type schemaCommand struct {
	node *schemaNode
	
	// Usage string for builtins and overrides
	usage string
	
	// Points to output type (can be nil if command have no output)
	output *schemaNode
	
	// Options and arguments that can be passed to this command
	options map[string]*schemaNode
	args []*schemaNode
}

type schemaCommandOption struct {
	node *schemaNode
	argName string
}

type schemaHandler interface {
	HandleCommand(parser *schemaParser, node *schemaNode, cmd *cmdCommandTokenWalker)
}

// maps type names to locally defined types
type schemaNodeTable map[string]schemaNodeId

type schemaParser struct {
	schema *schemaRoot
	
	typeTableStack []schemaNodeTable
	
	LastError *cmdProcessorError
}

type schemaRoot struct {
	// Pointers to all nodes
	nodes []*schemaNode
	
	// Stack of globally accessible types and commands
	types schemaNodeTable
	commands schemaNodeTable
	
	// Customizable handlers for special nodes and commands
	handlers map[string]schemaHandler
}

func (schema *schemaRoot) init() {
	schema.handlers = make(map[string]schemaHandler)
	schema.reset()
}

func (schema *schemaRoot) reset() {
	schema.nodes = make([]*schemaNode, 0, 128)
	
	schema.types = make(schemaNodeTable)
	schema.commands = make(schemaNodeTable)
}

// Recursively parses schema commands and blocks
func (schema *schemaRoot) parse(tokenParser *cmdTokenParser) (*schemaParser) {
	parser := new(schemaParser)
	parser.schema = schema
	parser.typeTableStack = []schemaNodeTable{schema.types}
	
	walker := tokenParser.createRootWalker()
	for parser.LastError == nil {
		cmd := walker.nextCommand()
		if cmd == nil {
			break
		}
		
		token := cmd.getFirstToken()
		switch token.token {
			case "type":
				parser.parseType(cmd)
			case "command":
				parser.parseCommand(cmd)
			default:
				parser.LastError = cmd.newCommandError(fmt.Errorf(
						"Unknown top-level command"))
		} 
	}
	
	return parser
}

func (parser *schemaParser) newNode(name string) (*schemaNode) {
	node := new(schemaNode)
	node.nodeId = schemaNodeId(len(parser.schema.nodes))
	node.name = name
	
	parser.schema.nodes = append(parser.schema.nodes, node)
	return node
}

func (parser *schemaParser) parseType(cmd *cmdCommandTokenWalker) {
	var typeOpt struct {
		Name string `arg:"1"`
		TypeClass string `arg:"2"`
		ExtraType string `arg:"3,opt"`
	}
	if !parser.tryArgParse(cmd, &typeOpt) {
		return
	}
	
	// Check if this type is already defined somewhere in stack
	if parser.findType(typeOpt.Name) != nil {
		parser.LastError = cmd.newPositionalArgumentError(fmt.Errorf(
				"Type '%s' already defined", typeOpt.Name), 1)
		return
	}
	
	// Check compound type class
	typeClass := parser.getTypeClass(typeOpt.TypeClass)
	if typeClass < varUserType {
		parser.LastError = cmd.newPositionalArgumentError(fmt.Errorf(
			"Unexpected type '%s' in type definition", typeOpt.TypeClass), 2)
		return
	}
	
	// Create node and insert to local table
	node := parser.newNode(typeOpt.Name)
	table := parser.typeTableStack[len(parser.typeTableStack)-1]
	table[typeOpt.Name] = node.nodeId
	
	// Parse real compound type (may require subblock)
	switch typeClass {
		case varArray:
			parser.parseArray(node, cmd, typeOpt.ExtraType)
		case varStruct:
			parser.parseStruct(node, cmd, false)
		case varEnum:
			parser.parseEnum(node, cmd)
		case varUnion:
			parser.parseStruct(node, cmd, true)
	}
}

func (parser *schemaParser) parseArray(node *schemaNode, cmd *cmdCommandTokenWalker, extraType string) {
	elementType := parser.findType(extraType)
	if elementType == nil {
		parser.LastError = cmd.newPositionalArgumentError(fmt.Errorf("Array element type '%s' is not defined", 
			elementType), 3)
		return
	}
	
	node.data = schemaArray {
		node: node,
		elementType: elementType,
	}
	parser.parseNodeCommands(node, cmd.nextBlock())
	return
}

func (parser *schemaParser) parseEnum(node *schemaNode, cmd *cmdCommandTokenWalker) {
	values := parser.parseValues(node, cmd.nextBlock())
	if len(values) == 0 {
		parser.LastError = cmd.newCommandError(fmt.Errorf(
				"Missing values in enum compound"))
		return 	
	}
	
	node.data = schemaEnum {
		node: node,
		values: values,
	}
}

func (parser *schemaParser) parseStruct(node *schemaNode, cmd *cmdCommandTokenWalker, isUnion bool)  {
	block := cmd.nextBlock()
	if block == nil {
		parser.LastError = cmd.newCommandError(fmt.Errorf(
				"Struct/union type requires subblock with subtypes"))
		return
	}
	
	parser.growStack()
	
	compound := schemaStruct {isUnion: isUnion}
	for parser.LastError == nil {
		cmd := block.nextCommand()
		if cmd == nil {
			break
		}
		
		token := cmd.getFirstToken()
		switch token.token {
			case "type":
				parser.parseType(cmd)
			case "var":
				varNode := parser.parseVar(cmd)
				if varNode != nil {
					compound.variables = append(compound.variables, varNode)
				}
			case "default":
				if isUnion {
					varNode := parser.newNode("")
					varNode.data = schemaVariable {
						node: varNode,
					}
					compound.variables = append(compound.variables, varNode)
				} else {
					parser.LastError = cmd.newCommandError(fmt.Errorf(
							"Only union supports default tagless tokens"))
				}
			default:
				parser.parseNodeCommand(node, cmd)
		}
	}
	
	parser.popStack()
	node.data = compound
}

func (parser *schemaParser) parseVar(cmd *cmdCommandTokenWalker) (*schemaNode) {
	var varOpt struct {
		IsList bool `opt:"l|list,opt"`
		Name string `arg:"1"`
		TypeName string `arg:"2,opt"`
	}
	if !parser.tryArgParse(cmd, &varOpt) {
		return nil
	}
	if len(varOpt.TypeName) == 0 {
		varOpt.TypeName = varOpt.Name
	} 
	
	// Resolve variable type
	typeClass := parser.getTypeClass(varOpt.TypeName)
	var compoundType *schemaNode 
	if typeClass >= varUserType {
		parser.LastError = cmd.newPositionalArgumentError(fmt.Errorf(
				"Unexpected compound type '%s' in var definition", varOpt.TypeName), 2)
	} else if typeClass == varUnknown {
		compoundType = parser.findType(varOpt.TypeName)
		if compoundType == nil {
			parser.LastError = cmd.newPositionalArgumentError(fmt.Errorf(
					"Variable type '%s' is not defined", varOpt.TypeName), 2)
			return nil
		}
		
		switch compoundType.data.(type) {
			case (schemaArray):
				typeClass = varArray
			case (schemaStruct):
				typeClass = varStruct
			case (schemaEnum):
				typeClass = varEnum
			default:
				compoundType = nil
		}
	}
	
	node := parser.newNode(varOpt.Name)
	variable := schemaVariable {
		node: node,
		typeClass: typeClass,
		compoundType: compoundType,
		isList: varOpt.IsList,
		values: parser.parseValues(node, cmd.nextBlock()),
	}
	node.data = variable
	return node
}

func (parser *schemaParser) parseDefaultUnionVar(cmd *cmdCommandTokenWalker) schemaTypeClass {
	var varOpt struct {
		TypeName string `arg:"1,opt"`
	}
	if !parser.tryArgParse(cmd, &varOpt) {
		return varUnknown
	}
	
	typeClass := parser.getTypeClass(varOpt.TypeName)
	if typeClass == varUnknown || typeClass >= varUserType {
		parser.LastError = cmd.newPositionalArgumentError(fmt.Errorf(
					"Variable type '%s' is not a builtin type", varOpt.TypeName), 1)
	}
		
	return typeClass
}

func (parser *schemaParser) parseValues(node *schemaNode, block *cmdBlockTokenWalker) map[string]*schemaNode {
	values := make(map[string]*schemaNode)
	
	for block != nil && parser.LastError == nil {
		cmd := block.nextCommand()
		if cmd == nil {
			break
		}
		
		token := cmd.getFirstToken()
		switch token.token {
			case "value":
				var valueOpt struct {
					Value string `arg:"1"`
				}
				if parser.tryArgParse(cmd, &valueOpt) {
					valueNode := parser.newNode("")
					valueNode.data = schemaValue{
						node: node,
						value: valueOpt.Value,						
					}
					values[valueOpt.Value] = valueNode
					
					parser.parseNodeCommands(valueNode, cmd.nextBlock())
				}
			default:
				parser.parseNodeCommand(node, cmd)
		}
	}
	
	return values
}

func (parser *schemaParser) parseNodeCommand(node *schemaNode, cmd *cmdCommandTokenWalker) {
	token := cmd.getFirstToken()
	if token.token == "help" {
		parser.parseHelp(node, cmd)
		return 
	}
	
	if len(parser.schema.handlers) > 0 {
		commandName := token.token
		if handler, ok := parser.schema.handlers[commandName]; ok {
			handler.HandleCommand(parser, node, cmd)
		} else {
			parser.LastError = cmd.newCommandError(fmt.Errorf(
					"Unknown command %s: not a builtin or custom handler", commandName))
		}
	}
}

func (parser *schemaParser) parseHelp(node *schemaNode, cmd *cmdCommandTokenWalker) {
	var helpOpt struct {
		Help string `arg:"1"`
	}
	if parser.tryArgParse(cmd, &helpOpt) {
		node.help = helpOpt.Help
	}
}

func (parser *schemaParser) parseNodeCommands(node *schemaNode, block *cmdBlockTokenWalker) {
	if block == nil {
		return
	}
	
	for parser.LastError == nil {
		cmd := block.nextCommand()
		if cmd == nil {
			break
		}
		parser.parseNodeCommand(node, cmd)
	}
}

func (parser *schemaParser) getTypeClass(typeClassName string) schemaTypeClass {
	for typeClass, name := range typeClassNames {
		if name == typeClassName {
			return schemaTypeClass(typeClass)
		}
	}
	
	return varUnknown
}

func (parser *schemaParser) tryArgParse(cmd *cmdCommandTokenWalker, optStruct interface{}) bool {
	argParser := cmd.parseArgs(optStruct, func(s string) string {return s})
	
	if argParser.LastError != nil {
		parser.LastError = cmd.newArgParserError(argParser)
		return false
	}
	
	return true
}

func (parser *schemaParser) findType(name string) (*schemaNode) {
	for i := len(parser.typeTableStack)-1 ; i >= 0; i-- {
		table := parser.typeTableStack[i]
		if index, ok := table[name]; ok {
			return parser.schema.nodes[index]
		}
	}
	
	return nil
}

func (parser *schemaParser) growStack() {
	parser.typeTableStack = append(parser.typeTableStack, make(schemaNodeTable))
}

func (parser *schemaParser) popStack() schemaNodeTable {
	topIndex := len(parser.typeTableStack)-1
	top := parser.typeTableStack[topIndex]
	
	parser.typeTableStack = parser.typeTableStack[:topIndex]
	return top
}

func (parser *schemaParser) parseCommand(cmd *cmdCommandTokenWalker) {
	var cmdOpt struct {
		Name string `arg:"1"`
		Output string `arg:"2,opt"`
		Usage string `opt:"u|usage,opt"`
	}
	if !parser.tryArgParse(cmd, &cmdOpt) {
		return
	}
	
	if _, ok := parser.schema.commands[cmdOpt.Name]; ok {
		parser.LastError = cmd.newPositionalArgumentError(fmt.Errorf("Command '%s' already defined", cmdOpt.Name), 2)
		return
	}
	
	node := parser.newNode(cmdOpt.Name)
	command := schemaCommand{
		node: node,
		usage: cmdOpt.Usage,
		options: make(map[string]*schemaNode),
	}
	if len(cmdOpt.Output) != 0 {
		command.output = parser.findType(cmdOpt.Output)
		if command.output == nil {
			parser.LastError = cmd.newPositionalArgumentError(fmt.Errorf(
					"Command '%s' defines output '%s', but such type doesn't exist", 
					cmdOpt.Name, cmdOpt.Output), 2)
			return 
		}
	} 
	
	parser.parseCommandDefinition(node, &command, cmd.nextBlock())
	
	node.data = command
	parser.schema.commands[node.name] = node.nodeId
}

func (parser *schemaParser) parseCommandDefinition(node *schemaNode, 
				command *schemaCommand, block *cmdBlockTokenWalker) {
	if block == nil {
		return
	}
	
	for parser.LastError == nil {
		cmd := block.nextCommand()
		if cmd == nil {
			break
		}
		
		token := cmd.getFirstToken()
		switch token.token {
			case "help":
				parser.parseHelp(node, cmd)
			case "opt":
				parser.parseCommandOption(node, command, cmd)
			default:
				parser.LastError = cmd.newCommandError(fmt.Errorf(
						"Unexpected command in command context"))
		}
	}
}
	
func (parser *schemaParser) parseCommandOption(node *schemaNode, 
				command *schemaCommand, cmd *cmdCommandTokenWalker) {
	var optOpt struct {
		Option string `opt:"o|opt,opt"`
		ArgIndex int `opt:"a|arg,opt"`
		ArgName string `opt:"n|name,opt"`
	}
	if !parser.tryArgParse(cmd, &optOpt) {
		return
	}
	
	var optNode *schemaNode 
	opt := schemaCommandOption {
		argName: optOpt.ArgName,
	}
	if optOpt.ArgIndex >= 1 {
		argIndex := optOpt.ArgIndex
		for len(command.args) < argIndex {
			command.args = append(command.args, nil)
		}
		
		optNode = parser.newNode(optOpt.ArgName)
		command.args[argIndex-1] = optNode
	} else if len(optOpt.Option) > 0 {
		optNode = parser.newNode(optOpt.Option)
		command.options[optOpt.Option] = optNode
	} else {
		parser.LastError = cmd.newCommandError(fmt.Errorf(
				"Command '%s' defines option, but neither -opt nor -arg specified", node.name))
		return
	}
	
	optNode.data = opt
	
	// Now parse help for argument
	block := cmd.nextBlock()
	if block == nil {
		return
	}
	
	for parser.LastError == nil {
		cmd := block.nextCommand()
		if cmd == nil {
			break
		}
		
		token := cmd.getFirstToken()
		switch token.token {
			case "help":
				parser.parseHelp(optNode, cmd)
			default:
				parser.LastError =  cmd.newCommandError(fmt.Errorf(
						"Unexpected command '%s' in argument context"))
		}
	}
}
				
func (root *schemaRoot) findType(name string) *schemaNode {
	if index, ok := root.types[name]; ok {
		return root.nodes[index]
	}
	return nil
}
func (root *schemaRoot) findCommand(name string) *schemaNode {
	if index, ok := root.commands[name]; ok {
		return root.nodes[index]
	}
	return nil
}

func (node *schemaNode) getHelp() string {
	if node != nil {
		return node.help 
	}
	return ""
}

func (node *schemaNode) toCommand() (cmd schemaCommand) {
	if node != nil {
		switch node.data.(type) {
			case (schemaCommand):
				return node.data.(schemaCommand)
		}
	}
	return 
}

func (node *schemaNode) toCommandOption() (cmd schemaCommandOption) {
	if node != nil {
		switch node.data.(type) {
			case (schemaCommandOption):
				return node.data.(schemaCommandOption)
		}
	}
	return 
}

