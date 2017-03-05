package fishly

import (
	"io"
	"bytes"
	"errors"
	
	"strings"
	
    "testing"
)

// Loop test mocks readline driver with this:
type FakeReadline struct {
	// Lines to be fed to reader. EOF is produced after last line
	// ^C is a special line which returns "interrupted" 
	lines []string
	index int
	
	// Updated by SetPrompt
	prompt string
	
	// pseudo-stderr and command outputs to collect fishly outputs
	sinks []*bytes.Buffer 
	stderr *bytes.Buffer
	
	// pointer to context (updated by factory)
	ctx *Context
}

func (rl *FakeReadline) Close() error {
	return nil
}
func (rl *FakeReadline) Stderr() io.Writer {
	return rl.stderr
}
func (rl *FakeReadline) SetPrompt(prompt string) {
	rl.prompt = prompt
} 
func (rl *FakeReadline) Readline() (string, error) {
	if rl.index >= len(rl.lines) {
		return "", io.EOF
	}
	
	line := rl.lines[rl.index]
	rl.index++
	
	if line == "^C" {
		return "", errors.New("Interrupted")
	}
	return line, nil
}
func (rl *FakeReadline) Create(ctx *Context) (ReadlineDriver, error) {
	rl.stderr = bytes.NewBuffer([]byte{})
	rl.ctx = ctx
	return rl, nil
}

type FakeReadlineSink struct {
	HandlerWithoutCompletion
	HandlerWithoutOptions
	rl *FakeReadline
}
func (rls *FakeReadlineSink) IsTTY(ctx *Context) bool {
	return false
}
func (rls *FakeReadlineSink) NewSink(ctx *Context, rq *IOSinkRequest) (io.WriteCloser, error) {
	buf := bytes.NewBuffer([]byte{})
	rls.rl.sinks = append(rls.rl.sinks, buf)
	return &streamWrapper{Writer: buf}, nil
}

type FakeExternalContext struct {}
func (ctx *FakeExternalContext) Update(cliCtx *Context) error {	
	return nil
}
func (ctx *FakeExternalContext) Cancel(rq *Request) {
}

func testCommandsRun(lines []string) *FakeReadline {
	var extCtx FakeExternalContext
	rl := &FakeReadline{
		lines: lines,
	}
	rls := &FakeReadlineSink {rl: rl}
	cfg := Config{
		Cancel: &CLIInterruptHandlerFactory{},
		Readline: rl,
	}
	
	// Always use cat formatter in tests
	cat := new(textFormatter)
	cfg.RegisterIOFormatter(cat, "cat")
	cfg.DefaultTextFormatter = cat
	cfg.DefaultRichTextFormatter = cat
	cfg.DefaultSink = rls
	cfg.DefaultPagerSink = rls
	
	cfg.Run(&extCtx)
	return rl
} 

func TestNoCommands(t *testing.T) {
	rl := testCommandsRun([]string{})
	
	if rl.stderr.Len() > 0 {
		t.Errorf("Unexpected error")
		t.Log(rl.stderr.String())
	}
}

func TestInvalidCommand(t *testing.T) {
	rl := testCommandsRun([]string{"invalid"})
	
	errStr := rl.stderr.String()
	if !strings.Contains(errStr, "not found or not applicable") {
		t.Errorf("Invalid error is reported")
		t.Log(errStr)
	}
}

