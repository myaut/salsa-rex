package rexlib

import (
	"io/ioutil"
	"os"
	"path/filepath"

	"runtime"
	"sync"

	"fmt"
	"log"

	"strings"

	"yatima"
)

//
// Training session is similar to logic with incidents and implement trial-and-
// error logic with Yatima. Training is hierarchial:
// 	- On first stage only one incident is used to seed good networks and
//	  get rid of bad networks
//	- On the later stage, multiple incidents are used to synthesize more complex
//	  networks
//

type TrainingNetworkResult struct {
	Signature  string `json:"signature"`
	Ratio      int64  `json:"ratio"`
	ModelError int64  `json:"error"`
	Error      string `json:"err,omitempty"`
}

type TrainingSession struct {
	Name string `json:"name"`
	subdirectory

	// Name of incidents and sessions on which this learning session is based
	Incidents []string `json:"incidents"`
	Sessions  []string `json:"sessions"`

	Started bool                    `json:"started"`
	Results []TrainingNetworkResult `json:"results"`

	Trace bool `json:"trace"`
}

type trainingState struct {
	mtx sync.Mutex

	sessions map[string]*TrainingSession
	path     string

	templates *yatima.Library

	modelChan chan *trainingModelHandle
}

var Training trainingState

func InitializeYatima(templatesPath, trainingPath string) error {
	lib, err := yatima.LoadLibraryFromPath(templatesPath)
	if err != nil {
		return err
	}
	Training.templates = lib

	if _, err := os.Stat(trainingPath); os.IsNotExist(err) {
		err = os.Mkdir(trainingPath, incidentDirectoryPermissions)
		if err != nil {
			return err
		}
	}
	Training.path = trainingPath

	// spawn training goroutines and create their channel
	Training.modelChan = make(chan *trainingModelHandle, runtime.NumCPU()*4)
	for i := 0; i < runtime.NumCPU()-1; i++ {
		go Training.trainLoop()
	}

	return Training.load()
}

func (state *trainingState) load() error {
	state.sessions = make(map[string]*TrainingSession)

	// Walk over data dir and load incidents
	dirs, err := ioutil.ReadDir(Training.path)
	if err != nil {
		return err
	}

	for _, fi := range dirs {
		if fi.IsDir() {
			session := new(TrainingSession)
			session.Name = fi.Name()
			session.path = filepath.Join(Training.path, session.Name)

			err := session.loadJSONFile(session, "session.json")
			if err != nil {
				continue
			}

			state.sessions[session.Name] = session
		}
	}

	return nil
}

func (state *trainingState) Get(name string) (*TrainingSession, bool) {
	state.mtx.Lock()
	defer state.mtx.Unlock()

	if session, ok := state.sessions[name]; ok {
		if len(session.path) == 0 {
			return nil, false
		}

		return session, true
	}

	return nil, false
}

func (state *trainingState) List() (sessions []string) {
	state.mtx.Lock()
	defer state.mtx.Unlock()

	for name := range state.sessions {
		sessions = append(sessions, name)
	}
	return sessions
}

func (state *trainingState) remove(names []string) (paths []string, err error) {
	state.mtx.Lock()
	defer state.mtx.Unlock()

	for _, name := range names {
		if session, ok := state.sessions[name]; ok {
			paths = append(paths, session.path)
			session.path = ""
			delete(state.sessions, name)
		} else {
			err = fmt.Errorf("Session '%s' was not found", name)
			return
		}
	}

	return
}

func (state *trainingState) Remove(names ...string) (err error) {
	paths, err := state.remove(names)

	for _, path := range paths {
		if !strings.HasPrefix(path, state.path) {
			err = fmt.Errorf("Session has invalid path %s", path)
			continue
		}
		os.RemoveAll(path)
	}
	return
}

func (state *trainingState) Run(session *TrainingSession) (err error) {
	if len(session.Incidents) == 0 {
		return fmt.Errorf("Cannot run a training session without input data")
	}

	handle := &trainingHandle{
		incidents: make([]*Incident, 0),
		parents:   make([]*TrainingSession, 0),
		session:   session,

		resultsChan: make(chan TrainingNetworkResult, 32),
	}

	for _, name := range session.Sessions {
		session, ok := state.Get(name)
		if !ok {
			return fmt.Errorf("Training session '%s' is not found")
		}
		handle.parents = append(handle.parents, session)
	}

	for _, name := range session.Incidents {
		incident, err := Incidents.Get(name)
		if err != nil {
			return err
		}
		if len(session.Name) == 0 && len(session.Incidents) == 1 {
			session.Name = name
		}
		handle.incidents = append(handle.incidents, incident)
	}

	session.create(Training.path, session.Name, '_')

	handle.logFile, err = os.OpenFile(filepath.Join(session.path, "training.log"),
		os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("Cannot create session log: %v", err)
	}
	handle.log = log.New(handle.logFile, "", log.Ltime|log.Lmicroseconds)

	err = state.start(session)
	if err != nil {
		return err
	}

	go handle.run()
	return nil
}

func (state *trainingState) start(session *TrainingSession) error {
	state.mtx.Lock()
	defer state.mtx.Unlock()

	if _, ok := state.sessions[session.Name]; session.Started || ok {
		return fmt.Errorf("Training session is already started")
	}

	state.sessions[session.Name] = session

	session.Started = true
	session.save()
	return nil
}

func (session *TrainingSession) save() error {
	return session.saveJSONFile(session, "session.json")
}
