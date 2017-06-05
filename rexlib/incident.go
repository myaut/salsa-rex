package rexlib

import (
	"os"
	"time"

	"fmt"

	"io/ioutil"
	"path/filepath"
	"strings"

	"sync"

	"encoding/json"

	"tsfile"
)

//
// incident -- code to handle incidents handled by rex tracing framework
// incidents are very similar to experiments in TSLoad
//
// Incidents occupy a subdirectory in /var/lib/rex (or wherever incidentDir
// is pointing to) and contain .json configuration and .tsf trace. They
// are created or copied from previous incidents, and can be started and
// stopped, and then removed
//

const (
	defaultIncidentTick = 100
)

type IncState int

const (
	IncCreated IncState = iota
	IncRunning
	IncStopped
)

type IncidentHandle struct {
	incident *Incident

	// TODO: Log
	Trace *tsfile.TSFile
	Tick  int

	Providers []Provider
}

type IncidentProvider struct {
	StartedAt time.Time `json:"started_at"`
	StoppedAt time.Time `json:"stopped_at,omitempty"`
	finalized bool

	SchemaTags []tsfile.TSFPageTag
}

type Incident struct {
	// Incident's name and path to incident directory
	Name string `json:"name"`
	path string

	// Incident's scheduler ticks in milliseconds
	Tick int `json:"tick"`

	// Description of incident
	Description string `json:"descr,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	StartedAt time.Time `json:"started_at"`
	StoppedAt time.Time `json:"stopped_at,omitempty"`

	Providers []IncidentProvider `json:"providers,omitempty"`
}

type IncidentDescriptor struct {
	Name        string
	Description string
	State       IncState
}

// Global incident directory which subdirs are incidents
var incidentDir string = "."

// Global cache of incidents
type incidentsState struct {
	// Protects following lists & maps
	mtx sync.Mutex

	removed int
	loaded  bool
	list    []*Incident
	cache   map[string]*Incident
}

var Incidents incidentsState

func Initialize(path string) {
	incidentDir = path
	Incidents.cache = make(map[string]*Incident)
}

func (state *incidentsState) add(incident *Incident) {
	state.mtx.Lock()
	defer state.mtx.Unlock()

	state.list = append(state.list, incident)
	state.cache[incident.Name] = incident
}

func (state *incidentsState) load() error {
	var oldIncidents []*Incident

	state.mtx.Lock()
	defer state.mtx.Unlock()
	if state.loaded {
		return nil
	}

	// Walk over data dir and load incidents
	dirs, err := ioutil.ReadDir(incidentDir)
	if err != nil {
		return err
	}

	for _, fi := range dirs {
		if fi.IsDir() {
			incident := new(Incident)
			incident.Name = fi.Name()
			incident.path = filepath.Join(incidentDir, incident.Name)

			err := incident.load()
			if err == nil {
				oldIncidents = append(oldIncidents, incident)
				state.cache[incident.Name] = incident
			}
			// TODO: decide what to do with failed incidents
		}
	}

	state.list = append(oldIncidents, state.list...)
	state.loaded = true
	return nil
}

func (state *incidentsState) GetList() (incidents []IncidentDescriptor, err error) {
	if !state.loaded {
		err = state.load()
		if err != nil {
			return
		}
	}

	state.mtx.Lock()
	defer state.mtx.Unlock()

	for _, incident := range state.list {
		if len(incident.path) == 0 {
			// This incident was removed and should be ignored
			continue
		}

		incidents = append(incidents, IncidentDescriptor{
			Name:        incident.Name,
			Description: incident.Description,
			State:       incident.GetState(),
		})
	}
	return
}

func (state *incidentsState) Get(name string) (incident *Incident, err error) {
	state.mtx.Lock()
	defer state.mtx.Unlock()

	if incident, ok := state.cache[name]; ok {
		if len(incident.path) == 0 {
			return nil, fmt.Errorf("Incident '%s' was removed", name)
		}

		return incident, nil
	}

	return nil, fmt.Errorf("Incident '%s' is not found", name)
}

func (state *incidentsState) remove(names []string) (paths []string, err error) {
	state.mtx.Lock()
	defer state.mtx.Unlock()

	for _, name := range names {
		if incident, ok := state.cache[name]; ok {
			paths = append(paths, incident.path)
			incident.path = ""
			delete(state.cache, name)
		} else {
			err = fmt.Errorf("Incident '%s' was not found", name)
			return
		}
	}

	return
}

func (state *incidentsState) Remove(names ...string) (err error) {
	paths, err := state.remove(names)

	for _, path := range paths {
		if !strings.HasPrefix(path, incidentDir) {
			err = fmt.Errorf("Incident has invalid path %s, this is unexpected", path)
			continue
		}
		os.RemoveAll(path)
	}
	return
}

func (state *incidentsState) New(other *Incident) (incident *Incident, err error) {
	incident = new(Incident)

	// Pick incident subdir (by the time) and create it
	timeStr := time.Now().Format(time.RFC3339)
	for suffix := 0; suffix < 10; suffix++ {
		name := timeStr
		if suffix > 0 {
			name = fmt.Sprintf("%s.%d", timeStr, suffix)
		}
		path := filepath.Join(incidentDir, name)

		err := os.Mkdir(path, 0700)
		if err == nil {
			incident.Name = name
			incident.path = path
			break
		}
		if os.IsExist(err) {
			continue
		}

		return nil, fmt.Errorf("Cannot create incident dir: %v", err)
	}
	if len(incident.path) == 0 {
		return nil, fmt.Errorf("Cannot pick incident dir")
	}

	// default-initialize it or copy
	incident.CreatedAt = time.Now()

	if other.IsZero() {
		incident.Tick = defaultIncidentTick
		err = incident.save()
	} else {
		err = incident.Merge(other)
	}

	if err == nil {
		state.add(incident)
	}
	return
}

func (incident *Incident) IsZero() bool {
	// We can't send nils over rpc, so we should be able to send
	// empty incident
	return len(incident.Name) == 0
}

func (incident *Incident) Merge(other *Incident) error {
	incident.Description = other.Description
	incident.Tick = other.Tick

	// TODO: merge providers list

	return incident.save()
}

// Loads incident from incident.json
func (incident *Incident) load() error {
	path := filepath.Join(incident.path, "incident.json")
	f, err := os.Open(path)
	if err != nil {
		return err
	}

	return json.NewDecoder(f).Decode(incident)
}

// Saves current incident configuration
func (incident *Incident) save() error {
	path := filepath.Join(incident.path, "incident.json")
	f, err := os.Create(path)
	if err != nil {
		return err
	}

	encoder := json.NewEncoder(f)
	encoder.SetIndent("  ", "  ")
	return encoder.Encode(incident)
}

// Returns incident state based on variables set
func (incident *Incident) GetState() IncState {
	switch {
	case !incident.StoppedAt.IsZero():
		return IncStopped
	case !incident.StartedAt.IsZero():
		return IncRunning
	}
	return IncCreated
}

func (incident *Incident) Start() error {
	if incident.GetState() != IncCreated {
		return fmt.Errorf("Incident already running or completed, cannot start")
	}

	tsf, err := os.Create(filepath.Join(incident.path, "trace.tsf"))
	if err != nil {
		return fmt.Errorf("Cannot create trace file: %v", err)
	}

	handle := new(IncidentHandle)
	handle.Trace, err = tsfile.NewTSFile(tsf, 2)
	if err != nil {
		return fmt.Errorf("Cannot create trace TS file: %v", err)
	}

	// TODO: open log
	handle.incident = incident
	go handle.run()

	return nil
}

func (incident *Incident) Stop() error {
	if incident.GetState() != IncRunning {
		return fmt.Errorf("Incident is not running, cannot stop")
	}

	// Mark all providers as stopped. run() will be interrupted
	// automatically
	for provIndex, _ := range incident.Providers {
		prov := &incident.Providers[provIndex]
		if prov.StoppedAt.IsZero() {
			prov.StoppedAt = time.Now()
		}
	}
	return nil
}

func (handle *IncidentHandle) Close() {
	// Finalize all providers
	for provIndex, _ := range handle.incident.Providers {
		prov := &handle.incident.Providers[provIndex]
		if !prov.StartedAt.IsZero() && !prov.finalized {
			handle.Providers[provIndex].Finalize(handle)
			prov.finalized = true
		}
	}

	// TODO: log error
	handle.Trace.Close()

	// TODO: close log
}

func (handle *IncidentHandle) run() {
	incident := handle.incident

	incident.StartedAt = time.Now()
	incident.save()

	defer handle.Close()
	defer incident.doStop()

	for {
		// Handle all providers if no more provers exit
		var provCount int
		for provIndex, _ := range incident.Providers {
			prov := &incident.Providers[provIndex]
			provHandle := handle.Providers[provIndex]

			if prov.StartedAt.IsZero() {
				// TODO: Initialize provider?
				prov.StartedAt = time.Now()
				provHandle.Prepare(handle)
			}

			if prov.StoppedAt.IsZero() {
				provHandle.Collect(handle)
				provCount++
			} else if !prov.finalized {
				provHandle.Finalize(handle)
				prov.finalized = true
			}
		}

		if provCount == 0 {
			return
		}

		// Save incident properties (if providers were reinitialized)
		incident.save()

		// Sleep until next tick
		handle.Tick++
		nextTime := incident.StartedAt.Add(time.Duration(handle.Tick*incident.Tick) * time.Millisecond)
		time.Sleep(nextTime.Sub(time.Now()))
	}
}

func (incident *Incident) doStop() {
	incident.StoppedAt = time.Now()
	incident.save()
}
