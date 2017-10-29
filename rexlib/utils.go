package rexlib

import (
	"fmt"

	"os"
	"path/filepath"

	"encoding/json"

	"time"
)

type closerWatchdog struct {
	delay  time.Duration
	closer chan struct{}
}

func newCloserWatchdog(delay time.Duration) *closerWatchdog {
	return &closerWatchdog{
		delay:  delay,
		closer: make(chan struct{}, 1),
	}
}

func (wd *closerWatchdog) Notify() {
	wd.closer <- struct{}{}
}

func (wd *closerWatchdog) Wait() {
	ticker := time.NewTicker(wd.delay)
	defer ticker.Stop()

loop:
	for {
		// Automatically close opened handle after some time of inactivity
		// If there is activity, we recieve a message over closer,
		// and reset the timer
		select {
		case <-ticker.C:
			break loop
		case <-wd.closer:
			ticker = time.NewTicker(wd.delay)
		}
	}
}

func Shutdown() {
	if IsMonitorMode() {
		DisconnectAll()
	}

	// TODO stop all incidents tracing
}

type subdirectory struct {
	path string
}

func (sd *subdirectory) create(basePath, baseName string, delimiter rune) (name string, err error) {
	if len(baseName) == 0 {
		baseName = time.Now().Format(time.RFC3339)
	}

	for suffix := 0; suffix < 10; suffix++ {
		name = baseName
		if suffix > 0 {
			name = fmt.Sprintf("%s%c%d", baseName, delimiter, suffix)
		}
		sd.path = filepath.Join(basePath, name)

		err = os.Mkdir(sd.path, 0700)
		if err == nil {
			return
		}
		if os.IsExist(err) {
			continue
		}

		return "", err
	}

	return "", fmt.Errorf("Cannot pick directory name")
}

func (sd *subdirectory) saveJSONFile(obj interface{}, fileName string) (err error) {
	tempFileName := fmt.Sprintln("%s.tmp", fileName)

	tempPath := filepath.Join(sd.path, tempFileName)
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
		err = os.Rename(tempPath, filepath.Join(sd.path, fileName))
	}
	return
}

func (sd *subdirectory) loadJSONFile(obj interface{}, fileName string) error {
	path := filepath.Join(sd.path, fileName)
	f, err := os.Open(path)
	if err != nil {
		return err
	}

	return json.NewDecoder(f).Decode(obj)
}
