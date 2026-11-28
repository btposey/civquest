package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// cq-3ru: a started cabin is the one worked on; no other is begun, whatever the settlement.
func TestSiteCabin_StartedCabinIsFinishedBeforeAnother(t *testing.T) {
	st := NewAgentState()
	st.SetCollectiveSettlementSite(0.5, 64, 0.5)
	lw := &fsmWorld{ctx: &FSMContext{AgentID: "a", State: st, Frame: rf(100, 1000)}}
	started := &civquestv1.BlockPosProto{X: 40, Y: 64, Z: 40}
	l := shelterLocal{BuildIndex: 30, Pos: started}
	if _, v := lw.siteCabin(&l); v != siteBuild || l.Pos != started || l.BuildIndex != 30 {
		t.Fatalf("verdict %v local %+v, want the started cabin kept", v, l)
	}
	// Too far to go back to, and no settlement near: nothing is started here.
	l = shelterLocal{BuildIndex: 30, Pos: &civquestv1.BlockPosProto{X: 900, Y: 64, Z: 0}}
	lw.ctx.Frame.Status.Position = &civquestv1.Vec3{X: 500, Y: 64, Z: 0}
	if _, v := lw.siteCabin(&l); v != siteNone || l.BuildIndex != 30 {
		t.Fatalf("verdict %v local %+v, want no cabin work and the progress kept", v, l)
	}
}
