package fishly

import (
	"io"
	
	"strings"
	
	"os"	
	"os/exec"
)

//
// io_sinks -- default sinks implementation in fishly
//

// wrapper for streams which ignores Close() 
type streamWrapper struct {
	io.Writer
}

func (w *streamWrapper) Write(p []byte) (int, error) {
	return w.Writer.Write(p)
}

func (w *streamWrapper) Close() error {
	// Flush and print new line after last object
	io.WriteString(w.Writer, "\n")
	return nil
}

// stdout sink -- a very simple sink which dumps data directly to 
// readline stdout. however, it ignores close call
type stdoutSink struct {
	HandlerWithoutCompletion
	HandlerWithoutOptions
}

func (*stdoutSink) IsTTY(ctx *Context) bool {
	// TODO: actually identify what os.Stdout is?
	return true
}

func (*stdoutSink) NewSink(ctx *Context, rq *IOSinkRequest) (io.WriteCloser, error) {
	return &streamWrapper{Writer: os.Stdout}, nil
}


// wrapper for commands that also waits for command to finish
type commandWrapper struct {
	cmd 	*exec.Cmd
	pipe 	io.WriteCloser
}

func (w *commandWrapper) Write(p []byte) (int, error) {
	return w.pipe.Write(p)
}

func (w *commandWrapper) Close() error {
	err := w.pipe.Close()
	if err != nil {
		w.cmd.Process.Kill()
		w.cmd.Wait()
		return err		
	}
	
	return w.cmd.Wait()
}

// pager sink -- spawns pager from config 
type pagerSink struct {
	HandlerWithoutCompletion
	HandlerWithoutOptions
	
	command string
}

func (*pagerSink) IsTTY(ctx *Context) bool {
	return true
}

func (sink *pagerSink) NewSink(ctx *Context, rq *IOSinkRequest) (io.WriteCloser, error) {
	pager := strings.Split(strings.TrimSpace(sink.command), " ")
	 
	cmd := exec.Command(pager[0], pager[1:]...)
	pipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	
	// Inherit current terminal 
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	
	err = cmd.Start()
	
	return &commandWrapper{cmd: cmd, pipe: pipe}, err
}
