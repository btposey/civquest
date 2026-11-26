package agent

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// cq-yn1.4: a frame that never finishes (the bed-planner freeze) is reported with a
// goroutine dump, even though the stuck frame still holds its locks.
func TestStallMonitorReportsHungFrame(t *testing.T) {
	release := make(chan struct{})
	reports := make(chan StallReport, 2)
	r := &AgentRuntime{
		agentID: "stuck", stallAfter: 30 * time.Millisecond, stallDumpDir: t.TempDir(),
		onStall: func(rep StallReport) { reports <- rep },
		framePipeline: NewFramePipeline(FrameStage{Name: "hang", Run: func(r *AgentRuntime, _ *FrameContext) {
			r.mu.Lock()
			defer r.mu.Unlock()
			<-release
		}}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.watchStalls(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		end := r.beginFrame(42)
		defer end()
		r.framePipeline.Run(r, &FrameContext{Frame: &civquestv1.TelemetryFrame{WorldTick: 42}})
	}()

	select {
	case rep := <-reports:
		if rep.Tick != 42 || rep.Elapsed < 30*time.Millisecond {
			t.Errorf("report = %+v", rep)
		}
		dump, err := os.ReadFile(rep.DumpPath)
		if err != nil || !strings.Contains(string(dump), "TestStallMonitorReportsHungFrame") {
			t.Errorf("goroutine dump %q: err=%v", rep.DumpPath, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hung frame never reported")
	}
	close(release)
	<-done
	select {
	case rep := <-reports:
		t.Errorf("reported twice: %+v", rep)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestStallMonitorIgnoresFinishedFrames(t *testing.T) {
	reports := make(chan StallReport, 1)
	r := &AgentRuntime{agentID: "fine", stallAfter: 50 * time.Millisecond, onStall: func(rep StallReport) { reports <- rep }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.watchStalls(ctx)
	for i := 0; i < 20; i++ {
		end := r.beginFrame(int64(i))
		time.Sleep(10 * time.Millisecond)
		end()
	}
	select {
	case rep := <-reports:
		t.Errorf("healthy frames reported as stalled: %+v", rep)
	default:
	}
}
