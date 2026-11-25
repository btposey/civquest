package system1

import (
	"fmt"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
)

func newSafeNodeState(n int) *AgentState {
	st := NewAgentState()
	nodes := make([]db.MapNode, n)
	for i := range nodes {
		nodes[i] = db.MapNode{ID: fmt.Sprintf("n%d", i), SafetyScore: 0.9}
	}
	st.SetSafeNodes(nodes)
	return st
}

// SafeNodes is read on every telemetry frame; it must not allocate or copy
// proportionally to the number of cached nodes (cq-aik.29).
func TestSafeNodes_ReadIsAllocationFree(t *testing.T) {
	st := newSafeNodeState(2000)
	allocs := testing.AllocsPerRun(200, func() { _ = st.SafeNodes() })
	if allocs != 0 {
		t.Fatalf("SafeNodes allocated %.1f times per call; want 0", allocs)
	}
}

// Published snapshots are immutable: writers are copy-on-write.
func TestSafeNodes_SnapshotImmutableAcrossUpserts(t *testing.T) {
	st := newSafeNodeState(3)
	snap := st.SafeNodes()
	st.UpsertSafeNode(db.MapNode{ID: "n1", SafetyScore: 0.1})
	st.UpsertSafeNode(db.MapNode{ID: "new", SafetyScore: 0.8})
	if len(snap) != 3 || snap[1].SafetyScore != 0.9 {
		t.Fatalf("snapshot mutated: %+v", snap)
	}
	_ = append(snap, db.MapNode{ID: "x"}) // must not corrupt cache
	cur := st.SafeNodes()
	if len(cur) != 4 || cur[1].SafetyScore != 0.1 || cur[3].ID != "new" {
		t.Fatalf("unexpected cache: %+v", cur)
	}
}

func BenchmarkSafeNodes_2000(b *testing.B) {
	st := newSafeNodeState(2000)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = st.SafeNodes()
	}
}
