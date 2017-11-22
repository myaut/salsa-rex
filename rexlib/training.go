package rexlib

import (
	"os"
	"path/filepath"

	"runtime/debug"

	"fmt"
	"log"

	"time"

	"tsfile"
	"yatima"
)

// Core for training actor networks based on experiment+incident pairs. Management
// of such training sessions is handled by yatima.go. To generate actors with
// proper inputs, we use genetic algorithm from yatima which tries every
// valid combination and synthesizes at max single actor. Then we feed actor
// network with data from trace, and in the end, gather result

type trainingHandle struct {
	session *TrainingSession

	incidents []*Incident
	parents   []*TrainingSession

	logFile *os.File
	log     *log.Logger

	baseModel          *yatima.BaseModel
	outError, outRatio yatima.PinIndex

	resultsChan chan TrainingNetworkResult
}

type trainingModelHandle struct {
	handle *trainingHandle
	model  *yatima.Model

	prog *yatima.LinkedProgram
	path string
}

type trainingTimeGenerator struct {
	nextTime int64
	tick     int64
}

func (handle *trainingHandle) run() {
	defer handle.logFile.Close()

	err := handle.prepareBaseModel()
	if err != nil {
		handle.log.Printf("Cannot prepare base model: %v", err)
		return
	}

	mutator := handle.baseModel.NewMutator()
	var dispatched, completed int
	for model := mutator.Next(); model != nil; model = mutator.Next() {
		if model.Error != nil {
			if handle.session.Trace {
				handle.log.Printf("%s is rejected: %v", model.Signature(), model.Error)
			}
			continue
		}

		Training.modelChan <- &trainingModelHandle{handle: handle, model: model}
		dispatched++
		completed += handle.gatherResults()
	}

	handle.log.Printf("Scheduled training of %d models", dispatched)

	for dispatched > completed {
		completed += handle.gatherResults()
	}

	handle.log.Printf("Completed training of %d models", completed)
	handle.session.save()
}

func (handle *trainingHandle) gatherResults() int {
	var completed int

	for {
		select {
		case result := <-handle.resultsChan:
			handle.session.Results = append(handle.session.Results, result)
			completed++
		default:
			return completed
		}
	}

	return completed
}

func (handle *trainingHandle) prepareBaseModel() error {
	// Seed initial network (we want network to generate exactly one result
	// so we use an aggregator here) and list of available inputs
	handle.baseModel = yatima.NewBaseModel(Training.templates)

	for _, incident := range handle.incidents {
		trace, err := incident.GetTraceFile()
		if err != nil {
			return err
		}
		defer trace.Put()

		cluster := yatima.PinCluster{Name: incident.Name}
		stats := trace.GetStats()
		for _, seriesStats := range stats.Series {
			group := yatima.PinGroup{Name: seriesStats.Name}

			schema, err := trace.GetSchema(seriesStats.Tag)
			if err != nil {
				return err
			}

			info := schema.Info()
			for _, field := range info.Fields {
				hint := yatima.RIOUnusable
				switch field.FieldType {
				case tsfile.TSFFieldInt, tsfile.TSFFieldStartTime, tsfile.TSFFieldEndTime:
					hint = yatima.RIORandom
				case tsfile.TSFFieldEnumerable:
					hint = yatima.RIOEnumerable
				}

				group.Pins = append(group.Pins, yatima.Pin{
					Name: field.FieldName,

					// TODO support enumerables in tsfile
					Hint: hint,
				})
			}

			cluster.Groups = append(cluster.Groups, group)
		}

		handle.baseModel.Inputs = append(handle.baseModel.Inputs, cluster)
	}

	// TODO seed selected subnetworks from other sessions

	// Generate network framework. We want our latest two actors to be regr
	// linear which will deduce correlations between two possibly dependent
	// random variables and aggregator which will ensure that value is steady
	regrLinId, err := handle.baseModel.AddActor("regr_lin", yatima.ActorTimeNone,
		make([]yatima.PinIndex, 2))
	if err != nil {
		return err
	}

	// TODO replace with stddev?
	aggrAvgId, err := handle.baseModel.AddActor("aggr_avg", yatima.ActorTimeEnd,
		[]yatima.PinIndex{handle.baseModel.FindActorOutput(regrLinId, 1)})
	if err != nil {
		return err
	}

	// Save pin indeces of the important outputs
	handle.outError = handle.baseModel.FindActorOutput(aggrAvgId, 0)
	handle.outRatio = handle.baseModel.FindActorOutput(regrLinId, 0)

	// Dump base model to file
	modelFile, err := os.Create(filepath.Join(handle.session.path, "model.yab"))
	if err != nil {
		return err
	}
	defer modelFile.Close()

	yabw, err := yatima.NewWriter(modelFile)
	if err != nil {
		return err
	}
	defer yabw.Close()

	return yabw.AddBaseModel(handle.baseModel)
}

// Create generator of window and end events which used by training session
func (handle *trainingHandle) newTimeGenerator() *trainingTimeGenerator {
	generator := new(trainingTimeGenerator)

	for index, incident := range handle.incidents {
		startTime := incident.StartedAt.UnixNano()
		tick := int64(incident.TickInterval) * int64(time.Millisecond)

		if index == 0 || startTime < generator.nextTime {
			generator.nextTime = startTime
		}
		if index == 0 || tick < generator.tick {
			generator.tick = tick
		}
	}

	generator.nextTime += generator.tick
	return generator
}

// Updates generator state and returns non-zero time if we surpassed window
// interval and need to generate window event
func (generator *trainingTimeGenerator) updateTime(t int64) int64 {
	if t > generator.nextTime {
		t = generator.nextTime
		generator.nextTime += generator.tick
		return t
	}
	return 0
}

// Train loop limits number of models run simultaneously: it receives
// model and its handle over channel and  tries to run it
func (state *trainingState) trainLoop() {
	var machine *yatima.Machine
	var handle *trainingModelHandle

	defer func() {
		if r := recover(); r != nil {
			if handle != nil {
				result := TrainingNetworkResult{
					Signature: handle.model.Signature(),
					Error:     fmt.Sprintf("panic: %v", r),
				}

				log := handle.handle.log
				log.Printf("Cannot run %s, got panic: %v", result.Signature, r)
				log.Print(string(debug.Stack()))
				if machine != nil {
					machine.DumpRegisters(log.Printf)
				}

				handle.handle.resultsChan <- result
			}

			// Reset training procedure for another routing
			go state.trainLoop()
		}
	}()

	for {
		handle = <-state.modelChan
		modelSig := handle.model.Signature()

		err := handle.prepareTrainingModel()
		if err != nil {
			handle.handle.log.Printf("Cannot create dir for %s: %v", modelSig, err)
		}

		machine = handle.prog.NewMachine()
		handle.train(machine)

		handle, machine = nil, nil
	}
}

func (handle *trainingModelHandle) prepareTrainingModel() (err error) {
	handle.prog, err = handle.model.Link()
	if err != nil {
		return
	}

	handle.path, err = handle.handle.session.makeDirs(handle.model.Signature())
	if err != nil {
		return
	}

	err = handle.dumpLinkedProgram(handle.path, handle.model, handle.prog)
	if err != nil {
		return
	}

	return nil
}

func (handle *trainingModelHandle) train(machine *yatima.Machine) {
	result := TrainingNetworkResult{
		Signature: handle.model.Signature(),
	}

	var reader incidentSeriesDataReader
	defer reader.Put()
	defer func(res *TrainingNetworkResult) {
		handle.handle.resultsChan <- *res
	}(&result)

	for index, incident := range handle.handle.incidents {
		err := reader.AddIncident(index, incident)
		if err != nil {
			result.Error = err.Error()
			handle.handle.log.Printf("Cannot load incident %s: %v", incident.Name, err)
			return
		}
	}

	// Main training loop: update machine state with each event
	generator := handle.handle.newTimeGenerator()
	event, err := reader.Next()
	for event.Buffer != nil {
		if err != nil {
			handle.handle.log.Printf("Error in %s: %v", handle.model.Signature(), *machine)
		}

		deserializer := event.Deserializer

		startTime := deserializer.GetStartTime(event.Buffer)
		windowTime := generator.updateTime(int64(startTime))
		if windowTime != 0 {
			machine.WriteTime(windowTime, yatima.ActorTimeWindow)
			machine.Run()
		}
		if startTime > tsfile.TSTimeStart(0) {
			machine.WriteTime(int64(startTime), yatima.ActorTimeNone)
		}

		inputs, base := handle.prog.FindInputs(yatima.PinIndex{
			Cluster: uint32(event.IncidentIndex + 1),
			Group:   uint32(event.SeriesIndex),
		})
		if inputs != nil {
			for _, input := range inputs {
				// TODO string support
				_, value := deserializer.GetInt64(event.Buffer, int(input.Pin))
				machine.WriteInput(base, value)

				base++
			}
		}

		machine.Run()
		event, err = reader.Next()
	}

	machine.WriteTime(generator.nextTime, yatima.ActorTimeEnd)
	machine.Run()

	// Collect result -- first of all we need to find general output
	outputs := handle.prog.FindOutputs([]yatima.PinIndex{handle.handle.outRatio,
		handle.handle.outError})
	if len(outputs) == 2 {
		result.Ratio = machine.Registers[outputs[0]]
		result.ModelError = machine.Registers[outputs[1]]
	} else {
		result.Error = fmt.Sprintf("Unexpected number of outputs: %d", len(outputs))
	}

	handle.handle.log.Print(result)
	machine.DumpRegisters(handle.handle.log.Printf)
}

func (handle *trainingModelHandle) dumpLinkedProgram(path string, model *yatima.Model,
	prog *yatima.LinkedProgram) (err error) {

	yabFile, err := os.Create(filepath.Join(path, "program.yab"))
	if err != nil {
		return
	}
	defer yabFile.Close()

	yabWriter, err := yatima.NewWriter(yabFile)
	if err != nil {
		return
	}

	err = yabWriter.AddModel(model)
	if err == nil {
		yabWriter.AddLinkedProgram(prog)
	}
	yabWriter.Close()

	return
}
