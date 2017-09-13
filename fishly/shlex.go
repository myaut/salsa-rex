package fishly

import (
	"bytes"

	"bufio"
	"fmt"
	"io"

	"strconv"
	"strings"

	"unicode"
)

// shlex ("shell lexer") is a main parser in fishly which splits input into tokens and
// pre-parses tokens. fishly simplified syntax is below:
//
// <whitespace-char> ::= SPACE | TAB
// <whitespace-opt> ::= <whitespace-char> <whitespace-opt> | ""
// <newline> ::= LF
// <newline-opt> ::== <newline> <newline-opt> | ""
// <end> ::= <newline-opt> | EOL
//
// <separator> ::= <whitespace-char> <whitespace-opt> |
//				   "\" <whitespace-opt> <newline> <whitespace-opt>
//
// <token-escape> ::= "\u" HEXDIGIT HEXDIGIT HEXDIGIT HEXDIGIT |
//					  "\x" OCTDIGIT OCTDIGIT OCTDIGIT
// <token-first-char> ::== ALNUM | <token-escape>
// <token-char> ::= PRINT | <token-escape> | ""
// <token-chars> ::= <token-char> <token-chars> | ""
// <token> ::= <token-first-char> <token-chars>
//
// <raw-token> ::= PRINT <whitespace-opt> <raw-token> | ""
// <raw-token-nl> ::= <raw-token> <newline-opt> <raw-token>
//
// <command-name> ::= <token>
// <command-separator> ::= <newline> | ";"
// <command-separator-opt> ::= <command-separator> | ""
//
// <raw-argument> ::= <token>
// <sq-argument> ::= "'" <raw-token> "'"
// <dq-argument> ::= '"' <raw-token> '"'
// <argument-class> ::= <raw-argument> | <sq-argument> | <dq-argument>
// <argument> ::= <argument-class> <argument-class> | <argument-class>
//
// <option> ::= "-" <token>
// <option-with-arg> ::= <option> <separator> <argument>
// <option-separator> ::= "--"
//
// <opt-sequence> ::= <whitespace-opt> <option> <opt-sequence> |
//					  <whitespace-opt> <option-with-arg> <opt-sequence> | ""
// <arg-sequence> ::= <whitespace-opt> <argument> <arg-sequence> | ""
// <opt-arg-sequence> ::= <opt-sequence> <option-serator> <arg-sequence> |
//						  <opt-sequence> <arg-sequence>
//
// <comment-opt> ::= "#" <raw-token> <end> | ""
//
// <redirection> ::= "|" <whitespace-opt> <newline-opt> <raw-token>
// <sh-redirection> ::= "!" <whitespace-opt> <raw-token> <end>
// <file-redirection> ::= ">" <whitespace-opt> <raw-token> <end>
//
// <redirection-class> ::= <redirection> <opt-arg-sequence> <command-block-sequence> |
//						   <sh-redirection> | <file-redirection>
// <redirection-sequence> ::= <redirection-class> <whitespace-opt> <redirection-sequence> | ""
//
// <command> ::= <command-name> <opt-arg-sequence> <command-block-sequence> <redirection-sequence>
// <command-next> ::=  <whitespace-opt> <command-separator> <whitespace-opt> <command> | ""
// <command-sequence> ::= <command-separator-opt> <command> <command-next>
// <command-block-opt> ::= "{" <command-sequence> "}" | ""
// <command-block-sequence> ::= <command-block-opt> <command-block-opt>
//
// Note that fishly syntax is not context free: tokens can be interpreted as commands
// or arguments depending on position, tokens starting with "-" can be either options
// or argument and even commands (this is not shown in BNF above)
//
// Parser outputs a one-dimensional token stream as slice of cmdToken structures and
// cmdCommand anchors that form a tree of commands, their redirections and corresponding
// subblocks and arguments, i.e.:
//
//     if i = 0 { xxx = 22 } ; return xxx
//     \  \args/\-subblock-/         \args/
//      \----command----/    \--command--/
//
// Knowing beginning and ending index of subblock allows us to ignore it and immediately
// jump to next command (return). cmdTokenWalker is an API to do that
//

type cmdTokenType int

const (
	tCommand          = iota
	tCommandSeparator // ';', starts a new command
	tRedirection      // '|', the rest is parsed as command & arguments
	tShellRedirection // '!' followed by the command
	tFileRedirection  // '>' followed by filename
	tOption           // Option starting with '-'
	tRawArgument
	tSingleQuotedArgument
	tDoubleQuotedArgument // Command argument which supports interpolation
	tBlockBegin           // '{'
	tBlockEnd             // '}'
)

var tokenTypeStrings = []string{
	"Command",
	"CommandSeparator",
	"Redirect",
	"ShellRedirect",
	"FileRedirect",
	"Option",
	"RawArg",
	"SQArg",
	"DQArg",
	"BBegin",
	"BEnd",
}

const (
	whiteSpaceDelimiters = " \t" // Whitespace characters using as delimiters
	shlexDelimiters      = whiteSpaceDelimiters + "|'\"\n;{}"
	shRedirDelimiters    = "\n;"
)

type cmdTokenRange struct {
	// start and end indeces (inclusive)
	start, end int
}

// Helper structure for cmdToken which contains forward token indeces
type cmdCommand struct {
	command      cmdTokenRange
	args         cmdTokenRange
	redirections []cmdTokenRange
	blocks       []cmdTokenRange
}

type cmdToken struct {
	// Positions (for auto-complete)
	startPos, endPos int
	line             int

	// Type of the token
	tokenType cmdTokenType

	// Token text
	token string

	// Index of argument
	argIndex int

	// For tokens of type tCommand and tRedirection -- forward indexes
	command *cmdCommand
}

type cmdTokenParser struct {
	// Produced tokens
	Tokens []cmdToken

	// In case of error -- contains error
	LastError error
	Position  int
	Line      int

	// Set to true if we expect more lines to come if quoted argument unfinished,
	// blocks are not matched or
	ExpectMore bool

	// Active stack of commands to set indeces
	commandStack []*cmdCommand

	// Token type denotes type for next raw token which would be either command
	// or raw argument
	tokenType cmdTokenType

	// allowOptions become false when we expect command or raw argument
	allowOptions bool

	// argument state -- current index of arguments (multiple argument tokens with same
	// index form same argument) and last line of argument to denote arguments split by
	// escaped newline (2nd form of separator above)
	argIndex    int
	lastArgLine int

	// Last expected delimiter (for autocompleters)
	lastDelimiter rune

	// Active input buffer. hasMore becomes false when buf is exhausted
	buf     io.RuneScanner
	hasMore bool

	// last control rune and following runes in buffer from failed read (after EOF)
	lastRune     rune
	lastBuf      *bytes.Buffer
	lastPosition int
}

func newParser() *cmdTokenParser {
	parser := cmdTokenParser{
		// first token should be command
		Tokens: make([]cmdToken, 0),

		Position: 0,
		Line:     1,

		hasMore:    false,
		ExpectMore: true,

		tokenType:    tCommand,
		allowOptions: false,
		argIndex:     1,
		lastArgLine:  1,
	}

	parser.growStack()
	return &parser
}

// Parse text line by line. If current line is not finished and abrupts,
// parser will have ExpectMore set and reverted position
func (parser *cmdTokenParser) parseLine(line string) {
	if parser.lastBuf == nil && parser.lastRune == '\000' {
		parser.parseFromScanner(bytes.NewBufferString(line))
		return
	}

	// Assemble buffer from unread parts left by previous call to parseLine()
	// and new line
	buf := bytes.NewBuffer([]byte{})
	if parser.lastRune != '\000' {
		buf.WriteRune(parser.lastRune)
		parser.lastPosition--
	}
	if parser.lastBuf != nil {
		buf.Write(parser.lastBuf.Bytes())
	}
	buf.WriteRune('\n')
	buf.WriteString(line)

	parser.parseFromScanner(buf)
}

// Parse all text at once from reader (i.e., file)
func (parser *cmdTokenParser) parseReader(rd io.Reader) {
	parser.parseFromScanner(bufio.NewReader(rd))
}

func (parser *cmdTokenParser) parseFromScanner(buf io.RuneScanner) {
	// Reset parser state from previous parseLine() call
	parser.ExpectMore = false
	parser.hasMore = true
	parser.LastError = nil
	parser.Position = parser.lastPosition
	parser.buf = buf

loop:
	for parser.hasMore && parser.LastError == nil {
		parser.ignoreWhiteSpace(false)
		if !parser.hasMore {
			break
		}

		ch := parser.readRune(true)
		parser.lastRune = ch
		parser.lastPosition = parser.Position
		if !parser.hasMore {
			break
		}

		var token cmdToken
		switch ch {
		case '\n':
			token = parser.readCharacterToken(tCommandSeparator, "\n")
			parser.Line++
		case ';':
			token = parser.readCharacterToken(tCommandSeparator, ";")
		case '#':
			// Comment -- ignore the rest of the line
			for parser.hasMore && ch != '\n' {
				ch = parser.readRune(true)
				parser.lastRune = '\000'
			}
			parser.Line++
			continue loop
		case '\'':
			token = parser.readToken(tSingleQuotedArgument, "'", false, false)
		case '"':
			token = parser.readToken(tDoubleQuotedArgument, `"`, false, false)
		case '-':
			if !parser.allowOptions {
				parser.unreadRune()
				token = parser.readToken(parser.tokenType, whiteSpaceDelimiters, true, true)
			} else {
				token = parser.readToken(tOption, shlexDelimiters, true, true)
				token.startPos--
				if token.token == "-" {
					parser.allowOptions = false
					continue loop
				}
			}
		case '|':
			parser.ignoreWhiteSpace(true)
			token = parser.readToken(tRedirection, shlexDelimiters, true, true)
			if token.token == "sh" {
				token = parser.readSHCommandLine(token)
			}
		case '>':
			parser.ignoreWhiteSpace(true)
			token = parser.readToken(tFileRedirection, shlexDelimiters, true, true)
		case '!':
			token = parser.readCharacterToken(tRedirection, "!")
			token = parser.readSHCommandLine(token)
		case '{':
			token = parser.readCharacterToken(tBlockBegin, "{")
		case '}':
			token = parser.readCharacterToken(tBlockEnd, "}")
		default:
			parser.unreadRune()
			token = parser.readToken(parser.tokenType, shlexDelimiters, true, true)

			// Escape symbol followed by whitespace -- may be it is line delimiter?
			if parser.checkSeparator(token) {
				parser.Line++
				continue loop
			}
		}

		if parser.LastError != nil {
			break loop
		}

		// fmt.Printf("%d/%d %s -> %v\n", parser.Position, parser.Line, strconv.QuoteRune(ch), token)

		// If we met command separator -- reset state of the parser to command mode
		// If we parsed command, start parsing its arguments
		switch token.tokenType {
		case tCommand:
			parser.tokenType = tRawArgument
			parser.allowOptions = true
		case tCommandSeparator:
			if parser.tokenType == tCommand {
				// Do not stack up command separators if we already expect command
				continue loop
			}
			fallthrough
		case tBlockBegin, tBlockEnd:
			parser.tokenType = tCommand
			parser.allowOptions = false
			fallthrough
		case tRedirection:
			parser.argIndex = 1
		case tRawArgument, tSingleQuotedArgument, tDoubleQuotedArgument:
			// Find next token after argument. If they're merged, do not start
			// new argument (do not increase index)
			token.argIndex = parser.argIndex
			if parser.ignoreWhiteSpace(false) > 0 {
				parser.argIndex++
			}
		}

		parser.insertToken(token)
		parser.lastRune = '\000'
		parser.lastBuf = nil
	}

	if parser.LastError == nil && len(parser.commandStack) > 2 {
		parser.LastError = fmt.Errorf("EOF: unpaired curly braces")
		parser.ExpectMore = true
	}
}

func (parser *cmdTokenParser) readToken(tokenType cmdTokenType, delimiters string,
	expectEOF, unreadRune bool) cmdToken {
	var token cmdToken

	if !parser.hasMore {
		parser.LastError = fmt.Errorf("EOF while reading %s", tokenTypeStrings[tokenType])
		parser.ExpectMore = true
		return token
	}

	token.line = parser.Line
	token.startPos = parser.Position
	token.tokenType = tokenType
	token.token = parser.readUntil(delimiters, expectEOF, unreadRune)
	token.endPos = parser.Position
	if !unreadRune {
		token.endPos--
	}

	return token
}

func (parser *cmdTokenParser) readCharacterToken(tokenType cmdTokenType, ch string) cmdToken {
	// The trick is to feed read rune to readToken
	parser.unreadRune()
	token := parser.readToken(tokenType, ch, false, false)
	token.token = ch
	token.endPos++
	parser.ignoreWhiteSpace(false)

	return token
}

func (parser *cmdTokenParser) readSHCommandLine(token cmdToken) cmdToken {
	// Read until the end of the line
	parser.ignoreWhiteSpace(false)
	return parser.readToken(tShellRedirection, shRedirDelimiters, true, true)
}

func (parser *cmdTokenParser) ignoreWhiteSpace(ignoreNewLines bool) int {
	whiteSpaceCount := 0

	for parser.hasMore {
		ch := parser.readRune(true)
		if !parser.hasMore {
			break
		}
		if (!ignoreNewLines && ch == '\n') || !unicode.IsSpace(ch) {
			parser.unreadRune()
			break
		}

		if ch == '\n' {
			parser.Line++
		}
		whiteSpaceCount += 1
	}

	return whiteSpaceCount
}

// Reads until delimiters will be found
func (parser *cmdTokenParser) readUntil(delimiters string, expectEOF, unreadRune bool) string {
	parser.lastPosition = parser.Position

	buffer := bytes.NewBuffer([]byte{})

	for parser.hasMore {
		ch := parser.readRune(expectEOF)
		if parser.LastError != nil || !parser.hasMore {
			// Even if we failed while parsing string, return what we managed
			// to parse (for auto completion)
			if len(delimiters) > 0 {
				parser.lastDelimiter = []rune(delimiters)[0]
			}
			break
		}

		if ch == '\\' {
			ch = parser.readRune(false)
			if buffer.Len() == 0 && strings.ContainsRune(whiteSpaceDelimiters, ch) {
				buffer.WriteRune(ch)
				break
			}
			ch = parser.readEscapeSequence(ch)
			if parser.LastError != nil {
				break
			}

			buffer.WriteRune(ch)
			continue
		}

		index := strings.IndexRune(delimiters, ch)
		if index >= 0 {
			if unreadRune {
				parser.unreadRune()
			}
			break
		}

		buffer.WriteRune(ch)
	}

	parser.lastBuf = buffer
	return buffer.String()
}

func (parser *cmdTokenParser) readEscapeSequence(ch rune) rune {
	if parser.LastError != nil {
		return '\000'
	}

	switch ch {
	case 'n':
		return '\n'
	case 't':
		return '\t'
	case 'u':
		// Unicode-sequence
		ord := parser.readEscapeSequenceEncoded(4, 16)
		if ord == -1 {
			return '\000'
		}
		return rune(ord)
	case 'x':
		// ASCII-sequence
		ord := parser.readEscapeSequenceEncoded(3, 8)
		if ord == -1 {
			return '\000'
		}
		return rune(ord)
	}

	return ch
}

func (parser *cmdTokenParser) readEscapeSequenceEncoded(maxChars, base int) int {
	buffer := bytes.NewBuffer([]byte{})
	for i := 0; i < maxChars; i += 1 {
		ch := parser.readRune(false)
		if parser.LastError != nil {
			return -1
		}

		buffer.WriteRune(ch)
	}

	// Now convert number
	ord, err := strconv.ParseInt(buffer.String(), base, 32)
	if err != nil {
		parser.LastError = fmt.Errorf("failed to decode escape sequence: %s", err)
		return -1
	}

	return int(ord)
}

// Checks 2nd form of separator: \\\s+\n
func (parser *cmdTokenParser) checkSeparator(token cmdToken) bool {
	if token.token == "\n" {
		return true
	}
	if token.token == " " {
		parser.ignoreWhiteSpace(false)
		ch := parser.readRune(false)
		if ch == '\n' {
			return true
		}
		parser.unreadRune()
	}
	return false
}

func (parser *cmdTokenParser) readRune(expectEOF bool) (ch rune) {
	ch, _, parser.LastError = parser.buf.ReadRune()

	if parser.LastError != nil {
		parser.hasMore = false
	}
	if parser.LastError == io.EOF {
		if !expectEOF {
			parser.ExpectMore = true
		} else {
			parser.LastError = nil
		}
	} else {
		parser.Position++
	}

	return
}

func (parser *cmdTokenParser) unreadRune() {
	parser.LastError = parser.buf.UnreadRune()
	parser.Position--
	parser.hasMore = (parser.LastError == nil)
}

func (trange *cmdTokenRange) update(tokenIndex int) {
	if trange.start == 0 {
		trange.start = tokenIndex
	}
	trange.end = tokenIndex
}

func (parser *cmdTokenParser) insertToken(token cmdToken) {
	parser.Tokens = append(parser.Tokens, token)
	tokenIndex := len(parser.Tokens) - 1
	tokenRange := cmdTokenRange{start: tokenIndex, end: tokenIndex}

	// For commands and separators -- set or reset top-level element in stack
	switch token.tokenType {
	case tCommand:
		command := new(cmdCommand)
		command.command = tokenRange

		parser.Tokens[tokenIndex].command = command
		parser.setStackHeads(command, nil)
		return
	case tCommandSeparator:
		parser.setStackHeads(nil, nil)
		return
	case tBlockEnd:
		// shrink stack before getting heads because we are not interested
		// in current heads (and they can be nil if block is empty)
		parser.shrinkStack()
	}

	command, redir, top := parser.getStackHeads()
	if top == nil {
		return
	}

	switch token.tokenType {
	case tOption, tRawArgument, tSingleQuotedArgument, tDoubleQuotedArgument:
		top.args.update(tokenIndex)
		if redir != nil {
			lastRedir := &command.redirections[len(command.redirections)-1]
			lastRedir.update(tokenIndex)
		}
	case tRedirection:
		redir = new(cmdCommand)
		parser.Tokens[tokenIndex].command = redir
		parser.setStackHeads(command, redir)
		fallthrough
	case tShellRedirection, tFileRedirection:
		command.redirections = append(command.redirections, tokenRange)
	case tBlockBegin:
		top.blocks = append(top.blocks, tokenRange)
		parser.growStack()
	case tBlockEnd:
		lastBlock := len(top.blocks) - 1
		if lastBlock < 0 {
			parser.LastError = fmt.Errorf("block underflow in command")
			return
		}
		top.blocks[lastBlock].update(tokenIndex)

		if redir != nil {
			lastRedir := &command.redirections[len(command.redirections)-1]
			lastRedir.update(tokenIndex)
		}
	}

	if command != nil {
		// Cannot use "update" here as command is the only token which may have
		// starting index =0 (update logic relies on that tokens cannot have it)
		command.command.end = tokenIndex
	}
}

// Command stack simple operations (helper for insertToken)
func (parser *cmdTokenParser) getCommandIndex() int {
	return len(parser.commandStack) - 2
}

func (parser *cmdTokenParser) getRedirIndex() int {
	return len(parser.commandStack) - 1
}

func (parser *cmdTokenParser) setStackHeads(command *cmdCommand, redir *cmdCommand) {
	if len(parser.commandStack) < 2 {
		parser.LastError = fmt.Errorf("command stack underflow while setting new head")
		return
	}

	parser.commandStack[parser.getCommandIndex()] = command
	parser.commandStack[parser.getRedirIndex()] = redir
}

func (parser *cmdTokenParser) getStackHeads() (command *cmdCommand, redir *cmdCommand, top *cmdCommand) {
	if len(parser.commandStack) < 2 {
		parser.LastError = fmt.Errorf("command stack underflow")
		return
	}

	command = parser.commandStack[parser.getCommandIndex()]
	if command == nil {
		parser.LastError = fmt.Errorf("empty command head")
		return
	}

	redir = parser.commandStack[parser.getRedirIndex()]
	top = redir
	if top == nil {
		top = command
	}
	return
}

func (parser *cmdTokenParser) growStack() {
	parser.commandStack = append(parser.commandStack, []*cmdCommand{nil, nil}...)
}

func (parser *cmdTokenParser) shrinkStack() {
	parser.commandStack = parser.commandStack[:parser.getCommandIndex()]
}

// For debugging
func (token cmdToken) String() string {
	return fmt.Sprintf("{ token [%2d;%2d]@%d %8s %s #%d }", token.startPos,
		token.endPos, token.line, tokenTypeStrings[token.tokenType],
		strconv.Quote(token.token), token.argIndex)
}
