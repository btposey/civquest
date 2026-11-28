package system1

// Stack measurements (ADR-040 stage 2). With CIVQUEST_STACK_METRICS naming a file, every agent
// appends what its activity stack did, so any test run or live run can be scored with
// scripts/stack-metrics.py. Without it this does nothing.

import (
	"encoding/json"
	"os"
	"strings"
	"sync"

	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
)

// stackMetricsEnv names the file measurements are appended to.
const stackMetricsEnv = "CIVQUEST_STACK_METRICS"

// meterFlushTicks bounds how long ticks without stack changes stay unwritten.
const meterFlushTicks = 25

var (
	meterOnce sync.Once
	meterFile *os.File
)

func meterOutput() *os.File {
	meterOnce.Do(func() {
		if path := os.Getenv(stackMetricsEnv); path != "" {
			// Several test processes append to one file; each record is one write.
			meterFile, _ = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
		}
	})
	return meterFile
}

// stackRecord is one line of measurements since the previous one.
type stackRecord struct {
	Ticks      int      `json:"ticks,omitempty"`
	Push       []string `json:"push,omitempty"`
	Idle       []string `json:"idle,omitempty"`       // popped having done nothing
	Violations []string `json:"violations,omitempty"` // "reason|under>kind"
	Depth      int      `json:"depth,omitempty"`      // deepest stack seen
	GivenUp    int      `json:"given_up,omitempty"`   // walks stopped for going nowhere
	Watchdog   []string `json:"watchdog,omitempty"`   // "condition|activity"
}

type stackMeter struct{ pending stackRecord }

// walkGivenUp counts a walk the walking policy stopped.
func (m *stackMeter) walkGivenUp() { m.pending.GivenUp++ }

// watchdog counts a report of a stuck agent. It is written at once: a stuck agent may never
// change its stack again.
func (m *stackMeter) watchdog(key string) {
	m.pending.Watchdog = append(m.pending.Watchdog, key)
	m.flush()
}

// note folds one dispatch into the pending record.
func (m *stackMeter) note(tick bool, fx htpda.Effects) {
	out := meterOutput()
	if out == nil {
		return
	}
	if tick {
		m.pending.Ticks++
	}
	for _, e := range fx.Lifecycle {
		if e.Op == htpda.LifePush || e.Op == htpda.LifeReplace {
			m.pending.Push = append(m.pending.Push, string(e.Kind))
			m.pending.Depth = max(m.pending.Depth, e.Depth)
		}
		if e.Idle {
			m.pending.Idle = append(m.pending.Idle, string(e.Kind))
		}
	}
	for _, d := range fx.Diagnostics {
		if strings.HasPrefix(d.Reason, "shape:") {
			m.pending.Violations = append(m.pending.Violations, d.Reason+"|"+string(d.Under)+">"+string(d.Kind))
		}
	}
	p := m.pending
	if p.Push == nil && p.Idle == nil && p.Violations == nil && p.GivenUp == 0 && p.Ticks < meterFlushTicks {
		return
	}
	m.flush()
}

// flush writes and clears the pending record.
func (m *stackMeter) flush() {
	out := meterOutput()
	if out == nil {
		return
	}
	if line, err := json.Marshal(m.pending); err == nil {
		_, _ = out.Write(append(line, '\n'))
	}
	m.pending = stackRecord{}
}
