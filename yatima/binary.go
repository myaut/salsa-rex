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
	BDEntryPoint

	// this is "YAB0" in little endian
	yabMagic = 0x30424159
)

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
	reader    io.Reader
	strReader io.ReadSeeker

	length uint32
	offset int

	stack []BDBlock

	lastDirective BinaryDirective
	hasLD         bool
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
	yabw.writer.Write(bytes.Repeat([]byte{'\000'}, 16-int(yabw.strOff%16)))

	// Finally, update header in the beginning of the file
	_, err = yabw.writer.Seek(0, io.SeekStart)
	if err != nil {
		return err
	}

	hdr := BinaryDirective{
		Type:   yabMagic,
		Length: uint32(offset / 16),
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
	preamble := make([]BinaryDirective, 0, 8)
	preamble = append(preamble, BinaryDirective{
		Type: BDProgram,
		P0:   yabw.addString(prog.Name),
		P1:   yabw.progCount})

	for _, hint := range prog.Hints {
		preamble = append(preamble, BinaryDirective{
			Type: BDRegisterHint,
			P0:   uint32(hint.Register),
			P1:   uint32(hint.Hint),
		})

		if len(hint.Name) > 0 {
			preamble = append(preamble, BinaryDirective{
				Type: BDRegisterName,
				P0:   uint32(hint.Register),
				P1:   yabw.addString(hint.Name),
			})
		}
	}
	for _, ep := range prog.EntryPoints {
		preamble = append(preamble, BinaryDirective{
			Type: BDEntryPoint,
			P0:   uint32(ep.Register),
			P1:   uint32(ep.Address),
		})
	}
	preamble = append(preamble)

	// Compute total length of the program including values
	valLength, valPad := len(prog.Values)/2, false
	if len(prog.Values)%2 == 1 {
		valLength++ // Round up as last value will be written with padding
		valPad = true
	}

	preamble[0].Length = uint32(len(preamble) + len(prog.Instructions) +
		valLength)

	// Write preamble (register hints & program tags)
	err = yabw.write(preamble)
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

func NewReader(reader io.Reader, strReader io.ReadSeeker) (*BinaryReader, error) {
	yabr := &BinaryReader{
		reader:    reader,
		strReader: strReader,
	}

	var hdrRaw [16]byte
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
	if last > 0 {
		block = yabr.stack[last]
	}

	return
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
	_, err := yabr.strReader.Seek(int64(off+yabr.length*16), io.SeekStart)
	if err != nil {
		return ""
	}

	buf := bytes.NewBuffer([]byte{})
readLoop:
	for {
		var tmp [16]byte
		yabr.strReader.Read(tmp[:])
		buf.Write(tmp[:])

		for _, b := range tmp {
			if b == '\000' {
				break readLoop
			}
		}
	}

	str, _ = buf.ReadString('\000')
	return
}
