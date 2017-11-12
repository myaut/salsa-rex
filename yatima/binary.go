package yatima

import (
	"fmt"
	"io"

	"bytes"

	"encoding/binary"
)

type BinaryDirectiveType uint32

const (
	BDProgram BinaryDirectiveType = iota + 1<<24
	BDProgramBody
	BDProgramEnd
	BDValues
	BDRegisterHint
	BDRegisterName
	BDRegisterTransitiveHint
	BDEntryPoint

	BDModel
	BDPinCluster
	BDPinGroup
	BDPin
	BDActorInstance
	BDActorInput

	BDLProgram
	BDLRegisters
	BDLEntryPoint
)

// this is "YAB0" in little endian
const yabMagic = 0x30424159
const sizeofDirective = 16

type BinaryDirective struct {
	Type BinaryDirectiveType

	// Parameters for the directive
	P0, P1 uint32

	// Number of entries which follow this one
	Length uint32
}

type BinaryWriter struct {
	writer io.WriteSeeker

	strings []string
	strOff  uint32

	progCount uint32
}

type BDBlock struct {
	Type   BinaryDirectiveType
	Offset int
	Length int
}

type BinaryReader struct {
	reader    io.ReadSeeker
	strReader io.ReadSeeker

	length uint32
	offset int

	stack []BDBlock

	lastDirective BinaryDirective
	hasLD         bool
}

type BinaryDirectiveBuffer struct {
	directives []BinaryDirective
	blocks     []int
}

func (writer *BinaryWriter) newBuffer(capacity int) *BinaryDirectiveBuffer {
	return &BinaryDirectiveBuffer{
		directives: make([]BinaryDirective, 0, capacity),
		blocks:     make([]int, 0, 8),
	}
}

func (bdb *BinaryDirectiveBuffer) addDirectiveImpl(dirType BinaryDirectiveType, p0, p1 uint32, block bool) {
	dir := BinaryDirective{
		Type: dirType,
		P0:   p0,
		P1:   p1,
	}
	bdb.directives = append(bdb.directives, dir)

	for _, blockIndex := range bdb.blocks {
		bdb.directives[blockIndex].Length++
	}
	if block {
		index := len(bdb.directives) - 1
		bdb.blocks = append(bdb.blocks, index)
	}
}

func (bdb *BinaryDirectiveBuffer) addBlock(dirType BinaryDirectiveType, p0, p1 uint32) {
	bdb.addDirectiveImpl(dirType, p0, p1, true)
}

func (bdb *BinaryDirectiveBuffer) addDirective(dirType BinaryDirectiveType, p0, p1 uint32) {
	bdb.addDirectiveImpl(dirType, p0, p1, false)
}

func (bdb *BinaryDirectiveBuffer) endBlock() {
	bdb.blocks = bdb.blocks[:len(bdb.blocks)-1]
}

func NewWriter(writer io.WriteSeeker) (*BinaryWriter, error) {
	yabw := &BinaryWriter{
		writer:  writer,
		strings: make([]string, 0, 8),
	}

	err := yabw.write(BinaryDirective{})
	return yabw, err
}

func (yabw *BinaryWriter) Close() error {
	offset, err := yabw.writer.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if offset > 1<<24 {
		return fmt.Errorf("File too big")
	}

	// After writing program text, we write strings to the end of the file
	// (and pad them with zeroes)
	for _, str := range yabw.strings {
		n, err := yabw.writer.Write(append([]byte(str), '\000'))
		if err != nil {
			return err
		}
		if n != len(str)+1 {
			return fmt.Errorf("Not enough bytes written for a string")
		}
	}
	yabw.writer.Write(bytes.Repeat([]byte{'\000'}, sizeofDirective-int(yabw.strOff%sizeofDirective)))

	// Finally, update header in the beginning of the file
	_, err = yabw.writer.Seek(0, io.SeekStart)
	if err != nil {
		return err
	}

	hdr := BinaryDirective{
		Type:   yabMagic,
		Length: uint32(offset / sizeofDirective),
	}
	return yabw.write(hdr)
}

func (yabw *BinaryWriter) write(data interface{}) error {
	return binary.Write(yabw.writer, binary.LittleEndian, data)
}

func (yabw *BinaryWriter) addString(str string) (offset uint32) {
	yabw.strings = append(yabw.strings, str)

	offset = yabw.strOff
	yabw.strOff += uint32(len(str) + 1)
	return
}

func (yabw *BinaryWriter) AddProgram(prog *Program) (err error) {
	preamble := yabw.newBuffer(16)
	preamble.addBlock(BDProgram, yabw.addString(prog.Name), yabw.progCount)

	for _, hint := range prog.Hints {
		preamble.addDirective(BDRegisterHint, uint32(hint.Register), uint32(hint.Hint))
		if len(hint.Name) > 0 {
			preamble.addDirective(BDRegisterName, uint32(hint.Register),
				yabw.addString(hint.Name))
		}
	}
	for _, hint := range prog.TransHints {
		preamble.addDirective(BDRegisterTransitiveHint, uint32(hint.Register0),
			uint32(hint.Register1))
	}
	for _, ep := range prog.EntryPoints {
		preamble.addDirective(BDEntryPoint, uint32(ep.Register), uint32(ep.Address))
	}

	return yabw.addProgram(prog, preamble)
}

func (yabw *BinaryWriter) addProgram(prog *Program, preamble *BinaryDirectiveBuffer) (err error) {
	// Compute total length of the program including values
	valLength, valPad := len(prog.Values)/2, false
	if len(prog.Values)%2 != 0 {
		valLength++ // Round up as last value will be written with padding
		valPad = true
	}

	// We do not directly write instructions/values through buffer, but need to
	// account them. Then write buffer contents
	preamble.directives[0].Length += uint32(len(prog.Instructions) + valLength + 2)
	if valLength > 0 {
		preamble.directives[0].Length++
	}

	err = yabw.write(preamble.directives)
	if err != nil {
		return
	}

	// Write values. We compress them as two values per "directive"
	if valLength > 0 {
		err = yabw.write(&BinaryDirective{
			Type:   BDValues,
			P0:     uint32(len(prog.Values)),
			Length: uint32(valLength),
		})
		if err == nil {
			err = yabw.write(prog.Values)
			if valPad && err == nil {
				var pad int64
				err = yabw.write(pad)
			}
		}
		if err != nil {
			return
		}
	}

	// Write instructions. We expect that Instruction is binary compatible
	// with yabDirective (4 * uint32)
	err = yabw.write(&BinaryDirective{
		Type:   BDProgramBody,
		Length: uint32(len(prog.Instructions)),
	})
	if err == nil {
		err = yabw.write(prog.Instructions)
	}
	if err != nil {
		return
	}

	yabw.progCount++
	return yabw.write(&BinaryDirective{Type: BDProgramEnd})
}

func (yabw *BinaryWriter) AddModel(model *Model) (err error) {
	// To avoid dependency on the external library, put actor sources directly to a model
	progIndeces, err := yabw.addModelPrograms(model)
	if err != nil {
		return err
	}

	mbuf := yabw.newBuffer(32)
	mbuf.addBlock(BDModel, 0, 0)

	for _, cluster := range model.base.Inputs {
		mbuf.addBlock(BDPinCluster, yabw.addString(cluster.Name), 0)
		for _, group := range cluster.Groups {
			mbuf.addBlock(BDPinGroup, yabw.addString(group.Name), 0)
			for _, pin := range group.Pins {
				mbuf.addDirective(BDPin, yabw.addString(pin.Name), uint32(pin.Hint))
			}
			mbuf.endBlock()
		}
		mbuf.endBlock()
	}

	for _, actor := range model.Actors {
		mbuf.addBlock(BDActorInstance, progIndeces[actor.ProgramIndex], uint32(actor.TimeMode))
		for _, pin := range actor.Inputs {
			mbuf.addDirective(BDActorInput, pin.Encode(), 0)
		}
		for _, pin := range actor.Outputs {
			mbuf.addDirective(BDPin, yabw.addString(pin.Name), uint32(pin.Hint))
		}
		mbuf.endBlock()
	}

	return yabw.write(mbuf.directives)
}

func (yabw *BinaryWriter) AddBaseModel(base *BaseModel) (err error) {
	return yabw.AddModel(&Model{
		base: base,

		Actors: base.Actors,
	})
}

func (yabw *BinaryWriter) addModelPrograms(model *Model) (indeces map[uint32]uint32, err error) {
	indeces = make(map[uint32]uint32)

	for _, actor := range model.Actors {
		// This program was already used, ignore it
		if _, ok := indeces[actor.ProgramIndex]; ok {
			continue
		}

		// Insert program assembly to a output file and save index
		indeces[actor.ProgramIndex] = yabw.progCount
		err = yabw.AddProgram(model.base.library.Programs[actor.ProgramIndex])

		if err != nil {
			return
		}
	}

	return
}

func (yabw *BinaryWriter) AddLinkedProgram(prog *LinkedProgram) (err error) {
	lpbuf := yabw.newBuffer(32)
	lpbuf.addBlock(BDLProgram, 0, 0)

	lpbuf.addBlock(BDLRegisters, 0, 0)
	for _, reg := range prog.Registers {
		lpbuf.addDirective(BDActorInput, reg.Encode(), 0)
	}
	lpbuf.endBlock()

	for _, ep := range prog.EntryPoints {
		lpbuf.addDirective(BDLEntryPoint, ep, 0)
	}

	return yabw.addProgram(&Program{
		Instructions: prog.Instructions,
		Values:       prog.Values,
	}, lpbuf)
}

func NewReader(reader io.ReadSeeker, strReader io.ReadSeeker) (*BinaryReader, error) {
	yabr := &BinaryReader{
		reader:    reader,
		strReader: strReader,
	}

	var hdrRaw [sizeofDirective]byte
	_, err := reader.Read(hdrRaw[:])
	if err != nil {
		return nil, err
	}

	var hdr BinaryDirective
	err = binary.Read(bytes.NewReader(hdrRaw[:]), binary.LittleEndian, &hdr)
	if err != nil {
		return nil, err
	}
	if hdr.Type != yabMagic {
		return nil, fmt.Errorf("Invalid magic sequence")
	}

	yabr.length = hdr.Length
	yabr.offset = 1
	return yabr, nil
}

// Updates offsets after reading directive (or data) and removes finished blocks
func (yabr *BinaryReader) updateCounters(offset, lenOff int) {
	for i := range yabr.stack {
		// A small hack: for values we want prettier offsets (there are 2 values
		// per directive), so we add 2 to the head
		if i == len(yabr.stack)-1 {
			yabr.stack[i].Offset += offset
		} else {
			yabr.stack[i].Offset++
		}
		yabr.stack[i].Length -= lenOff
	}
	for len(yabr.stack) > 0 {
		last := len(yabr.stack) - 1
		if yabr.stack[last].Length > 0 {
			break
		}

		yabr.stack = yabr.stack[:last]
	}

	yabr.offset += lenOff
}

func (yabr *BinaryReader) getBlock() (block BDBlock) {
	last := len(yabr.stack) - 1
	if last >= 0 {
		block = yabr.stack[last]
	}

	return
}

// Returns true if current block stack contains directive of specified type
func (yabr *BinaryReader) InBlock(dirType BinaryDirectiveType) bool {
	for _, block := range yabr.stack {
		if block.Type == dirType {
			return true
		}
	}

	return false
}

func (yabr *BinaryReader) GetState() (block BDBlock, off int, depth int) {
	return yabr.getBlock(), yabr.offset, len(yabr.stack)
}

func (yabr *BinaryReader) read(data interface{}) error {
	if yabr.offset >= int(yabr.length) {
		return io.EOF
	}

	return binary.Read(yabr.reader, binary.LittleEndian, data)
}

// Ignores current block and rewinds to the next directive
func (yabr *BinaryReader) IgnoreBlock() (err error) {
	length := yabr.getBlock().Length

	_, err = yabr.reader.Seek(int64(length*sizeofDirective), io.SeekCurrent)
	yabr.updateCounters(length, length)

	return
}

// Reads directive from the input stream (or takes last unread directive)
// and updates stack of block directives appropriately
func (yabr *BinaryReader) ReadDirective() (dir BinaryDirective, err error) {
	if yabr.hasLD {
		yabr.hasLD = false
		if yabr.lastDirective.Type == 0 {
			return dir, fmt.Errorf("Empty last directive after unread()")
		}

		yabr.lastDirective, dir = dir, yabr.lastDirective
	} else {
		err = yabr.read(&dir)
		if err != nil {
			return
		}
		yabr.lastDirective = dir
	}

	// If this is a block, add corresponding entry to stack
	yabr.updateCounters(1, 1)
	if dir.Type >= BDProgram && dir.Length > 0 {
		yabr.stack = append(yabr.stack, BDBlock{
			Type:   dir.Type,
			Length: int(dir.Length),
		})
	}

	return
}

// Returns last read non-block directive back to the stream
func (yabr *BinaryReader) UnreadDirective() error {
	if yabr.hasLD {
		return fmt.Errorf("Only one directive can be unread by the reader")
	}

	// Revert latest stack addition (if there were) and restore values
	if yabr.lastDirective.Length > 0 {
		yabr.stack = yabr.stack[:len(yabr.stack)-1]
	}
	yabr.updateCounters(0, -1)

	yabr.hasLD = true
	return nil
}

func (yabr *BinaryReader) ReadInstruction() (instr Instruction, err error) {
	if yabr.getBlock().Type != BDProgramBody {
		return Instruction{}, fmt.Errorf("Instructions are only valid in program bodies")
	}

	err = yabr.read(&instr)
	if err != nil {
		return
	}

	yabr.updateCounters(1, 1)
	return
}

func (yabr *BinaryReader) ReadValuesPair() (values []int64, err error) {
	if yabr.getBlock().Type != BDValues {
		return nil, fmt.Errorf("Values are only valid in values context")
	}

	values = make([]int64, 2, 2)
	err = yabr.read(values)
	if err != nil {
		return
	}

	if yabr.getBlock().Length == 1 {
		// Truncate value: remove padding value which was added to the end
		values = values[:1]
	}
	yabr.updateCounters(2, 1)
	return
}

func (yabr *BinaryReader) ReadString(off uint32) (str string) {
	_, err := yabr.strReader.Seek(int64(off+yabr.length*sizeofDirective), io.SeekStart)
	if err != nil {
		return ""
	}

	buf := bytes.NewBuffer([]byte{})
readLoop:
	for {
		var tmp [sizeofDirective]byte
		yabr.strReader.Read(tmp[:])

		for i, b := range tmp {
			if b == '\000' {
				buf.Write(tmp[:i])
				break readLoop
			}
		}

		buf.Write(tmp[:])
	}

	return buf.String()
}

// Reads program which goes after current BDProgram directive (no need for
// UnreadDirective())
func (yabr *BinaryReader) ReadProgram() (*Program, error) {
	prog := &Program{
		Name: yabr.ReadString(yabr.lastDirective.P0),
	}

	block := yabr.getBlock()
	if block.Type != BDProgram || block.Offset > 0 {
		return nil, fmt.Errorf("ReadProgram() should be called after program directive")
	}

	for block.Length > 1 {
		dir, err := yabr.ReadDirective()
		if err != nil {
			return nil, err
		}

		switch dir.Type {
		case BDRegisterHint:
			prog.Hints = append(prog.Hints, RegisterHint{
				Register: RegisterIndex(dir.P0),
				Hint:     RegisterHintType(dir.P1),
			})
		case BDRegisterName:
			reg := RegisterIndex(dir.P0)
			for index, hint := range prog.Hints {
				if hint.Register == reg {
					prog.Hints[index].Name = yabr.ReadString(dir.P1)
				}
			}
		case BDRegisterTransitiveHint:
			prog.TransHints = append(prog.TransHints, RegisterTransitiveHint{
				Register0: RegisterIndex(dir.P0),
				Register1: RegisterIndex(dir.P1),
			})
		case BDEntryPoint:
			prog.EntryPoints = append(prog.EntryPoints, EntryPoint{
				Register: RegisterIndex(dir.P0),
				Address:  dir.P1,
			})
		case BDProgramBody:
			prog.Instructions = make([]Instruction, dir.Length)
			err = yabr.read(prog.Instructions)
			if err != nil {
				return nil, err
			}

			yabr.updateCounters(int(dir.Length), int(dir.Length))
		case BDValues:
			prog.Values = make([]int64, dir.P0)
			err = yabr.read(prog.Values)
			if err != nil {
				return nil, err
			}
			if dir.P0%2 != 0 {
				// Read padding value if it was provided
				var pad int64
				yabr.read(&pad)
			}

			yabr.updateCounters(int(dir.Length), int(dir.Length))
		default:
			return nil, fmt.Errorf("Unknown directive %x at offset %x", dir.Type, yabr.offset)
		}

		block = yabr.getBlock()
	}

	end, err := yabr.ReadDirective()
	if err != nil {
		return nil, err
	}
	if end.Type != BDProgramEnd {
		yabr.UnreadDirective()
		return nil, fmt.Errorf("Missing .AEND as the last directive at offset %x",
			yabr.offset)
	}

	return prog, nil
}
