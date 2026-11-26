package eval

// Fauna, drops and fishing for the PlaytestSim (cq-390.3 wool, cq-390.4 fishing). Additive: the
// core sim only calls the hooks below (faunaBlocks, faunaEntities, fishingState, advanceFauna,
// applyFaunaIntent).
//
// Fidelity notes (all design budgets, not measurements): sheep and fish do not move; shearing
// yields 2 wool and the fleece regrows after simSheepRegrowTicks; killing an unsheared adult
// drops 1 wool + mutton, killing a baby or a sheared sheep drops no wool; a cast line gets a bite
// simBiteDelayTicks later that lasts simBiteWindowTicks, reeling inside that window lands a cod.

import (
	"math"
	"sort"
	"strings"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

const (
	simSheepRegrowTicks = int64(1200)
	simBiteDelayTicks   = int64(200)
	simBiteWindowTicks  = int64(60)
	simMeleeReach       = 3.5
	simShearReach       = 4.5
	simPickupReach      = 1.5
	simMaxWaterCells    = 24 // mirrors SensoryFrustumCollector.MAX_WATER_BLOCKS
	simShearWool        = int32(2)
)

// SimSheep is a stationary sheep.
type SimSheep struct {
	ID       int32
	Pos      SimVec
	HP       float32
	Baby     bool
	Sheared  bool
	Color    string // "white", "black", ...
	Dead     bool
	regrowAt int64
}

// SimFish is a stationary cod or salmon.
type SimFish struct {
	ID   int32
	Kind string // "cod" | "salmon"
	Pos  SimVec
	HP   float32
	Dead bool
}

// SimDrop is a dropped item entity.
type SimDrop struct {
	ID    int32
	Pos   SimVec
	Item  string
	Name  string
	Count int32
}

type simLine struct {
	cast    bool
	inWater bool
	biteAt  int64
}

type simFauna struct {
	Sheep  []*SimSheep
	Fish   []*SimFish
	Drops  []*SimDrop
	nextID int32
	line   simLine

	// Counters for scenario assertions.
	Shears      int
	ShearsDeny  int
	SheepKilled int
	FishKilled  int
	Casts       int
	Reels       int
	Catches     int
}

func (s *PlaytestSim) fauna() *simFauna {
	if s.faunaState == nil {
		s.faunaState = &simFauna{nextID: 5000}
	}
	return s.faunaState
}

// Fauna exposes the fauna state (counters, entities) to scenarios.
func (s *PlaytestSim) Fauna() *simFauna { return s.fauna() }

func (f *simFauna) id() int32 { f.nextID++; return f.nextID }

// AddSheep places a stationary adult sheep.
func (s *PlaytestSim) AddSheep(x, z float64, color string) *SimSheep {
	if color == "" {
		color = "white"
	}
	f := s.fauna()
	sh := &SimSheep{ID: f.id(), Pos: SimVec{X: x, Y: 64, Z: z}, HP: 8, Color: color}
	f.Sheep = append(f.Sheep, sh)
	return sh
}

// AddFish places a stationary fish inside the water at (x, z).
func (s *PlaytestSim) AddFish(kind string, x, y, z float64) *SimFish {
	f := s.fauna()
	fi := &SimFish{ID: f.id(), Kind: kind, Pos: SimVec{X: x, Y: y, Z: z}, HP: 3}
	f.Fish = append(f.Fish, fi)
	return fi
}

// AddRecipe makes an extra recipe available to the sim's craft handling.
func (s *PlaytestSim) AddRecipe(n *civquestv1.RecipeNode) { s.recipes = append(s.recipes, n) }

// FishingRodRecipe is the vanilla fishing rod (3 sticks + 2 string, 3x3).
func FishingRodRecipe() *civquestv1.RecipeNode {
	return &civquestv1.RecipeNode{
		RecipeId: "minecraft:fishing_rod", RecipeType: "minecraft:crafting_shaped",
		WorkstationBlock: "minecraft:crafting_table", Requires_3X3: true,
		IngredientSlots: []*civquestv1.RecipeIngredientSlot{
			{AcceptedItemIds: []string{"minecraft:stick"}, Count: 3},
			{AcceptedItemIds: []string{"minecraft:string"}, Count: 2},
		},
		Result: &civquestv1.ItemStackProto{ItemId: "minecraft:fishing_rod", Count: 1},
	}
}

// ---- telemetry hooks ----

// faunaBlocks mirrors the collector's surface-water pass: still-water cells with no water above,
// nearest MAX_WATER_BLOCKS within the view radius.
func (s *PlaytestSim) faunaBlocks(add func(BlockPosKey)) {
	f := cellOf(s.Pos.X, s.Pos.Y+0.01, s.Pos.Z)
	r2 := float64(s.cfg.ViewRadius * s.cfg.ViewRadius)
	type cand struct {
		k BlockPosKey
		d float64
	}
	var cands []cand
	for k, id := range s.World.Blocks {
		if !isSimWater(id) || isSimWater(s.World.Blocks[BlockPosKey{X: k.X, Y: k.Y + 1, Z: k.Z}]) {
			continue
		}
		dx, dy, dz := float64(k.X-f.X), float64(k.Y-f.Y), float64(k.Z-f.Z)
		if d := dx*dx + dy*dy + dz*dz; d <= r2 {
			cands = append(cands, cand{k, d})
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].d != cands[j].d {
			return cands[i].d < cands[j].d
		}
		a, b := cands[i].k, cands[j].k
		if a.X != b.X {
			return a.X < b.X
		}
		if a.Y != b.Y {
			return a.Y < b.Y
		}
		return a.Z < b.Z
	})
	for i := 0; i < len(cands) && i < simMaxWaterCells; i++ {
		add(cands[i].k)
	}
}

func (s *PlaytestSim) faunaEntities() []*civquestv1.VisibleEntity {
	if s.faunaState == nil {
		return nil
	}
	var out []*civquestv1.VisibleEntity
	dist := func(p SimVec) float64 { return math.Hypot(p.X-s.Pos.X, p.Z-s.Pos.Z) }
	vec := func(p SimVec) *civquestv1.Vec3 { return &civquestv1.Vec3{X: p.X, Y: p.Y, Z: p.Z} }
	for _, sh := range s.faunaState.Sheep {
		if d := dist(sh.Pos); !sh.Dead && d <= 24 {
			out = append(out, &civquestv1.VisibleEntity{EntityId: sh.ID, EntityType: "minecraft:sheep", DisplayName: "Sheep",
				Health: sh.HP, Distance: float32(d), Position: vec(sh.Pos), IsBaby: sh.Baby, IsSheared: sh.Sheared})
		}
	}
	for _, fi := range s.faunaState.Fish {
		if d := dist(fi.Pos); !fi.Dead && d <= 24 {
			out = append(out, &civquestv1.VisibleEntity{EntityId: fi.ID, EntityType: "minecraft:" + fi.Kind, DisplayName: strings.Title(fi.Kind),
				Health: fi.HP, Distance: float32(d), Position: vec(fi.Pos)})
		}
	}
	for _, dr := range s.faunaState.Drops {
		if d := dist(dr.Pos); d <= 24 {
			out = append(out, &civquestv1.VisibleEntity{EntityId: dr.ID, EntityType: "minecraft:item", DisplayName: dr.Name,
				Distance: float32(d), Position: vec(dr.Pos)})
		}
	}
	return out
}

func (s *PlaytestSim) fishingState() *civquestv1.FishingState {
	if s.faunaState == nil || !s.faunaState.line.cast {
		return nil
	}
	l := s.faunaState.line
	return &civquestv1.FishingState{Cast: true, InWater: l.inWater, Bite: l.inWater && s.biting(),
		BobberPos: &civquestv1.Vec3{X: s.Pos.X, Y: s.Pos.Y, Z: s.Pos.Z}}
}

func (s *PlaytestSim) biting() bool {
	l := s.faunaState.line
	return s.Tick >= l.biteAt && s.Tick < l.biteAt+simBiteWindowTicks
}

// ---- world advance ----

func (s *PlaytestSim) advanceFauna(int64) {
	if s.faunaState == nil {
		return
	}
	f := s.faunaState
	for _, sh := range f.Sheep {
		if sh.Sheared && s.Tick >= sh.regrowAt {
			sh.Sheared = false
		}
	}
	// A bite that nobody reeled escapes; the next fish nibbles later.
	if f.line.cast && f.line.inWater && s.Tick >= f.line.biteAt+simBiteWindowTicks {
		f.line.biteAt = s.Tick + simBiteDelayTicks
	}
	// Pick up drops in reach.
	keep := f.Drops[:0]
	for _, dr := range f.Drops {
		if math.Hypot(dr.Pos.X-s.Pos.X, dr.Pos.Z-s.Pos.Z) <= simPickupReach {
			s.AddItem(dr.Item, dr.Count)
			continue
		}
		keep = append(keep, dr)
	}
	f.Drops = keep
}

func (f *simFauna) drop(p SimVec, item, name string, n int32) {
	f.Drops = append(f.Drops, &SimDrop{ID: f.id(), Pos: p, Item: item, Name: name, Count: n})
}

func titleColor(c string) string { return strings.Title(c) }

// ---- intents ----

// applyFaunaIntent handles attacks on fauna and the shear/cast/reel UseItem modes. It reports
// whether the intent was consumed.
func (s *PlaytestSim) applyFaunaIntent(in *civquestv1.AgentIntent) bool {
	f := s.fauna()
	horiz := func(p SimVec) float64 { return math.Hypot(p.X-s.Pos.X, p.Z-s.Pos.Z) }
	if a := in.GetAttack(); a != nil && !a.FleeImmediately {
		dmg := float32(1)
		switch {
		case s.CountSuffix("_sword") > 0:
			dmg = 4
		case s.CountSuffix("_axe") > 0:
			dmg = 3
		}
		for _, sh := range f.Sheep {
			if sh.ID != a.TargetEntityId || sh.Dead {
				continue
			}
			s.AttackLog = append(s.AttackLog, AttackRecord{Tick: s.Tick, Target: a.TargetEntityId, HP: s.HP})
			if horiz(sh.Pos) <= simMeleeReach {
				sh.HP -= dmg
				if sh.HP <= 0 {
					sh.Dead = true
					f.SheepKilled++
					if !sh.Baby && !sh.Sheared {
						f.drop(sh.Pos, "minecraft:"+sh.Color+"_wool", titleColor(sh.Color)+" Wool", 1)
					}
					if !sh.Baby {
						f.drop(sh.Pos, "minecraft:mutton", "Raw Mutton", 1)
					}
				}
			}
			return true
		}
		for _, fi := range f.Fish {
			if fi.ID != a.TargetEntityId || fi.Dead {
				continue
			}
			s.AttackLog = append(s.AttackLog, AttackRecord{Tick: s.Tick, Target: a.TargetEntityId, HP: s.HP})
			if horiz(fi.Pos) <= simMeleeReach {
				fi.HP -= dmg
				if fi.HP <= 0 {
					fi.Dead = true
					f.FishKilled++
					f.drop(fi.Pos, "minecraft:"+fi.Kind, "Raw "+strings.Title(fi.Kind), 1)
				}
			}
			return true
		}
		return false
	}
	u := in.GetUseItem()
	if u == nil {
		return false
	}
	switch u.Mode {
	case civquestv1.UseItemIntent_SHEAR_ENTITY:
		for _, sh := range f.Sheep {
			if sh.ID != u.TargetEntityId || sh.Dead {
				continue
			}
			if s.ItemCount("minecraft:shears") == 0 || sh.Baby || sh.Sheared || horiz(sh.Pos) > simShearReach {
				f.ShearsDeny++
				return true
			}
			sh.Sheared, sh.regrowAt = true, s.Tick+simSheepRegrowTicks
			f.Shears++
			f.drop(sh.Pos, "minecraft:"+sh.Color+"_wool", titleColor(sh.Color)+" Wool", simShearWool)
			return true
		}
		f.ShearsDeny++
		return true
	case civquestv1.UseItemIntent_CAST_FISHING_ROD:
		if s.ItemCount("minecraft:fishing_rod") == 0 || f.line.cast {
			return true
		}
		f.Casts++
		wet := false
		if u.AimAt != nil {
			wet = isSimWater(s.World.Blocks[cellOf(u.AimAt.X, u.AimAt.Y, u.AimAt.Z)]) &&
				horiz(SimVec{X: u.AimAt.X, Z: u.AimAt.Z}) <= 7
		}
		f.line = simLine{cast: true, inWater: wet, biteAt: s.Tick + simBiteDelayTicks}
		return true
	case civquestv1.UseItemIntent_REEL_FISHING_ROD:
		if !f.line.cast {
			return true
		}
		f.Reels++
		if f.line.inWater && s.biting() {
			f.Catches++
			s.AddItem("minecraft:cod", 1)
		}
		f.line = simLine{}
		return true
	}
	return false
}
