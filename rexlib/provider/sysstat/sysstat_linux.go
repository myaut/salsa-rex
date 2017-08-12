package sysstat

import (
	"fmt"
	"os"

	"bufio"

	"strconv"
	"strings"

	"time"

	"reflect"

	"rexlib/provider"
	"tsfile"
)

const (
	fStat   string = "/proc/stat"
	fVMStat string = "/proc/vmstat"
)

const (
	sstRaw = iota
	sstJiffies
	sstPages
)

type statistic struct {
	dataType int
	name     string

	fileName   string
	key        string
	index      int
	lastInLine bool

	cumulative bool
}

// List of statistics collectible by sysstat provider. It is important
// to maintain same order as in actual files because we want to read
// data sequentally
var stats = []statistic{
	statistic{sstJiffies, "cpu_usr", fStat, "cpu", 0, false, true},
	statistic{sstJiffies, "cpu_usr_nice", fStat, "cpu", 1, false, true},
	statistic{sstJiffies, "cpu_sys", fStat, "cpu", 2, false, true},

	statistic{sstRaw, "ctxsw", fStat, "ctxt", 0, true, true},
	statistic{sstRaw, "forks", fStat, "processes", 0, true, true},

	statistic{sstRaw, "prun", fStat, "procs_running", 0, true, false},
	statistic{sstRaw, "pwait", fStat, "procs_blocked", 0, true, false},

	statistic{sstPages, "pgin", fVMStat, "pgpgin", 0, true, true},
	statistic{sstPages, "pgout", fVMStat, "pgpgout", 0, true, true},
}

type SysStatProvider struct {
	// List of indeces of statistics which has to be collected
	stats []int

	// ID of the corresponding schema
	traceTag tsfile.TSFPageTag

	// Last snapshot (for cumulative stats)
	lastSnap []int64
	lastTime time.Time
}

type statFileReader struct {
	fileName string
	file     *os.File
	reader   *bufio.Reader

	key   string
	index int

	lastStat *statistic

	lastError error
}

func (sfr *statFileReader) ReadStatistic(stat *statistic) int64 {
	if stat.fileName != sfr.fileName {
		sfr.lastError = sfr.openFile(stat.fileName)
		sfr.lastStat = nil
	}

	for sfr.key != stat.key && sfr.lastError == nil {
		if sfr.lastStat != nil && !sfr.lastStat.lastInLine {
			// Ignore this line's contents (if we already reading file)
			_, sfr.lastError = sfr.reader.ReadString('\n')
			sfr.lastStat = nil
		}
		if sfr.lastError == nil {
			sfr.key, sfr.lastError = sfr.reader.ReadString(' ')
			sfr.key = strings.TrimRight(sfr.key, " ")
			sfr.index = -1
		}
	}

	var strValue string
	for sfr.index < stat.index && sfr.lastError == nil {
		// Scan the line unless we find field at index
		separator := ' '
		if stat.lastInLine {
			separator = '\n'
		}

		strValue, sfr.lastError = sfr.reader.ReadString(byte(separator))
		if strings.Count(strValue, string(separator)) == len(strValue) {
			// This string is only separator, try next rune
			continue
		}

		if stat.lastInLine && strings.IndexByte(strValue, ' ') != -1 {
			// We expected that there will be only sinle value in this line, but
			// we found a space character, so raise an error
			sfr.lastError = fmt.Errorf("Too many values per line")
			return -1
		}
		if !stat.lastInLine && strings.IndexByte(strValue, '\n') != -1 {
			// If we had reached End-Of-Line, but didn't find item at
			// specified index, that is an error
			sfr.lastError = fmt.Errorf("EOL")
			return -1
		}

		strValue = strings.TrimRight(strValue, "\n ")

		sfr.index++
	}

	if len(strValue) == 0 || sfr.lastError != nil {
		return -1
	}

	sfr.lastStat = stat

	// Ignore this error, as we don't expect broken integers here
	value, _ := strconv.ParseInt(strValue, 10, 64)
	return value
}

func (sfr *statFileReader) openFile(fileName string) (err error) {
	// Close previous file
	sfr.Close()

	sfr.file, err = os.Open(fileName)
	if err == nil {
		sfr.reader = bufio.NewReader(sfr.file)
		sfr.fileName = fileName
	}
	return
}

func (sfr *statFileReader) Close() {
	if sfr.file != nil {
		sfr.reader = nil
		sfr.file.Close()
	}
}

func (prov *SysStatProvider) Configure(action provider.ConfigurationAction,
	step *provider.ConfigurationStep) ([]*provider.ConfigurationStep, error) {

	// Get current configuration
	if action == provider.ConfigureGetValues {
		step = &provider.ConfigurationStep{
			Name:   "stat",
			Values: make([]string, len(prov.stats)),
		}

		for i, statIdx := range prov.stats {
			step.Values[i] = stats[statIdx].name
		}

		return []*provider.ConfigurationStep{step}, nil
	}

	var newStats []int
	var availableStatNames []string

	statIdxOff := 0
	for statIdx, stat := range stats {
		if statIdxOff < len(prov.stats) && prov.stats[statIdxOff] == statIdx {
			// This statistic was specified earlier, keep it in stats array
			newStats = append(newStats, prov.stats[statIdxOff])
			statIdxOff++
			continue
		}

		if step.PopValue(stat.name) {
			if action == provider.ConfigureSetValue {
				// This is new (requested stat), add it to new stats array
				newStats = append(newStats, statIdx)
			}
			continue
		}

		// This statistic is neither new one, nor pre-existed, so it can
		// be picked in future configuration steps
		availableStatNames = append(availableStatNames, stat.name)
	}
	if !step.CheckValues() {
		return []*provider.ConfigurationStep{step}, provider.ErrInvalidConfigurationValue
	}

	if action == provider.ConfigureSetValue {
		prov.stats = newStats
	}

	step = &provider.ConfigurationStep{
		Name:   "stat",
		Values: availableStatNames,
	}
	return []*provider.ConfigurationStep{step}, nil
}

func (prov *SysStatProvider) Prepare(handle *provider.OutputHandle) (err error) {
	// Generate TSF schema for a slice we're going to provide
	rType := reflect.TypeOf(int64(0))

	var fields []tsfile.TSFSchemaField
	for _, statIdx := range prov.stats {
		stat := &stats[statIdx]
		fields = append(fields, tsfile.NewField(stat.name, rType))
	}

	schema, err := tsfile.NewSchema("sysstat", fields)
	if err == nil {
		prov.traceTag, err = handle.Trace.AddSchema(schema)
	}
	return
}

func (prov *SysStatProvider) Finalize(handle *provider.OutputHandle) {

}

func (prov *SysStatProvider) Collect(handle *provider.OutputHandle) {
	sfr := new(statFileReader)
	defer sfr.Close()

	// Current snapshot of absolute values
	snap := make([]int64, len(prov.stats))

	// Processed values which take into account cumulative
	values := make([]int64, len(prov.stats))
	var valueCount int

	now := handle.Now
	timeDelta := now.Sub(prov.lastTime)
	for index, statIdx := range prov.stats {
		stat := &stats[statIdx]
		value := sfr.ReadStatistic(stat)
		if sfr.lastError != nil {
			// TODO ratelimit this message
			handle.Log.Println(sfr.lastError)
			continue
		}

		// TODO Convert value depending on units
		switch stat.dataType {
		case sstJiffies:
			value = value
		case sstPages:
			value = value
		}

		// Save absolute value
		snap[index] = value

		// Normalize value as per-second value if it is cumulative
		if stat.cumulative {
			if len(prov.lastSnap) <= index {
				// Not enough data for now
				continue
			}

			value = int64(time.Second) * (value - prov.lastSnap[index]) / int64(timeDelta)
		}

		values[index] = value
		valueCount++
	}

	if valueCount == len(prov.stats) {
		// All values have been collected, time to write something to TSF
		handle.Trace.AddEntries(prov.traceTag, [][]int64{values})
	}

	prov.lastSnap = snap
	prov.lastTime = now
}

// Factory creating sysstat provider
func Create() provider.Provider {
	return new(SysStatProvider)
}
