package yatima

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
type RegisterHintType uint
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
	nearJumpRegister = RL0
)

const (
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
