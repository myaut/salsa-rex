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
		data, rawData, err := decodeDirective(yabr, block)

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

		fmt.Printf("%6x %3s | %24s | ", off, address, rawData)
		fmt.Println(indent, data)
	}

	return
}

func decodeDirective(yabr *yatima.BinaryReader, block yatima.BDBlock) (data, rawData string, err error) {
	switch block.Type {
	case yatima.BDProgramBody:
		instr, err := yabr.ReadInstruction()

		rawData = fmt.Sprintf("%4x %4x %4x %4x", instr.I, instr.RI0, instr.RI1, instr.RO)
		return instr.Disassemble(), rawData, err
	case yatima.BDValues:
		values, err := yabr.ReadValuesPair()
		valStrings := make([]string, 2)

		for i, val := range values {
			valStrings[i] = fmt.Sprintf("%%v%d = %d", block.Offset+i, val)
		}

		rawData = fmt.Sprintf("%16x", values[0])
		if len(values) > 1 {
			data = fmt.Sprintf("; %-24s | %16x %s", valStrings[0], values[1], valStrings[1])
		} else {
			data = fmt.Sprintf("; %s", valStrings[0])
		}
		return data, rawData, err
	}

	dir, err := yabr.ReadDirective()
	if err != nil {
		return "", "", err
	}
	rawData = fmt.Sprintf("%2x'%4x %4x %4x %4x", dir.Type>>24, dir.Type&0xFFFFFF,
		dir.P0, dir.P1, dir.Length)

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
	case yatima.BDRegisterTransitiveHint:
		data = fmt.Sprintf(".TRANS %s %s", yatima.RegisterIndex(dir.P0).Name(),
			yatima.RegisterIndex(dir.P1).Name())
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

			registers = append(registers, yatima.RegisterIndex(dir2.P0).Name())
		}

		data = fmt.Sprintf("; .ENTRY %s +%d", strings.Join(registers, " "),
			dir.P1)
	case yatima.BDModel:
		data = ".MODEL"
	case yatima.BDPinCluster, yatima.BDPinGroup:
		data = fmt.Sprintf(".PINS %s", yabr.ReadString(dir.P0))
	case yatima.BDPin:
		data = fmt.Sprintf(".PIN _ %s %s", yatima.RegisterHintType(dir.P1).Name(),
			yabr.ReadString(dir.P0))
	case yatima.BDActorInstance:
		data = fmt.Sprintf(".AINST %d %s", dir.P0, yatima.ActorTimeMode(dir.P1).Name())
	case yatima.BDActorInput:
		var pin yatima.PinIndex
		pin.Decode(dir.P0)
		data = fmt.Sprintf(".INPUT %d:%d:%d", pin.Cluster, pin.Group, pin.Pin)
	default:
		data = fmt.Sprint(dir)
	}

	return
}
