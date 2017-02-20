package fishly

import (
	"reflect"
	
	"strings"
		
	"testing"
)

func assertBlockWalkerNil(t *testing.T, walker *cmdBlockTokenWalker, descr string) {
	if walker != nil {
		t.Errorf("Got extra block walker after %s", descr)
	}
}

func assertBlockWalkerExists(t *testing.T, walker *cmdBlockTokenWalker, descr string) (*cmdBlockTokenWalker) {
	if walker == nil {
		t.Errorf("Missing walker for %s", descr)
		return nil
	}
	return walker
}

type cmdCommandTokenWalkerInterface interface {
	getFirstToken() cmdToken
	getArguments() []cmdToken
}

func commandWalkerIsNil(walker cmdCommandTokenWalkerInterface) bool {
	return walker == (*cmdCommandTokenWalker)(nil) || walker == (*cmdRedirTokenWalker)(nil)
}

func assertCommandWalker(t *testing.T, walker cmdCommandTokenWalkerInterface, name string, args... string) bool {
	if commandWalkerIsNil(walker) {
		t.Errorf("Missing walker for %s", name)
		return false
	}
	
	firstToken := walker.getFirstToken()
	if firstToken.token != name {
		t.Errorf("Unexpected first token for %s: got %s", name, firstToken.token)
		return false
	}
	
	argTokens := walker.getArguments()
	if len(argTokens) > len(args) {
		t.Errorf("Extra arguments for %s: expected %d, got %d", name, 
			len(args), len(argTokens))
	}
	for i, arg := range args {
		if i >= len(argTokens) {
			t.Errorf("Missing token argument/option %s", arg)
			continue
		}
		if arg != argTokens[i].token {
			t.Errorf("Invalid token argument/option %s: got %s", arg, argTokens[i].token)
		}
	}
	
	return true
}

func assertCommandWalkerNil(t *testing.T, walker cmdCommandTokenWalkerInterface, descr string) {
	if !commandWalkerIsNil(walker) {
		t.Errorf("Got extra walker after %s (%v)", descr, walker.getFirstToken())
	}
}

func TestBlocksWalker(t *testing.T) {
	line := "cmd1 { cmd2 arg2 } | redir1 { cmd3 } > fileX ; cmd4 -opt4"
	parser := newParser()
	parser.parseLine(line)
	
	walker := parser.createRootWalker()
	
	if cmd1 := walker.nextCommand(); assertCommandWalker(t, cmd1, "cmd1") {
		if cmd1block := assertBlockWalkerExists(t, cmd1.nextBlock(), "cmd1 block"); cmd1block != nil {
			assertCommandWalker(t, cmd1block.nextCommand(), "cmd2", "arg2")
			assertCommandWalkerNil(t, cmd1block.nextCommand(), "cmd2")
		}
		assertBlockWalkerNil(t, cmd1.nextBlock(), "cmd1 block")
		
		if redir1 := cmd1.nextRedirection(); assertCommandWalker(t, redir1, "redir1") {
			if redir1block := assertBlockWalkerExists(t, redir1.nextBlock(), "redir1 block"); redir1block != nil {
				assertCommandWalker(t, redir1block.nextCommand(), "cmd3")
				assertCommandWalkerNil(t, redir1block.nextCommand(), "cmd3")
			}
		}
		
		assertCommandWalker(t, cmd1.nextRedirection(), "fileX")
		assertCommandWalkerNil(t, cmd1.nextRedirection(), "cmd1 redir")
	}
	
	assertCommandWalker(t, walker.nextCommand(), "cmd4", "opt4")
	assertCommandWalkerNil(t, walker.nextCommand(), "cmd4")
}

func TestBlocksNestingWalker(t *testing.T) {
	line := "cmd1 {cmd2 { cmd3 }} {cmd4 {} } ; cmd5"
	parser := newParser()
	parser.parseLine(line)
	
	walker := parser.createRootWalker()
	
	if cmd1 := walker.nextCommand(); assertCommandWalker(t, cmd1, "cmd1") {
		if cmd1block := assertBlockWalkerExists(t, cmd1.nextBlock(), "cmd1 block #1"); cmd1block != nil {
			if cmd2 := cmd1block.nextCommand(); assertCommandWalker(t, cmd2, "cmd2") {
				if cmd2block := assertBlockWalkerExists(t, cmd2.nextBlock(), "cmd2 block"); cmd2block != nil {
					if cmd3 := cmd2block.nextCommand(); assertCommandWalker(t, cmd3, "cmd3") {
						assertBlockWalkerNil(t, cmd3.nextBlock(), "cmd3 block")
					}
					assertCommandWalkerNil(t, cmd2block.nextCommand(), "cmd2")
				}
				assertBlockWalkerNil(t, cmd2.nextBlock(), "cmd2 block")
			}
			assertCommandWalkerNil(t, cmd1block.nextCommand(), "cmd1")
		}
		if cmd1block := assertBlockWalkerExists(t, cmd1.nextBlock(), "cmd1 block #2"); cmd1block != nil {
			if cmd4 := cmd1block.nextCommand(); assertCommandWalker(t, cmd4, "cmd4") {
				if cmd4block := assertBlockWalkerExists(t, cmd4.nextBlock(), "cmd4 block"); cmd4block != nil {
					assertCommandWalkerNil(t, cmd4block.nextCommand(), "cmd4")
				}
				assertBlockWalkerNil(t, cmd4.nextBlock(), "cmd4 block")
			}
		}
		assertBlockWalkerNil(t, cmd1.nextBlock(), "cmd1 block #2")
	}
	
	assertCommandWalker(t, walker.nextCommand(), "cmd5")
	assertCommandWalkerNil(t, walker.nextCommand(), "cmd5")
}

func assertParserWalker(t *testing.T, line string, command string) (*cmdCommandTokenWalker) {
	parser := newParser()
	parser.parseLine(line)
	if parser.LastError != nil {
		t.Error(parser.LastError)
		return nil
	}
	
	cmd := parser.createRootWalker().nextCommand() 
	if cmd == nil || cmd.getFirstToken().token != command {
		t.Errorf("Walker didn't produce command or command is invalid")
		return nil
	}
	
	return cmd
}

func noInterpolate(s string) string {
	return s
}

func TestArgParser(t *testing.T) {
	line := `cmd1 -opt1 1 -opt1 2 -opt2 -opt3 -opt3 \
				1 arg'2' arg3 arg4`
	cmd := assertParserWalker(t, line, "cmd1")
	if cmd == nil {
		return
	}
	
	type opt1 struct {
		Opt1 []int		`opt:"opt1"`
		Opt2 bool		`opt:"opt2"`
		Opt3 int		`opt:"opt3,count"`
		Arg1 uint32		`arg:"1"`
		Arg2 string		`arg:"2"`
		Arg3 []string	`arg:"3,opt"`
	}
	
	var opts opt1
	argParser := cmd.parseArgs(&opts, noInterpolate)
	if argParser.LastError != nil {
		if argParser.index < len(argParser.args) {
			t.Errorf("Error: %s at token %s", argParser.LastError, 
				argParser.args[argParser.index])
		} else {
			t.Error(argParser.LastError)
		}
	}
	
	optExpect := opt1{
		Opt1: []int{1, 2},
		Opt2: true,
		Opt3: 2,
		Arg1: 1,
		Arg2: "arg2",
		Arg3: []string{"arg3", "arg4"},
	}
	if !reflect.DeepEqual(opts, optExpect) {
		t.Errorf("Invalid argparse output")
		t.Logf("%+v (expected)", optExpect)
		t.Logf("%+v (got)", opts)
	}
}

func assertError(t *testing.T, argParser *cmdArgumentParser, substr string, tokenIndex int) {
	if argParser.LastError == nil {
		t.Errorf("Parser didn't produce error %s", substr)
		return
	}
	
	if !strings.Contains(argParser.LastError.Error(), substr) {
		t.Errorf("Parser produced incorrect error (expected: %s)", substr)
		t.Log(argParser.LastError)
	}
	
	if argParser.index != tokenIndex {
		t.Errorf("Incorrect parser error index %d (expected %d)", argParser.index, tokenIndex)
		if tokenIndex < len(argParser.args) {
			t.Logf("%+v (expected)", argParser.args[tokenIndex])	
		}
		if argParser.index < len(argParser.args) {
			t.Logf("%+v (got)", argParser.args[argParser.index])	
		}
	}
}

func TestArgParserErrors(t *testing.T) {
	// overflow
	line := `cmd1 12345678901234567890`
	cmd := assertParserWalker(t, line, "cmd1")
	if cmd != nil {
		type opt1 struct {
			Arg1 int16 `arg:"1"`
		}
		argParser := cmd.parseArgs(&opt1{}, noInterpolate)
		assertError(t, argParser, "value out of range", 0)
	}
	
	// missing opt value
	line = `cmd1 -opt1`
	cmd = assertParserWalker(t, line, "cmd1")
	if cmd != nil {
		type opt1 struct {
			Opt1 string `opt:"opt1"`
		}
		argParser := cmd.parseArgs(&opt1{}, noInterpolate)
		assertError(t, argParser, "argument is expected, got EOL", 0)
	}
	
	// missing opt value #2
	line = `cmd1 -opt1 -opt2`
	cmd = assertParserWalker(t, line, "cmd1")
	if cmd != nil {
		type opt1 struct {
			Opt1 string `opt:"opt1"`
			Opt2 bool `opt:"opt2"`
		}
		argParser := cmd.parseArgs(&opt1{}, noInterpolate)
		assertError(t, argParser, "argument is expected", 0)
	}
	
	// specified two times
	line = `cmd1 -opt1 s1 -opt1 s2`
	cmd = assertParserWalker(t, line, "cmd1")
	if cmd != nil {
		type opt1 struct {
			Opt1 string `opt:"opt1"`
		}
		argParser := cmd.parseArgs(&opt1{}, noInterpolate)
		assertError(t, argParser, "option or argument already specified", 3)
	}
	
	// missing necessary argument
	line = `cmd1`
	cmd = assertParserWalker(t, line, "cmd1")
	if cmd != nil {
		type opt1 struct {
			Arg1 bool `arg:"1"`
		}
		argParser := cmd.parseArgs(&opt1{}, noInterpolate)
		assertError(t, argParser, "missing argument #1", 0)
	}
}
