package system1

import (
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// consecrateSettlement records a verified settlement at the origin.
func consecrateSettlement(st *AgentState) {
	st.SpatialIndex().UpsertPOI(db.AgentPOI{POIID: "test-hearth", Category: POICategorySettlement, X: 0.5, Y: 64, Z: 0.5, Reliability: 1})
}

// cq-6l3.12: no professions before a settlement; every agent is an explorer until then.
func TestProfession_ExplorerUntilSettlementEstablished(t *testing.T) {
	for _, prof := range []string{"FARMER", "GUARD", "BUILDER", "UNASSIGNED", ""} {
		st := NewAgentState()
		cfg := xdg.DefaultAgentIdentityConfig("settler")
		cfg.Profession = prof
		if got := EffectiveIdentity(st, cfg).Profession; got != "EXPLORER" {
			t.Errorf("%q before settlement: %q", prof, got)
		}
		if cfg.Profession != prof {
			t.Errorf("%q: the configured identity was modified to %q", prof, cfg.Profession)
		}
		consecrateSettlement(st)
		if got := EffectiveIdentity(st, cfg).Profession; got != prof {
			t.Errorf("%q after settlement: %q", prof, got)
		}
	}
}

// The FSM decides with the effective identity and hands the caller's one back.
func TestProfession_FSMTreatsUnsettledGuardAsExplorer(t *testing.T) {
	cfg := xdg.DefaultAgentIdentityConfig("unsettled_guard")
	cfg.Profession = "GUARD"
	run := func(st *AgentState) (bool, string) {
		fsm := NewAgentFSM(StageSettlement, nil)
		ctx := &FSMContext{AgentID: "unsettled_guard", State: st, Config: cfg,
			Frame: makeArbiterTestFrame(100, 3000, 20, fullSurvivalKitInv(), nil)}
		fsm.Tick(ctx)
		if ctx.Config != cfg {
			t.Fatal("Tick must restore the caller's identity")
		}
		return ctx.Facts.GetBool("patrol.active"), cfg.Profession
	}
	if patrol, prof := run(NewAgentState()); patrol || prof != "GUARD" {
		t.Errorf("before settlement a guard must not be on patrol: patrol=%t configured=%q", patrol, prof)
	}
	settled := NewAgentState()
	consecrateSettlement(settled)
	if patrol, _ := run(settled); !patrol {
		t.Error("after settlement the guard patrols")
	}
}
