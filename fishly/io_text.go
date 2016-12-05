package fishly

import (
	"io"
	"fmt"
	
	"bytes"
	
	"strings"
	"strconv"
)

//
// io_text -- text formatter for go which supports rich text formatting
//

// text style is used to format text output
type textStyle struct {
	NewLine bool
	
	// Blocks start with indentantion and finish with a new line 
	Block bool 
	Indent int
	
	// left '<', right '>', or center '^'
	Align string
	
	// Delimiter used if value with same tag appears
	Delimiter string
	NoDelimit bool
	
	// Starting column of output
	Column int
	
	// if set -- specifies width of the field 
	Width int
	
	// dumps table header
	Header string
	
	// for rich terminals
	TermStyle []string
}

type textStyleNode struct {
	children map[string]*textStyleNode
	
	style *textStyle
}

func newTextStyleNode() (*textStyleNode) {
	newNode := new(textStyleNode)
	newNode.children = make(map[string]*textStyleNode)
	
	return newNode
}

func (node *textStyleNode) GetChild(name string) StyleSheetNode {
	if child, ok := node.children[name] ; ok {
		return child
	}
	return nil
}
	
func (node *textStyleNode) CreateChild(name string) StyleSheetNode {
	newNode := newTextStyleNode()
	
	node.children[name] = newNode
	return newNode
}

// iterator which builds style sheet path
type textStyleIterator struct {
	path []*textStyleNode
	
	rootNode *textStyleNode
}

func (node *textStyleNode) CreateStyle() interface{} {
	node.style = new(textStyle)
	node.style.Delimiter = " "
	
	return node.style
}

func (node *textStyleNode) newIterator() (*textStyleIterator) {
	return &textStyleIterator{
		rootNode: node,
	}
}

func (iter *textStyleIterator) GetCurrent() StyleSheetNode {
	if len(iter.path) > 0 {
		return iter.path[len(iter.path)-1]
	}
	
	return iter.rootNode
}

func (iter *textStyleIterator) Enter(name string) {
	node := iter.GetCurrent().(*textStyleNode)
	
	if child, ok := node.children[name] ; ok {
		iter.path = append(iter.path, child)
	}
}

func (iter *textStyleIterator) Back(index int) {
	iter.path = iter.path[:index]
}


func (style *textStyle) parseHeader() []Token {
	if len(style.Header) == 0 {
		return nil
	}
	
	// Very simplistic parser which parses entries in header 
	// in the following format:
	//  _:"Description and words" arg:"ARG"
	// It assembles entries with spaces (like _). Of course,
	// it loses space symbols, and doesn't support escaping
	
	pieces := strings.Split(style.Header, " ")
	header := make([]Token, 0)
	
	for i := 0 ; i < len(pieces) ; i++ {
		colon := strings.IndexRune(pieces[i], ':')
		if colon == -1 {
			continue
		}
		
		entry := Token{
			Tag: pieces[i][:colon],
			TokenType: Value, 
			Text: pieces[i][colon+1:],
		}
		
		// If header has quotes in it, gather pieces
		if strings.Count(entry.Text, "\"") > 0 {
			i++
			buf := bytes.NewBufferString(entry.Text)
			
			for ; (strings.Count(buf.String(), "\"") % 2 != 0) && i < len(pieces) ; i++ {
				buf.WriteRune(' ')
				buf.WriteString(pieces[i])
			}
			
			entry.Text, _ = strconv.Unquote(buf.String())
		}
		
		header = append(header, entry)
	}
	
	return header
}

// text formatter
type textFormatter struct {
	richText bool
}

type textFormatterOpt struct {
	// Undocumented option used to raw dump of tokens 
	Dump bool `opt:"dump,opt,undoc"` 
}

// Colors -- compile term style map from several tables
func createTermStyleMap() map[string]int {
	type termStyleTable struct {
		base int 
		styles []string
	}
	
	var termStyleTables = []termStyleTable {
		termStyleTable{
			base: 0,
			styles: []string{"reset", "bold", "faint", "italic", "underline"},
		},
		termStyleTable{
			base: 30,
			styles: []string{"black", "red", "green", "yellow", 
				"blue", "magenta", "cyan"},
		},
		termStyleTable{
			base: 90,
			styles: []string{"hiblack", "hired", "higreen", "hiyellow", 
				"hiblue", "himagenta", "hicyan"},
		},
	}
	
	termMap := make(map[string]int)
	for _, table := range termStyleTables {
		for offset, style := range table.styles {
			termMap[style] = table.base + offset
		}
	}
	return termMap
}
var termStyleMap = createTermStyleMap();

type textFormatterRq struct {
	richText bool
	
	w io.Writer
	
	tokenPath TokenPath
	
	// coordinates
	indent int
	row, col int
	
	blocks []string
	header []Token
	lastTag string
	hasStyle bool
}

func (*textFormatter) NewOptions() interface{} {
	return new(textFormatterOpt)
}

func (*textFormatter) Complete(ctx *Context, rq *CompleterRequest) {
}

func (f *textFormatter) Run(ctx *Context, rq *IOFormatterRequest) {
	options := rq.Options.(*textFormatterOpt)
	defer rq.Close(nil)
	
	var stylePath StyleSheetPath
	
	styleIter := ctx.style.newIterator() 
	
	frq := &textFormatterRq {
		richText: f.richText,
		w: rq.Output,
		blocks: make([]string, 0),	
	}
	
	for {
		token := <- rq.Input
		if token.TokenType == EOF {
			return
		}
		
		frq.tokenPath.UpdatePath(token)
		haveNode := stylePath.Update(styleIter, &frq.tokenPath)
		
		// For debugging - redirect actual output to buffer & dump it later
		var dumpBuffer *bytes.Buffer 
		if options.Dump {
			dumpBuffer = bytes.NewBuffer([]byte{})
			frq.w = dumpBuffer 
		}
		
		// Not found style node, or it doesn't contain useful style -- no print
		var styleNode *textStyleNode
		if haveNode {
			styleNode = styleIter.path[len(styleIter.path)-1]
			
			// In some cases style won't be created (if we found intermediate node)
			if styleNode.style != nil {
				frq.printToken(&token, styleNode)
			}
		}
		
		if options.Dump {
			frq.debugDump(rq.Output, &token, strconv.Quote(dumpBuffer.String()), 
					strings.Join(stylePath.Path, "."), styleNode)
		}
	}
}

func (rq *textFormatterRq) printToken(token *Token, styleNode *textStyleNode) {
	// If that is the first time we found a non-empty entry, 
	// dump its header first
	if token.TokenType != ObjectEnd && len(rq.header) > 0 {
		rq.printHeader(rq.header, styleNode)
	}
	rq.header = nil
	
	style := styleNode.style
	if style.Block {
		if token.TokenType == ObjectStart || token.TokenType == ArrayStart {
			rq.printBlock(style)
		} else if token.TokenType == ObjectEnd {
			rq.completeBlock(style)
		}
	}
	
	if token.TokenType == Value {
		// If we have specific value in token, customize style for it  
		if valueNode, ok := styleNode.children["=" + token.Text]; ok {
			style = valueNode.style
		}
		
		rq.printValue(token, style, false)
	}
}

func (rq *textFormatterRq) newline() {
	rq.row++
	rq.col = 0
	io.WriteString(rq.w, "\n")
	rq.writePadding(rq.indent)
}

func (rq *textFormatterRq) writePadding(n int) {
	if n > 0 {
		rq.col += n
		io.WriteString(rq.w, strings.Repeat(" ", n))
	}
}

func (rq *textFormatterRq) writeTermStyle(termStyle string) {
	if seq, ok := termStyleMap[termStyle] ; ok {
		fmt.Fprintf(rq.w, "%s[%dm", "\x1b", seq)
		rq.hasStyle = true
	}
}

func (rq *textFormatterRq) resetTermStyle() {
	if rq.hasStyle {
		fmt.Fprintf(rq.w, "%s[%dm", "\x1b", 0)
		rq.hasStyle = false
	}
}

func (rq *textFormatterRq) writeString(s string) {
	if len(s) > 0 {
		// TODO: support for hanging output
		io.WriteString(rq.w, s)
		rq.col += len(s)
	}
}

// Returns token path of block that we are handling now
func (rq *textFormatterRq) getCurrentBlock() string {
	if len(rq.blocks) > 0 {
		return rq.blocks[len(rq.blocks)-1]
	}
	
	return ""
}

// If new block starts, checks it and prints its header/indentation
func (rq *textFormatterRq) printBlock(style *textStyle) {
	// If it is a new block, update style
	if style.Indent > 0 {
		rq.indent += style.Indent
	}
		
	tokenPath := rq.tokenPath.GetPath()
	if tokenPath != rq.getCurrentBlock() {
		if len(rq.blocks) > 0 {
			rq.newline()
		}
		rq.blocks = append(rq.blocks, tokenPath)
	}

	rq.lastTag = ""
	rq.header = style.parseHeader()	
}

func (rq *textFormatterRq) completeBlock(style *textStyle) {
	if rq.tokenPath.GetPath() != rq.getCurrentBlock() {
		return
	}
	
	rq.indent -= style.Indent	
	rq.blocks = rq.blocks[:len(rq.blocks)-1] 
}

func (rq *textFormatterRq) printValue(token *Token, style *textStyle, forceBold bool) {
	// Separate value from previous value
	if style.NewLine {
		rq.newline()
	} else {
		if len(rq.lastTag) > 0 && len(token.Tag) > 0 && !style.NoDelimit {
			rq.writeString(style.Delimiter)
		}
	}
	
	// Calculate padding
	var leftPad, rightPad int
	if style.Width != 0 {
		padding := style.Width - len(token.Text)
		if padding > 0 {
			switch style.Align {
				case "<":
					rightPad = padding
				case ">":
					leftPad = padding
				case "^":
					rightPad = padding / 2
					leftPad = padding - padding/2
				default:
					// By default left-pad everything
					rightPad = padding
			}
		}
	}
	if style.Column > rq.col {
		leftPad += (style.Column - rq.col)
	}
	
	// Set style & pad text
	rq.writePadding(leftPad)
	if rq.richText {
		for _, termStyle := range style.TermStyle {
			rq.writeTermStyle(termStyle)	
		}
		if forceBold {
			rq.writeTermStyle("bold")
		}
	}
	
	rq.writeString(token.Text)
	
	// Reset style & clear style
	rq.writePadding(rightPad)
	rq.resetTermStyle()
	
	rq.lastTag = token.Tag
}

func (rq *textFormatterRq) printHeader(header []Token, styleNode *textStyleNode) {
	for _, token := range header {
		// Find appropriate style and print header using same style as tokens use
		if node, ok := styleNode.children[token.Tag]; ok {
			if node.style != nil {
				// Use header's width as column width if it is not enough
				if node.style.Width == 0 {
					node.style.Width = len(token.Text)
				}
				
				rq.printValue(&token, node.style, true)
				continue
			}
		}
		
		var defaultStyle textStyle
		rq.printValue(&token, &defaultStyle, true)
	}
}

func (rq *textFormatterRq) debugDump(w io.Writer, token *Token, 
				text, stylePath string, styleNode *textStyleNode) {		
	fmt.Fprintf(w, "%d tag:%-10s (last:%s) i:%d x:%d y:%d %s (%s)\n", token.TokenType, token.Tag,
			rq.lastTag, rq.indent, rq.col, rq.row, text, token.Text)
	fmt.Fprintf(w, " -> %s \n", rq.tokenPath.GetPath())
	
	if styleNode != nil && styleNode.style != nil {
		fmt.Fprintf(w, " -> style: %s (%v)\n", stylePath, styleNode.style)
	}
	
	if len(rq.blocks) > 0 {
		fmt.Fprintf(w, " -> blocks: %d ... %s\n", len(rq.blocks), rq.getCurrentBlock())
	}
}