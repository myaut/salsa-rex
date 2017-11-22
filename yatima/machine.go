package yatima

import (
	"fmt"
)

// Yatima (named after main hero of "Diaspora" by Greg Egan, ve was an AI)
// is is engine for machine learning based on experiment's time series.
//
// Originally, neural networks and decision trees have been considered for
// ML core in SALSA/REX, but as neural networks use polynoms inside neurons,
// they do not seem a good fit for time sequences and decision tree is not
// very good in crunching numbers
//
// Yatima uses actor networks: instead of simple polynomial experssion, actors
// might have logic (including branching). There is a library of predefined
// actors which are connected and pass integer and string values in network.
//
// Each actor is written in YAS (assembler-like language, except it has more
// user-friendly expressions instead of using mnemonics). When network is built,
// actor code snippets is linked: their input/output variables as well as state
// variables are being relocated to global register window. There is a potential
// possibility to translate YAS into machine native instructions, thus achieve
// max performance.
//
// The machine state is represented as global register window which is updated
// when input arrives and by actors which propagate computed values to their
// subscribers.

type RegisterIndex uint32
type InstructionCode uint32

const (
	// Global register block
	RZ  RegisterIndex = iota // "null" register -- can't be used anywhere
	R0                       // "zero" register -- always zero
	RT                       // global time register
	RIP                      // instruction pointer register

	// 8 registers for local variables
	RL0
	RL1
	RL2
	RL3
	RL4
	RL5
	RL6
	RL7

	// After that point registers will be relocated to actual machine addresses
	// (thus we call them virtual)

	// Virtual registers used as input registers for current set of actors
	RI0 // "a" -- first input
	RI1 // "b" -- second input
	RI2
	RI3

	// Virtual registers used as output registers
	RO0 // "c" -- first output
	RO1
	RO2
	RO3

	// Virtual registers for actor state
	RS0
	RS1
	RS2
	RS3

	// First virtual register for constant value
	RV
)

const (
	nearJumpRegister = RI0
)

const (
	// Note: when adding new instruction, do not forget to update
	// instructionDefinitions array in assembler.go

	NOP InstructionCode = iota

	// Special instructions for handling subprograms. Cannot be generated
	// by assembler, created when linker adds a subroutine.
	CALL // CALL [on %RO] $RI0:$RI1
	RET

	MOV // %RO = 		%RI1

	ADD // %RO = %RI0 + %RI1
	SUB // %RO = %RI0 - %RI1
	MUL // %RO = %RI0 * %RI1
	DIV // %RO = %RI0 / %RI1

	INC // %RO ++
	DEC // %RO --

	SHL // %RO = %RI0 << %RI1
	SHR // %RO = %RI0 >> %RI1

	ABS // %RO = 	abs %RI1

	JMP // %RO go
	JEQ // %RO if %RI0 == %RI1
	JNE // %RO if %RI0 != %RI1
)

type Machine struct {
	Program *LinkedProgram

	// Big registers table -- contains state of all actors
	Registers []int64

	// Stack of addresses that should be called. No state is saved here as
	// register table is used for that. Note that stack is operates like list
	// and can contain two sequental addresses written which are actually
	// siblings (i.e. in the case of writing multiple inputs and notifying machine)
	Stack []uint32
}

// Creates a machine which is ready to execute it
func (program *LinkedProgram) NewMachine() *Machine {
	regTableSize := int(RL7+1) + len(program.Registers) + len(program.Values)
	machine := &Machine{
		Program:   program,
		Registers: make([]int64, regTableSize),
		Stack:     make([]uint32, 0, 16),
	}

	// Copy initial program's values
	copy(machine.Registers[regTableSize-len(program.Values):],
		program.Values)

	return machine
}

// Tries to write to machine register or panics if register is read only
func (machine *Machine) WriteInput(pinIndex uint32, value int64) {
	pin := machine.Program.Registers[pinIndex]
	if pin.Cluster == 0 {
		panic("only inputs can be written")
	}

	regIndex := RL7 + 1 + RegisterIndex(pinIndex)
	machine.Registers[regIndex] = value

	machine.notify(pinIndex + 2)
}

func (machine *Machine) WriteTime(value int64, mode ActorTimeMode) {
	machine.Registers[RT] = value

	switch mode {
	case ActorTimeWindow:
		machine.notify(0)
	case ActorTimeEnd:
		machine.notify(1)
	}
}

// Notify on register update -- save corresponding call address to stack
func (machine *Machine) notify(vecIndex uint32) {
	callInstruction := machine.Program.Instructions[vecIndex]
	if callInstruction.RI1 != 0 {
		machine.Stack = append(machine.Stack, uint32(callInstruction.RI1))
	}
}

func (machine *Machine) Run() {
	for len(machine.Stack) > 0 {
		stackHead := len(machine.Stack) - 1
		address := machine.Stack[stackHead]
		machine.Stack = machine.Stack[:stackHead]

		machine.runAt(address)
	}
}

func (machine *Machine) runAt(address uint32) {
	instructions := machine.Program.Instructions
	registers := machine.Registers

	var instr Instruction

instructionLoop:
	for instr.I != RET {
		registers[RIP] = int64(address)
		instr = instructions[address]
		address++

		switch instr.I {
		case NOP, RET:
			continue instructionLoop
		case CALL:
			// TODO handle RI0 & segments
			machine.Stack = append(machine.Stack, uint32(instr.RI1))
			continue instructionLoop
		case INC:
			registers[instr.RO]++
			continue instructionLoop
		case DEC:
			registers[instr.RO]--
			continue instructionLoop
		}

		// The following instructions use RI0/RI1 as arithmetic operands, so
		// dereference values. In worst case, if they don't, operand will be set
		// to 0 and we'll read RZ
		i0, i1 := registers[instr.RI0], registers[instr.RI1]

		// Small trick for conditional jumps as goto case doesn't feel well. If
		// condition is false, continue with next address. If it's true, treat
		// jump as unconditional (see below)
		switch instr.I {
		case JNE:
			if i0 == i1 {
				continue instructionLoop
			}
		case JEQ:
			if i0 != i1 {
				continue instructionLoop
			}
		}

		switch instr.I {
		case JNE, JEQ, JMP:
			// Perform near or far jump (depending on the value)
			var offset int64
			if instr.RO >= RV {
				offset = registers[instr.RO]
			} else {
				offset = int64(instr.RO) - int64(nearJumpRegister)
			}
			address = uint32(int64(address-1) + offset)
			continue instructionLoop

		// Arithmetic operations -- nothing interesting here
		case MOV:
			registers[instr.RO] = i1
		case ADD:
			registers[instr.RO] = i0 + i1
		case SUB:
			registers[instr.RO] = i0 - i1
		case MUL:
			registers[instr.RO] = i0 * i1
		case DIV:
			registers[instr.RO] = i0 / i1
		case SHL:
			registers[instr.RO] = i0 << uint(i1)
		case SHR:
			registers[instr.RO] = i0 >> uint(i1)
		case ABS:
			if i1 >= 0 {
				registers[instr.RO] = i1
			} else {
				registers[instr.RO] = -i1
			}
		}

	}
}

func (machine *Machine) DumpRegisters(printf func(string, ...interface{})) {

	for index, value := range machine.Registers {
		origIndex := index

		var name string
		if RegisterIndex(index) <= RL7 {
			name = RegisterIndex(index).Name()
		} else {
			index -= int(RL7 + 1)
			if index < len(machine.Program.Registers) {
				reg := machine.Program.Registers[index]
				if reg.IsZero() {
					// Padded register, nothing interesting here
					continue
				}

				name = fmt.Sprintf("%d:%d:%d", reg.Cluster, reg.Group, reg.Pin)
			} else {
				name = fmt.Sprintf("%%v%x", index-len(machine.Program.Registers))
			}
		}

		printf("%8s [%4x] = %08x ", name, origIndex, value)
	}
}
