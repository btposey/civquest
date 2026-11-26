package agent

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

func TestAnnotateNavigateEventAddsTarget(t *testing.T) {
	r := &AgentRuntime{}
	d := map[string]any{}
	r.annotateNavigateEvent(d, &civquestv1.AgentIntent{Action: &civquestv1.AgentIntent_Navigate{
		Navigate: &civquestv1.NavigateIntent{Target: &civquestv1.Vec3{X: 1, Y: 64, Z: -3}}}})
	got, ok := d["target"].([]float64)
	if !ok || len(got) != 3 || got[0] != 1 || got[1] != 64 || got[2] != -3 {
		t.Fatalf("target = %v", d["target"])
	}
	d2 := map[string]any{}
	r.annotateNavigateEvent(d2, &civquestv1.AgentIntent{})
	if len(d2) != 0 {
		t.Fatalf("non-navigate intent annotated: %v", d2)
	}
}
