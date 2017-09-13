package rexlib

import (
	"os"
	"time"

	"fmt"
	"log"

	"os/exec"

	"io/ioutil"
	"path/filepath"
	"strings"

	"sync"
	"sync/atomic"

	"encoding/json"

	"tsfile"

	"rexlib/provider"
	"rexlib/tsload"
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
	defaultIncidentTickInterval = 100
)

type IncState int

const (
	IncCreated IncState = iota
	IncRunning
	IncStopped
)

type IncidentHandle struct {
	incident *Incident

	providerOutput provider.OutputHandle

	tsfFile *os.File
	logFile *os.File

	tsExperiment *exec.Cmd
}

type IncidentProvider struct {
	StartedAt time.Time `json:"started_at"`
	StoppedAt time.Time `json:"stopped_at,omitempty"`
	finalized bool

	Name   string                      `json:"name"`
	Config provider.ConfigurationState `json:"config"`

	handle provider.Provider
}

type Incident struct {
	// Protects running provider
	mtx sync.Mutex

	// Incident's name and path to incident directory
	Name string `json:"name"`
	path string

	// Incident's scheduler ticks in milliseconds
	TickInterval int `json:"tick"`

	// Description of incident
	Description string `json:"descr,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	StartedAt time.Time `json:"started_at"`
	StoppedAt time.Time `json:"stopped_at,omitempty"`

	Providers []*IncidentProvider `json:"providers,omitempty"`

	Experiment *tsload.Experiment `json:"-"`
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

	Incidents.load()
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
	baseName := other.Name
	if len(baseName) == 0 {
		baseName = time.Now().Format(time.RFC3339)
	}

	for suffix := 0; suffix < 10; suffix++ {
		name := baseName
		if suffix > 0 {
			name = fmt.Sprintf("%s.%d", baseName, suffix)
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

	incident.TickInterval = defaultIncidentTickInterval
	err = incident.Merge(other)
	if err == nil {
		err = incident.save()
	}
	if err == nil {
		state.add(incident)
	}
	return
}

func (incident *Incident) Merge(other *Incident) error {
	if len(other.Description) > 0 {
		incident.Description = other.Description
	}

	if other.TickInterval > 0 {
		incident.TickInterval = other.TickInterval
	}

	if other.Experiment != nil {
		incident.Experiment = other.Experiment

		// Save experiment or fail. Experiment config is not changed during
		// incident run, so it is not called from save()
		err := incident.saveJSONFile(incident.Experiment, "experiment.json")
		if err != nil {
			return err
		}
	}

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
func (incident *Incident) saveJSONFile(obj interface{}, fileName string) (err error) {
	tempFileName := fmt.Sprintln("%s.tmp", fileName)

	tempPath := filepath.Join(incident.path, tempFileName)
	f, err := os.Create(tempPath)
	if err != nil {
		return
	}

	encoder := json.NewEncoder(f)
	encoder.SetIndent("  ", "  ")
	err = encoder.Encode(obj)
	if err == nil {
		err = f.Close()
	}
	if err == nil {
		err = os.Rename(tempPath, filepath.Join(incident.path, fileName))
	}
	return
}

func (incident *Incident) save() (err error) {
	return incident.saveJSONFile(incident, "incident.json")
}

// Returns incident state based on variables set
func (incident *Incident) GetState() IncState {
	incident.mtx.Lock()
	defer incident.mtx.Unlock()

	return incident.getStateNoLock()
}

func (incident *Incident) getStateNoLock() IncState {
	switch {
	case !incident.StoppedAt.IsZero():
		return IncStopped
	case !incident.StartedAt.IsZero():
		return IncRunning
	}
	return IncCreated
}

func (incident *Incident) Start() (err error) {
	incident.mtx.Lock()
	defer incident.mtx.Unlock()

	if incident.getStateNoLock() != IncCreated {
		return fmt.Errorf("Incident already running or completed, cannot start")
	}

	handle := new(IncidentHandle)
	handle.incident = incident

	handle.tsfFile, err = os.Create(filepath.Join(incident.path, "trace.tsf"))
	if err != nil {
		handle.Close()
		return fmt.Errorf("Cannot create trace file: %v", err)
	}

	handle.providerOutput.Trace, err = tsfile.NewTSFile(handle.tsfFile, 2)
	if err != nil {
		handle.Close()
		return fmt.Errorf("Cannot create trace TS file: %v", err)
	}

	handle.logFile, err = os.OpenFile(filepath.Join(incident.path, "incident.log"),
		os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		handle.Close()
		return fmt.Errorf("Cannot create incident log: %v", err)
	}

	handle.providerOutput.Log = log.New(handle.logFile, "", log.Ltime|log.Lmicroseconds)

	// If we have an experiment here, create a corresponding command
	handle.tsExperiment = tsload.CreateTSExperimentCommand(incident.path)

	// If we succeeded, run the incident main routine
	go handle.run()
	return
}

func (incident *Incident) Stop() error {
	incident.mtx.Lock()
	defer incident.mtx.Unlock()

	if incident.getStateNoLock() != IncRunning {
		return fmt.Errorf("Incident is not running, cannot stop")
	}

	// Mark all providers as stopped. run() will be interrupted automatically
	for provIndex, _ := range incident.Providers {
		prov := incident.Providers[provIndex]
		if prov.StoppedAt.IsZero() {
			prov.StoppedAt = time.Now()
		}
	}
	return nil
}

func (handle *IncidentHandle) Close() {
	handle.incident.mtx.Lock()
	defer handle.incident.mtx.Unlock()

	// Finalize all providers
	for provIndex, _ := range handle.incident.Providers {
		prov := handle.incident.Providers[provIndex]
		if !prov.StartedAt.IsZero() && !prov.finalized {
			prov.handle.Finalize(&handle.providerOutput)
			prov.finalized = true
		}
	}

	ilog := handle.providerOutput.Log
	if handle.providerOutput.Trace != nil {
		err := handle.providerOutput.Trace.Close()
		if err != nil {
			ilog.Printf("ERROR: Error while closing trace: %v", err)
		}
	}

	if handle.tsfFile != nil {
		err := handle.tsfFile.Close()
		if err != nil {
			ilog.Printf("ERROR: Error while closing tsfile: %v", err)
		}
	}

	if handle.tsExperiment != nil {
		if handle.tsExperiment.ProcessState == nil {
			err := handle.tsExperiment.Process.Kill()
			if err != nil {
				ilog.Printf("Failed to kill tsexperiment (pid: %d): %v",
					handle.tsExperiment.Process.Pid, err)
			}
		}
		ilog.Printf("Finished tsexperiment (pid: %d): %s",
			handle.tsExperiment.Process.Pid, handle.tsExperiment.ProcessState.String())
	}

	handle.providerOutput.Log = nil
	handle.logFile.Close()
}

func (handle *IncidentHandle) run() {
	var err error
	incident := handle.incident
	ilog := handle.providerOutput.Log

	incident.StartedAt = time.Now()
	err = incident.save()
	if err != nil {
		ilog.Println(err)
	}

	defer handle.Close()
	defer incident.doStop()

	// Start tsload experiment in parallel with us
	if handle.tsExperiment != nil {
		err = handle.tsExperiment.Start()
		if err == nil {
			ilog.Printf("Started TSExperiment (pid: %d)", handle.tsExperiment.Process.Pid)
			go handle.waitTSExperiment()
		} else {
			ilog.Println(err)
			handle.tsExperiment = nil
		}
	}

	ticker := time.NewTicker(time.Duration(incident.TickInterval) * time.Millisecond)
	defer ticker.Stop()

	ilog.Println("Started incident provider loop")
	for handle.providerOutput.Now = range ticker.C {
		// Save incident properties (if providers were reinitialized)
		err = incident.save()
		if err != nil {
			ilog.Println(err)
		}

		// Handle all providers if no more provers exit
		if handle.runProviders() == 0 {
			return
		}
	}

	if handle.tsExperiment != nil {
		err = handle.tsExperiment.Wait()
		if err != nil {
			ilog.Println(err)
		}
	}
}

func (handle *IncidentHandle) runProviders() (provCount int) {
	incident := handle.incident
	incident.mtx.Lock()
	defer incident.mtx.Unlock()

	for provIndex, _ := range incident.Providers {
		prov := incident.Providers[provIndex]
		if atomic.LoadUint32(&prov.Config.Committed) != 1 {
			// Discard providers that are still configuring
			continue
		}

		var err error
		if prov.handle == nil {
			// This provider has not yet spawned corresponding handle,
			// time to configure it
			err = incident.initializeProvider(prov)
			if err != nil {
				goto badProvider
			}
		}

		if !prov.StoppedAt.IsZero() {
			// Discard providers that are already stopped
			if !prov.finalized {
				prov.handle.Finalize(&handle.providerOutput)
				prov.finalized = true
			}
			continue
		}

		// Start new providers
		if prov.StartedAt.IsZero() {
			prov.StartedAt = time.Now()
			err = prov.handle.Prepare(&handle.providerOutput)
			if err != nil {
				goto badProvider
			}
		}

		// Finally, gather some data
		// XXX: we're doing this while holding the lock, how about moving
		// this to separate loop?
		prov.handle.Collect(&handle.providerOutput)
		provCount++
		continue

	badProvider:
		handle.providerOutput.Log.Println("ERROR: Error in provider #%d: %v",
			provIndex, err)
		prov.StoppedAt = prov.StartedAt
		prov.finalized = true
	}
	return
}

// Wait for completion of TSExperiment process and stop it after
func (handle *IncidentHandle) waitTSExperiment() {
	handle.tsExperiment.Wait()
	handle.incident.Stop()
}

func (incident *Incident) doStop() {
	incident.StoppedAt = time.Now()
	incident.save()
}

func (incident *Incident) initializeProvider(prov *IncidentProvider) (err error) {
	// Re-initialize provider
	prov.handle = incident.providerFactory(prov.Name)
	if prov.handle == nil {
		return fmt.Errorf("Unknown provider '%s'", prov.Name)
	}

	return incident.configureProvider(provider.ConfigureSetValue,
		&prov.Config, prov)
}

func (incident *Incident) ConfigureProvider(action provider.ConfigurationAction,
	state *provider.ConfigurationState) (err error) {

	// Create or get already existing provider
	var prov *IncidentProvider
	if state.ProviderIndex < 0 {
		if action != provider.ConfigureSetValue {
			return fmt.Errorf("Provider is not exists")
		}

		prov, err = incident.createProvider(state)
	} else {
		prov, err = incident.getConfigurableProvider(state.ProviderIndex)
	}

	if err != nil {
		return
	}

	incident.configureProvider(action, state, prov)

	incident.save()
	return
}

func (incident *Incident) configureProvider(action provider.ConfigurationAction,
	state *provider.ConfigurationState, prov *IncidentProvider) (err error) {

	steps := state.Configuration
	if len(steps) > 1 {
		steps, err = prov.reorderSteps(steps)
		if err != nil {
			return
		}
	}

	// Now when steps are reordered, call ConfigureStep one at a time and
	// update state with list of available options
	for _, step := range steps {
		if action == provider.ConfigureSetValue && step == nil {
			return fmt.Errorf("Step was expected")
		}

		state.Configuration, err = prov.handle.Configure(action, step)
		if err != nil {
			break
		}
	}

	// Update local (serialized) state with new steps
	prov.Config.Configuration, err = prov.handle.Configure(
		provider.ConfigureGetValues, nil)

	atomic.StoreUint32(&prov.Config.Committed, state.Committed)
	return nil
}

func (incident *Incident) createProvider(state *provider.ConfigurationState) (*IncidentProvider, error) {
	// Pop first configuration parameter as provider name
	if len(state.Configuration) == 0 || len(state.Configuration[0].Values) != 1 {
		return nil, fmt.Errorf("Provider creation is requested, but no provider name given")
	}

	prov := new(IncidentProvider)

	// Create an implementation object or fail
	prov.Name = state.Configuration[0].Values[0]
	prov.handle = incident.providerFactory(prov.Name)
	if prov.handle == nil {
		return nil, fmt.Errorf("Unknown provider '%s'", prov.Name)
	}

	// Add provider and update state
	state.Configuration = state.Configuration[1:]

	incident.mtx.Lock()
	defer incident.mtx.Unlock()

	state.ProviderIndex = len(incident.Providers)
	incident.Providers = append(incident.Providers, prov)

	return prov, nil
}

func (incident *Incident) getConfigurableProvider(index int) (*IncidentProvider, error) {
	incident.mtx.Lock()
	defer incident.mtx.Unlock()

	if len(incident.Providers) <= index {
		return nil, fmt.Errorf("Invalid provider index %d was given", index)
	}

	prov := incident.Providers[index]
	if atomic.LoadUint32(&prov.Config.Committed) != 0 {
		return nil, fmt.Errorf("Can't configure provider that is already committed")
	}

	return prov, nil
}

func (prov *IncidentProvider) reorderSteps(steps []*provider.ConfigurationStep) (
	[]*provider.ConfigurationStep, error) {

	// Get correct order of steps
	guide, err := prov.handle.Configure(provider.ConfigureGetOptions, nil)
	if err != nil {
		return steps, err
	}

	// Reorder steps according to a guideline coming from provider. I.e. if user
	// gives us tid=2 pid=1, we might want to reorder it to pid=1 tid=2 because
	// provider wants us to set thread id after we set process id

	indices := make([]int, len(steps))
	for i, _ := range steps {
		indices[i] = -1
	}

	var j int
	for _, guideStep := range guide {
		for i, step := range steps {
			if guideStep.CompareStepName(step) {
				if indices[i] != -1 {
					return nil, fmt.Errorf("Invalid step %s:%s: it matches to more than one step",
						step.NameSpace, step.Name)
				}

				indices[i] = j
				j++
			}
		}
	}

	newSteps := make([]*provider.ConfigurationStep, len(steps))
	for i, j := range indices {
		if j == -1 {
			return steps, fmt.Errorf("Invalid step %s:%s: provider is not aware of it",
				steps[i].NameSpace, steps[i].Name)
		}

		newSteps[j] = steps[i]
	}

	return newSteps, nil
}
