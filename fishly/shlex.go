package fishly

import (
	"bytes"
	
	"io"
	"fmt"
	
	"strings"
	"strconv"
	
	"unicode"
)

type cmdTokenType int

const (
	tCommand = iota
	tCommandSeparator 	// ';', starts a new command
	tRedirection		// '|', the rest is parsed as command & arguments
	tFileRedirection	// '>' followed by filename
	tOption				// Option starting with '-'
	tRawArgument
	tSingleQuotedArgument		      
	tDoubleQuotedArgument // Command argument which supports interpolation
)

var tokenTypeStrings = []string {
	"Command",
	"CommandSeparator",
	"Redirect",
	"FileRedirect",
	"Option",
	"RawArg",
	"SQArg",
	"DQArg",
}

const (
	whiteSpaceDelimiters = " \n\t"	// Whitespace characters using as delimiters
	shlexDelimiters = whiteSpaceDelimiters + "|'\";"
)

type cmdToken struct {
	// Positions (for auto-complete)
	startPos, endPos int
	
	// Type of the token
	tokenType cmdTokenType
	
	// Token text
	token string
	
	// Index of argument
	argIndex int
}

type cmdTokenParser struct {
	// Output data
	Tokens []cmdToken
	
	// In case of error -- contains error
	LastError error
	Position int
	
	hasMore bool
	
	// Some state variables
	tokenType cmdTokenType
	allowOptions bool
	argIndex int
	
	// Buffer (input)
	buf *bytes.Buffer
}

func (ctx *Context) parseLine(line string) (*cmdTokenParser) {
	parser := cmdTokenParser {
		// first token should be command
		Tokens:  make([]cmdToken, 0),
		
		Position: 0,
		
		hasMore: true,
		tokenType: tCommand,
		allowOptions: false,
		argIndex: 0,
		buf: bytes.NewBufferString(line),
	}
	
	for parser.hasMore {
		whiteSpaceCount := parser.ignoreWhiteSpace()
		if !parser.hasMore {
			break
		}
		if whiteSpaceCount > 0 {
			parser.argIndex += 1
		}
		
		ch := parser.readRune(true)
		if !parser.hasMore {
			break
		}
		
		var token cmdToken
		switch ch {
			case ';':
				if parser.unreadRune() {
					// The trick is to feed read ';' rune to readToken
					token = parser.readToken(tCommandSeparator, ";", false, false)
					token.token = ";"
					parser.ignoreWhiteSpace()
				}
			case '#':
				// Comment -- ignore the rest of the line
				parser.hasMore = false
				continue
			case '\'':
				token = parser.readToken(tSingleQuotedArgument, "'", false, false)
			case '"':
				token = parser.readToken(tDoubleQuotedArgument, `"`, false, false) 
			case '-':
				if !parser.allowOptions {
					if parser.unreadRune() {
						token = parser.readToken(parser.tokenType, whiteSpaceDelimiters, true, true)
					}
				} else {
					token = parser.readToken(tOption, shlexDelimiters, true, true)
					if token.token == "-" {
						parser.allowOptions = false
						continue
					}
				}
			case '|':
				token = parser.readToken(tRedirection, shlexDelimiters, true, true)
				if len(token.token) == 0 || token.token == "sh" {
					// Shell-style redirect -- ignore the rest and interpret it as 
					// single token. | is alias for |sh
					parser.Tokens = append(parser.Tokens, token)
					
					// Read until the end of the line 
					parser.ignoreWhiteSpace()
					token = parser.readToken(tRawArgument, "", true, false)
				}
			case '>':
				parser.ignoreWhiteSpace()
				token = parser.readToken(tFileRedirection, whiteSpaceDelimiters, true, true)
				
				parser.ignoreWhiteSpace()
				if parser.hasMore {
					parser.LastError = fmt.Errorf("extra characters after redirection")
					break
				}
			default:
				if !parser.unreadRune() {
					break
				}
				
				token = parser.readToken(parser.tokenType, shlexDelimiters, true, true)	
		}
		
		if parser.LastError != nil {
			break 
		}
			
		parser.Tokens = append(parser.Tokens, token)
		
		// If we met command separator -- reset state of the parser to command mode
		// If we parsed command, start parsing its arguments 
		switch token.tokenType {
			case tCommand:
				parser.tokenType = tRawArgument
				parser.allowOptions = true
			case tCommandSeparator:
				parser.tokenType = tCommand
				parser.allowOptions = false
				fallthrough
			case tRedirection:
				parser.argIndex = 0
		} 
	}
	
	return &parser
}

func (parser *cmdTokenParser) readToken(tokenType cmdTokenType, delimiters string, 
										expectEOF, unreadRune bool) cmdToken {
	var token cmdToken
	
	token.startPos = parser.Position
	token.tokenType = tokenType
	token.token = parser.readUntil(delimiters, expectEOF, unreadRune)
	token.argIndex = parser.argIndex
	token.endPos = parser.Position - 1
	
	return token
}

func (parser *cmdTokenParser) ignoreWhiteSpace() int {
	whiteSpaceCount := 0 
	
	for parser.hasMore {
		ch := parser.readRune(true)
		if !parser.hasMore { 
			break
		}
		if !unicode.IsSpace(ch) {
			parser.unreadRune()
			break
		}
		
		whiteSpaceCount += 1
	}
	
	return whiteSpaceCount
}

// Reads until delimiters will be found
func (parser *cmdTokenParser) readUntil(delimiters string, expectEOF, unreadRune bool) string {
	buffer := bytes.NewBuffer([]byte{})
	
	for parser.hasMore {
		ch := parser.readRune(expectEOF)
		if parser.LastError != nil || !parser.hasMore {
			// Even if we failed while parsing string, return what we managed 
			// to parse (for auto completion) 
			break
		}
		
		if ch == '\\' {
			ch = parser.readEscapeSequence()
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
	
	return buffer.String()
}

func (parser *cmdTokenParser) readEscapeSequence() (rune) {
	ch := parser.readRune(false)
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

func (parser *cmdTokenParser) readEscapeSequenceEncoded(maxChars, base int) (int) {
	buffer := bytes.NewBuffer([]byte{})
	for i := 0 ; i < maxChars ; i += 1 {
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

func (parser *cmdTokenParser) readRune(expectEOF bool) (ch rune) {
	ch, _, parser.LastError = parser.buf.ReadRune()
	
	if parser.LastError != nil {
		parser.hasMore = false
	}
	if parser.LastError == io.EOF {
		if expectEOF {
			parser.LastError = nil
		}
	} else {
		parser.Position += 1
	}
	
	return 
}

func (parser *cmdTokenParser) unreadRune() bool {
	parser.LastError = parser.buf.UnreadRune()
	parser.Position -= 1
	if parser.LastError == nil {
		parser.hasMore = true
	}
	
	return parser.hasMore
}
