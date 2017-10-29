package fishly

import (
	"testing"

	"strconv"
)

func assertSchemaNode(t *testing.T, schema *schemaRoot, i int, name string) (node *schemaNode) {
	if i < len(schema.nodes) {
		node = schema.nodes[i]
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
	switch node.data.(type) {
	case schemaStruct:
		compound = node.data.(schemaStruct)
	default:
		t.Errorf("Unexpected type of node %v, expected schemaStruct", node.data)
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
	switch varNode.data.(type) {
	case schemaVariable:
		variable = varNode.data.(schemaVariable)
	default:
		t.Errorf("Unexpected type of node %v, expected schemaVariable", varNode.data)
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
	schemaText := `
		type x struct { var y string }
	`
	tokenParser := newParser()
	tokenParser.parseLine(schemaText)

	var schema schemaRoot
	schema.init()
	parser := schema.parse(tokenParser)
	if parser.LastError != nil {
		t.Error(parser.LastError)
	}

	x := assertSchemaNode(t, &schema, 0, "x")
	y := assertSchemaStructVariable(t, x, 0, "y")
	assertSchemaStructVariableVar(t, y, varString, "")
}

func TestSchemaComplexStruct(t *testing.T) {
	schemaText := `
		type x struct { 
			type s struct {
				var i int
			}
			type ss array s
			var z ss
			var -list y string {
				help "axis"
			}
			
			help "hello"
		}
		
		command x x {
			opt -arg 1 {
				help "help"
			}
		}
	`
	tokenParser := newParser()
	tokenParser.parseLine(schemaText)

	var schema schemaRoot
	schema.init()
	parser := schema.parse(tokenParser)
	if parser.LastError != nil {
		t.Error(tokenParser.Tokens[parser.LastError.index], parser.LastError.err)
	}

	x := assertSchemaNode(t, &schema, 0, "x")
	z := assertSchemaStructVariable(t, x, 0, "z")
	assertSchemaStructVariableVar(t, z, varArray, "ss")
	y := assertSchemaStructVariable(t, x, 1, "y")

	if assertSchemaStructVariableVar(t, y, varString, "") {
		yv := y.data.(schemaVariable)
		if !yv.isList {
			t.Errorf("y should be a list")
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

func (handler *testSchemaHandler) HandleCommand(parser *schemaParser,
	node *schemaNode, cmd *cmdCommandTokenWalker) {
	handler.nodeId = node.nodeId
}

func TestSchemaStructHandler(t *testing.T) {
	schemaText := `
		type x struct { 
			var y string 
			
			test
		}
	`
	tokenParser := newParser()
	tokenParser.parseLine(schemaText)

	var schema schemaRoot
	schema.init()

	handler := testSchemaHandler{
		nodeId: -1,
	}
	schema.handlers = map[string]schemaHandler{
		"test": &handler,
	}

	parser := schema.parse(tokenParser)
	if parser.LastError != nil {
		t.Error(parser.LastError)
	}

	assertSchemaNode(t, &schema, 0, "x")
	if handler.nodeId != schemaNodeId(0) {
		t.Errorf("Handler didn't work, node id: %d", handler.nodeId)
	}
}

func assertTokenPathError(t *testing.T, path *TokenPath, errmsg string) {
	if path.lastError == nil {
		t.Errorf("Error at %s didn't produce error", path.GetPath())
		return
	}

	if path.lastError.Error() != errmsg {
		t.Errorf("Error at %s produced incorrect error", path.GetPath())
		t.Logf("%s (expected)", errmsg)
		t.Logf("%s (actual)", path.lastError.Error())
	}

	path.lastError = nil
}

func assertTokenPath(t *testing.T, path *TokenPath, expected string) {
	actual := path.GetPath()
	if actual != expected {
		t.Error("Incorrect path: ", path.String())
		t.Logf("%s (expected)", expected)
		t.Logf("%s (actual)", actual)
	}

	if path.lastError != nil {
		t.Errorf("Error at %s: %v", expected, path.lastError)
	}
}

func TestTokenPath(t *testing.T) {
	schemaText := `
		type object struct {
			type ctag struct {
				var tag string
				var rate string
			}
			type tag union {
				var ctag
				var xtag string
				
				default string
			}
			type tags array tag
			
			type counter struct {
				var x int
			}
			type counters array counter 
			
			var prop1 string {
				value "value"
			}
			var tags
			var counters
		}
		type objects array object
	`

	tokenParser := newParser()
	tokenParser.parseLine(schemaText)

	var schema schemaRoot
	schema.init()
	parser := schema.parse(tokenParser)
	if parser.LastError != nil {
		t.Error(tokenParser.Tokens[parser.LastError.index], parser.LastError.err)
	}

	var path TokenPath
	path.schema = &schema

	path.UpdatePath(Token{TokenType: ObjectStart, Tag: "objects"})
	path.UpdatePath(Token{TokenType: ObjectStart, Tag: "object"})

	path.UpdatePath(Token{TokenType: Value, Tag: "prop1", Text: "value", RawValue: "value"})
	assertTokenPath(t, &path, "objects, object -> var prop1 -> value")

	path.UpdatePath(Token{TokenType: Value, Tag: "prop2", RawValue: 2})
	assertTokenPathError(t, &path, "Undefined variable 'prop2'")
	assertTokenPath(t, &path, "objects, object")

	path.UpdatePath(Token{TokenType: ObjectStart, Tag: "tags"})
	assertTokenPath(t, &path, "objects, object -> var tags, tags")

	path.UpdatePath(Token{TokenType: Value, Tag: "xtag", RawValue: "tag"})
	assertTokenPath(t, &path, "objects, object -> var tags, tags -> union tag -> var xtag")

	path.UpdatePath(Token{TokenType: ObjectStart, Tag: "ctag"})
	path.UpdatePath(Token{TokenType: Value, Tag: "tag", RawValue: "tag"})
	assertTokenPath(t, &path, "objects, object -> var tags, tags -> union tag -> var ctag, ctag -> var tag")
	path.UpdatePath(Token{TokenType: Value, Tag: "rate", RawValue: 10})
	assertTokenPath(t, &path, "objects, object -> var tags, tags -> union tag -> var ctag, ctag -> var rate")
	path.UpdatePath(Token{TokenType: ObjectEnd}) // ctag

	path.UpdatePath(Token{TokenType: Value, RawValue: "text"})
	assertTokenPath(t, &path, "objects, object -> var tags, tags -> union tag -> var ")
	path.UpdatePath(Token{TokenType: ObjectEnd}) // tags

	path.UpdatePath(Token{TokenType: ObjectStart, Tag: "counters"})
	path.UpdatePath(Token{TokenType: ObjectStart, Tag: "counter"})
	path.UpdatePath(Token{TokenType: Value, Tag: "x", RawValue: 10})
	path.UpdatePath(Token{TokenType: ObjectEnd}) // counters
	path.UpdatePath(Token{TokenType: ObjectEnd}) // counter

	path.UpdatePath(Token{TokenType: ObjectStart, Tag: "sum"})
	path.UpdatePath(Token{TokenType: Value, Tag: "x", RawValue: 100})
	path.UpdatePath(Token{TokenType: ObjectEnd}) // counters

	path.UpdatePath(Token{TokenType: ObjectEnd}) // objects
	path.UpdatePath(Token{TokenType: ObjectEnd}) // object
	assertTokenPath(t, &path, "")
}
