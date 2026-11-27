package system1

import (
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// consecrateSettlement makes the agent an established member of a settlement at the origin.
func consecrateSettlement(st *AgentState) {
	st.SetCollectiveSettlementSite(0.5, 64, 0.5)
	st.AssignStrategy(StrategyProfession, 0)
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

// cq-6l3.14: a campfire or bed on its own is not a settlement.
func TestProfession_HearthIsNotASettlement(t *testing.T) {
	st := NewAgentState()
	for _, block := range []string{"minecraft:campfire", "minecraft:red_bed"} {
		st.SpatialIndex().UpsertPOI(db.AgentPOI{POIID: block, Category: POICategoryHabitation, BlockID: block, X: 0.5, Y: 64, Z: 0.5, Reliability: 1, IsActive: true})
	}
	cfg := xdg.DefaultAgentIdentityConfig("camper")
	cfg.Profession = "FARMER"
	if st.SettlementEstablished() || EffectiveIdentity(st, cfg).Profession != "EXPLORER" {
		t.Error("a hearth alone ended the explorer phase")
	}
}

// cq-6l3.14: a lone agent that claims a homestead becomes a homesteader.
func TestProfession_LoneAgentBecomesHomesteader(t *testing.T) {
	for _, prof := range []string{"FARMER", "EXPLORER"} {
		st := NewAgentState()
		cfg := xdg.DefaultAgentIdentityConfig("loner")
		cfg.Profession = prof
		st.applyHomesteadClaim([3]float64{10, 64, 10})
		if got := EffectiveIdentity(st, cfg).Profession; got != "HOMESTEADER" {
			t.Errorf("%s with a homestead: %q", prof, got)
		}
		if cfg.Profession != prof {
			t.Errorf("configured identity modified to %q", cfg.Profession)
		}
		restored := NewAgentState()
		was, ok := st.Settlement()
		if !ok {
			t.Fatal("no settlement recorded")
		}
		restored.RestoreSettlement(was)
		if got, _ := restored.Settlement(); got != was {
			t.Errorf("restored %+v, want %+v", got, was)
		}
	}
}
