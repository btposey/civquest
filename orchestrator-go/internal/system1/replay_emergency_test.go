package system1

import (
	"strings"
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// Replay scenarios for the emergency hold (cq-gpa.2) and held fight-vs-flee (cq-gpa.3).

func withWater(inWater, submerged bool, air int32) fo {
	return func(f *civquestv1.TelemetryFrame) {
		f.Status.IsInWater, f.Status.Submerged = inWater, submerged
		f.Status.AirSupply, f.Status.MaxAirSupply = air, 300
	}
}

func withBurning(on bool) fo { return func(f *civquestv1.TelemetryFrame) { f.Status.IsOnFire = on } }

func withDamage(src civquestv1.DamageSourceType, attacker int32) fo {
	return func(f *civquestv1.TelemetryFrame) {
		f.LastDamageTaken = &civquestv1.DamageEvent{SourceType: src, AttackerEntityId: attacker, DamageAmount: 1}
	}
}

func zombieAt(id int32, x, z float64, dist float32) *civquestv1.VisibleEntity {
	return &civquestv1.VisibleEntity{EntityId: id, EntityType: "minecraft:zombie", IsHostile: true, Distance: dist, Position: &civquestv1.Vec3{X: x, Y: 64, Z: z}}
}

func topIs(r replayRecord, kind string) bool { return strings.HasPrefix(r.Top, kind) }

// Drowning: damage lands once per 20 ticks; the frames between damage edges must stay in the
// reflex, and it only pops once out of the water with air restored.
func scenarioDrownHoldsUntilAirRestored() replayScenario {
	shore := blk(6, 64, 0, "minecraft:sand")
	water := blk(2, 63, 0, "minecraft:water")
	d := civquestv1.DamageSourceType_DAMAGE_SOURCE_TYPE_DROWN
	return replayScenario{
		Name: "drown_holds_until_air_restored", Stage: StageBootstrap, Profession: "EXPLORER",
		Steps: []replayStep{
			{Frame: rf(100, 3000, withBlocks(treeColumn...)), Annotation: "dry: harvesting"},
			{Frame: rf(110, 3000, withBlocks(treeColumn...), withBlocks(shore, water), withWater(true, true, 60), withDamage(d, 0)), Annotation: "submerged, air 20%, drown damage: emergency, swim to dry cell"},
			{Frame: rf(115, 3000, withBlocks(shore, water), withWater(true, true, 40)), Annotation: "no damage edge this frame: still held (old edge trigger popped here)"},
			{Frame: rf(130, 3000, withBlocks(shore, water), withWater(true, true, 20), withDamage(d, 0)), Annotation: "periodic damage: held"},
			{Frame: rf(145, 3000, withBlocks(shore, water), withWater(true, false, 120)), Annotation: "head above water but still in water: held"},
			{Frame: rf(155, 3000, withPos(6.5, 65, 0.5), withBlocks(shore, water), withWater(false, false, 180)), Annotation: "on shore, air 60% < 80%: hold, no motion"},
			{Frame: rf(170, 3000, withPos(6.5, 65, 0.5), withBlocks(treeColumn...), withWater(false, false, 270)), Annotation: "air restored: pop, routine resumes"},
		},
		Check: func(t *testing.T, tr []replayRecord) {
			for i := 1; i <= 5; i++ {
				if tr[i].Stage != StageEmergency {
					t.Fatalf("step %d must hold the emergency: %s", i, tr[i])
				}
			}
			if !hasIntentPrefix(tr[1], "Navigate(x=6.5") {
				t.Fatalf("expected swim to the dry shore cell: %s", tr[1])
			}
			if len(tr[5].Intents) != 0 {
				t.Fatalf("no motion while air regenerates on shore: %s", tr[5])
			}
			if tr[6].Stage == StageEmergency {
				t.Fatalf("expected resolution once air restored: %s", tr[6])
			}
		},
	}
}

// Burning: on_fire is a sustained fact, so the reflex holds through frames without damage
// and heads for water; it pops the first frame the agent is no longer burning.
func scenarioBurningHoldsUntilExtinguished() replayScenario {
	water := blk(4, 63, 0, "minecraft:water")
	return replayScenario{
		Name: "burning_holds_until_extinguished", Stage: StageBootstrap, Profession: "EXPLORER",
		Steps: []replayStep{
			{Frame: rf(100, 3000, withBlocks(treeColumn...)), Annotation: "dry: harvesting"},
			{Frame: rf(110, 3000, withBlocks(water), withBurning(true)), Annotation: "burning: emergency, run to water"},
			{Frame: rf(116, 3000, withBlocks(water), withBurning(true), withNav(navPathing)), Annotation: "pathing, no new damage frame: held, nav not re-spammed"},
			{Frame: rf(135, 3000, withBlocks(water), withBurning(true), withNav(navPathing)), Annotation: "still burning 20+ ticks on: held, nav re-issued"},
			{Frame: rf(150, 3000, withBlocks(treeColumn...), withBurning(false)), Annotation: "extinguished: pop, routine resumes"},
		},
		Check: func(t *testing.T, tr []replayRecord) {
			for i := 1; i <= 3; i++ {
				if tr[i].Stage != StageEmergency {
					t.Fatalf("step %d must hold the emergency: %s", i, tr[i])
				}
			}
			if !hasIntentPrefix(tr[1], "Navigate(x=4.5") || len(tr[2].Intents) != 0 || !hasIntentPrefix(tr[3], "Navigate(x=4.5") {
				t.Fatalf("expected water nav, quiet hold, re-issue: %s | %s | %s", tr[1], tr[2], tr[3])
			}
			if tr[4].Stage == StageEmergency {
				t.Fatalf("expected pop once extinguished: %s", tr[4])
			}
		},
	}
}

// Zombie engagement: the verdict is taken once at engagement start and held while HP and
// confidence move; one fight->flee escalation at the HP floor, never back.
func scenarioZombieEngagementHeldOnce() replayScenario {
	z := func(d float32) fo { return withEntities(zombieAt(31, 0.5+float64(d), 0.5, d)) }
	melee := civquestv1.DamageSourceType_DAMAGE_SOURCE_TYPE_MOB
	return replayScenario{
		Name: "zombie_engagement_decided_once", Stage: StageBootstrap, Profession: "EXPLORER",
		Steps: []replayStep{
			{Frame: rf(100, 3000, withInv(survivalKit()...), z(5)), Annotation: "zombie in range, full HP + sword: decide fight"},
			{Frame: rf(110, 3000, withInv(survivalKit()...), withHP(12), z(2), withDamage(melee, 31)), Annotation: "hit, HP 12: confidence moved but verdict held (fight)"},
			{Frame: rf(130, 3000, withInv(survivalKit()...), withHP(8), z(2), withDamage(melee, 31)), Annotation: "HP 8 (40%, above panic): still fight"},
			{Frame: rf(150, 3000, withInv(survivalKit()...), withHP(3), z(2), withDamage(melee, 31)), Annotation: "HP 3 <= floor: single escalation to flee"},
			{Frame: rf(160, 3000, withInv(survivalKit()...), withHP(9), z(6)), Annotation: "HP regained above panic: flee held, never back to fight"},
		},
		Check: func(t *testing.T, tr []replayRecord) {
			flee := func(r replayRecord) (hasFlee, hasStand bool) {
				for _, s := range r.Intents {
					if strings.HasPrefix(s, "Attack(") {
						if strings.Contains(s, "flee=true") {
							hasFlee = true
						} else {
							hasStand = true
						}
					}
				}
				return
			}
			for i := 0; i < 3; i++ {
				if f, s := flee(tr[i]); f || !s {
					t.Fatalf("step %d must hold fight: %s", i, tr[i])
				}
			}
			for i := 3; i < 5; i++ {
				if f, s := flee(tr[i]); !f || s {
					t.Fatalf("step %d must hold flee exclusively: %s", i, tr[i])
				}
			}
		},
	}
}
