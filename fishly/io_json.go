package fishly

import (
	"fmt"
	"reflect"
	"strconv"

	"bufio"
)

type jsonFormatter struct {
	HandlerWithoutCompletion
}
type jsonFormatterOpt struct {
	Types bool `opt:"t|types,opt"`
}
type jsonFormatterRq struct {
	indent int
	w      *bufio.Writer
}

func (*jsonFormatter) NewOptions() interface{} {
	return new(jsonFormatterOpt)
}

func (f *jsonFormatter) Run(ctx *Context, rq *IOFormatterRequest) {
	options := rq.Options.(*jsonFormatterOpt)
	defer rq.Close(nil)

	path := ctx.NewTokenPath()
	jrq := jsonFormatterRq{
		indent: 0,
		w:      bufio.NewWriter(rq.Output),
	}
	defer jrq.w.Flush()

	for {
		token := <-rq.Input
		if token.TokenType == EOF {
			return
		}

		path.UpdatePath(token)
		top := path.getTopElement()
		prevTop := path.getNthElement(2)
		closed := path.getClosedElement()
		variable := path.getVariableElement()

		if closed != nil {
			// Print closing characters for nodes that were recently ended
			if closed.old {
				switch closed.node.data.(type) {
				case (schemaArray):
					jrq.endObject(']')
				case (schemaStruct):
					if top.elementNode != nil {
						jrq.endObject('}')
					}
					jrq.endObject('}')
				}
			}
			closed.old = false
		}

		if top != nil && top.node != nil {
			// If we got new top-node, start new object(-s).
			if !top.old {
				if variable == nil && prevTop != nil && prevTop.count > 0 {
					jrq.w.WriteRune(',')
				}
				if variable != nil {
					// Special case for unions which have possible struct element
					if variable.count > 0 {
						jrq.w.WriteRune(',')
					}
					if prevTop.elementNode != nil {
						jrq.startObject('{')
					}
					jrq.newline()
					jrq.writePropName(variable.varNode.name)
				}
				switch top.node.data.(type) {
				case (schemaArray):
					jrq.startObject('[')
				case (schemaStruct):
					jrq.startObject('{')
					if options.Types {
						jrq.newline()
						jrq.writePropName("@type")
						jrq.writePropValueRaw(reflect.ValueOf(top.node.name))
						top.count++
					}
				}

			}
			top.old = true
		}

		// Print normal value token as struct property or as substruct in array
		// of unions (if elementNode != nil). If it is not the first property
		// for current struct, add comma
		if variable != nil && token.TokenType == Value {
			if top.count > 0 {
				jrq.w.WriteRune(',')
			}
			jrq.newline()

			if top.elementNode != nil && len(variable.varNode.name) == 0 {
				jrq.writePropValue(token)
			} else {
				if top.elementNode != nil {
					jrq.w.WriteRune('{')
				}
				jrq.writePropName(variable.varNode.name)
				jrq.writePropValue(token)
				if top.elementNode != nil {
					jrq.w.WriteRune('}')
				}
			}
		}
	}
}

func (jrq *jsonFormatterRq) writePropName(name string) {
	jrq.w.WriteString(strconv.Quote(name))
	jrq.w.WriteRune(':')
	jrq.w.WriteRune(' ')
}

func (jrq *jsonFormatterRq) writePropValue(token Token) {
	if token.RawValue == nil {
		if len(token.Text) >= 0 {
			jrq.w.WriteString(strconv.Quote(token.Text))
		} else {
			jrq.w.WriteString("null")
		}
	} else {
		rawValue := reflect.ValueOf(token.RawValue)
		switch rawValue.Type().Kind() {
		case reflect.Array, reflect.Slice:
			jrq.w.WriteRune('[')
			for i := 0; i < rawValue.Len(); i++ {
				if i > 0 {
					jrq.w.WriteString(", ")
				}
				jrq.writePropValueRaw(rawValue.Index(i))
			}
			jrq.w.WriteRune(']')
		default:
			jrq.writePropValueRaw(rawValue)
		}
	}
}

func (jrq *jsonFormatterRq) writePropValueRaw(rawValue reflect.Value) {
	switch rawValue.Type().Kind() {
	case reflect.Bool:
		jrq.w.WriteString(strconv.FormatBool(rawValue.Bool()))
	case reflect.Uint, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		jrq.w.WriteString(strconv.FormatUint(rawValue.Uint(), 10))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		jrq.w.WriteString(strconv.FormatInt(rawValue.Int(), 10))
	default:
		jrq.w.WriteString(strconv.Quote(fmt.Sprintf("%v", rawValue.Interface())))
	}
}

func (jrq *jsonFormatterRq) startObject(prefix rune) {
	jrq.newline()
	jrq.w.WriteRune(prefix)

	jrq.indent += 2
}

func (jrq *jsonFormatterRq) endObject(suffix rune) {
	jrq.indent -= 2

	jrq.newline()
	jrq.w.WriteRune(suffix)
}

func (jrq *jsonFormatterRq) newline() {
	jrq.w.WriteRune('\n')
	for i := 0; i < jrq.indent; i++ {
		jrq.w.WriteRune(' ')
	}
}
