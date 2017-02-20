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
	node interface{}
	
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
	
	// multi-variable is a special flag for fields which can be listed 
	// more than once without using arrays (but json should produce them!)
	multiVar bool
	
	// for special values -- parse them too
	values []*schemaNode
}

type schemaStruct struct {
	node *schemaNode
	variables []*schemaNode
}

type schemaEnum struct {
	node *schemaNode
	values []*schemaNode
}

type schemaUnion struct {
	node *schemaNode
	types []*schemaNode
}

type schemaArray struct {
	node *schemaNode
	elementType *schemaNode
}

type schemaHandler interface {
	HandleCommand(node *schemaNode, cmd *cmdCommandTokenWalker)
}

// maps type names to locally defined types
type schemaTypeTable map[string]schemaNodeId

type schemaParser struct {
	ctx *Context
	
	typeTableStack []schemaTypeTable
	
	LastError error
	LastToken cmdToken
}

// Recursively parses schema tokens
func (ctx *Context) parseSchema(tokenParser *cmdTokenParser) (*schemaParser) {
	parser := new(schemaParser)
	parser.ctx = ctx
	parser.typeTableStack = []schemaTypeTable{make(schemaTypeTable)}
	
	walker := tokenParser.createRootWalker()
	for parser.LastError == nil {
		cmd := walker.nextCommand()
		if cmd == nil {
			break
		}
		
		parser.LastToken = cmd.getFirstToken()
		switch parser.LastToken.token {
			case "type":
				parser.parseType(cmd)
		} 
	}
	
	return parser
}

func (parser *schemaParser) newNode(name string) (*schemaNode) {
	node := new(schemaNode)
	node.nodeId = schemaNodeId(len(parser.ctx.schema))
	node.name = name
	
	parser.ctx.schema = append(parser.ctx.schema, node)
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
	
	// Check compound type class
	typeClass := parser.getTypeClass(typeOpt.TypeClass)
	if typeClass < varUserType {
		parser.LastError = fmt.Errorf("Unexpected type '%s' in type definition", 
			typeOpt.TypeClass)
		return
	}
	
	// Check if this type is already defined somewhere in stack
	if parser.findType(typeOpt.Name) != nil {
		parser.LastError = fmt.Errorf("Type '%s' already defined", typeOpt.Name)
		return
	}
	
	// Create node and insert to local table
	node := parser.newNode(typeOpt.Name)
	table := parser.typeTableStack[len(parser.typeTableStack)-1]
	table[typeOpt.Name] = node.nodeId
	
	block := cmd.nextBlock()
	
	// Parse real compound type (may require subblock)
	switch typeClass {
		case varArray:
			parser.parseArray(node, block, typeOpt.ExtraType)
		case varStruct:
			parser.parseStruct(node, block)
		case varEnum:
			parser.parseEnum(node, block)
		case varUnion:
			parser.parseUnion(node, block)
	}
}

func (parser *schemaParser) parseArray(node *schemaNode, block *cmdBlockTokenWalker, extraType string) {
	elementType := parser.findType(extraType)
	if elementType == nil {
		parser.LastError = fmt.Errorf("Array element type '%s' is not defined", 
			elementType)
		return
	}
	
	node.node = schemaArray {
		node: node,
		elementType: elementType,
	}
	parser.parseNodeCommands(node, block)
	return
}

func (parser *schemaParser) parseEnum(node *schemaNode, block *cmdBlockTokenWalker) {
	values := parser.parseValues(node, block)
	if len(values) == 0 {
		parser.LastError = fmt.Errorf("Missing values in enum compound")
		return 	
	}
	
	node.node = schemaEnum {
		node: node,
		values: values,
	}
}

func (parser *schemaParser) parseStruct(node *schemaNode, block *cmdBlockTokenWalker) {
	if block == nil {
		parser.LastError = fmt.Errorf("Union type requires subblock with subtypes")
		return
	}
	parser.growStack()
	
	var compound schemaStruct
	for parser.LastError == nil {
		cmd := block.nextCommand()
		if cmd == nil {
			break
		}
		
		parser.LastToken = cmd.getFirstToken()
		switch parser.LastToken.token {
			case "type":
				parser.parseType(cmd)
			case "var":
				varNode := parser.parseVar(cmd)
				if varNode != nil {
					compound.variables = append(compound.variables, varNode)
				}
			default:
				parser.parseNodeCommand(node, cmd)
		}
	}
	
	parser.shrinkStack()
	node.node = compound
}

func (parser *schemaParser) parseVar(cmd *cmdCommandTokenWalker) (*schemaNode) {
	var varOpt struct {
		MultiVar bool `opt:"m|multi,opt"`
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
		parser.LastError = fmt.Errorf("Unexpected compound type '%s' in var definition", 
			varOpt.TypeName)
	} else if typeClass == varUnknown {
		compoundType = parser.findType(varOpt.TypeName)
		switch compoundType.node.(type) {
			case (schemaArray):
				typeClass = varArray
			case (schemaStruct):
				typeClass = varStruct
			case (schemaEnum):
				typeClass = varEnum
			case (schemaUnion):
				typeClass = varUnion
			default:
				compoundType = nil
		}
		if compoundType == nil {
			parser.LastError = fmt.Errorf("Variable type '%s' is not defined", varOpt.TypeName)
			return nil
		}
	}
	
	node := parser.newNode(varOpt.Name)
	variable := schemaVariable {
		node: node,
		typeClass: typeClass,
		compoundType: compoundType,
		multiVar: varOpt.MultiVar,
		values: parser.parseValues(node, cmd.nextBlock()),
	}
	node.node = variable
	return node
}

func (parser *schemaParser) parseValues(node *schemaNode, block *cmdBlockTokenWalker) []*schemaNode {
	var values []*schemaNode
	
	for block != nil && parser.LastError == nil {
		cmd := block.nextCommand()
		if cmd == nil {
			break
		}
		
		parser.LastToken = cmd.getFirstToken()
		switch parser.LastToken.token {
			case "value":
				var valueOpt struct {
					Value string `arg:"1"`
				}
				if parser.tryArgParse(cmd, &valueOpt) {
					valueNode := parser.newNode("")
					valueNode.node = schemaValue{
						node: node,
						value: valueOpt.Value,						
					}
					values = append(values, node)
					
					parser.parseNodeCommands(node, cmd.nextBlock())
				}
			default:
				parser.parseNodeCommand(node, cmd)
		}
	}
	
	return values
}

func (parser *schemaParser) parseUnion(node *schemaNode, block *cmdBlockTokenWalker) {
	if block == nil {
		parser.LastError = fmt.Errorf("Union type requires subblock with subtypes")
		return
	}
}

func (parser *schemaParser) parseNodeCommand(node *schemaNode, cmd *cmdCommandTokenWalker) {
	parser.LastToken = cmd.getFirstToken()
	if parser.LastToken.token == "help" {
		var helpOpt struct {
			Help string `arg:"1"`
		}
		if parser.tryArgParse(cmd, &helpOpt) {
			node.help = helpOpt.Help
		}
		return 
	}
	
	if len(parser.ctx.schemaHanders) > 0 {
		commandName := parser.LastToken.token
		if handler, ok := parser.ctx.schemaHanders[commandName]; ok {
			handler.HandleCommand(node, cmd)
		} else {
			parser.LastError = fmt.Errorf("Unknown command %s: not a builtin or custom hander", commandName)
		}
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
		parser.LastError = fmt.Errorf("Error parsing arguments: %v", argParser.LastError)
		if argParser.index < len(argParser.args) {
			parser.LastToken =  argParser.args[argParser.index]	
		}
		return false
	}
	
	return true
}

func (parser *schemaParser) findType(name string) (*schemaNode) {
	for i := len(parser.typeTableStack)-1 ; i >= 0; i-- {
		table := parser.typeTableStack[i]
		if index, ok := table[name]; ok {
			return parser.ctx.schema[index]
		}
	}
	
	return nil
}

func (parser *schemaParser) growStack() {
	parser.typeTableStack = append(parser.typeTableStack, make(schemaTypeTable))
}

func (parser *schemaParser) shrinkStack() {
	parser.typeTableStack = parser.typeTableStack[:len(parser.typeTableStack)-1]
}
