package yatima

import (
	"bufio"
	"bytes"
	"io"

	"fmt"
	"strconv"
)

const (
	// Actor may use arithmetic operations for random values, but not for
	// enumerables, as say sum of process ids makes no sense
	RIORandom RegisterHintType = 1 << iota
	RIOEnumerable
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

var registerHintNames map[string]RegisterHintType = map[string]RegisterHintType{
	"random": RIORandom,
	"enum":   RIOEnumerable,
	"static": RSVariable,
	"param":  RSParam,
}

type Instruction struct {
	I        InstructionCode
	RI0, RI1 RegisterIndex
	RO       RegisterIndex
}

type RegisterHint struct {
	Name     string
	Register RegisterIndex
	Hint     RegisterHintType
}

type EntryPoint struct {
	Address  int
	Register RegisterIndex
}

type Program struct {
	Name         string
	Hints        []RegisterHint
	EntryPoints  []EntryPoint
	Instructions []Instruction
	Values       []int64
}

type CompileError struct {
	LineNo int
	Text   string
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
		state.setError("Missing AEND directive in the actor")
	}
	return state.programs, state.err
}

func (state *compilerState) createActor(name string) {
	if state.prog != nil {
		state.setError("Missing AEND directive in the actor")
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

func (state *compilerState) setError(text string) {
	if state.err == nil {
		state.err = &CompileError{
			LineNo: state.lineNo,
			Text:   text,
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
			if len(bufs) < 2 {
				state.setError("Actor requires a name")
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
		default:
			state.setError(fmt.Sprintf("Unknown directive '%s'", bufs[0].String()))
		}
	}

	if state.prog == nil || len(state.prog.EntryPoints) == 0 {
		state.setError("Expected declaration, got expression, add entry point first")
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
			state.setError("Unknown instruction")
			return
		}

		state.validateInstruction(instr)
		state.addInOutRegs(instr)
		state.prog.Instructions = append(state.prog.Instructions, instr)
		return
	}

	state.setError(fmt.Sprintf("%d tokens is too many", len(bufs)))
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
			state.setError(fmt.Sprintf("Undefined label :%s", lName))
			break
		}

		for _, iAddress := range label.subbedInstrs {
			instr := &state.prog.Instructions[iAddress]
			if instr.RO != RIP {
				state.setError(fmt.Sprintf("Cannot bind label to non-referring"+
					" instruction <+%d> %s", iAddress, instr.Disassemble()))
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
	if len(bufs) < 2 {
		state.setError("Entry point requires at least one register")
		return
	}
	if state.prog == nil {
		state.setError("Entry points only valid inside the actor")
	}

	if len(state.prog.EntryPoints) > 0 {
		// If this is not the first entry point, add return expression
		state.compileReturn()
	}

	for _, buf := range bufs[1:] {
		// This is an entry point, generate it
		reg := state.compileOperand(buf)
		if reg != RT && !reg.isInputRegister() {
			state.setError("Only %t and input registers can be used for entry points")
			return
		}

		state.prog.EntryPoints = append(state.prog.EntryPoints,
			EntryPoint{Address: state.getAddress(), Register: reg})
	}
}

func (state *compilerState) compileRegisterHint(bufs []*bytes.Buffer) {
	if len(bufs) < 3 {
		state.setError("At least 3 tokens required by .REG")
		return
	}
	if state.prog == nil || len(state.prog.EntryPoints) > 0 {
		state.setError("Register directives should be defined in the beginning of actor")
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
		state.setError(fmt.Sprintf("Unknown hint '%s'", hintName))
		return
	}

	switch hint.Hint {
	case RSParam, RSVariable:
		if !hint.Register.isStaticRegister() {
			state.setError(fmt.Sprintf("Static register is expected for hint '%s', got %s",
				hintName, hint.Register.Name()))
			return
		}

		if len(bufs) >= 3 {
			hint.Name = bufs[3].String()
			if _, ok := state.variables[hint.Name]; ok {
				state.setError(fmt.Sprintf("Variable '%s' is already defined", hint.Name))
				return
			}

			state.variables[hint.Name] = hint.Register
		}
	default:
		if !hint.Register.isIORegister() {
			state.setError(fmt.Sprintf("I/O register is expected for hint '%s', got %s",
				hintName, hint.Register.Name()))
			return
		}
	}

	state.prog.Hints = append(state.prog.Hints, hint)
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
				state.setError(err.Error())
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
			state.setError(err.Error())
			return RZ
		}

		return state.compileIntegerValue(value)
	case '"':
		state.setError("String literals are not supported")
		return RZ
	case '%':
		regName := buf.String()
		if index, ok := registerAliases[regName]; ok {
			// Well-known variable name like first input "a"
			return index
		}

		state.setError(fmt.Sprintf("Unknown register %%%s", regName))
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
		state.setError("Too many variables, not enough local registers")
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
	var binaryOp, writeOp, jumpOp bool

	switch instr.I {
	case ADD, SUB, MUL, DIV:
		binaryOp = true
		writeOp = true
	case MOV, ABS, INC, DEC:
		writeOp = true
	case JMP:
		jumpOp = true
	case JEQ, JNE:
		binaryOp = true
		jumpOp = true
	}

	if jumpOp && (instr.RO < RV && instr.RO != RIP) {
		state.setError("Jump instructions require value or label operand")
	}
	if writeOp && !instr.RO.isWriteableRegister() {
		state.setError("Cannot write to non-writeable register")
	}
	if instr.RO == RZ || instr.RI1 == RZ || (binaryOp && instr.RI0 == RZ) {
		state.setError("Cannot use %z in instruction")
	}
}

func (state *compilerState) addInOutRegs(instr Instruction) {
	switch instr.I {
	case MOV, ADD, SUB, MUL, DIV, ABS:
		state.inRegs = state.addInOutReg(instr.RI0, state.inRegs, true)
		state.inRegs = state.addInOutReg(instr.RI1, state.inRegs, true)
		state.outRegs = state.addInOutReg(instr.RO, state.outRegs, false)
	}
}

func (state *compilerState) addInOutReg(reg RegisterIndex, regs []RegisterIndex,
	inReg bool) []RegisterIndex {
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
			state.setError(err.Error())
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

var codeFormat []string = []string{
	"nop",
	"%c0:%c1 call on %o",
	"ret",
	"%o = %1",
	"%o = %0 + %1",
	"%o = %0 - %1",
	"%o = %0 * %1",
	"%o = %0 / %1",
	"%o ++",
	"%o --",
	"%o = %0 << %1",
	"%o = %0 >> %1",
	"%o = abs %1",
	"%jo go",
	"%jo if %0 == %1",
	"%jo if %0 != %1",
}

func (instr Instruction) Disassemble() string {
	if int(instr.I) >= len(codeFormat) {
		return fmt.Sprintf("(bad opcode %x)", instr.I)
	}

	var decodeFmt byte
	var field bool
	inBuf := bytes.NewBufferString(codeFormat[instr.I])
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
