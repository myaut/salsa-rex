package main

import (
	"fmt"
	"io"
	"os"

	"strings"

	"yatima"
)

func dumpFile(inFile string, doShowModel bool) (err error) {
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

	if doShowModel {
		return showModel(yabr)
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

		fmt.Printf("%6x %3s | %28s | ", off, address, rawData)
		fmt.Println(indent, data)
	}

	return
}

func decodeDirective(yabr *yatima.BinaryReader, block yatima.BDBlock) (data, rawData string, err error) {
	switch block.Type {
	case yatima.BDProgramBody:
		isLinked := yabr.InBlock(yatima.BDLProgram)
		instr, err := yabr.ReadInstruction()

		rawData = fmt.Sprintf("%4x %6x %6x %6x", instr.I, instr.RI0, instr.RI1, instr.RO)
		return instr.Disassemble(isLinked), rawData, err
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
	rawData = fmt.Sprintf("%2x'%4x %6x %6x %6x", dir.Type>>24, dir.Type&0xFFFFFF,
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
	case yatima.BDLProgram:
		data = ".PROGRAM (linked)"
	case yatima.BDLRegisters:
		data = ".REGS"
	case yatima.BDLEntryPoint:
		data = fmt.Sprintf(".ENTRY ... +%x", dir.P0)
	default:
		data = fmt.Sprint(dir)
	}

	return
}

// Generate model as braces-form and dump to stdout
func showModel(yabr *yatima.BinaryReader) (err error) {
	var actorNames []string
	for {
		dir, err := yabr.ReadDirective()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if dir.Type == yatima.BDProgram {
			actorNames = append(actorNames, yabr.ReadString(dir.P0))
		}
		if dir.Type != yatima.BDModel {
			err = yabr.IgnoreBlock()
			if err != nil {
				return err
			}
			continue
		}

		model, err := yabr.ReadModel()
		if err != nil {
			return err
		}

		// Ignore actors that are connected to the following actors
		usedActors := make(map[uint32]struct{})
		for _, actor := range model.Actors {
			for _, input := range actor.Inputs {
				if input.Cluster == 0 {
					usedActors[input.Group-1] = struct{}{}
				}
			}
		}

		// Generate brace form for the actors that are not connected to other actors
		for actorIndex := range model.Actors {
			if _, ok := usedActors[uint32(actorIndex)]; ok {
				continue
			}

			showModelActor(model, actorIndex, actorNames, 0)
		}
	}
}

func showModelActor(model *yatima.BaseModel, actorIndex int, actorNames []string,
	indent int) {

	indentStr := strings.Repeat("  ", indent)
	actor := &model.Actors[actorIndex]
	fmt.Println(indentStr, actorNames[actor.ProgramIndex], "(")

	for _, input := range actor.Inputs {
		if input.Cluster == 0 {
			showModelActor(model, int(input.Group-1), actorNames, indent+1)
		} else {
			pinCluster := model.Inputs[input.Cluster-1]
			pinGroup := pinCluster.Groups[input.Group]
			pin := pinGroup.Pins[input.Pin]

			fmt.Println(indentStr, "  ", pinCluster.Name, ".", pinGroup.Name, ".",
				pin.Name, ",")
		}
	}

	fmt.Println(indentStr, " )")
}
