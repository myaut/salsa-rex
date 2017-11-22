package yatima_test

import (
	"bytes"

	"testing"

	"yatima"
)

func TestMachineSummator(t *testing.T) {
	programs, compileError := yatima.Compile(bytes.NewBufferString(summatorText))
	if compileError != nil {
		t.Error(compileError)
		return
	}

	library := &yatima.Library{Programs: programs}

	base := yatima.NewBaseModel(library)
	base.Inputs = []yatima.PinCluster{
		yatima.PinCluster{
			Groups: []yatima.PinGroup{
				yatima.PinGroup{
					Pins: []yatima.Pin{
						yatima.Pin{Name: "left", Hint: yatima.RIORandom},
						yatima.Pin{Name: "right", Hint: yatima.RIORandom},
					},
				},
			},
		},
	}

	_, err := base.AddActor("plus", yatima.ActorTimeNone,
		[]yatima.PinIndex{
			yatima.PinIndex{Cluster: 1, Pin: 0},
			yatima.PinIndex{Cluster: 1, Pin: 1},
		})
	if err != nil {
		t.Error(err)
	}

	model := base.Clone()
	linkedProgram, err := model.Link()
	if err != nil {
		t.Error(err)
		return
	}

	machine := linkedProgram.NewMachine()
	t.Log(machine)

	machine.WriteInput(0, 10)
	machine.WriteInput(1, 21)
	machine.Run()

	machine.DumpRegisters(t.Logf)
}
