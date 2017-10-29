package yatima

import (
	"bufio"
	"bytes"
	"io"

	"fmt"
	"strconv"
)

type ParameterType uint32

const (
	// Actor may use arithmetic operations for random values, but not for
	// enumerables, as say sum of process ids makes no sense
	RIORandom RegisterHintType = 1 << iota
	RIOEnumerable
	// Types that cannot be used in learning networks
	RIOUnusable
	// Variable registers used to share / save information between subprograms
	RSVariable
	// Parameter register is predefined with some value before spawning actor
	RSParam
	// Local variables are reset on restart of actor, so they are never
	// declared explicitly, but created on the first appearance
	RLLocal
)

var registerAliases map[string]RegisterIndex = map[string]RegisterIndex{
	"0": RZ, "t": RT,

	"a": RI0, "b": RI1, "i2": RI2, "i3": RI3,
	"c": RO0, "o1": RO1, "o2": RO2, "o3": RO3,
	"s0": RS0, "s1": RS1, "s2": RS2, "s3": RS3,
}

const (
	ifWrite = 1 << iota
	ifJump
	ifBinary
)

type instructionDescr struct {
	instr  InstructionCode
	format string
	flags  uint32
}

var instructionDescriptors []instructionDescr = []instructionDescr{
	instructionDescr{instr: NOP, flags: 0, format: "nop"},
	instructionDescr{instr: CALL, flags: 0, format: "%c0:%c1 call on %o"},
	instructionDescr{instr: RET, flags: 0, format: "ret"},
	instructionDescr{instr: MOV, flags: ifWrite, format: "%o = %1"},
	instructionDescr{instr: ADD, flags: ifWrite | ifBinary, format: "%o = %0 + %1"},
	instructionDescr{instr: SUB, flags: ifWrite | ifBinary, format: "%o = %0 - %1"},
	instructionDescr{instr: MUL, flags: ifWrite | ifBinary, format: "%o = %0 * %1"},
	instructionDescr{instr: SUB, flags: ifWrite | ifBinary, format: "%o = %0 / %1"},
	instructionDescr{instr: INC, flags: ifWrite, format: "%o ++"},
	instructionDescr{instr: DEC, flags: ifWrite, format: "%o --"},
	instructionDescr{instr: SHL, flags: ifWrite | ifBinary, format: "%o = %0 << %1"},
	instructionDescr{instr: SHR, flags: ifWrite | ifBinary, format: "%o = %0 >> %1"},
	instructionDescr{instr: ABS, flags: ifWrite, format: "%o = abs %1"},
	instructionDescr{instr: JMP, flags: ifJump, format: "%jo go"},
	instructionDescr{instr: JEQ, flags: ifJump, format: "%jo if %0 == %1"},
	instructionDescr{instr: JNE, flags: ifJump, format: "%jo if %0 != %1"},
}

var registerHintNames map[string]RegisterHintType = map[string]RegisterHintType{
	"random": RIORandom,
	"enum":   RIOEnumerable,
	"static": RSVariable,
	"param":  RSParam,
}

// Instruction is represented as 4 words (uint32) and has opcode, two input
// registers (RI0 is being optional) and output register where result is
// written. Some instructions may encode special values in registers
// and use output registers not directly for output, i.e.: call uses RO as
// name of register for event trigger and RI0 and RI1 as absolute addresses
type Instruction struct {
	I        InstructionCode
	RI0, RI1 RegisterIndex
	RO       RegisterIndex
}

// Register hints help learn engine to synthesize reasonable network and
// avoid "childish" mistakes of picking two incompatible inputs. It also used
// for keeping variables names and scheduling them to proper registers.
type RegisterHint struct {
	Name     string
	Register RegisterIndex
	Hint     RegisterHintType
}

// Transitive hint determines pair of input registers which can be mutually
// exchanged without changing the outcome. In such case only one combination
// will be tried by the learner
type RegisterTransitiveHint struct {
	Register0, Register1 RegisterIndex
}

// Entry points contain relative addresses within programs (actors) pointing
// to the first instruction which should be executed when input value is changed
type EntryPoint struct {
	Address  int
	Register RegisterIndex
}

type Program struct {
	Name         string
	Hints        []RegisterHint
	TransHints   []RegisterTransitiveHint
	EntryPoints  []EntryPoint
	Instructions []Instruction
	Values       []int64
}

type CompilerErrorClass int

const (
	CETokensCount CompilerErrorClass = iota
	CEWrongDirective
	CEWrongRegister
	CEUnknownInstruction
	CEUnknownLabel
	CEInvalidConstant

	CEExternalError
)

type CompileError struct {
	LineNo     int
	ErrorClass CompilerErrorClass
	Text       string
}

type labelState struct {
	address      int
	subbedInstrs []int
}

type compilerState struct {
	reader   *bufio.Reader
	prog     *Program
	programs []*Program

	lineNo int
	err    *CompileError

	variables map[string]RegisterIndex
	labels    map[string]*labelState

	inRegs, outRegs []RegisterIndex

	done bool
}

func Compile(pReader io.Reader) ([]*Program, *CompileError) {
	state := compilerState{
		reader: bufio.NewReader(pReader),
	}

	// Compile expression into assembled instruction of YVM
	for !state.done {
		state.readLine()
		state.lineNo++
	}

	if state.prog != nil {
		state.setError(CEWrongDirective, "Missing AEND directive in the actor")
	}
	return state.programs, state.err
}

func (state *compilerState) createActor(name string) {
	if state.prog != nil {
		state.setError(CEWrongDirective, "Missing AEND directive in the actor")
		return
	}

	state.prog = &Program{
		Name:         name,
		Instructions: make([]Instruction, 0, 16),
		Values:       make([]int64, 0, 4),
	}
	state.variables = make(map[string]RegisterIndex)
	state.programs = append(state.programs, state.prog)
	state.labels = nil
	state.inRegs = nil
	state.outRegs = nil
}

func (state *compilerState) getAddress() int {
	return len(state.prog.Instructions)
}

func (state *compilerState) setError(ce CompilerErrorClass, fmtstr string, a ...interface{}) {
	if state.err == nil {
		state.err = &CompileError{
			LineNo:     state.lineNo,
			ErrorClass: ce,
			Text:       fmt.Sprintf(fmtstr, a...),
		}
	}
	state.done = true
}

func (state *compilerState) readLine() {
	bufs := state.readInstrTokens()

	if len(bufs) == 0 {
		// Whitespace or comment -- ignore it completely
		return
	}

	ch, _ := bufs[0].ReadByte()
	if ch == '.' {
		switch bufs[0].String() {
		case "ACTOR":
			if !state.requireExactBuffers(bufs, 2, "ACTOR") {
				return
			}
			state.createActor(bufs[1].String())
			return
		case "AEND":
			state.resolveLabels()
			state.compileReturn()
			state.prog = nil
			return
		case "ENTRY":
			state.compileEntryPoint(bufs)
			return
		case "REG":
			state.compileRegisterHint(bufs)
			return
		case "TRANS":
			state.compileTransitiveRegisterHint(bufs)
			return
		default:
			state.setError(CEWrongDirective, "Unknown directive .%s", bufs[0].String())
		}
	}

	if !state.requireProgramBody() {
		return
	}

	if len(bufs) == 1 {
		if ch == ':' {
			// This is a label, save it into labels
			label := state.getLabel(bufs[0].String())
			label.address = state.getAddress()
			return
		}
	} else if len(bufs) <= 5 {
		// Return ch back to stream as this is not label or directive
		bufs[0].UnreadByte()

		instr := state.compileInstruction(bufs)
		if instr.I == NOP {
			return
		}

		state.validateInstruction(instr)
		state.addInOutRegs(instr)
		state.prog.Instructions = append(state.prog.Instructions, instr)
		return
	}

	state.setError(CETokensCount, "Expression has too many (%d) tokens", len(bufs))
	return
}

func (state *compilerState) getLabel(name string) *labelState {
	if state.labels == nil {
		state.labels = make(map[string]*labelState, 0)
	} else {
		if label, ok := state.labels[name]; ok {
			return label
		}
	}

	label := &labelState{
		address:      -1,
		subbedInstrs: make([]int, 0, 1),
	}
	state.labels[name] = label
	return label
}

func (state *compilerState) resolveLabels() {
	if state.labels == nil {
		return
	}

	// Resolve label addresses
	for lName, label := range state.labels {
		if label.address < 0 {
			state.setError(CEUnknownLabel, "Undefined label :%s", lName)
			break
		}

		for _, iAddress := range label.subbedInstrs {
			instr := &state.prog.Instructions[iAddress]
			if instr.RO != RIP {
				state.setError(CEUnknownLabel, "Cannot bind label to non-referring instruction %s",
					instr.Disassemble())
				break
			}

			// Since we cannot use normal registers in jump instructions,
			// instead of using small values for jumps within +/- 8 instructions,
			// encode them as register indexes, i.e. jmp +4 --> jmp %rl4
			xReg := RegisterIndex(int(nearJumpRegister) + label.address - iAddress)
			if xReg >= RI0 && xReg < RV {
				instr.RO = xReg
			} else {
				instr.RO = state.compileIntegerValue(int64(label.address))
			}
		}
	}
}

func (state *compilerState) compileEntryPoint(bufs []*bytes.Buffer) {
	if !state.requireMinimumBuffers(bufs, 2, "ENTRY") || !state.requireProgram("ENTRY") {
		return
	}

	if len(state.prog.EntryPoints) > 0 {
		// If this is not the first entry point, add return expression
		state.compileReturn()
	}

	for _, buf := range bufs[1:] {
		// This is an entry point, generate it
		reg := state.compileOperand(buf)
		if reg != RT && !reg.isInputRegister() {
			state.setError(CEWrongRegister, "Only %%t and input registers can be used for entry points")
			return
		}

		state.prog.EntryPoints = append(state.prog.EntryPoints,
			EntryPoint{Address: state.getAddress(), Register: reg})

		// Even if we do not use directly reg's value (like %t) in the expressions,
		// we want to listen for the updates, so generate daisy chained call
		state.inRegs = append(state.inRegs, reg)
	}
}

func (state *compilerState) compileRegisterHint(bufs []*bytes.Buffer) {
	if !state.requireMinimumBuffers(bufs, 3, "REG") || !state.requireDeclaration("REG") {
		return
	}

	hint := RegisterHint{Register: state.compileOperand(bufs[1])}
	if hint.Register == RZ {
		return
	}

	var ok bool
	hintName := bufs[2].String()
	hint.Hint, ok = registerHintNames[hintName]
	if !ok {
		state.setError(CEWrongDirective, "Unknown register hint type '%s'", hintName)
		return
	}

	switch hint.Hint {
	case RSParam, RSVariable:
		if !hint.Register.isStaticRegister() {
			state.setError(CEWrongRegister, "Static register is expected for hint '%s', got %s",
				hintName, hint.Register.Name())
			return
		}
		fallthrough
	case RLLocal:
		if hint.Hint == RLLocal && !hint.Register.isLocalRegister() {
			state.setError(CEWrongRegister, "Static register is expected for hint '%s', got %s",
				hintName, hint.Register.Name())
			return
		}

		if len(bufs) == 4 {
			hint.Name = bufs[3].String()
			if _, ok := state.variables[hint.Name]; ok {
				state.setError(CEWrongDirective, "Variable '%s' is already defined", hint.Name)
				return
			}

			state.variables[hint.Name] = hint.Register
		}
	default:
		if !hint.Register.isIORegister() {
			state.setError(CEWrongRegister, "I/O register is expected for hint '%s', got %s",
				hintName, hint.Register.Name())
			return
		}
	}

	state.prog.Hints = append(state.prog.Hints, hint)
	return
}

func (state *compilerState) compileTransitiveRegisterHint(bufs []*bytes.Buffer) {
	if !state.requireExactBuffers(bufs, 3, "TRANS") || !state.requireDeclaration("TRANS") {
		return
	}

	var hint RegisterTransitiveHint
	for i, buf := range bufs[1:] {
		reg := state.compileOperand(buf)
		if !reg.isInputRegister() {
			state.setError(CEWrongRegister, "Input register is expected for .TRANS hint, got '%s'",
				reg.Name())
			return
		}

		if i == 0 {
			hint.Register0 = reg
		} else {
			hint.Register1 = reg
		}
	}

	state.prog.TransHints = append(state.prog.TransHints, hint)
	return
}

// Compiles instruction operand which could be: an integer literal which will
// be added to values and allocated from RV space, input/output register
// or a local variable which will use one of the RL* registers
func (state *compilerState) compileOperand(buf *bytes.Buffer) RegisterIndex {
	ch, _ := buf.ReadByte()

	switch ch {
	case '0':
		ch2, err := buf.ReadByte()
		if err == io.EOF {
			// a single 0 constant (i.e. used in comparisons) resolves to R0 register
			return R0
		}

		if ch2 == 'x' {
			value, err := strconv.ParseInt(buf.String(), 16, 64)
			if err != nil {
				state.setError(CEInvalidConstant, "%s", err.Error())
				return RZ
			}

			return state.compileIntegerValue(value)
		}

		buf.UnreadByte()
		fallthrough
	case '-', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		buf.UnreadByte()

		value, err := strconv.ParseInt(buf.String(), 10, 64)
		if err != nil {
			state.setError(CEInvalidConstant, "%s", err.Error())
			return RZ
		}

		return state.compileIntegerValue(value)
	case '"':
		state.setError(CEInvalidConstant, "String constants are not supported")
		return RZ
	case '%':
		regName := buf.String()
		if index, ok := registerAliases[regName]; ok {
			// Well-known variable name like first input "a"
			return index
		}

		state.setError(CEWrongRegister, "Unknown register %%%s", regName)
		return RZ
	case ':':
		// We can't use IP directly in instructions, so use it as a placeholder
		// for label references. It will be later replaced during address
		// resolving
		label := state.getLabel(buf.String())
		label.subbedInstrs = append(label.subbedInstrs, state.getAddress())

		return RIP
	default:
		buf.UnreadByte()
	}

	varName := buf.String()
	if reg, ok := state.variables[varName]; ok {
		// Non-common variable, but already allocated register for it
		return reg
	}

	// Unknown variable, allocate space for it if possible
	reg := RL0 + RegisterIndex(len(state.variables))
	if !reg.isLocalRegister() {
		state.setError(CEWrongRegister, "Too many variables, not enough local registers")
		return RZ
	}

	state.variables[varName] = reg
	state.prog.Hints = append(state.prog.Hints, RegisterHint{
		Name: varName, Register: reg, Hint: RLLocal})
	return reg
}

func (state *compilerState) compileIntegerValue(val int64) RegisterIndex {
	// Allocate cell in value data array and return its index
	index := RV + RegisterIndex(len(state.prog.Values))
	state.prog.Values = append(state.prog.Values, val)
	return index
}

func (state *compilerState) compileInstruction(bufs []*bytes.Buffer) (instr Instruction) {
	var out, op1, in0, op2, in1 *bytes.Buffer

	out, op1, op2 = bufs[0], bufs[1], bytes.NewBufferString("")
	switch len(bufs) {
	case 2:
		// Special case for increment / decrement -- save result to the same
		// register as input
		in1 = out
	case 5:
		in0 = bufs[len(bufs)-3]
		fallthrough
	case 4:
		op2 = bufs[len(bufs)-2]
		fallthrough
	case 3:
		in1 = bufs[len(bufs)-1]
	}

	instr.I = state.compileInstructionCode(op1.String(), op2.String())
	if instr.I == NOP {
		state.setError(CEUnknownInstruction, "Unknown instruction %s, %s",
			op1.String(), op2.String())
		return
	}

	instr.RO = state.compileOperand(out)
	if in0 != nil {
		instr.RI0 = state.compileOperand(in0)
	}
	instr.RI1 = state.compileOperand(in1)

	return
}

func (state *compilerState) compileInstructionCode(op1, op2 string) InstructionCode {
	switch op1 {
	case "++":
		if len(op2) == 0 {
			return INC
		}
	case "--":
		if len(op2) == 0 {
			return DEC
		}
	case "=":
		switch op2 {
		case "":
			return MOV
		case "+":
			return ADD
		case "-":
			return SUB
		case "*":
			return MUL
		case "/":
			return DIV
		case "<<":
			return SHL
		case ">>":
			return SHR
		case "abs":
			return ABS
		}
	case "go":
		if len(op2) == 0 {
			return JMP
		}
	case "if":
		switch op2 {
		case "==":
			return JEQ
		case "!=":
			return JNE
		}
	}

	return NOP
}

func (state *compilerState) validateInstruction(instr Instruction) {
	descr := &instructionDescriptors[instr.I]

	jumpOp := (descr.flags&ifJump != 0)
	writeOp := (descr.flags&ifWrite != 0)
	binaryOp := (descr.flags&ifBinary != 0)

	if jumpOp && (instr.RO < RV && instr.RO != RIP) {
		state.setError(CEWrongRegister, "Jump instructions require value or label operand")
	}
	if writeOp && !instr.RO.isWriteableRegister() {
		state.setError(CEWrongRegister, "Cannot write to non-writeable register %s",
			instr.RO.Name())
	}
	if instr.RO == RZ || instr.RI1 == RZ || (binaryOp && instr.RI0 == RZ) {
		state.setError(CEWrongRegister, "Cannot use register %z in instruction")
	}
}

func (state *compilerState) requireExactBuffers(bufs []*bytes.Buffer, required int,
	directive string) bool {

	if len(bufs) < required {
		state.setError(CETokensCount, "Directive .%s requires exactly %d buffers, %d are given",
			directive, required, len(bufs))
		return false
	}
	return true
}

func (state *compilerState) requireMinimumBuffers(bufs []*bytes.Buffer, minimum int,
	directive string) bool {

	if len(bufs) < minimum {
		state.setError(CETokensCount, "Directive .%s requires at least %d buffers, %d are given",
			directive, minimum, len(bufs))
		return false
	}
	return true
}

func (state *compilerState) requireProgram(directive string) bool {
	if state.prog == nil {
		state.setError(CEWrongDirective, "Directive .%s requires program to be defined first with .ACTOR")
		return false
	}
	return true
}

func (state *compilerState) requireDeclaration(directive string) bool {
	if len(state.prog.EntryPoints) > 0 {
		state.setError(CEWrongDirective, "Directive .%s should be placed before entry points")
		return false
	}
	return true
}

func (state *compilerState) requireProgramBody() bool {
	if len(state.prog.EntryPoints) == 0 {
		state.setError(CEWrongDirective, "Expression should be placed after entry point")
		return false
	}
	return true
}

func (state *compilerState) addInOutRegs(instr Instruction) {
	descr := &instructionDescriptors[instr.I]

	if (descr.flags & ifWrite) != 0 {
		state.inRegs = state.addInOutReg(instr.RI0, state.inRegs, true)
		state.outRegs = state.addInOutReg(instr.RO, state.outRegs, false)

		if (descr.flags & ifBinary) != 0 {
			state.inRegs = state.addInOutReg(instr.RI1, state.inRegs, true)
		}
	}
}

func (state *compilerState) addInOutReg(reg RegisterIndex, regs []RegisterIndex,
	inReg bool) []RegisterIndex {

	if reg.isIORegister() || reg.isStaticRegister() {
		var hasHint bool
		for _, hint := range state.prog.Hints {
			if hint.Register == reg {
				hasHint = true
				break
			}
		}
		if !hasHint {
			state.setError(CEWrongRegister, "Non-local register %s should be supplemented with hint",
				reg.Name())
			return regs
		}
	}

	if (inReg && reg >= RI0 && reg <= RI3) || (!inReg && reg >= RO0 && reg <= RO3) {
		for _, reg2 := range regs {
			if reg == reg2 {
				// This register was already added
				return regs
			}
		}

		return append(regs, reg)
	}

	return regs
}

func (state *compilerState) compileReturn() {
	// Generate CALL stubs for network linker and ret instruction
	if state.inRegs != nil {
		for _, reg := range state.inRegs {
			state.prog.Instructions = append(state.prog.Instructions,
				Instruction{I: CALL, RO: reg})
		}
	}
	if state.outRegs != nil {
		for _, reg := range state.outRegs {
			state.prog.Instructions = append(state.prog.Instructions,
				Instruction{I: CALL, RO: reg})
		}
	}
	state.prog.Instructions = append(state.prog.Instructions, Instruction{I: RET})

	state.inRegs = nil
	state.outRegs = nil
}

// A simple white-space based tokenizer which splits input line (until newline)
// into 4 or 5 buffers. The syntax of the line is:
//	[o] op1 [i1] [op2] i2 ; optional comment
func (state *compilerState) readInstrTokens() []*bytes.Buffer {
	var buf *bytes.Buffer
	buffers := make([]*bytes.Buffer, 0, 5)
	inWhiteSpace := true
	inComment := false

	for {
		ch, err := state.reader.ReadByte()
		if err == io.EOF {
			state.done = true
			break
		}
		if err != nil {
			state.setError(CEExternalError, "%s", err.Error())
			return nil
		}
		if ch == '\n' {
			break
		}
		if ch == ';' {
			inComment = true
		}
		if inComment {
			continue
		}
		if ch == ' ' || ch == '\t' {
			inWhiteSpace = true
			continue
		}

		// All whitespace prior to token were ignored, time to allocate buffer
		// with a first character of token in it
		if inWhiteSpace {
			storage := make([]byte, 1, 8)
			storage[0] = ch

			buf = bytes.NewBuffer(storage)
			buffers = append(buffers, buf)

			inWhiteSpace = false
			continue
		}

		buf.WriteByte(ch)
	}

	return buffers
}

func (hint RegisterHintType) Name() string {
	switch hint {
	case RIORandom:
		return "random"
	case RIOEnumerable:
		return "enumerable"
	case RSVariable:
		return "static"
	case RSParam:
		return "param"
	case RLLocal:
		return "local"
	}

	return "_"
}

func (reg RegisterIndex) Name() string {
	switch reg {
	case RZ:
		return "_"
	case R0:
		return "0"
	case RT:
		return "%t"
	case RIP:
		return "@"
	}

	if reg.isInputRegister() {
		return fmt.Sprint("%i", reg-RI0)
	} else if reg.isOutputRegister() {
		return fmt.Sprint("%o", reg-RO0)
	} else if reg.isLocalRegister() {
		return fmt.Sprint("%l", reg-RL0)
	} else if reg.isStaticRegister() {
		return fmt.Sprint("%s", reg-RS0)
	} else if reg >= RV {
		return fmt.Sprint("%v", reg-RV)
	}

	return "???"
}

func (reg RegisterIndex) isInputRegister() bool {
	return reg >= RI0 && reg <= RI3
}
func (reg RegisterIndex) isOutputRegister() bool {
	return reg >= RO0 && reg <= RO3
}
func (reg RegisterIndex) isIORegister() bool {
	return reg >= RI0 && reg <= RO3
}
func (reg RegisterIndex) isStaticRegister() bool {
	return reg >= RS0 && reg <= RS3
}
func (reg RegisterIndex) isLocalRegister() bool {
	return reg >= RL0 && reg <= RL7
}
func (reg RegisterIndex) isWriteableRegister() bool {
	return reg.isOutputRegister() || reg.isStaticRegister() || reg.isLocalRegister()
}

func (instr Instruction) Disassemble() string {
	if int(instr.I) >= len(instructionDescriptors) {
		return fmt.Sprintf("(bad opcode %x)", instr.I)
	}

	var decodeFmt byte
	var field bool
	inBuf := bytes.NewBufferString(instructionDescriptors[instr.I].format)
	outBuf := bytes.NewBuffer([]byte{})

	for {
		ch, err := inBuf.ReadByte()
		if err != nil {
			break
		}

		if field {
			var reg RegisterIndex
			switch ch {
			case '%':
				outBuf.WriteByte('%')
			case 'o':
				reg = instr.RO
				field = false
			case '0':
				reg = instr.RI0
				field = false
			case '1':
				reg = instr.RI1
				field = false
			default:
				decodeFmt = ch
			}

			if !field {
				switch decodeFmt {
				case '\000':
					outBuf.WriteString(reg.Name())
				case 'j':
					if instr.RO < RV {
						offset := int(reg) - int(nearJumpRegister)
						fmt.Fprintf(outBuf, "%+x", offset)
					} else {
						outBuf.WriteString(reg.Name())
					}
				case 'c':
					if reg != RZ {
						fmt.Fprintf(outBuf, "%+x", reg)
					}
				case 'x':
					fmt.Fprintf(outBuf, "%+x", reg)
				}
			}
		} else if ch == '%' {
			field = true
			decodeFmt = '\000'
		} else {
			outBuf.WriteByte(ch)
		}
	}

	return outBuf.String()
}
