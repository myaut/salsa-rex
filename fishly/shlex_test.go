package fishly

import (
	"fmt"
	"strings"
	"strconv"
	
	"testing"
)

const notArgument = -1

func assertToken(t *testing.T, line string, parser *cmdTokenParser, i int, 
			fullText string, tokenType cmdTokenType, 
			tokenText string, argIndex int) {
	if len(parser.Tokens) <= i {
		t.Errorf("Missing token #%d", i)
		return 
	}
	
	token := parser.Tokens[i]
	
	startPos := strings.Index(line, fullText)
	length := len(fullText)
	if token.startPos != startPos || token.endPos != startPos+length {
		t.Errorf("Invalid token #%d bounds: [%d;%d] != [%d;%d]", i,
			token.startPos, token.endPos, startPos, startPos+length)
	}
	if token.tokenType != tokenType {
		t.Errorf("Invalid token #%d type: %s != %s", i,
			tokenTypeStrings[token.tokenType], tokenTypeStrings[tokenType])
	}
	if token.token != tokenText {
		t.Errorf("Invalid token #%d text: %s != %s", i, 
			strconv.Quote(token.token), strconv.Quote(tokenText))
	}
	if argIndex > -1 && token.argIndex != argIndex {
		t.Errorf("Invalid token #%d argument index: %d != %d", i,
			token.argIndex, argIndex)
	}
}

func TestEmpty(t *testing.T) {
	parser := newParser()
	parser.parseLine("")
	
	if len(parser.Tokens) != 0 {
		t.Errorf("Unexpected number of tokens: %d", len(parser.Tokens))
	}
}

func TestDashes(t *testing.T) {
	line := "-cmd -opt -- -arg"
	parser := newParser()
	parser.parseLine(line)
	
	assertToken(t, line, parser, 0, "-cmd", tCommand, "-cmd", notArgument)
	assertToken(t, line, parser, 1, "-opt", tOption, "opt", notArgument)
	assertToken(t, line, parser, 2, "-arg", tRawArgument, "-arg", 1)
}

func TestSemicolon(t *testing.T) {
	line := "cmd1 arg1 ; cmd2"
	parser := newParser()
	parser.parseLine(line)
	
	assertToken(t, line, parser, 0, "cmd1", tCommand, "cmd1", notArgument)
	assertToken(t, line, parser, 1, "arg1", tRawArgument, "arg1", 1)
	assertToken(t, line, parser, 2, ";", tCommandSeparator, ";", notArgument)
	assertToken(t, line, parser, 3, "cmd2", tCommand, "cmd2", notArgument)
}

func TestMultiArg(t *testing.T) {
	line := `cmd1 a'r'"g" xxx`
	parser := newParser()
	parser.parseLine(line)
	
	assertToken(t, line, parser, 0, "cmd1", tCommand, "cmd1", notArgument)
	assertToken(t, line, parser, 1, "a", tRawArgument, "a", 1)
	assertToken(t, line, parser, 2, "r", tSingleQuotedArgument, "r", 1)
	assertToken(t, line, parser, 3, "g", tDoubleQuotedArgument, "g", 1)
	assertToken(t, line, parser, 4, "xxx", tRawArgument, "xxx", 2)
}

func TestBlock(t *testing.T) {
	line := `cmd1 { cmd2 }`
	parser := newParser()
	parser.parseLine(line)
	
	assertToken(t, line, parser, 0, "cmd1", tCommand, "cmd1", notArgument)
	assertToken(t, line, parser, 1, "{", tBlockBegin, "{", notArgument)
	assertToken(t, line, parser, 2, "cmd2", tCommand, "cmd2", notArgument)
	assertToken(t, line, parser, 3, "}", tBlockEnd, "}", notArgument)
}

func TestComment(t *testing.T) {
	line := "cmd1 arg1 # cmd2"
	parser := newParser()
	parser.parseLine(line)
	
	assertToken(t, line, parser, 0, "cmd1", tCommand, "cmd1", notArgument)
	assertToken(t, line, parser, 1, "arg1", tRawArgument, "arg1", 1)
	if len(parser.Tokens) > 2 {
		t.Errorf("Only 2 tokens are expected")
	}
}

func TestRedirect(t *testing.T) {
	line := "cmd1 |filt1 -opt arg1| filt2 arg2 > file "
	parser := newParser()
	parser.parseLine(line)
	
	assertToken(t, line, parser, 0, "cmd1", tCommand, "cmd1", notArgument)
	assertToken(t, line, parser, 1, "filt1", tRedirection, "filt1", notArgument)
	assertToken(t, line, parser, 2, "-opt", tOption, "opt", notArgument)
	assertToken(t, line, parser, 3, "arg1", tRawArgument, "arg1", 1)
	assertToken(t, line, parser, 4, "filt2", tRedirection, "filt2", notArgument)
	assertToken(t, line, parser, 5, "arg2", tRawArgument, "arg2", 1)
	assertToken(t, line, parser, 6, "file", tFileRedirection, "file", notArgument)
}

func testShellRedirect(t *testing.T, redir string) {
	shellArg := "grep .* | awk {} | sed -e 's//g' "
	redir = fmt.Sprintf("%s %s", redir, shellArg)
	line := fmt.Sprintf("cmd1 %s; cmd2", redir)
	
	parser := newParser()
	parser.parseLine(line)
	
	assertToken(t, line, parser, 0, "cmd1", tCommand, "cmd1", notArgument)
	assertToken(t, line, parser, 1, shellArg, tShellRedirection, shellArg, notArgument)
	assertToken(t, line, parser, 3, "cmd2", tCommand, "cmd2", notArgument)
}

func TestShellRedirect1(t *testing.T) {
	testShellRedirect(t, "|sh")
}

func TestShellRedirect2(t *testing.T) {
	testShellRedirect(t, "!")
}

func TestMultiline(t *testing.T) {
	line := `
		# Comment
		cmd1 |
			filt1
		cmd2 \*	\
			arg2
	`
	parser := newParser()
	parser.parseLine(line)
	
	assertToken(t, line, parser, 0, "cmd1", tCommand, "cmd1", notArgument)
	assertToken(t, line, parser, 1, "filt1", tRedirection, "filt1", notArgument)
	// 2 -- command separator
	assertToken(t, line, parser, 3, "cmd2", tCommand, "cmd2", notArgument)
	assertToken(t, line, parser, 4, "\\*", tRawArgument, "*", 1)
	assertToken(t, line, parser, 5, "arg2", tRawArgument, "arg2", 2)
}

func TestMultilineSH(t *testing.T) {
	line := `
		cmd1 ! grep \
			awk
	`
	parser := newParser()
	parser.parseLine(line)
	if len(parser.Tokens) > 3 {
		t.Errorf("Only 3 tokens are expected: Command, ShellRedirect and Separator")
	}
}

func assertNotExpectMore(t *testing.T, line string) {
	parser := newParser()
	parser.parseLine(line)
	if parser.ExpectMore {
		t.Errorf("Parser shouldn't expect more for %s", strconv.Quote(line))		
	}
}

func assertExpectMore(t *testing.T, line string) (*cmdTokenParser) {
	parser := newParser()
	parser.parseLine(line)
	if !parser.ExpectMore {
		t.Errorf("Parser should expect more for %s", strconv.Quote(line))		
	}
	
	return parser
}

func TestExpectMore(t *testing.T) {
	assertNotExpectMore(t, "cmd1")
	assertNotExpectMore(t, "cmd1 arg1 { cmd2 }")
	assertNotExpectMore(t, "cmd1 arg1 \\n ")
	
	lines := []string{"cmd1 'arg1", "arg2'"}
	parser := assertExpectMore(t, lines[0])
	parser.parseLine(lines[1])
	assertToken(t, strings.Join(lines, "\n"), parser, 1, "arg1\narg2", 
		tSingleQuotedArgument, "arg1\narg2", 1)
	
	lines = []string{"cmd1 arg1 |", "redir2"}
	parser = assertExpectMore(t, lines[0])
	parser.parseLine(lines[1])
	assertToken(t, strings.Join(lines, "\n"), parser, 2, "redir2", 
		tRedirection, "redir2", notArgument)
	
	assertExpectMore(t, "cmd1 arg1 \\")
	assertExpectMore(t, "cmd1 arg1 {")
}

func assertCommandOrRedirection(t *testing.T, parser *cmdTokenParser, i int, command string) (*cmdCommand) {
	if len(parser.Tokens) <= i {
		t.Errorf("Missing token #%d", i)
		return nil
	}
	
	token := parser.Tokens[i]
	if token.token != command || (token.tokenType != tCommand && 
								  token.tokenType != tRedirection) {
		t.Errorf("Invalid token #%d: expected command/redir %s, got %s", i, 
			command, token.String())
		return nil
	}
	
	cmd := token.command
	if cmd == nil {
		t.Errorf("Invalid token #%d (command/redir %s), missing command pointer", i, command)
	}
	return cmd
}

func assertTokenRange(t *testing.T, ranges []cmdTokenRange, i int, start, end int) {
	if len(ranges) <= i {
		t.Errorf("Missing token range #%d", i)
		return
	}
	
	r := ranges[i]
	if r.start != start || r.end != end {
		t.Errorf("Invalid token range #%d bounds: [%d;%d] != [%d;%d]", i,
			r.start, r.end, start, end)
	}
}

func TestBlocks(t *testing.T) {
	// Note that ; is ignored because file redirection is implicit 
	// command separator
	//        0   1  2   3    4     5   6  7    8       9  10
	line := "cmd1 { cmd2 } | redir1 { cmd3 } > fileX ; cmd4 -opt4"
	parser := newParser()
	parser.parseLine(line)
	
	cmd1 := assertCommandOrRedirection(t, parser, 0, "cmd1")
	if cmd1 != nil {
		assertTokenRange(t, []cmdTokenRange{cmd1.command}, 0, 0, 8)
		assertTokenRange(t, cmd1.blocks, 0, 1, 3)
		assertTokenRange(t, cmd1.redirections, 0, 4, 7)
		assertTokenRange(t, cmd1.redirections, 1, 8, 8)
	}
	
	redir1 := assertCommandOrRedirection(t, parser, 4, "redir1")
	if redir1 != nil {
		assertTokenRange(t, redir1.blocks, 0, 5, 7)
		if len(redir1.redirections) > 0 {
			t.Errorf("Unexpected redirections for redir1")
		}
	}
	
	cmd4 := assertCommandOrRedirection(t, parser, 9, "cmd4")
	if cmd4 != nil {
		assertTokenRange(t, []cmdTokenRange{cmd4.command}, 0, 9, 10)
		assertTokenRange(t, []cmdTokenRange{cmd4.args}, 0, 10, 10)
		if len(cmd4.blocks) > 0 || len(cmd4.redirections) > 0 {
			t.Errorf("Unexpected blocks or redirecitons for cmd4")
		}
	}
}


func TestBlocksNesting(t *testing.T) {
	// Note: like with file redirection, last command block 
	// produces implicit command separator and wasn't added to
	// token stream (but this shouldn't confuse walkers and ranges)
	//        0   1  2  3   4  56 7  8  9 10 11   12
	line := "cmd1 {cmd2 { cmd3 }} {cmd4 {}}     ; cmd5"
	
	parser := newParser()
	parser.parseLine(line)
	
	cmd1 := assertCommandOrRedirection(t, parser, 0, "cmd1")
	if cmd1 != nil {
		assertTokenRange(t, []cmdTokenRange{cmd1.command}, 0, 0, 11)
		assertTokenRange(t, cmd1.blocks, 0, 1, 6)
		assertTokenRange(t, cmd1.blocks, 1, 7, 11)
		if len(cmd1.blocks) > 2 {
			t.Errorf("Unexpected redirections for redir1")
		}
	}
	
	cmd2 := assertCommandOrRedirection(t, parser, 2, "cmd2")
	if cmd1 != nil {
		assertTokenRange(t, []cmdTokenRange{cmd2.command}, 0, 2, 5)
		assertTokenRange(t, cmd2.blocks, 0, 3, 5)
	}
	
	cmd5 := assertCommandOrRedirection(t, parser, 12, "cmd5")
	if cmd5 != nil {
		if len(cmd5.blocks) > 0 || len(cmd5.redirections) > 0 {
			t.Errorf("Unexpected blocks or redirecitons for cmd5")
		}
	}
}
