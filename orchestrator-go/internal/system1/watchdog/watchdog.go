// Package watchdog carries the firings of System 1's report-only watchdog (ADR-041 section 4)
// to whoever watches the whole process: the test harnesses, which fail on them. Nothing here
// feeds back into what an agent does.
package watchdog

import (
	"fmt"
	"sync"
)

// Condition is what the watchdog saw.
type Condition string

const (
	// Refused: an activity that for long only asks for walks that are withheld.
	Refused Condition = "refused"
	// NoProgress: daylight passes with the agent neither moving nor doing anything the game
	// confirms.
	NoProgress Condition = "no_progress"
	// Thrash: work pushed over other work falls straight through, or keeps ending almost as
	// soon as it begins.
	Thrash Condition = "thrash"
)

// Firing is one report. A condition that lasts is reported again at intervals.
type Firing struct {
	// Source is the agent state machine that fired, to tell one agent from another.
	Source     any
	Agent      string
	Tick       int64
	Condition  Condition
	Activity   string
	Reason     string
	Attempting string
}

func (f Firing) String() string {
	return fmt.Sprintf("agent %q tick %d: %s on %s (%s); attempting: %s",
		f.Agent, f.Tick, f.Condition, f.Activity, f.Reason, f.Attempting)
}

var (
	mu        sync.RWMutex
	observers []*func(Firing)
)

// Observe calls fn for every firing in the process until the returned function is called. fn
// runs inside the agent's tick: it must be quick and must not call back into the agent.
func Observe(fn func(Firing)) (stop func()) {
	mu.Lock()
	defer mu.Unlock()
	p := &fn
	observers = append(observers, p)
	return func() {
		mu.Lock()
		defer mu.Unlock()
		for i, o := range observers {
			if o == p {
				observers = append(observers[:i:i], observers[i+1:]...)
				return
			}
		}
	}
}

// Notify hands f to every observer.
func Notify(f Firing) {
	mu.RLock()
	now := observers
	mu.RUnlock()
	for _, o := range now {
		(*o)(f)
	}
}
