package yatima

import (
	"io"
	"os"

	"fmt"
)

// Actor time mode specifies when %t entry point should be activated
// We do not currently variate this parameter as we expect that end-mode
// will be used only explicitly by REX-MON
type ActorTimeMode uint32

const (
	ActorTimeNone ActorTimeMode = iota
	ActorTimeWindow
	ActorTimeEnd
)

type Library struct {
	programs []*Program
}

// Pin index entry:
//				Network				Inputs
//	- Cluster 	0					1 + Incident index
//	- Group		Actor index			PageTag of series
// 	- Pin		Actor output index	Field index
//
type PinIndex struct {
	Cluster, Group, Pin uint32
}

// Pin defines an edge which interconnects input and actor or two actors,
// or actor output
type Pin struct {
	// Index is not defined here as we use offset in PinGroup as index of
	// TSF field or id of ActorInstance
	Name string
	Hint RegisterHintType
}

type Input struct {
	PinIndex

	// Address of the first entry point (others are single-linked list
	// using tail CALL instructions)
	EntryPoint uint32
}

type PinGroup struct {
	Name string
	Pins []Pin
}

type PinCluster struct {
	Name   string
	Groups []PinGroup
}

type ActorInstance struct {
	// Index of actor in program library
	ProgramIndex uint32

	TimeMode ActorTimeMode
	Inputs   []PinIndex
}

type Model struct {
	Pins   []PinCluster
	Actors []ActorInstance
	Inputs []Input

	library *Library
}

func NewModel(library *Library) *Model {
	return &Model{
		// First pin group is always for interconnecting actors
		Pins:   []PinCluster{PinCluster{Name: "network"}},
		Actors: make([]ActorInstance, 0, 1),

		library: library,
	}
}

func LoadLibrary(yabr *BinaryReader) (*Library, error) {
	library := new(Library)

	for {
		dir, err := yabr.ReadDirective()
		if err == io.EOF {
			break
		}
		if err == nil && dir.Type != BDProgram {
			err = yabr.IgnoreBlock()
		}
		if err != nil {
			return nil, err
		}

		prog, err := yabr.ReadProgram()
		if err != nil {
			return nil, err
		}

		library.programs = append(library.programs, prog)
	}

	return library, nil
}

func LoadLibraryFromPath(path string) (*Library, error) {
	inf, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer inf.Close()

	strf, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer strf.Close()

	yabr, err := NewReader(inf, strf)
	if err != nil {
		return nil, err
	}

	return LoadLibrary(yabr)
}

func (library *Library) FindProgram(name string) *Program {
	for _, prog := range library.programs {
		if prog.Name == name {
			return prog
		}
	}

	return nil
}

func (model *Model) AddActor(name string, inputs []PinIndex) (uint32, error) {
	prog := model.library.FindProgram(name)
	if prog == nil {
		return 0, fmt.Errorf("Program '%s' is not found", name)
	}

	return 0, nil
}
