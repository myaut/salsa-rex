package main

import (
	"fmt"
	"io"
	"os"

	"strings"

	"yatima"
)

func dumpFile(inFile string) (err error) {
	inf, err := os.Open(inFile)
	if err != nil {
		return
	}
	defer inf.Close()

	strf, err := os.Open(inFile)
	if err != nil {
		return
	}
	defer strf.Close()

	yabr, err := yatima.NewReader(inf, strf)
	if err != nil {
		return
	}

	for {
		block, off, depth := yabr.GetState()
		data, err := decodeDirective(yabr, block)

		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Println(err)
			break
		}

		indent := strings.Repeat("    ", depth)
		address := ""
		switch block.Type {
		case yatima.BDProgramBody, yatima.BDValues:
			address = fmt.Sprintf("+%x", block.Offset)
		}

		fmt.Printf("%6x %3s | ", off, address)
		fmt.Println(indent, data)
	}

	return
}

func decodeDirective(yabr *yatima.BinaryReader, block yatima.BDBlock) (data string, err error) {
	switch block.Type {
	case yatima.BDProgramBody:
		instr, err := yabr.ReadInstruction()

		return instr.Disassemble(), err
	case yatima.BDValues:
		values, err := yabr.ReadValuesPair()
		valStrings := make([]string, 2)

		for i, val := range values {
			valStrings[i] = fmt.Sprintf("%%v%d = %x (%d)", block.Offset+i, val, val)
		}

		if len(values) > 1 {
			data = fmt.Sprintf("; %-24s | %s", valStrings[0], valStrings[1])
		} else {
			data = fmt.Sprintf("; %s", valStrings[0])
		}
		return data, err
	}

	dir, err := yabr.ReadDirective()
	if err != nil {
		return "", err
	}

	switch dir.Type {
	case yatima.BDProgram:
		data = fmt.Sprintf(".ACTOR %s \t ; #%d", yabr.ReadString(dir.P0), dir.P1)
	case yatima.BDProgramBody:
		data = ".PROGRAM"
	case yatima.BDProgramEnd:
		data = ".AEND"
	case yatima.BDValues:
		data = fmt.Sprintf(".VALUES %d", dir.P0)
	case yatima.BDRegisterHint:
		var name string
		// Check if the next directive is BDRegisterName

		dir2, err2 := yabr.ReadDirective()
		if err2 != nil {
			err = err2
			return
		}
		if dir2.Type != yatima.BDRegisterName || dir2.P0 != dir.P0 {
			err = yabr.UnreadDirective()
		} else {
			name = yabr.ReadString(dir2.P1)
		}

		data = fmt.Sprintf(".REG %s %s %s", yatima.RegisterIndex(dir.P0).Name(),
			yatima.RegisterHintType(dir.P1).Name(), name)
	case yatima.BDRegisterName:
		data = fmt.Sprintf("; .REG %s _ %s", yatima.RegisterIndex(dir.P0).Name(),
			yabr.ReadString(dir.P1))
	case yatima.BDEntryPoint:
		// Entry points for the same address generated sequentally, improve
		// readability by merging it
		registers := []string{yatima.RegisterIndex(dir.P0).Name()}
		for {
			dir2, err2 := yabr.ReadDirective()
			if err2 != nil {
				err = err2
				return
			}
			if dir2.Type != yatima.BDEntryPoint || dir2.P1 != dir.P1 {
				err = yabr.UnreadDirective()
				break
			}
		}

		data = fmt.Sprintf("; .ENTRY %s +%d", strings.Join(registers, " "),
			dir.P1)
	default:
		data = fmt.Sprint(dir)
	}

	return
}
