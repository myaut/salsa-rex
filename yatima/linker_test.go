package yatima_test

import (
	"testing"

	"fmt"

	"bytes"
	"io/ioutil"
	"os"
	"path/filepath"

	"yatima"
)

type testLinkerCallback func(tmpDir string) error

func testLinker(t *testing.T, callback testLinkerCallback) {
	tmpDir, err := ioutil.TempDir("/tmp", "yatima_test")
	if err != nil {
		t.Error(err)
		return
	}
	defer os.RemoveAll(tmpDir)

	err = callback(tmpDir)
	if err != nil {
		t.Error(err)
		return
	}
}

func linkLibrary(tmpDir string, programs []*yatima.Program) error {
	libFile, err := os.Create(filepath.Join(tmpDir, "actor.yab"))
	if err != nil {
		return err
	}
	defer libFile.Close()

	yabWriter, err := yatima.NewWriter(libFile)
	if err != nil {
		return err
	}

	for _, prog := range programs {
		err = yabWriter.AddProgram(prog)
		if err != nil {
			yabWriter.Close()
			return err
		}
	}
	yabWriter.Close()

	return nil
}

type testLinkerWithLibraryCallback func(tmpDir string, library *yatima.Library) error

func testLinkerWithLibrary(t *testing.T, callback testLinkerWithLibraryCallback) {
	testLinker(t, func(tmpDir string) error {
		programs, asErr := yatima.Compile(bytes.NewBufferString(summatorText))
		if asErr != nil {
			t.Error(asErr)
			return fmt.Errorf("Error in assembler")
		}

		err := linkLibrary(tmpDir, programs)
		if err != nil {
			return err
		}

		library, err := yatima.LoadLibraryFromPath(filepath.Join(tmpDir, "actor.yab"))
		if err != nil {
			return err
		}

		return callback(tmpDir, library)
	})
}

func TestLinkerLibrary(t *testing.T) {
	testLinkerWithLibrary(t, func(tmpDir string, library *yatima.Library) error {
		progIndex, prog := library.FindProgram("plus")
		if prog == nil {
			return fmt.Errorf("Plus was not found")
		}

		validateSummator(t, library.Programs[progIndex])
		return nil
	})
}

func linkModel(tmpDir string, model *yatima.Model) error {
	libFile, err := os.Create(filepath.Join(tmpDir, "model.yab"))
	if err != nil {
		return err
	}
	defer libFile.Close()

	yabWriter, err := yatima.NewWriter(libFile)
	if err != nil {
		return err
	}

	err = yabWriter.AddModel(model)
	if err != nil {
		yabWriter.Close()
		return err
	}

	yabWriter.Close()
	return nil
}

func linkLinkedProgram(tmpDir string, model *yatima.Model, prog *yatima.LinkedProgram) error {
	path := filepath.Join(tmpDir, model.Signature()+".yab")
	err := os.MkdirAll(filepath.Dir(path), 0777)
	if err != nil {
		return err
	}

	libFile, err := os.Create(path)
	if err != nil {
		return err
	}
	defer libFile.Close()

	yabWriter, err := yatima.NewWriter(libFile)
	if err != nil {
		return err
	}

	err = yabWriter.AddModel(model)
	if err == nil {
		yabWriter.AddLinkedProgram(prog)
	}
	yabWriter.Close()

	return err
}

func TestLinkerModel(t *testing.T) {
	testLinkerWithLibrary(t, func(tmpDir string, library *yatima.Library) error {
		fmt.Println(tmpDir)

		base := yatima.NewBaseModel(library)
		base.Inputs = []yatima.PinCluster{
			yatima.PinCluster{
				Name: "cluster",
				Groups: []yatima.PinGroup{
					yatima.PinGroup{
						Name: "group",
						Pins: []yatima.Pin{
							yatima.Pin{Name: "pin1", Hint: yatima.RIORandom},
							yatima.Pin{Name: "pin2", Hint: yatima.RIORandom},
							yatima.Pin{Name: "pinE", Hint: yatima.RIOEnumerable},
						},
					},
				},
			},
		}

		_, err := base.AddActor("plus", yatima.ActorTimeNone, make([]yatima.PinIndex, 2))
		if err != nil {
			return err
		}

		linkModel(tmpDir, base.Clone())

		mutator := base.NewMutator()
		var i int
		for model := mutator.Next(); model != nil; model = mutator.Next() {
			if model.Error == nil {
				fmt.Println(model.Signature())
				prog, err := model.Link()

				if err == nil {
					err = linkLinkedProgram("/tmp", model, prog)
				}
				if err != nil {
					t.Error(err)
				}
			}

			i++
			if i > 1024 {
				t.Error("Too many variants")
				break
			}
		}

		return nil
	})
}
