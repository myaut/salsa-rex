package yatima_test

import (
	"testing"

	"bytes"
	"io/ioutil"
	"os"
	"path/filepath"

	"yatima"
)

func TestLinkerLibrary(t *testing.T) {
	programs, asErr := yatima.Compile(bytes.NewBufferString(summatorText))
	if asErr != nil {
		t.Error(asErr)
		return
	}

	tmpDir, err := ioutil.TempDir("/tmp", "yatima_test")
	if err != nil {
		t.Error(err)
		return
	}
	defer os.RemoveAll(tmpDir)

	libFile, err := os.Create(filepath.Join(tmpDir, "actor.yab"))
	if err != nil {
		t.Error(err)
		return
	}
	defer libFile.Close()

	yabWriter, err := yatima.NewWriter(libFile)
	if err != nil {
		t.Error(err)
		return
	}

	for _, prog := range programs {
		err = yabWriter.AddProgram(prog)
		if err != nil {
			t.Error(err)
			return
		}
	}
	yabWriter.Close()

	library, err := yatima.LoadLibraryFromPath(filepath.Join(tmpDir, "actor.yab"))
	if err != nil {
		t.Error(err)
		return
	}

	prog := library.FindProgram("plus")
	if prog == nil {
		t.Errorf("plus() is not found")
		return
	}

	validateSummator(t, prog)
}
