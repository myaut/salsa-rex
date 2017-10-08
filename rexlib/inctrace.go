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

func (incident *Incident) createTraceFile() (err error) {
	incident.traceFile, err = os.Create(filepath.Join(incident.path, "trace.tsf"))
	if err == nil {
		incident.trace, err = tsfile.NewTSFile(incident.traceFile,
			tsfile.TSFFormatV2|tsfile.TSFFormatExt)
	}

	return
}

func (incident *Incident) GetTraceFile() (tsf *tsfile.TSFile, err error) {
	incident.mtx.Lock()
	defer incident.mtx.Unlock()

	if incident.traceFile == nil {
		err = incident.loadTraceFile()
	} else {
		incident.traceCloser.Notify()
	}
	return incident.trace, err
}

func (incident *Incident) loadTraceFile() (err error) {
	if incident.traceCloser == nil {
		incident.traceCloser = newCloserWatchdog(defferedTraceCloseDelay)
	}

	incident.traceFile, err = os.Open(filepath.Join(incident.path, "trace.tsf"))
	if err == nil {
		incident.trace, err = tsfile.LoadTSFile(incident.traceFile)
	}

	go func(incident *Incident) {
		incident.traceCloser.Wait()

		incident.mtx.Lock()
		defer incident.mtx.Unlock()

		incident.traceFile = nil
		incident.closeTraceFile()
	}(incident)

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
