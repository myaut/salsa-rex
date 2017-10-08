package rexlib

import (
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
