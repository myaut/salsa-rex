package yatima_test

import (
	"bytes"
	"fmt"

	"testing"

	"yatima"
)

func TestCorrelator(t *testing.T) {
	// Try to compile correlator and see its disassembly

	pText := `
		.ACTOR plus
			.REG %a random
			.REG %b random
			.REG %c random

			.ENTRY %a %b
				%c = %a + %b
		.AEND
	`

	programs, err := yatima.Compile(bytes.NewBufferString(pText))
	if err != nil {
		t.Error(err)
	} else {
		prog := programs[0]

		for _, instr := range prog.Instructions {
			t.Log(instr.Disassemble())
		}
		for index, value := range prog.Values {
			t.Logf("%4s = %d", fmt.Sprint("%v", index), value)
		}
	}
}
