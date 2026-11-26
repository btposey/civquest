package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
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

// frameMonitor holds what is measured about telemetry frame processing.
type frameMonitor struct {
	latencyMicros atomic.Int64
	inFlight      atomic.Pointer[frameInFlight]
	stallAfter    time.Duration
	dumpDir       string
	onStall       func(StallReport)
}

type frameInFlight struct {
	tick  int64
	since time.Time
}

// SetFramePipeline replaces the telemetry pipeline. Call it before Start; it exists so tests
// can inject a stage (for example one that never returns).
func (r *AgentRuntime) SetFramePipeline(p *FramePipeline) { r.framePipeline = p }

// beginFrame marks a frame as processing; the returned func marks it finished.
func (r *AgentRuntime) beginFrame(tick int64) func() {
	r.frames.inFlight.Store(&frameInFlight{tick: tick, since: time.Now()})
	return func() { r.frames.inFlight.Store(nil) }
}

// watchStalls reports the first frame that runs longer than the configured limit. It touches
// only atomics and the debug logger, because the stuck frame may hold any runtime lock.
func (r *AgentRuntime) watchStalls(ctx context.Context) {
	limit := r.frames.stallAfter
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
		f := r.frames.inFlight.Load()
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
	if r.frames.dumpDir != "" {
		name := fmt.Sprintf("agent-%s-stall-%s.txt", filepath.Base(rep.AgentID), time.Now().UTC().Format("20060102T150405.000"))
		path := filepath.Join(r.frames.dumpDir, name)
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
	if r.frames.onStall != nil {
		r.frames.onStall(rep)
	}
}
