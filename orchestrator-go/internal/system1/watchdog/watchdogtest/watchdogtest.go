// Package watchdogtest makes every test fail on a watchdog firing (ADR-041 section 4). A
// package whose tests drive agents calls Main from TestMain, and nothing else is needed. A test
// that leaves an agent stuck on purpose, or that shows a fault already filed, says so with
// Expect, which then requires the firing.
package watchdogtest

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/system1/watchdog"
)

type expectation struct {
	source    any
	condition watchdog.Condition
	activity  string
}

func (e expectation) matches(f watchdog.Firing) bool {
	return e.source == f.Source && e.condition == f.Condition && e.activity == f.Activity
}

var (
	mu       sync.Mutex
	expected = map[*expectation]bool{} // the standing expectations
)

// covered reports whether a standing expectation takes f.
func covered(f watchdog.Firing) bool {
	mu.Lock()
	defer mu.Unlock()
	for e := range expected {
		if e.matches(f) {
			return true
		}
	}
	return false
}

// collector gathers the firings no test expected.
type collector struct {
	mu    sync.Mutex
	order []string
	first map[string]string
	count map[string]int
}

func (c *collector) observe(f watchdog.Firing) {
	if covered(f) {
		return
	}
	key := fmt.Sprintf("%s: agent %q: %s on %s", testName(), f.Agent, f.Condition, f.Activity)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.count == nil {
		c.count, c.first = map[string]int{}, map[string]string{}
	}
	if c.count[key] == 0 {
		c.order = append(c.order, key)
		c.first[key] = f.String()
	}
	c.count[key]++
}

func (c *collector) report() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.order) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("--- FAIL: watchdog fired where no test expected it (an agent was stuck; ADR-041 section 4)\n")
	for _, key := range c.order {
		fmt.Fprintf(&b, "    %s (%d times)\n        first: %s\n", key, c.count[key], c.first[key])
	}
	return b.String()
}

// testName is the test function on the calling goroutine's stack.
func testName() string {
	pcs := make([]uintptr, 256)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(3, pcs)])
	name := "(test not on the stack)"
	for {
		f, more := frames.Next()
		if strings.HasSuffix(f.File, "_test.go") {
			if fn := f.Function[strings.LastIndex(f.Function, "/")+1:]; strings.Contains(fn, ".Test") {
				name = fn
			}
		}
		if !more {
			return name
		}
	}
}

// Main runs a package's tests, and fails them if the watchdog fired unexpectedly. Call it from
// TestMain.
func Main(m *testing.M) {
	var c collector
	stop := watchdog.Observe(c.observe)
	code := m.Run()
	stop()
	if r := c.report(); r != "" {
		fmt.Fprint(os.Stdout, r)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

// Expect declares that the agent run by source (its *system1.AgentFSM) will make the watchdog
// report condition on activity, because of why: the test leaves it stuck on purpose, or a bead
// names the fault. Declare it before the agent runs. The test fails if the firing does not
// come, so an expectation cannot outlive its fault. The returned function says how often it
// has come so far.
func Expect(t testing.TB, source any, condition watchdog.Condition, activity, why string) (seen func() int) {
	t.Helper()
	e := &expectation{source: source, condition: condition, activity: activity}
	var n atomic.Int64
	stop := watchdog.Observe(func(f watchdog.Firing) {
		if e.matches(f) {
			n.Add(1)
		}
	})
	mu.Lock()
	expected[e] = true
	mu.Unlock()
	seen = func() int { return int(n.Load()) }
	t.Cleanup(func() {
		stop()
		mu.Lock()
		delete(expected, e)
		mu.Unlock()
		if seen() == 0 {
			t.Errorf("expected the watchdog to report %s on %s, and it did not (%s); if the fault is fixed, remove the expectation",
				condition, activity, why)
		}
	})
	return seen
}
