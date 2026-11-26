package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// A frame normally takes milliseconds. One that runs this long means the agent can no longer
// move, speak or react, although its process looks healthy to the supervisor (cq-yn1.4).
const (
	DefaultStallAfter = 10 * time.Second
	maxStackDump      = 8 << 20
)

// StallReport describes a telemetry frame that did not finish.
type StallReport struct {
	AgentID  string
	Tick     int64
	Elapsed  time.Duration
	DumpPath string // goroutine dump, "" when it could not be written
}

type frameInFlight struct {
	tick  int64
	since time.Time
}

// beginFrame marks a frame as processing; the returned func marks it finished.
func (r *AgentRuntime) beginFrame(tick int64) func() {
	r.inFlight.Store(&frameInFlight{tick: tick, since: time.Now()})
	return func() { r.inFlight.Store(nil) }
}

// watchStalls reports the first frame that runs longer than the configured limit. It touches
// only atomics and the debug logger, because the stuck frame may hold any runtime lock.
func (r *AgentRuntime) watchStalls(ctx context.Context) {
	limit := r.stallAfter
	if limit <= 0 {
		limit = DefaultStallAfter
	}
	poll := time.NewTicker(max(limit/10, time.Millisecond))
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
		}
		f := r.inFlight.Load()
		if f == nil || time.Since(f.since) < limit {
			continue
		}
		r.reportStall(StallReport{AgentID: r.agentID, Tick: f.tick, Elapsed: time.Since(f.since)})
		return
	}
}

func (r *AgentRuntime) reportStall(rep StallReport) {
	buf := make([]byte, maxStackDump)
	buf = buf[:runtime.Stack(buf, true)]
	if r.stallDumpDir != "" {
		name := fmt.Sprintf("agent-%s-stall-%s.txt", filepath.Base(rep.AgentID), time.Now().UTC().Format("20060102T150405.000"))
		path := filepath.Join(r.stallDumpDir, name)
		if err := os.WriteFile(path, buf, 0o640); err == nil {
			rep.DumpPath = path
		}
	}
	log.Printf("[civquest-agent] %s BRAIN STALL: frame at tick %d still running after %v (goroutine dump: %s)",
		rep.AgentID, rep.Tick, rep.Elapsed.Round(time.Millisecond), rep.DumpPath)
	if r.debugLogger != nil {
		r.debugLogger.Emit(rep.Tick, "BRAIN_STALL", "Telemetry frame did not finish", map[string]any{
			"elapsed_ms": rep.Elapsed.Milliseconds(),
			"dump_path":  rep.DumpPath,
		})
	}
	if r.onStall != nil {
		r.onStall(rep)
	}
}
