package fishly

import (
	"bytes"
	
	"io"
	"fmt"
	
	"strings"
	"strconv"
	
	"unicode"
)

const (
	tWhiteSpace = -1
	tCommand = iota
	tRedirect			// '|', the rest is parsed as command & arguments
	tFileRedirect		// '>' followed by filename
	tOption				// Option starting with '-'
	tArgument			// Command argument
	tInterporableArgument // Command argument which supports interpolation
)

const (
	whiteSpaceDelimiters = " \n\t"	// Whitespace characters using as delimiters
)

type cmdToken struct {
	// Positions (for auto-complete)
	startPos, endPos int
	
	// Type of the token
	tokenType int
	
	// Token text
	token string
}

type cmdTokenParser struct {
	tokenType int
	pos int
	
	hasMore bool
	
	buf *bytes.Buffer
	lastError error
}

func (ctx *Context) parseLine(line string) ([]cmdToken, error) {
	parser := cmdTokenParser {
		// first token should be command
		tokenType: tCommand,
		pos: 0,
		buf: bytes.NewBufferString(line),
		hasMore: true,
	}
	
	tokens := make([]cmdToken, 0)
	for parser.hasMore {
		nextTokenType := tArgument
		if !parser.ignoreWhiteSpace() {
			break
		}
		
		ch := parser.readRune(true)
		if !parser.hasMore {
			break
		}
		
		var token cmdToken
		switch ch {
			case '\'':
				token = parser.readToken(tArgument, "'", false)
			case '"':
				token = parser.readToken(tInterporableArgument, `"`, false) 
			case '-':
				token = parser.readToken(tOption, whiteSpaceDelimiters, true)
			case '|':
				token = parser.readToken(tRedirect, whiteSpaceDelimiters, false)
				if len(token.token) == 0 || token.token == "sh" {
					// Shell-style redirect -- ignore the rest and interpret it as 
					// single token. | is alias for |sh
					token.token = "sh" 
					tokens = append(tokens, token)
					
					// Read until the end of the line 
					parser.ignoreWhiteSpace()
					token = parser.readToken(tArgument, "", true)
				} else {
					nextTokenType = tCommand
				}
			case '>':
				parser.ignoreWhiteSpace()
				token = parser.readToken(tRedirect, whiteSpaceDelimiters, true)
				
				if parser.ignoreWhiteSpace() {
					parser.lastError = fmt.Errorf("extra characters after redirection")
					break
				}
			default:
				if !parser.unreadRune() {
					break
				}
				token = parser.readToken(parser.tokenType, whiteSpaceDelimiters, true)
		}
		
		if parser.lastError != nil {
			break 
		}
			
		tokens = append(tokens, token)
		parser.tokenType = nextTokenType
	}
	
	if parser.lastError != nil {
		return tokens, fmt.Errorf("Parse error at %d: %s", parser.pos+1, parser.lastError) 
	}
	return tokens, nil
}

func (parser *cmdTokenParser) readToken(tokenType int, delimiters string, expectEOF bool) cmdToken {
	var token cmdToken
	
	if parser.tokenType == tCommand && tokenType != tCommand {
		parser.lastError = fmt.Errorf("unexpected token #%d, expected command", tokenType)
		return token
	}
	
	token.startPos = parser.pos
	token.tokenType = tokenType
	token.token = parser.readUntil(delimiters, expectEOF)
	token.endPos = parser.pos - 1
	
	return token
}

func (parser *cmdTokenParser) ignoreWhiteSpace() bool {
	for parser.hasMore {
		ch := parser.readRune(true)
		if !parser.hasMore { 
			break
		}
		
		if !unicode.IsSpace(ch) {
			return parser.unreadRune()
		}
	}
	
	return parser.hasMore
}

// Reads until delimiters will be found
func (parser *cmdTokenParser) readUntil(delimiters string, expectEOF bool) string {
	buffer := bytes.NewBuffer([]byte{})
	
	for parser.hasMore {
		ch := parser.readRune(expectEOF)
		if parser.lastError != nil || !parser.hasMore {
			// Even if we failed while parsing string, return what we managed 
			// to parse (for auto completion) 
			break
		}
		
		if ch == '\\' {
			ch = parser.readEscapeSequence()
			if parser.lastError != nil {
				break
			}
			
			buffer.WriteRune(ch)
			continue
		}
		
		index := strings.IndexRune(delimiters, ch)
		if index >= 0 {
			break
		} 
		
		buffer.WriteRune(ch)
	}
	
	return buffer.String()
}

func (parser *cmdTokenParser) readEscapeSequence() (rune) {
	ch := parser.readRune(false)
	if parser.lastError != nil {
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
		if parser.lastError != nil {
			return -1
		}
		
		buffer.WriteRune(ch)
	}
	
	// Now convert number
	ord, err := strconv.ParseInt(buffer.String(), base, 32)
	if err != nil {
		parser.lastError = fmt.Errorf("failed to decode escape sequence: %s", err)
		return -1
	}
	
	return int(ord)
}

func (parser *cmdTokenParser) readRune(expectEOF bool) (ch rune) {
	ch, _, parser.lastError = parser.buf.ReadRune()
	parser.pos += 1
	
	if parser.lastError != nil {
		parser.hasMore = false
	}
	if parser.lastError == io.EOF {
		if expectEOF {
			parser.lastError = nil
		}
	}
	
	return 
}

func (parser *cmdTokenParser) unreadRune() bool {
	parser.lastError = parser.buf.UnreadRune()
	parser.pos -= 1
	if parser.lastError == nil {
		parser.hasMore = true
	}
	
	return parser.hasMore
}
