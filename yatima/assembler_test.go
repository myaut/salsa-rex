package yatima_test

import (
	"bytes"

	"testing"

	"yatima"
)

var summatorText string = `
	.ACTOR plus
		.REG %a random
		.REG %b random
		.REG %c random
		.TRANS %a %b

		.ENTRY %a %b
			%c = %a + %b
	.AEND
`

func validateSummator(t *testing.T, prog *yatima.Program) {
	if len(prog.EntryPoints) != 2 {
		t.Errorf("2 entry points expected, got %d", len(prog.EntryPoints))
	} else {
		expectedRegs := []yatima.RegisterIndex{yatima.RI0, yatima.RI1}
		for i, ep := range prog.EntryPoints {
			if ep.Register != expectedRegs[i] || ep.Address != 0 {
				t.Errorf("Unexpected entry point %v", ep)
			}
		}
	}

	if len(prog.Hints) != 3 {
		t.Errorf("3 register hints are expected, got %d", len(prog.Hints))
	} else {
		expectedRegs := []yatima.RegisterIndex{yatima.RI0, yatima.RI1, yatima.RO0}
		for i, hint := range prog.Hints {
			if hint.Register != expectedRegs[i] || hint.Hint != yatima.RIORandom {
				t.Errorf("Unexpected hint %v", hint)
			}
		}
	}

	if len(prog.Instructions) != 5 {
		t.Errorf("5 instructions expected, got %d", len(prog.Instructions))
	} else {
		instr := prog.Instructions[0]
		if instr.I != yatima.ADD || instr.RI0 != yatima.RI0 ||
			instr.RI1 != yatima.RI1 || instr.RO != yatima.RO0 {
			t.Errorf("Invalid first instruction %s", instr.Disassemble())
		}
	}
}

func TestAssembler(t *testing.T) {
	programs, err := yatima.Compile(bytes.NewBufferString(summatorText))
	if err != nil {
		t.Error(err)
	}
	if len(programs) != 1 {
		t.Errorf("1 program expected, got %d", len(programs))
		return
	}

	prog := programs[0]
	validateSummator(t, prog)

}

func testAssemblerError(t *testing.T, expClass yatima.CompilerErrorClass, pText string) {
	_, err := yatima.Compile(bytes.NewBufferString(pText))
	if err == nil {
		t.Errorf("Missing error for %v")
	} else {
		if err.ErrorClass != expClass {
			t.Errorf("Expected error class %d, got error %v", expClass, err)
		}
	}
}

func TestAssemblerErrors(t *testing.T) {
	testAssemblerError(t, yatima.CEWrongDirective, `
		.ACTOR plus
	`)

	testAssemblerError(t, yatima.CEWrongDirective, `
		.ACTOR plus
			.ENTRY %a
			.REG %a hint
		.AEND
	`)

	testAssemblerError(t, yatima.CETokensCount, `
		.ACTOR plus
			.ENTRY
		.AEND
	`)

	testAssemblerError(t, yatima.CEWrongDirective, `
		.ACTOR plus
			.REG %a hint
		.AEND
	`)

	testAssemblerError(t, yatima.CEWrongRegister, `
		.ACTOR plus
			.REG %a static x
		.AEND
	`)

	testAssemblerError(t, yatima.CEWrongRegister, `
		.ACTOR plus
			.ENTRY %a
				%t = %t * %t
		.AEND
	`)

	testAssemblerError(t, yatima.CEUnknownLabel, `
		.ACTOR plus
			.ENTRY %a
				:END if %a == 0
		.AEND
	`)
}
