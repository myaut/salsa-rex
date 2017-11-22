package yatima

import (
	"fmt"

	"sort"
)

// Linker builds a single code block by merging code of all actors in provided
// model and allocating registers in the register table to them, thus allowing
// to run network in a abstract machine

type LinkedProgram struct {
	Instructions []Instruction

	Registers []PinIndex
	NumInputs int

	EntryPoints []uint32
	Values      []int64
}

type linkedActorInstance struct {
	firstInstruction                           uint32
	baseStaticReg, baseOutputReg, baseValueReg uint32

	isLinked         bool
	subscribedGroups []int
	callBlocks       []uint32
}

type linkerState struct {
	prog  *LinkedProgram
	model *Model
	base  *Model

	callTails map[RegisterIndex]uint32

	firstInstruction uint32
	actors           []linkedActorInstance
}

type actorLinkerState struct {
	linker *linkerState

	actor       *ActorInstance
	baseActor   *ActorInstance
	linkedActor *linkedActorInstance

	callInputInstrucions []uint32
}

func (model *Model) Link() (*LinkedProgram, error) {
	linker := &linkerState{
		prog:  new(LinkedProgram),
		model: model.Clone(),
		base:  model,
	}
	if len(linker.model.UnboundPins) > 0 {
		return nil, fmt.Errorf("Not all pins were bound")
	}

	// Create linked state for each actor
	for _ = range linker.model.Actors {
		linker.actors = append(linker.actors, linkedActorInstance{})
	}

	linker.allocateRegisters()
	linker.generateCallVector()
	for actorIndex := range linker.model.Actors {
		linker.addActor(actorIndex)
	}
	linker.compressCalls()

	// Generate entry points table (for debugging)
	for _, linkedActor := range linker.actors {
		linker.prog.EntryPoints = append(linker.prog.EntryPoints,
			linkedActor.firstInstruction)
	}

	return linker.prog, nil
}

func (linker *linkerState) getAddress() uint32 {
	return uint32(len(linker.prog.Instructions))
}

// Compute register table: registers 0-12 are global/local registers, followed by
// input registers, state registers and value registers
type PinTable []PinIndex

func (pt PinTable) Len() int {
	return len(pt)
}
func (pt PinTable) Less(i, j int) bool {
	return pt[i].Less(pt[j])
}
func (pt PinTable) Swap(i, j int) {
	temp := pt[i]
	pt[i] = pt[j]
	pt[j] = temp
}

func (linker *linkerState) allocateRegisters() {
	var inputTable, staticTable PinTable

	// First pass -- build global list of all possible values / etc.
	inputMap := make(map[uint32]bool)
	for actorIndex, actor := range linker.model.Actors {
		for _, input := range actor.Inputs {
			if input.Cluster == 0 {
				// If this is internal pin, we will process them as actor output
				continue
			}

			key := input.Encode()
			if _, ok := inputMap[key]; ok {
				continue
			}

			inputMap[key] = true
			inputTable = append(inputTable, input)
		}

		for pinIndex := range actor.Outputs {
			staticTable = append(staticTable, linker.model.getActorOutput(
				uint32(actorIndex), uint32(pinIndex)))
		}

		// Also add static registers for this actor and values (if not yet added)
		prog := linker.model.base.library.Programs[actor.ProgramIndex]
		staticTable = append(staticTable, linker.model.base.generateProgramStatics(
			prog, uint32(actorIndex), uint32(len(actor.Outputs)))...)
	}

	sort.Sort(inputTable)
	// staticTable is sorted by default as we append values through iterating
	// with increasing indeces

	baseInputReg := uint32(RI0) // == 12
	baseStaticReg := baseInputReg + uint32(len(inputTable))
	baseValueReg := baseStaticReg + uint32(len(staticTable))

	// Second pass -- re-assign actor pins to zero indeces with Pin == reg offset
	progs := make(map[uint32]uint32)
	for actorIndex, actor := range linker.model.Actors {
		// Map actor inputs to corresponding registers from input table
		// or actor outputs in statics table
		for inputIndex, input := range actor.Inputs {
			var newPin PinIndex

			if input.Cluster == 0 {
				newPin.Pin = baseStaticReg + uint32(sort.Search(len(staticTable),
					func(i int) bool { return !staticTable[i].Less(input) }))
			} else {
				newPin.Pin = baseInputReg + uint32(sort.Search(len(inputTable),
					func(i int) bool { return !inputTable[i].Less(input) }))
			}

			actor.Inputs[inputIndex] = newPin
		}

		// Find first actor output from statics table and assign it as base number
		linkedActor := &linker.actors[actorIndex]
		firstOutputPin := linker.model.getActorOutput(uint32(actorIndex), 0)
		linkedActor.baseOutputReg = baseStaticReg + uint32(sort.Search(len(staticTable),
			func(i int) bool { return !staticTable[i].Less(firstOutputPin) }))

		// Find first static index beyound outputs. If there are no statics,
		// we will abort search
		actorStaticIndex := linkedActor.baseOutputReg - baseStaticReg + 1
		for actorStaticIndex < uint32(len(staticTable)) {
			pin := staticTable[actorStaticIndex]

			if pin.Group != uint32(actorIndex+1) {
				break
			}
			if pin.Pin >= uint32(len(actor.Outputs)) {
				linkedActor.baseStaticReg = baseStaticReg + actorStaticIndex
				break
			}

			actorStaticIndex++
		}

		// Finally, add values for a program (or use existing ones)
		if baseProgValueReg, ok := progs[actor.ProgramIndex]; ok {
			linkedActor.baseValueReg = baseProgValueReg
			continue
		}

		prog := linker.model.base.library.Programs[actor.ProgramIndex]
		if len(prog.Values) == 0 {
			linkedActor.baseValueReg = 0
		} else {
			linkedActor.baseValueReg = baseValueReg + uint32(len(linker.prog.Values))
			for _, value := range prog.Values {
				linker.prog.Values = append(linker.prog.Values, value)
			}
		}

		progs[actor.ProgramIndex] = linkedActor.baseValueReg
	}

	linker.prog.NumInputs = len(inputTable)
	linker.prog.Registers = append(inputTable, staticTable...)

	for len(linker.prog.Registers) < int(RV) {
		// Pad registers table with not-connected pins. We do this so we can
		// properly use near jump encoding
		linker.prog.Registers = append(linker.prog.Registers, PinIndex{})
	}
}

func (linker *linkerState) generateCallVector() {
	// Call vector is similar in concept to interrupt vector: first instructions
	// are the calls to first input actors to be activated when input is updated
	baseInputReg := RI0
	linker.callTails = make(map[RegisterIndex]uint32)

	// Add call vector slot for RT at window and RT at end (RT + 1 which is RIP
	// which is never used)
	linker.addCallVectorSlot(RT)
	linker.addCallVectorSlot(RT + 1)
	for inputIndex, input := range linker.prog.Registers {
		if input.Cluster == 0 {
			break
		}

		linker.addCallVectorSlot(baseInputReg + RegisterIndex(inputIndex))
	}
	linker.prog.Instructions = append(linker.prog.Instructions, Instruction{I: RET})

	linker.firstInstruction = linker.getAddress()
}

func (linker *linkerState) addCallVectorSlot(reg RegisterIndex) {
	linker.callTails[reg] = linker.getAddress()
	linker.prog.Instructions = append(linker.prog.Instructions, Instruction{
		I: CALL, RO: reg})
}

func (linker *linkerState) addActor(actorIndex int) {
	linkedActor := &linker.actors[actorIndex]
	if linkedActor.isLinked {
		return
	}
	linkedActor.isLinked = true

	baseActor := &linker.base.Actors[actorIndex]
	for _, input := range baseActor.Inputs {
		if input.Cluster == 0 {
			// Recursively add input actors on which we depend before generating
			// our own code in program (they won't be duplicated as addActor()
			// keeps history in linkedActors)
			linker.addActor(int(input.Group - 1))
		}
	}

	actor := &linker.model.Actors[actorIndex]
	linkedActor.firstInstruction = linker.getAddress()

	prog := linker.model.base.library.Programs[actor.ProgramIndex]
	actorLinker := actorLinkerState{
		linker:      linker,
		actor:       actor,
		baseActor:   baseActor,
		linkedActor: linkedActor,
	}
	for _, instruction := range prog.Instructions {
		if instruction.I == CALL {
			instruction = actorLinker.linkCallInstruction(instruction)
		} else {
			instruction = actorLinker.linkInstruction(instruction)
		}

		linker.prog.Instructions = append(linker.prog.Instructions, instruction)
	}

	actorLinker.linkCallInstructions()
}

func (linker *actorLinkerState) linkInstruction(instruction Instruction) Instruction {
	iFlags := instructionDescriptors[instruction.I].flags

	if iFlags&ifBinary != 0 {
		linker.mapRegister(&instruction.RI0)
	}
	linker.mapRegister(&instruction.RI1)

	if iFlags&ifJump == 0 {
		if iFlags&ifWrite != 0 || instruction.RO >= RV {
			linker.mapRegister(&instruction.RO)
		}
	} else {
		// Jump instruction -- map register RO only for far call (via value)
		if instruction.RO >= RV {
			linker.mapRegister(&instruction.RO)
		}
	}

	return instruction
}

func (actorLinker *actorLinkerState) linkCallInstruction(instruction Instruction) Instruction {
	// This time we only register our output call slots and map RO. Actual
	// chaining of call is done by linkCallInstructions()
	linker, linkedActor := actorLinker.linker, actorLinker.linkedActor

	isOutput := instruction.RO.isOutputRegister()
	isInput := instruction.RO.isInputRegister()

	if instruction.RO == RT {
		isInput = true
		if actorLinker.actor.TimeMode == ActorTimeEnd {
			instruction.RO++
		}
	} else {
		actorLinker.mapRegister(&instruction.RO)
	}

	address := linker.getAddress()
	if isOutput {
		linker.callTails[instruction.RO] = address
	} else if isInput {
		actorLinker.callInputInstrucions = append(actorLinker.callInputInstrucions, address)
	}

	if linker.prog.Instructions[address-1].I != CALL {
		// First instruction in the block -- save its address for compression
		linkedActor.callBlocks = append(linkedActor.callBlocks, address)
	}

	return instruction
}

func (actorLinker *actorLinkerState) linkCallInstructions() {
	// Build call chains activated by register updates. callTails[reg]
	// contains address of the previous call instruction which contains a
	// slot for writing current program's address. Then we update callTails
	// for the next subscriber (for the current actor's output register,
	// we add a slot for the first subscriber)
	//
	// Deduplicate actor subscriptions when actor is already subscribed to
	// an input group (which is mapped to REX entry thus all its values should
	// be provided at ance) or an actor output (which produces all outputs at
	// once), no need to generate a second subscription, i.e.:
	//	     +----+       +----+     X -- cut because both inputs are from
	//  -----|    |-------|    |---  	  the same group
	//	-+-X-|    |   +-Y-|    |     Y -- cut because we already depend on the
	//	 |   +----+   |   +----+		  input via left actor
	//   +------------+
	//
	// To do so, for each input we pick the input that gives us the longest
	// path (most likely, it has more group dependencies)

	linker, linkedActor := actorLinker.linker, actorLinker.linkedActor

	subscriptionMap := make(map[int]uint32)
	for _, index := range actorLinker.callInputInstrucions {
		instruction := linker.prog.Instructions[index]
		if instruction.RO == RT || instruction.RO == RT+1 {
			actorLinker.subscribeCallInstruction(index, instruction.RO)
			continue
		}

		groups := linker.getSubscribedGroups(instruction.RO)

		// If groups provided by this actor path is an subset of what we
		// already subscribed, do nothing, in case of superset assume longer
		// path and re-subscribe
		var alreadySubscribedCount int
		for _, group := range groups {
			if _, ok := subscriptionMap[group]; ok {
				alreadySubscribedCount++
			}
		}
		if alreadySubscribedCount < len(groups) {
			for _, group := range groups {
				subscriptionMap[group] = index
			}
		}

	}

	instructionMap := make(map[uint32]bool)

	for _, index := range subscriptionMap {
		// This instruction was already subscribed
		if _, ok := instructionMap[index]; ok {
			continue
		}

		instruction := linker.prog.Instructions[index]
		actorLinker.subscribeCallInstruction(index, instruction.RO)

		groups := linker.getSubscribedGroups(instruction.RO)
		linkedActor.subscribedGroups = append(linkedActor.subscribedGroups, groups...)

		instructionMap[index] = true
	}
}

func (actorLinker *actorLinkerState) subscribeCallInstruction(index uint32, reg RegisterIndex) {
	linker, linkedActor := actorLinker.linker, actorLinker.linkedActor

	// Restore original register index, find corresponding entry point and
	// add entry point offset for complex actor (i.e. having two subprograms)
	baseProg := linker.base.base.library.Programs[actorLinker.baseActor.ProgramIndex]
	baseInstruction := baseProg.Instructions[index-linkedActor.firstInstruction]
	var entryPoint uint32
	for _, ep := range baseProg.EntryPoints {
		if ep.Register == baseInstruction.RO {
			entryPoint = ep.Address
			break
		}
	}

	prevCall := &linker.prog.Instructions[linker.callTails[reg]]
	prevCall.RI1 = RegisterIndex(linkedActor.firstInstruction + entryPoint)
	linker.callTails[reg] = index
}

func (linker *actorLinkerState) mapRegister(reg *RegisterIndex) {
	if reg.isInputRegister() {
		// Inputs were relocated by allocateRegisters() and their offsets
		// are encoded in pin indeces
		*reg = RegisterIndex(linker.actor.Inputs[reg.inputRegisterIndex()].Pin)
	} else if reg.isOutputRegister() {
		*reg = RegisterIndex(linker.linkedActor.baseOutputReg +
			uint32(reg.outputRegisterIndex()))
	} else if reg.isStaticRegister() {
		*reg = RegisterIndex(linker.linkedActor.baseStaticReg +
			uint32(reg.staticRegisterIndex()))
	} else if *reg >= RV {
		*reg = RegisterIndex(linker.linkedActor.baseValueReg) + *reg - RV
	}

	// local or global register -- keep mapping to first 12 registers
}

func (linker *linkerState) getSubscribedGroups(reg RegisterIndex) []int {
	inputIndex := uint32(reg - RI0)
	input := linker.prog.Registers[inputIndex]

	subscribedGroups := []int{int(input.Cluster<<12) + int(input.Group)}
	if input.Cluster == 0 {
		subscribedGroups = append(subscribedGroups, linker.actors[input.Group-1].subscribedGroups...)
	}

	return subscribedGroups
}

type linkerAddressReferences struct {
	calls []uint32
	actor *linkedActorInstance
}
type linkerAddressReferencesMap map[uint32]*linkerAddressReferences

func (larm linkerAddressReferencesMap) get(address uint32) *linkerAddressReferences {
	lar, ok := larm[address]
	if !ok {
		lar = new(linkerAddressReferences)
		larm[address] = lar
	}
	return lar
}

func (linker *linkerState) compressCalls() {
	// Call compression is an optimization which removes unnecessary call
	// instructions from the final code, specifically: calls with zero
	// address, calls to the address going directly below
	linker.callTails = nil

	instructions := linker.prog.Instructions
	for _, actor := range linker.actors {
		for _, index := range actor.callBlocks {
			startIndex, curIndex := index, index

			var count int
			for instructions[curIndex].I == CALL {
				if instructions[curIndex].RI1 == 0 {
					// Clean up instruction at index (use later for swap)
					instructions[curIndex] = Instruction{I: NOP}
				} else {
					if curIndex != startIndex {
						// Swap instruction at start index and current one
						instructions[startIndex] = instructions[curIndex]
						instructions[curIndex] = Instruction{I: NOP}

						count++
					}
					startIndex++
				}

				curIndex++
			}

			if count == 1 && instructions[index].RI1 == RegisterIndex(curIndex+1) {
				// A special case when we have one output and our subscriber
				// is located directly below. We can generalize this case by
				// further relocating calls, but don't want to for now
				// Anyway, drop the only CALL and last RET
				instructions[index] = Instruction{I: NOP}
			} else {
				// Swap RET and NOPs
				instructions[startIndex] = Instruction{I: RET}
			}
			instructions[curIndex] = Instruction{I: NOP}
		}
	}

	// Second pass: filter out all nops that we produced. We also will need to
	// update all addresses we have, so we need to build a big map first
	references := make(linkerAddressReferencesMap)
	for index, instruction := range instructions {
		if instruction.I == CALL {
			lar := references.get(uint32(instruction.RI1))
			lar.calls = append(lar.calls, uint32(index))
		}
	}
	for actorIndex := range linker.actors {
		actor := &linker.actors[actorIndex]
		lar := references.get(actor.firstInstruction)
		lar.actor = actor
	}

	// Pass 2a -- since we're planning to update instructions, first update
	// call addresses
	startIndex := uint32(0)
	for curIndex := uint32(0); curIndex < uint32(len(instructions)); curIndex++ {
		if instructions[curIndex].I != NOP {
			if startIndex != curIndex {
				// Since we moved some instruction, update some pointers to it
				if lar, ok := references[curIndex]; ok {
					for _, callIndex := range lar.calls {
						newCallIndex := callIndex
						if callIndex < curIndex {
							callIndex -= (curIndex - startIndex)
						}
						instructions[newCallIndex].RI1 = RegisterIndex(startIndex)
					}
					if lar.actor != nil {
						lar.actor.firstInstruction = startIndex
					}
				}
			}
			startIndex++
		}
	}

	// Pass 2b -- compress instructions by removing NOP windows
	startIndex = uint32(0)
	for curIndex := uint32(0); curIndex < uint32(len(instructions)); curIndex++ {
		if instructions[curIndex].I != NOP {
			if startIndex != curIndex {
				instructions[startIndex] = instructions[curIndex]
			}
			startIndex++
		}
	}

	// Cut instructions array: remove dead instructions
	linker.prog.Instructions = instructions[:startIndex]
}

// Return slice of registers map corresponding to base input group and base
// index of pin to be provided for WriteInput()
func (prog *LinkedProgram) FindInputs(base PinIndex) ([]PinIndex, uint32) {
	baseIndex := sort.Search(prog.NumInputs,
		func(i int) bool { return !prog.Registers[i].Less(base) })
	if baseIndex == -1 {
		return nil, 0
	}

	index := baseIndex
	for ; index < len(prog.Registers); index++ {
		pin := prog.Registers[index]
		if base.Cluster != pin.Cluster || base.Group != pin.Group {
			break
		}
	}

	return prog.Registers[baseIndex:index], uint32(baseIndex)
}

// Linearly (via O(n*m)) find necessary general outputs for the model/program
// and return offsets in
func (prog *LinkedProgram) FindOutputs(pinIndeces []PinIndex) (indeces []uint32) {
	for index := prog.NumInputs; index < len(prog.Registers); index++ {
		pin := prog.Registers[index]
		for _, pin2 := range pinIndeces {
			if pin == pin2 {
				indeces = append(indeces, uint32(RI0)+uint32(index))
				break
			}
		}

		if len(indeces) == len(pinIndeces) {
			return
		}
	}

	return
}
