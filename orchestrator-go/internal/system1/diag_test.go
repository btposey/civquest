package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// The logs must be able to explain a death (cq-49c): what hurt, what the agent stood in and
// carried, who was near, what each batch came to and what became of its work.
func TestDiagnostics_ReportDamageItemsThreatsReceiptsAndTasks(t *testing.T) {
	var evs []capturedEvent
	standing := func(f *civquestv1.TelemetryFrame) {
		f.Status.FeetBlock, f.Status.HeadBlock, f.Status.FrozenTicks = "minecraft:powder_snow", "minecraft:powder_snow", 140
		f.BiomeId = "minecraft:grove"
	}
	hurt := func(f *civquestv1.TelemetryFrame) {
		f.LastDamageTaken = &civquestv1.DamageEvent{DamageType: "freeze", DamageAmount: 1}
	}
	runReplay(t, replayScenario{
		Name: "diagnostics", Stage: StageBootstrap, Profession: "EXPLORER", Sink: captureSink(&evs),
		Steps: []replayStep{
			{Frame: rf(100, 3000, withHP(20), withBlocks(treeColumn...))},
			{Frame: rf(110, 3000, withHP(19), withBlocks(treeColumn...), standing, hurt,
				withInv(item(0, "minecraft:oak_log", 2)), withEntities(creeperAt(9, 6, 0, 6))), Receipt: terrainStuck(1)},
		},
	})
	byCategory := map[string]capturedEvent{}
	for _, e := range evs {
		byCategory[e.Category] = e
	}
	for _, c := range []string{EventDamage, EventInventory, EventThreats, EventReceiptFailed, EventTaskStart} {
		if _, ok := byCategory[c]; !ok {
			t.Errorf("no %s event in %d events", c, len(evs))
		}
	}
	d := byCategory[EventDamage].Details
	if d["damage_type"] != "freeze" || d["feet_block"] != "minecraft:powder_snow" || d["frozen_ticks"] != int32(140) {
		t.Errorf("damage details = %+v", d)
	}
	if got := byCategory[EventVitals].Details["feet_block"]; got == nil {
		t.Errorf("vitals carry no feet block")
	}
}

// Notes taken while nobody is listening stay bounded.
func TestDiagnostics_Bounded(t *testing.T) {
	s := NewAgentState()
	for range maxDiagnostics * 2 {
		s.noteDiag(EventTaskStart, "x", nil)
	}
	if n := len(s.drainDiags()); n != maxDiagnostics {
		t.Fatalf("kept %d notes, want %d", n, maxDiagnostics)
	}
}
