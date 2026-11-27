package integration

import (
	"math"
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// Play test 9: an agent sat for fifty minutes in a pocket whose walls were two and three blocks
// away, holding a stone pickaxe, and never broke a block. The simulation navigates as the mod
// does (step up one, drop three, report "stuck, enclosed" when the reachable ground is small),
// and shows the agent only the blocks the mod always lists around it.

type pocketCell = [3]int32

type pocketSim struct {
	air  map[pocketCell]bool // cells below ground that are open
	feet pocketCell
	pos  civquestv1.Vec3
	top  int32 // open sky from this height up
	rock string
}

func (p *pocketSim) block(c pocketCell) string {
	if c[1] >= p.top || p.air[c] {
		return ""
	}
	return p.rock
}

func (p *pocketSim) clear(c pocketCell) bool { return p.block(c) == "" }

func (p *pocketSim) standable(c pocketCell) bool {
	return p.clear(c) && p.clear(pocketCell{c[0], c[1] + 1, c[2]}) && !p.clear(pocketCell{c[0], c[1] - 1, c[2]})
}

// reachable is the walkable ground from the feet cell, capped.
func (p *pocketSim) reachable() map[pocketCell]bool {
	seen := map[pocketCell]bool{p.feet: true}
	queue := []pocketCell{p.feet}
	for len(queue) > 0 && len(seen) < 400 {
		c := queue[0]
		queue = queue[1:]
		for _, d := range [][2]int32{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			for dy := int32(1); dy >= -3; dy-- {
				n := pocketCell{c[0] + d[0], c[1] + dy, c[2] + d[1]}
				if !p.standable(n) || (dy == 1 && !p.clear(pocketCell{c[0], c[1] + 2, c[2]})) {
					continue
				}
				if dy < 0 && !p.clear(pocketCell{n[0], c[1] + 1, n[2]}) {
					continue
				}
				if !seen[n] {
					seen[n] = true
					queue = append(queue, n)
				}
				break
			}
		}
	}
	return seen
}

func (p *pocketSim) moveTo(c pocketCell) {
	p.feet = c
	p.pos.X, p.pos.Y, p.pos.Z = float64(c[0])+0.5, float64(c[1]), float64(c[2])+0.5
}

func (p *pocketSim) frameBlocks() []*civquestv1.VisibleBlock {
	var out []*civquestv1.VisibleBlock
	add := func(x, y, z int32) {
		if id := p.block(pocketCell{x, y, z}); id != "" {
			out = append(out, &civquestv1.VisibleBlock{Pos: &civquestv1.BlockPosProto{X: x, Y: y, Z: z}, BlockId: id})
		}
	}
	f := p.feet
	add(f[0], f[1]-1, f[2])
	add(f[0], f[1]+2, f[2])
	for _, d := range [][2]int32{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
		for dist := int32(1); dist <= 2; dist++ {
			for dy := int32(0); dy <= 2; dy++ {
				add(f[0]+d[0]*dist, f[1]+dy, f[2]+d[1]*dist)
			}
		}
		for depth := int32(1); depth <= 4; depth++ {
			add(f[0]+d[0], f[1]-depth, f[2]+d[1])
		}
	}
	return out
}

func TestPlaytest9_AgentClimbsOutOfAWidePocket(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rock  string
		half  int32 // pocket reaches this far from its centre
		depth int32
	}{
		{"stone pit walls three away", "minecraft:stone", 3, 4},
		{"diorite pit walls two away", "minecraft:diorite", 2, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &pocketSim{air: map[pocketCell]bool{}, top: 64 + tc.depth, rock: tc.rock}
			for x := -tc.half; x <= tc.half; x++ {
				for z := -tc.half; z <= tc.half; z++ {
					for y := int32(64); y < p.top; y++ {
						p.air[pocketCell{x, y, z}] = true
					}
				}
			}
			p.moveTo(pocketCell{0, 64, 0})
			fsm := system1.NewAgentFSMWithRegistry(system1.StageSettlement, nil, system1.NewDefaultEngineRegistry())
			st := system1.NewAgentState()
			cfg := xdg.DefaultAgentIdentityConfig("dave")
			cfg.Profession = "EXPLORER"
			inv := []*civquestv1.ItemStackProto{{ItemId: "minecraft:stone_pickaxe", Count: 1}, {ItemId: "minecraft:stone_axe", Count: 1},
				{ItemId: "minecraft:stone_sword", Count: 1}, {ItemId: "minecraft:oak_log", Count: 8}}

			var receipt *civquestv1.IntentReceipt
			var survey *civquestv1.HorizonSurveyReport
			var batch int64
			breaks, walks := 0, 0
			for tick := int64(1000); tick < 9000; tick += 4 {
				reach := p.reachable()
				if survey != nil {
					st.IngestHorizonSurveyReport(survey, tick)
				}
				res := fsm.Tick(&system1.FSMContext{AgentID: "dave", State: st, Config: cfg, Frame: &civquestv1.TelemetryFrame{
					WorldTick: tick, TimeOfDay: tick % 24000, Receipt: receipt, HorizonSurvey: survey, InterestingBlocks: p.frameBlocks(),
					Status: &civquestv1.AgentStatus{AgentId: "dave", Health: 20, MaxHealth: 20, FoodLevel: 20, Inventory: inv,
						Position:           &civquestv1.Vec3{X: p.pos.X, Y: p.pos.Y, Z: p.pos.Z},
						NavigationState:    civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
						NavigationEnclosed: len(reach) <= 60},
				}})
				receipt, survey = nil, nil
				for _, in := range res.Intents {
					status, facts, reason := civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED, (*civquestv1.MotorFacts)(nil), ""
					switch {
					case in.Intent.GetNavigate() != nil:
						walks++
						nav := in.Intent.GetNavigate()
						goal := pocketCell{int32(math.Floor(nav.Target.X)), int32(math.Floor(nav.Target.Y + 0.1)), int32(math.Floor(nav.Target.Z))}
						best, found := pocketCell{}, false
						for c := range reach {
							near := math.Hypot(float64(c[0])+0.5-nav.Target.X, float64(c[2])+0.5-nav.Target.Z) <= math.Max(1, float64(nav.ArrivalTolerance)) &&
								math.Abs(float64(c[1])-nav.Target.Y) <= 2
							if nav.IntoCell {
								near = c == goal
							}
							if near && (!found || c == goal) {
								best, found = c, true
							}
						}
						if found {
							p.moveTo(best)
						} else {
							status, reason = civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED, "Navigation stuck"
							facts = &civquestv1.MotorFacts{Stuck: true, Enclosed: len(reach) <= 60}
						}
					case in.Intent.GetInteractBlock() != nil && in.Intent.GetInteractBlock().InteractionType == civquestv1.InteractBlockIntent_HARVEST_BREAK:
						b := in.Intent.GetInteractBlock().Pos
						c := pocketCell{b.X, b.Y, b.Z}
						if p.block(c) != "" && math.Sqrt(sqf(float64(b.X)+0.5-p.pos.X)+sqf(float64(b.Y)+0.5-p.pos.Y-1.6)+sqf(float64(b.Z)+0.5-p.pos.Z)) <= 5 {
							p.air[c] = true
							breaks++
							for p.clear(pocketCell{p.feet[0], p.feet[1] - 1, p.feet[2]}) {
								p.moveTo(pocketCell{p.feet[0], p.feet[1] - 1, p.feet[2]})
							}
						}
					case in.Intent.GetSurveyHorizon() != nil:
						survey = openSurvey(p.pos.X, p.pos.Y, p.pos.Z, tick)
					default:
						continue
					}
					batch++
					receipt = &civquestv1.IntentReceipt{BatchId: batch, Status: status, FailureReason: reason, Facts: facts, FrameId: in.Frame}
				}
				if p.feet[1] >= p.top {
					t.Logf("out after %d ticks, %d blocks broken, %d walks", tick-1000, breaks, walks)
					return
				}
			}
			t.Errorf("still in the pocket after 8000 ticks at %v (%d blocks broken, %d walks)", p.feet, breaks, walks)
		})
	}
}

func sqf(v float64) float64 { return v * v }

// openSurvey reports clear ground on every bearing, as seen over the rim.
func openSurvey(x, y, z float64, tick int64) *civquestv1.HorizonSurveyReport {
	var secs []*civquestv1.HorizonSector
	for i := int32(0); i < 36; i++ {
		rad := float64(i*10) * math.Pi / 180
		secs = append(secs, &civquestv1.HorizonSector{SectorIndex: i, BearingDeg: float32(i * 10), HitDistance: 64, FeatureType: "OPEN_PLAINS",
			HitBlockId: "minecraft:air", HitX: x - math.Sin(rad)*64, HitY: y, HitZ: z + math.Cos(rad)*64, OpenHorizon: true})
	}
	return &civquestv1.HorizonSurveyReport{OriginX: x, OriginY: y, OriginZ: z, CompletedTick: tick, MaxRange: 64, Sectors: secs}
}
