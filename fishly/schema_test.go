package fishly

import ( 
	"testing"
	
	"strconv" 
)

func assertSchemaNode(t *testing.T, ctx *Context, i int, name string) (node *schemaNode) {
	if i < len(ctx.schema) {
		node = ctx.schema[i]
	}
	if node == nil {
		t.Errorf("Missing schema node #%d", i)
	} else if node.name != name {
		t.Errorf("Unexpected schema node #%d: %s != %s", name, node.name)
		return nil
	}
	return 
}

func assertSchemaStructVariable(t *testing.T, node *schemaNode, i int, name string) (varNode *schemaNode) {
	if node == nil {
		return
	}
	
	var compound schemaStruct
	switch node.node.(type) {
		case schemaStruct:
			compound = node.node.(schemaStruct)
		default:
			t.Errorf("Unexpected type of node %v, expected schemaStruct", node.node)
			return
	}
	
	if len(compound.variables) <= i {
		t.Errorf("Missing variable #%d in type %s", i, node.name)
		return
	}
	
	varNode = compound.variables[i]
	if varNode.name != name {
		t.Errorf("Unexpected variable #%d in type %s: %s != %s", i, node.name, 
			name, varNode.name)
	}
	return
}

func assertSchemaStructVariableVar(t *testing.T, varNode *schemaNode, 
				typeClass schemaTypeClass, compoundTypeName string) bool {
	if varNode == nil {
		return false
	}
	
	var variable schemaVariable
	switch varNode.node.(type) {
		case schemaVariable:
			variable = varNode.node.(schemaVariable)
		default:
			t.Errorf("Unexpected type of node %v, expected schemaVariable", varNode.node)
			return false
	}
	
	if variable.typeClass != typeClass {
		t.Errorf("Unexpected variable %s type class: %s != %s", varNode.name, 
			typeClassNames[variable.typeClass], typeClassNames[typeClass])
	}
	if typeClass >= varUserType {
		if variable.compoundType == nil {
			t.Errorf("Variable %s is missing compound type", varNode.name)
			return false
		}
		
		if variable.compoundType.name != compoundTypeName {
			t.Errorf("Unexpected variable %s type name: %s != %s", varNode.name, 
				variable.compoundType.name, compoundTypeName)
		}
	}
	return true
}

func TestSchemaSimpleStruct(t *testing.T) {
	schema := `
		type x struct { var y string }
	`
	tokenParser := newParser()
	tokenParser.parseLine(schema)
	
	var ctx Context
	parser := ctx.parseSchema(tokenParser)
	if parser.LastError != nil {
		t.Error(parser.LastError, parser.LastToken) 
	}
	
	x := assertSchemaNode(t, &ctx, 0, "x")
	y := assertSchemaStructVariable(t, x, 0, "y")
	assertSchemaStructVariableVar(t, y, varString, "")
}

func TestSchemaComplexStruct(t *testing.T) {
	schema := `
		type x struct { 
			type s struct {
				var i int
			}
			type ss array s
			var z ss
			var -multi y string {
				help "axis"
			}
			
			help "hello"
		}
	`
	tokenParser := newParser()
	tokenParser.parseLine(schema)
	
	var ctx Context
	parser := ctx.parseSchema(tokenParser)
	if parser.LastError != nil {
		t.Error(parser.LastError, parser.LastToken) 
	}
	
	x := assertSchemaNode(t, &ctx, 0, "x")
	z := assertSchemaStructVariable(t, x, 0, "z")
	assertSchemaStructVariableVar(t, z, varArray, "ss")
	y := assertSchemaStructVariable(t, x, 1, "y")
	
	if assertSchemaStructVariableVar(t, y, varString, "") {
		yv := y.node.(schemaVariable)
		if !yv.multiVar {
			t.Errorf("y should be a multivar")
		}
		if y.help != "axis" {
			t.Errorf("Unexpected help string '%s' for y", strconv.Quote(y.help))
		}
	}
	
	if x != nil {
		if x.help != "hello" {
			t.Errorf("Unexpected help string '%s' for x", strconv.Quote(x.help))
		}
	}
}

type testSchemaHandler struct {
	nodeId schemaNodeId
}
func (handler *testSchemaHandler) HandleCommand(node *schemaNode, cmd *cmdCommandTokenWalker) {
	handler.nodeId = node.nodeId
}

func TestSchemaStructHandler(t *testing.T) {
	schema := `
		type x struct { 
			var y string 
			
			test
		}
	`
	tokenParser := newParser()
	tokenParser.parseLine(schema)
	
	var ctx Context
	
	handler := testSchemaHandler{
		nodeId: -1,
	}
	ctx.schemaHanders = map[string]schemaHandler{
		"test": &handler,
	}
	
	parser := ctx.parseSchema(tokenParser)
	if parser.LastError != nil {
		t.Error(parser.LastError, parser.LastToken) 
	}
	
	assertSchemaNode(t, &ctx, 0, "x")
	if handler.nodeId != schemaNodeId(0) {
		t.Errorf("Handler didn't work, node id: %d", handler.nodeId)
	}
}

