package fishly

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
)

//
// io_text -- text formatter for go which supports rich text formatting
// in terminal
//

type textSchemaStyle struct {
	TermStyle []string `opt:"ts|style,opt"`
}

type textSchemaBlock struct {
	textSchemaStyle

	Tokens []string `arg:"1,opt"`
}

type textSchemaColumn struct {
	textSchemaBlock

	group       string
	firstColumn bool

	Left   bool `opt:"<|left,opt"`
	Right  bool `opt:">|right,opt"`
	Center bool `opt:"^|center,opt"`

	Width  int `opt:"w|width,opt"`
	Column int `opt:"at,opt"`

	Union   bool `opt:"u|union,opt"`
	NoSpace bool `opt:"nospace,opt"`

	Header string `opt:"hdr|header,opt"`
}

type textSchemaBlocks []textSchemaBlock
type textSchemaTable struct {
	cols []textSchemaColumn
	rows textSchemaBlocks
}

type textSchema struct {
	tables map[schemaNodeId]textSchemaTable
	blocks map[schemaNodeId]textSchemaBlocks
	styles map[schemaNodeId]textSchemaStyle
}

var boldStyle *textSchemaStyle = &textSchemaStyle{TermStyle: []string{"bold"}}

func newTextSchema() *textSchema {
	return &textSchema{
		tables: make(map[schemaNodeId]textSchemaTable),
		blocks: make(map[schemaNodeId]textSchemaBlocks),
		styles: make(map[schemaNodeId]textSchemaStyle),
	}
}

func (schema *textSchema) HandleCommand(parser *schemaParser, node *schemaNode, cmd *cmdCommandTokenWalker) {
	var textOpt struct {
		Table  bool `opt:"table,opt"`
		Blocks bool `opt:"blocks,opt"`
	}
	if !parser.tryArgParse(cmd, &textOpt) {
		return
	}

	block := cmd.nextBlock()
	if block == nil {
		parser.LastError = cmd.newCommandError(fmt.Errorf("missing block for text node"))
		return
	}

	switch {
	case textOpt.Table:
		var table textSchemaTable
		table.parseTable(parser, block, "")
		schema.tables[node.nodeId] = table
	case textOpt.Blocks:
		var blocks textSchemaBlocks
		blocks.parseBlocks(parser, block)
		schema.blocks[node.nodeId] = blocks
	default:
		styleCmd := block.nextCommand()
		if block.nextCommand() != nil {
			parser.LastError = cmd.newCommandError(fmt.Errorf("more than one command in text style node"))
			return
		}
		if styleCmd.getFirstToken().token != "style" {
			parser.LastError = cmd.newCommandError(fmt.Errorf("unexpected command in style node"))
			return
		}

		var style textSchemaStyle
		parser.tryArgParse(styleCmd, &style)
		schema.styles[node.nodeId] = style
	}
}

func (table *textSchemaTable) parseTable(parser *schemaParser, block *cmdBlockTokenWalker, group string) {
	firstColumn := true
	for parser.LastError == nil {
		cmd := block.nextCommand()
		if cmd == nil {
			break
		}

		token := cmd.getFirstToken()
		switch token.token {
		case "col":
			var column textSchemaColumn
			if parser.tryArgParse(cmd, &column) {
				column.group = group
				column.firstColumn, firstColumn = firstColumn, false
				table.cols = append(table.cols, column)
			}
		case "row":
			var block textSchemaBlock
			if parser.tryArgParse(cmd, &block) {
				table.rows = append(table.rows, block)
			}
		case "group":
			var groupOpt struct {
				Group string `arg:"1"`
			}
			if parser.tryArgParse(cmd, &groupOpt) {
				groupBlock := cmd.nextBlock()
				if groupBlock != nil {
					table.parseTable(parser, groupBlock, groupOpt.Group)
				}
			}
		default:
			parser.LastError = cmd.newCommandError(fmt.Errorf("unexpected command in table context"))
		}
	}
}

func (blocks *textSchemaBlocks) parseBlocks(parser *schemaParser, block *cmdBlockTokenWalker) {
	for parser.LastError == nil {
		cmd := block.nextCommand()
		if cmd == nil {
			break
		}

		token := cmd.getFirstToken()
		switch token.token {
		case "block":
			var block textSchemaBlock
			if parser.tryArgParse(cmd, &block) {
				*blocks = append(*blocks, block)
			}
		default:
			parser.LastError = cmd.newCommandError(fmt.Errorf("unexpected command in blocks context"))
		}
	}
}

func (block *textSchemaBlock) hasToken(tag string) bool {
	if len(block.Tokens) == 0 {
		return true
	}
	for _, btag := range block.Tokens {
		if btag == tag {
			return true
		}
	}
	return false
}

func (col *textSchemaColumn) matchTopType(top *tokenPathElement) bool {
	if len(col.group) == 0 {
		return true
	}

	return top != nil && top.node != nil && top.node.name == col.group
}

// text formatter
type textFormatter struct {
	HandlerWithoutCompletion
	HandlerWithoutOptions

	richText bool

	schema *textSchema
}

// Colors -- compile term style map from several tables
func createTermStyleMap() map[string]int {
	type termStyleTable struct {
		base   int
		styles []string
	}

	var termStyleTables = []termStyleTable{
		termStyleTable{
			base:   0,
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

var termStyleMap = createTermStyleMap()

type textFormatterPrinter interface {
	handleValue(frq *textFormatterRq, value Token)

	commit(frq *textFormatterRq)
}

type textFormatterRq struct {
	f *textFormatter

	w         *bufio.Writer
	tokenPath *TokenPath

	indent int

	style   *textSchemaStyle
	printer textFormatterPrinter

	buf    *bytes.Buffer
	prefix *bytes.Buffer
	suffix *bytes.Buffer
}

type textFormatterBlockPrinter struct {
	blocks     textSchemaBlocks
	blockIndex int
	valueCount int
}

type textFormatterTablePrinter struct {
	table textSchemaTable
	top   *tokenPathElement

	colIndex int
	pos      int

	// Current column style
	style *textSchemaStyle

	// Printer which is used for rows
	blocks *textFormatterBlockPrinter
}

func (f *textFormatter) Run(ctx *Context, rq *IOFormatterRequest) {
	defer rq.Close(nil)

	frq := &textFormatterRq{
		f: f,
		w: bufio.NewWriter(rq.Output),

		tokenPath: ctx.NewTokenPath(),

		buf:    bytes.NewBuffer([]byte{}),
		prefix: bytes.NewBuffer([]byte{}),
		suffix: bytes.NewBuffer([]byte{}),
	}
	defer frq.w.Flush()

	var minIndent int

loop:
	for {
		token := <-rq.Input
		if token.TokenType == EOF {
			return
		}

		frq.tokenPath.UpdatePath(token)
		if token.TokenType == ObjectStart || token.TokenType == ObjectEnd {
			// Commit remaining tokens, reset current block and wait for the
			// first value to arrive to set correct block back
			if frq.printer != nil {
				frq.printer.commit(frq)
			}
			frq.printer = nil
			continue
		}

		frq.indent = len(frq.tokenPath.stack) - minIndent
		frq.style = nil

		top := frq.tokenPath.getTopElement()
		if top != nil {
			frq.setStyle(token, top)
		}
		if frq.printer == nil {
			// Setup printer based on top-level hierarchy level if possible
			for n := 1; ; n++ {
				top = frq.tokenPath.getNthElement(n)
				if top == nil || top.node == nil {
					continue loop
				}
				if frq.setPrinter(top) {
					if minIndent == 0 {
						minIndent, frq.indent = frq.indent, 0
					}
					break
				}
			}
		}

		if frq.printer != nil {
			frq.printer.handleValue(frq, token)
		}
	}
}

// Establishes printer -- a subclass which handles Value tokens
func (frq *textFormatterRq) setPrinter(top *tokenPathElement) bool {
	nodeId := top.node.nodeId
	if blocks, ok := frq.f.schema.blocks[nodeId]; ok {
		frq.printer = &textFormatterBlockPrinter{blocks: blocks}
		return true
	} else if table, ok := frq.f.schema.tables[nodeId]; ok {
		frq.printer = &textFormatterTablePrinter{
			table:  table,
			top:    top,
			blocks: &textFormatterBlockPrinter{blocks: table.rows},
		}
		return true
	}
	return false
}

// Sets value- or var- specific style for a token, if possible. Otherwise,
// style should be reset to nil so block-level or table-level style would be
// used
func (frq *textFormatterRq) setStyle(token Token, top *tokenPathElement) {
	frq.style = nil

	var node *schemaNode
	if top.valueNode != nil {
		node = top.valueNode
	} else if top.varNode != nil {
		node = top.varNode
	}

	if node != nil {
		if style, ok := frq.f.schema.styles[node.nodeId]; ok {
			frq.style = &style
		}
	}
}

func (frq *textFormatterRq) setBuf(text, prefix, suffix string) {
	frq.buf.WriteString(text)
	frq.prefix.WriteString(prefix)
	frq.suffix.WriteString(suffix)
}

func (frq *textFormatterRq) padPrefix(n int) {
	for i := 0; i < n; i++ {
		frq.prefix.WriteByte(' ')
	}
}
func (frq *textFormatterRq) padSuffix(n int) {
	for i := 0; i < n; i++ {
		frq.suffix.WriteByte(' ')
	}
}

func (frq *textFormatterRq) commitBuf() {
	style := frq.style
	if style == nil {
		style = new(textSchemaStyle)
	}

	frq.w.Write(frq.prefix.Bytes())
	for _, termStyle := range style.TermStyle {
		if seq, ok := termStyleMap[termStyle]; ok {
			fmt.Fprintf(frq.w, "%s[%dm", "\x1b", seq)
		}
	}

	frq.w.Write(frq.buf.Bytes())

	if len(style.TermStyle) > 0 {
		fmt.Fprintf(frq.w, "%s[%dm", "\x1b", 0)
	}
	frq.w.Write(frq.suffix.Bytes())

	frq.buf.Reset()
	frq.prefix.Reset()
	frq.suffix.Reset()
}

func (printer *textFormatterBlockPrinter) handleValue(frq *textFormatterRq, value Token) {
	for printer.blockIndex < len(printer.blocks) {
		blocks := printer.blocks[printer.blockIndex]
		if blocks.hasToken(value.Tag) {
			if frq.style == nil {
				frq.style = &blocks.textSchemaStyle
			}

			lines := strings.Split(value.Text, "\n")
			for _, line := range lines {
				// Setup prefix dependent on whether we're at the beginning of block
				if printer.valueCount == 0 {
					frq.prefix.WriteRune('\n')
					frq.padPrefix(frq.indent)
				} else {
					frq.prefix.WriteRune(' ')
				}

				// Write value line by line (for multi-line values)
				frq.buf.WriteString(line)
				frq.commitBuf()
			}
			printer.valueCount++
			return
		}

		printer.blockIndex++
	}

	frq.setBuf(value.Text, fmt.Sprintf(" %s=", value.Tag), " ")
	frq.commitBuf()
}

func (printer *textFormatterBlockPrinter) commit(frq *textFormatterRq) {
	frq.commitBuf()
}

func (printer *textFormatterTablePrinter) handleValue(frq *textFormatterRq, value Token) {
	for printer.colIndex < len(printer.table.cols) {
		col := printer.table.cols[printer.colIndex]

		if !col.matchTopType(printer.top) && !col.matchTopType(frq.tokenPath.getTopElement()) {
			// Ignore columns which belong to the wrong group
			printer.colIndex++
			continue
		}

		if frq.style != nil {
			printer.style = frq.style
		}

		// If we are still filling up current column, update buffer. If this is
		// new tag, commit current column and try next one
		if col.hasToken(value.Tag) {
			if printer.colIndex == 0 && printer.top.count == 0 {
				printer.writeHeader(frq)
			}

			if frq.buf.Len() > 0 {
				frq.buf.WriteRune(' ')
			}
			frq.buf.WriteString(value.Text)

			// Dirty hack for one-line ls variant: it has -u option which allows to
			// immediately commit data and reset printer state
			if col.Union {
				printer.commitColumn(frq)
				printer.style = nil
			}
			return
		}

		printer.commitColumn(frq)
		printer.colIndex++
	}

	frq.indent += 2
	printer.blocks.handleValue(frq, value)
}

func (printer *textFormatterTablePrinter) commitColumn(frq *textFormatterRq) {
	col := printer.table.cols[printer.colIndex]
	if printer.style == nil {
		printer.style = &col.textSchemaStyle
	}

	printer.writeColumn(printer.colIndex, frq)
}

func (printer *textFormatterTablePrinter) commit(frq *textFormatterRq) {
	if printer.colIndex < len(printer.table.cols) {
		printer.writeColumn(printer.colIndex, frq)
		frq.commitBuf()
	}
}

func (printer *textFormatterTablePrinter) writeHeader(frq *textFormatterRq) {
	// Save style
	style := frq.style
	hdrPrinter := &textFormatterTablePrinter{
		table: printer.table,
		style: boldStyle,
	}

	for colIndex, col := range hdrPrinter.table.cols {
		if !col.matchTopType(printer.top) && !col.matchTopType(frq.tokenPath.getTopElement()) {
			continue
		}

		frq.buf = bytes.NewBufferString(col.Header)
		hdrPrinter.writeColumn(colIndex, frq)
	}

	frq.style = style
}

func (printer *textFormatterTablePrinter) writeColumn(colIndex int, frq *textFormatterRq) {
	col := printer.table.cols[colIndex]

	// compute padding and pad
	var extraLength int
	if col.Column > 0 {
		extraLength = col.Column - printer.pos
		frq.padPrefix(extraLength)
	} else if col.firstColumn {
		frq.w.WriteRune('\n')
		frq.padPrefix(frq.indent)
	}

	length := len(frq.buf.String())
	if col.Width > length && length > 0 {
		padding := col.Width - length
		if extraLength < 0 {
			padding += extraLength
		}
		switch {
		case col.Right:
			frq.padPrefix(padding)
		case col.Center:
			frq.padPrefix(padding / 2)
			frq.padSuffix(padding - padding/2)
		default:
			frq.padSuffix(padding)
		}
	}

	if colIndex == 0 {
		printer.pos = 0
	} else {
		printer.pos += len(frq.prefix.String()) + length + len(frq.suffix.String())
	}
	if !col.NoSpace {
		frq.suffix.WriteRune(' ')
	}

	frq.style = printer.style
	frq.commitBuf()
}
