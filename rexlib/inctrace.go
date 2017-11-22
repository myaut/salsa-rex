package rexlib

import (
	"os"
	"time"

	"bytes"
	"fmt"

	"path/filepath"

	"tsfile"
)

const (
	defferedTraceCloseDelay time.Duration = 5 * time.Second
)

type incidentSeriesData struct {
	// Initialized at creating incidentSeriesData
	index int
	tag   tsfile.TSFPageTag
	count int

	// Next extracted data buffer, its time and index
	nextIndex int
	nextTime  tsfile.TSTimeStart
	next      []byte

	// Reference to trace and flag saying that this is first series for this
	// trace, hence it is owner of it and should release it
	trace      *tsfile.TSFile
	traceOwner bool

	deserializer *tsfile.TSFDeserializer
}

type IncidentEvent struct {
	IncidentIndex int
	SeriesIndex   int

	Buffer       []byte
	Deserializer *tsfile.TSFDeserializer
}

type incidentSeriesDataReader struct {
	series []incidentSeriesData
}

func (incident *Incident) createTraceFile() (err error) {
	traceFile, err := os.Create(filepath.Join(incident.path, "trace.tsf"))
	if err == nil {
		incident.trace, err = tsfile.NewTSFile(traceFile,
			tsfile.TSFFormatV2|tsfile.TSFFormatExt)
	}

	return
}

func (incident *Incident) GetTraceFile() (tsf *tsfile.TSFile, err error) {
	incident.mtx.Lock()
	defer incident.mtx.Unlock()

	if incident.trace != nil {
		trace := incident.trace.Get()
		if trace != nil {
			return trace, nil
		}
	}

	// Return first reference to trace file
	err = incident.loadTraceFile()
	return incident.trace, err
}

func (incident *Incident) loadTraceFile() (err error) {
	traceFile, err := os.Open(filepath.Join(incident.path, "trace.tsf"))
	if err == nil {
		incident.trace, err = tsfile.LoadTSFile(traceFile)
	}

	return
}

// Merge experiment workload traces produced by TSExperiment (in TSFv1 format
// which only supports one time series per file) to main trace file and
// delete original file
func (handle *IncidentHandle) importExperimentWorkloads() {
	incident := handle.incident
	ilog := handle.providerOutput.Log
	trace := handle.providerOutput.Trace
	for workloadName, _ := range incident.Experiment.Workloads {
		var tsf *tsfile.TSFile

		path := filepath.Join(incident.path, fmt.Sprintf("%s.tsf", workloadName))
		f, err := os.Open(path)
		if err == nil {
			defer f.Close()
			tsf, err = tsfile.LoadTSFile(f)
		}
		if err == nil {
			err = trace.AddFile(tsf)
		}

		if err != nil {
			ilog.Printf("Cannot merge workload '%s' trace: %v", workloadName, err)
		}

		os.Remove(path)
		os.Remove(filepath.Join(incident.path, fmt.Sprintf("%s-schema.json", workloadName)))
	}

	incident.TraceStats = trace.GetStats()
	incident.save()
}

func (handle *IncidentHandle) logTraceStatistics() {
	trace := handle.providerOutput.Trace

	statBuf := bytes.NewBufferString("Following traces were captured:")
	stats := trace.GetStats()
	for i, schemaStat := range stats.Series {
		statBuf.WriteString(fmt.Sprintf(" %s (%d)", schemaStat.Name, schemaStat.Count))
		if i != len(stats.Series)-1 {
			statBuf.WriteRune(',')
		}
	}

	handle.providerOutput.Log.Println(statBuf.String())
}

// Create incident series data reader for internal needs. It is similar to
// reader implemented by rex binary, but doesn't need bulk load (as we don't
// need RPC ops here), hence it has a bit simpler implementation
func (reader *incidentSeriesDataReader) AddIncident(index int, incident *Incident) error {
	trace, err := incident.GetTraceFile()
	if err != nil {
		return err
	}

	first := true
	for tag, tagEnd := trace.GetDataTags(); tag < tagEnd; tag++ {
		schema, err := trace.GetSchema(tag)
		if err != nil {
			return err
		}

		reader.series = append(reader.series, incidentSeriesData{
			index:        index,
			tag:          tag,
			count:        trace.GetEntryCount(tag),
			deserializer: tsfile.NewDeserializer(schema),

			trace:      trace,
			traceOwner: first,
		})
		reader.fetch(len(reader.series) - 1)
		first = false
	}

	return nil
}

func (reader *incidentSeriesDataReader) fetch(index int) error {
	seriesData := &reader.series[index]
	if seriesData.count > seriesData.nextIndex {
		bufs := [][]byte{nil}
		err := seriesData.trace.GetEntries(seriesData.tag, bufs, seriesData.nextIndex)
		if err != nil {
			return err
		}

		seriesData.next = bufs[0]
		seriesData.nextTime = seriesData.deserializer.GetStartTime(seriesData.next)
		seriesData.nextIndex++
	}

	return nil
}

// Returns next item with smallest time. If no more items exist, returns nil
// Note that returned buffer is invalidated on the next read operation,
func (reader *incidentSeriesDataReader) Next() (IncidentEvent, error) {
	var minTime tsfile.TSTimeStart
	minIndex := -1
	for index, seriesData := range reader.series {
		if seriesData.count <= seriesData.nextIndex {
			continue
		}
		if minIndex < 0 || seriesData.nextTime < minTime {
			minIndex = index
		}
	}

	if minIndex < 0 {
		// No more valid entries are found, return nils
		return IncidentEvent{}, nil
	}

	// Return current entry and fetch next one
	seriesData := &reader.series[minIndex]
	event := IncidentEvent{
		IncidentIndex: seriesData.index,
		SeriesIndex:   minIndex,
		Buffer:        seriesData.next,
		Deserializer:  seriesData.deserializer,
	}
	return event, reader.fetch(minIndex)
}

func (reader *incidentSeriesDataReader) Put() (err error) {
	for _, seriesData := range reader.series {
		if seriesData.traceOwner {
			err2 := seriesData.trace.Put()
			if err2 != nil {
				err = err2
			}
		}
	}

	return
}
