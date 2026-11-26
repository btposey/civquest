package eval

// PlaytestSim: a closed-loop synthetic world that drives the REAL System 1 kernel
// (system1.AgentFSM.Tick) the way the Minecraft mod does, so playtest-2 fixes can be
// proven by outcomes over simulated minutes instead of single-frame unit checks (cq-gpa.13).
//
// What it models (all deliberately small, deterministic, and hermetic):
//   - Telemetry fidelity: frames carry only what SensoryFrustumCollector sends (blocks within
//     2 cells of the agent plus "interesting" blocks within 16), not the whole voxel world.
//   - Sustained hazards (cq-gpa.1): in_water / submerged / air_supply / is_on_fire every frame,
//     periodic drown damage, fire damage, extinguish in water, lit campfire / lava cells.
//   - A chasing zombie with melee damage; the agent can fight or flee.
//   - Block break (reach + tool-for-drops), place, craft, eat.
//
// What it does not model: collision geometry beyond solid-cell blocking, real pathfinding,
// receipts (intents are fire-and-forget), light/mob spawning. Numbers such as walk speed are
// vanilla-ish design budgets, not measurements.

import (
	"fmt"
	"math"
	"strings"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

const (
	simEyeHeight   = 1.62
	simBlockReach  = 4.5
	simMaxAir      = int32(300)
	simWalkPerTick = 0.215
	simFireTicks   = int32(160)
)

// PlaytestSimConfig configures a PlaytestSim.
type PlaytestSimConfig struct {
	AgentID    string
	Profession string
	Stage      system1.State
	World      *SyntheticStartingWorld
	Start      SimVec
	Inventory  []*civquestv1.ItemStackProto
	Health     float32
	Air        int32 // initial air supply (0 => full)
	TickDelta  int64 // ticks per frame (default 4 = 5 Hz)
	TimeOfDay  int64 // initial time of day (default 1000 = morning)
	Registry   *system1.EngineRegistry
	Setup      func(st *system1.AgentState)
	// NoRecipeGraph leaves AgentState without a recipe graph, so the bootstrap planner never
	// decides it has gathered enough wood and moves on (isolates tree-reach scenarios).
	NoRecipeGraph bool
	// CraftLatency delays the effect of a craft intent by this many ticks (the mod applies
	// intents on a later server tick and reports a receipt after; 0 = instant).
	CraftLatency int64
	// ViewRadius is the interesting-block scan radius (default 16, like the mod).
	ViewRadius int32
}

// SimVec is a plain position (proto Vec3 carries a lock and cannot be copied freely).
type SimVec struct{ X, Y, Z float64 }

// SimZombie is a melee mob that walks straight at the agent.
type SimZombie struct {
	ID      int32
	Pos     SimVec
	HP      float32
	Speed   float64 // blocks per tick
	Damage  float32 // per hit (every 20 ticks within 1.6 blocks)
	Dead    bool
	nextHit int64
}

type deferredEffect struct {
	at int64
	fn func()
}

// SimStep is one recorded frame of the run.
type SimStep struct {
	Tick      int64
	Stage     system1.Stage
	Top       string // active activity kind after the tick
	Pos       SimVec
	HP        float32
	Air       int32
	Submerged bool
	InWater   bool
	OnFire    bool
	Intents   []*civquestv1.AgentIntent
}

// SimCraft is one craft intent and whether the simulated recipe succeeded.
type SimCraft struct {
	Tick   int64
	Recipe string
	OK     bool
}

// SimHarvest is one HARVEST_BREAK intent and its outcome.
type SimHarvest struct {
	Tick    int64
	Cell    BlockPosKey
	BlockID string
	Existed bool
	InReach bool
	NoDrop  bool // tool-gated block broken without the right tool
	HadTool bool
	Inv     string // inventory summary at the time
}

// PlaytestSim is the closed-loop world + agent.
type PlaytestSim struct {
	cfg   PlaytestSimConfig
	World *SyntheticStartingWorld
	FSM   *system1.AgentFSM
	State *system1.AgentState
	idCfg *xdg.AgentIdentityConfig

	Tick        int64
	TOD         int64
	Pos         SimVec
	HP          float32
	Food        int32
	Air         int32
	fire        int32
	nextAirDmg  int64
	nextFireDmg int64
	Dead        bool

	inventory     []*civquestv1.ItemStackProto
	navTarget     *civquestv1.Vec3
	navSpeed      float64
	navTol        float64
	navState      civquestv1.NavigationState
	pendingDmg    *civquestv1.DamageEvent
	pendingSurvey bool

	Zombies  []*SimZombie
	recipes  []*civquestv1.RecipeNode
	deferred []deferredEffect

	// Recorded history.
	Steps       []SimStep
	Crafts      []SimCraft // executed crafts (after latency) with outcome
	CraftIssued []SimCraft // every craft intent at the tick the kernel emitted it
	Harvests    []SimHarvest
	Placed      []BlockPosKey
	AttackLog   []AttackRecord
	hungerAcc   int64
	visits      map[[2]int32]int
	fleeing     bool
	fleeFrom    int32
}

// AttackRecord is one Attack intent as emitted by the kernel.
type AttackRecord struct {
	Tick   int64
	Target int32
	Flee   bool
	HP     float32
}

// NewPlaytestSim builds the sim around a fresh FSM + AgentState.
func NewPlaytestSim(cfg PlaytestSimConfig) *PlaytestSim {
	if cfg.AgentID == "" {
		cfg.AgentID = "sim_agent"
	}
	if cfg.TickDelta <= 0 {
		cfg.TickDelta = 4
	}
	if cfg.TimeOfDay == 0 {
		cfg.TimeOfDay = 1000
	}
	if cfg.Health <= 0 {
		cfg.Health = 20
	}
	if cfg.Air <= 0 {
		cfg.Air = simMaxAir
	}
	if cfg.ViewRadius <= 0 {
		cfg.ViewRadius = 16
	}
	if cfg.World == nil {
		cfg.World = NewBlankWorld()
	}
	idc := xdg.DefaultAgentIdentityConfig(cfg.AgentID)
	if cfg.Profession != "" {
		idc.Profession = cfg.Profession
	}
	st := system1.NewAgentState()
	if cfg.Setup != nil {
		cfg.Setup(st)
	}
	s := &PlaytestSim{
		cfg:    cfg,
		World:  cfg.World,
		FSM:    system1.NewAgentFSMWithRegistry(cfg.Stage, nil, cfg.Registry),
		State:  st,
		idCfg:  idc,
		Tick:   100,
		TOD:    cfg.TimeOfDay,
		Pos:    cfg.Start,
		HP:     cfg.Health,
		Food:   20,
		Air:    cfg.Air,
		visits: map[[2]int32]int{},
	}
	s.navState = civquestv1.NavigationState_NAVIGATION_STATE_IDLE
	s.recipes = VanillaRecipeNodes()
	if !cfg.NoRecipeGraph {
		st.SetRecipeGraph(VanillaRecipeGraph())
	}
	for _, it := range cfg.Inventory {
		if it != nil {
			s.AddItem(it.ItemId, it.Count)
		}
	}
	return s
}

// ---- inventory ----

func normItem(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	if !strings.Contains(id, ":") {
		id = "minecraft:" + id
	}
	return id
}

// AddItem adds count of item to the inventory.
func (s *PlaytestSim) AddItem(id string, count int32) {
	id = normItem(id)
	for _, it := range s.inventory {
		if it.ItemId == id {
			it.Count += count
			return
		}
	}
	s.inventory = append(s.inventory, &civquestv1.ItemStackProto{ItemId: id, Count: count})
}

// ItemCount returns the carried count of item.
func (s *PlaytestSim) ItemCount(id string) int32 {
	id = normItem(id)
	var n int32
	for _, it := range s.inventory {
		if it.ItemId == id {
			n += it.Count
		}
	}
	return n
}

// CountSuffix returns the total count of items whose id ends with suffix (e.g. "_pickaxe").
func (s *PlaytestSim) CountSuffix(suffix string) int32 {
	var n int32
	for _, it := range s.inventory {
		if strings.HasSuffix(it.ItemId, suffix) {
			n += it.Count
		}
	}
	return n
}

// RemoveItem removes count of item, reporting whether enough was carried.
func (s *PlaytestSim) RemoveItem(id string, count int32) bool {
	id = normItem(id)
	if s.ItemCount(id) < count {
		return false
	}
	rem := count
	for _, it := range s.inventory {
		if it.ItemId != id || rem == 0 {
			continue
		}
		take := it.Count
		if take > rem {
			take = rem
		}
		it.Count -= take
		rem -= take
	}
	out := s.inventory[:0]
	for _, it := range s.inventory {
		if it.Count > 0 {
			out = append(out, it)
		}
	}
	s.inventory = out
	return true
}

func (s *PlaytestSim) removeAnyLog() bool {
	for _, it := range s.inventory {
		if strings.HasSuffix(it.ItemId, "_log") && it.Count > 0 {
			return s.RemoveItem(it.ItemId, 1)
		}
	}
	return false
}

// InventorySummary renders the inventory as "item xN, ...".
func (s *PlaytestSim) InventorySummary() string {
	parts := make([]string, 0, len(s.inventory))
	for _, it := range s.inventory {
		parts = append(parts, fmt.Sprintf("%s x%d", strings.TrimPrefix(it.ItemId, "minecraft:"), it.Count))
	}
	return strings.Join(parts, ", ")
}

// ---- world queries ----

func cellOf(x, y, z float64) BlockPosKey {
	return BlockPosKey{X: int32(math.Floor(x)), Y: int32(math.Floor(y)), Z: int32(math.Floor(z))}
}

// BlockAt returns the block id at a cell ("" for air).
func (s *PlaytestSim) BlockAt(k BlockPosKey) string { return s.World.Blocks[k] }

func isSimWater(id string) bool { return strings.Contains(id, "water") }

func isSimSolid(id string) bool {
	if id == "" || isSimWater(id) || strings.Contains(id, "lava") || strings.Contains(id, "campfire") ||
		strings.Contains(id, "torch") || strings.Contains(id, "wheat") || strings.Contains(id, "carrots") {
		return false
	}
	return true
}

// hazardUnderfoot reports whether the feet cell is a damaging fire source (lit campfire, lava, fire).
func (s *PlaytestSim) hazardUnderfoot() bool {
	k := cellOf(s.Pos.X, s.Pos.Y+0.01, s.Pos.Z)
	id := s.World.Blocks[k]
	switch {
	case strings.Contains(id, "lava") || strings.HasSuffix(id, "fire"):
		return true
	case strings.Contains(id, "campfire"):
		return !strings.Contains(s.World.BlockStates[k], "lit=false")
	}
	return false
}

// InWater: feet cell is water. Submerged: eye cell is water.
func (s *PlaytestSim) InWater() bool {
	return isSimWater(s.World.Blocks[cellOf(s.Pos.X, s.Pos.Y+0.01, s.Pos.Z)])
}

// Submerged reports eyes below the water surface.
func (s *PlaytestSim) Submerged() bool {
	return isSimWater(s.World.Blocks[cellOf(s.Pos.X, s.Pos.Y+simEyeHeight, s.Pos.Z)])
}

// OnFire reports the agent is burning.
func (s *PlaytestSim) OnFire() bool { return s.fire > 0 }

// groundY returns the feet Y for standing at (x,z), searching down from just above the
// current feet, or ok=false when the column is blocked at the feet/head cells.
func (s *PlaytestSim) groundY(x, z, fromY float64) (float64, bool) {
	cx, cz := int32(math.Floor(x)), int32(math.Floor(z))
	top := int32(math.Floor(fromY)) + 1 // allow a 1-block step-up
	for y := top; y > top-12; y-- {
		if isSimSolid(s.World.Blocks[BlockPosKey{X: cx, Y: y, Z: cz}]) {
			feet := float64(y + 1)
			// feet and head cells must be free of solids
			if isSimSolid(s.World.Blocks[BlockPosKey{X: cx, Y: y + 1, Z: cz}]) ||
				isSimSolid(s.World.Blocks[BlockPosKey{X: cx, Y: y + 2, Z: cz}]) {
				return 0, false
			}
			return feet, true
		}
	}
	return 0, false // void: no ground in this column, the edge of the arena is impassable
}

// ---- telemetry ----

func isInterestingSimBlock(id string) bool {
	switch {
	case strings.HasSuffix(id, "_log") || strings.HasSuffix(id, "_wood") || strings.HasSuffix(id, "_stem"):
		return true
	case strings.Contains(id, "campfire"), strings.Contains(id, "crafting_table"), strings.Contains(id, "furnace"),
		strings.Contains(id, "chest"), strings.Contains(id, "barrel"), strings.Contains(id, "farmland"),
		strings.Contains(id, "wheat"), strings.Contains(id, "carrots"), strings.Contains(id, "_bed"),
		strings.Contains(id, "door"), strings.Contains(id, "torch"), strings.Contains(id, "lectern"),
		strings.Contains(id, "bell"), strings.Contains(id, "composter"):
		return true
	}
	return false
}

// frameBlocks mirrors SensoryFrustumCollector: immediate samples (feet/below/overhead and the
// ring at distance 1 and 2) plus every interesting block in the scan sphere.
func (s *PlaytestSim) frameBlocks() []*civquestv1.VisibleBlock {
	seen := map[BlockPosKey]bool{}
	var out []*civquestv1.VisibleBlock
	add := func(k BlockPosKey) {
		if seen[k] {
			return
		}
		id, ok := s.World.Blocks[k]
		if !ok || id == "" {
			return
		}
		seen[k] = true
		out = append(out, &civquestv1.VisibleBlock{
			Pos: &civquestv1.BlockPosProto{X: k.X, Y: k.Y, Z: k.Z}, BlockId: id, BlockStateString: s.World.BlockStates[k],
		})
	}
	f := cellOf(s.Pos.X, s.Pos.Y+0.01, s.Pos.Z)
	add(f)
	add(BlockPosKey{X: f.X, Y: f.Y - 1, Z: f.Z})
	add(BlockPosKey{X: f.X, Y: f.Y + 2, Z: f.Z})
	dirs := [][2]int32{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}
	for _, d := range dirs {
		for dist := int32(1); dist <= 2; dist++ {
			for _, dy := range []int32{0, 1, 2} {
				add(BlockPosKey{X: f.X + d[0]*dist, Y: f.Y + dy, Z: f.Z + d[1]*dist})
			}
		}
	}
	// Ground below the neighbouring columns, as the Java collector samples it (void-edge reflex, cq-71l.13).
	for _, d := range dirs {
		for depth := int32(1); depth <= 4; depth++ {
			add(BlockPosKey{X: f.X + d[0], Y: f.Y - depth, Z: f.Z + d[1]})
		}
	}
	r := s.cfg.ViewRadius
	r2 := float64(r * r)
	for k, id := range s.World.Blocks {
		if !isInterestingSimBlock(id) {
			continue
		}
		dx, dy, dz := float64(k.X-f.X), float64(k.Y-f.Y), float64(k.Z-f.Z)
		if dx*dx+dy*dy+dz*dz > r2 {
			continue
		}
		add(k)
	}
	return out
}

func (s *PlaytestSim) buildFrame() *civquestv1.TelemetryFrame {
	inv := make([]*civquestv1.ItemStackProto, 0, len(s.inventory))
	for i, it := range s.inventory {
		it.Slot = int32(i)
		inv = append(inv, &civquestv1.ItemStackProto{Slot: it.Slot, ItemId: it.ItemId, Count: it.Count})
	}
	air := s.Air
	if air < 0 {
		air = 0
	}
	f := &civquestv1.TelemetryFrame{
		WorldTick: s.Tick,
		TimeOfDay: ((s.TOD % 24000) + 24000) % 24000,
		Status: &civquestv1.AgentStatus{
			AgentId:         s.cfg.AgentID,
			Health:          s.HP,
			MaxHealth:       20,
			FoodLevel:       s.Food,
			Stamina:         100,
			Position:        &civquestv1.Vec3{X: s.Pos.X, Y: s.Pos.Y, Z: s.Pos.Z},
			IsInWater:       s.InWater(),
			Submerged:       s.Submerged(),
			IsOnFire:        s.OnFire(),
			AirSupply:       air,
			MaxAirSupply:    simMaxAir,
			NavigationState: s.navState,
			Inventory:       inv,
		},
		InterestingBlocks: s.frameBlocks(),
		LastDamageTaken:   s.pendingDmg,
	}
	s.pendingDmg = nil
	for _, z := range s.Zombies {
		if z.Dead {
			continue
		}
		d := math.Hypot(z.Pos.X-s.Pos.X, z.Pos.Z-s.Pos.Z)
		if d > 24 {
			continue
		}
		f.VisibleEntities = append(f.VisibleEntities, &civquestv1.VisibleEntity{
			EntityId: z.ID, EntityType: "minecraft:zombie", IsHostile: true, Health: z.HP,
			Distance: float32(d), Position: &civquestv1.Vec3{X: z.Pos.X, Y: z.Pos.Y, Z: z.Pos.Z},
		})
	}
	if s.pendingSurvey {
		s.pendingSurvey = false
		f.HorizonSurvey = s.syntheticSurvey()
	}
	return f
}

// syntheticSurvey returns an open-plains 36-sector survey (enough for the explorer to plan legs).
func (s *PlaytestSim) syntheticSurvey() *civquestv1.HorizonSurveyReport {
	secs := make([]*civquestv1.HorizonSector, 0, 36)
	for i := int32(0); i < 36; i++ {
		b := float32(i * 10)
		rad := float64(b) * math.Pi / 180
		secs = append(secs, &civquestv1.HorizonSector{
			SectorIndex: i, BearingDeg: b, HitDistance: 64, FeatureType: "OPEN_PLAINS", HitBlockId: "minecraft:air",
			HitX: s.Pos.X - math.Sin(rad)*64, HitY: s.Pos.Y, HitZ: s.Pos.Z + math.Cos(rad)*64, OpenHorizon: true,
		})
	}
	return &civquestv1.HorizonSurveyReport{OriginX: s.Pos.X, OriginY: s.Pos.Y, OriginZ: s.Pos.Z, CompletedTick: s.Tick, MaxRange: 64, Sectors: secs}
}

// ---- stepping ----

// Step advances the world by one frame, feeds the kernel, and applies its intents.
func (s *PlaytestSim) Step() SimStep {
	dt := s.cfg.TickDelta
	if !s.Dead {
		s.advanceBody(dt)
		s.advanceHazards(dt)
		s.advanceZombies(dt)
	}
	s.Tick += dt
	s.TOD += dt
	if len(s.deferred) > 0 {
		keep := s.deferred[:0]
		for _, d := range s.deferred {
			if s.Tick >= d.at {
				d.fn()
			} else {
				keep = append(keep, d)
			}
		}
		s.deferred = keep
	}

	rec := SimStep{Tick: s.Tick, Pos: s.Pos, HP: s.HP, Air: s.Air, Submerged: s.Submerged(), InWater: s.InWater(), OnFire: s.OnFire()}
	if s.Dead {
		rec.Stage = system1.StageDead
		s.Steps = append(s.Steps, rec)
		return rec
	}
	frame := s.buildFrame()
	if sv := frame.GetHorizonSurvey(); sv != nil {
		s.State.IngestHorizonSurveyReport(sv, s.Tick)
	}
	res := s.FSM.Tick(&system1.FSMContext{AgentID: s.cfg.AgentID, State: s.State, Config: s.idCfg, Frame: frame})
	rec.Stage = res.Stage
	rec.Top = s.FSM.ActiveActivityKind()
	for _, fi := range res.Intents {
		rec.Intents = append(rec.Intents, fi.Intent)
		s.applyIntent(fi.Intent)
	}
	s.Steps = append(s.Steps, rec)
	k := [2]int32{int32(math.Floor(s.Pos.X / 2)), int32(math.Floor(s.Pos.Z / 2))}
	s.visits[k]++
	return rec
}

// Run advances the sim for the given number of ticks (rounded up to whole frames).
func (s *PlaytestSim) Run(ticks int64) {
	for elapsed := int64(0); elapsed < ticks; elapsed += s.cfg.TickDelta {
		s.Step()
	}
}

// RunUntil steps until pred returns true or maxTicks elapse; it reports whether pred held.
func (s *PlaytestSim) RunUntil(pred func(*PlaytestSim) bool, maxTicks int64) bool {
	for elapsed := int64(0); elapsed < maxTicks; elapsed += s.cfg.TickDelta {
		if pred(s) {
			return true
		}
		s.Step()
	}
	return pred(s)
}

func (s *PlaytestSim) damage(amount float32, src civquestv1.DamageSourceType, attacker int32) {
	s.HP -= amount
	s.pendingDmg = &civquestv1.DamageEvent{SourceType: src, AttackerEntityId: attacker, DamageAmount: amount}
	if s.HP <= 0 {
		s.HP = 0
		s.Dead = true
	}
}

func (s *PlaytestSim) advanceBody(dt int64) {
	s.hungerAcc += dt
	if s.hungerAcc >= 1600 && s.Food > 0 {
		s.hungerAcc = 0
		s.Food--
	}
	if s.navTarget == nil {
		if s.navState == civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED {
			s.navState = civquestv1.NavigationState_NAVIGATION_STATE_IDLE
		}
		return
	}
	speed := s.navSpeed
	if s.InWater() {
		speed *= 0.6
	}
	step := speed * float64(dt)
	dx, dz := s.navTarget.X-s.Pos.X, s.navTarget.Z-s.Pos.Z
	dist := math.Hypot(dx, dz)
	arrive := dist
	if s.navTarget.Y > 0 {
		arrive = math.Sqrt(dist*dist + (s.navTarget.Y-s.Pos.Y)*(s.navTarget.Y-s.Pos.Y))
	}
	if arrive <= s.navTol || dist <= 1e-6 {
		s.navTarget = nil
		s.navState = civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED
		return
	}
	if step > dist {
		step = dist
	}
	nx, nz := s.Pos.X+dx/dist*step, s.Pos.Z+dz/dist*step
	gy, ok := s.groundY(nx, nz, s.Pos.Y)
	if !ok {
		s.navTarget = nil
		s.navState = civquestv1.NavigationState_NAVIGATION_STATE_IDLE
		return
	}
	s.Pos.X, s.Pos.Z, s.Pos.Y = nx, nz, gy
	s.navState = civquestv1.NavigationState_NAVIGATION_STATE_PATHING
}

func (s *PlaytestSim) advanceHazards(dt int64) {
	// Air: drains while the eyes are under water, refills fast outside it.
	if s.Submerged() {
		s.Air -= int32(dt)
		if s.Air <= -20 {
			s.Air = 0
		}
	} else if s.Air < simMaxAir {
		s.Air += int32(4 * dt)
		if s.Air > simMaxAir {
			s.Air = simMaxAir
		}
	}
	if s.Submerged() && s.Air <= 0 && s.Tick >= s.nextAirDmg {
		s.damage(2, civquestv1.DamageSourceType_DAMAGE_SOURCE_TYPE_DROWN, 0)
		s.nextAirDmg = s.Tick + 20
	}
	// Fire: ignite on a hazard cell, extinguish in water, burn for 1 hp per second.
	if s.hazardUnderfoot() {
		s.fire = simFireTicks
		if s.Tick >= s.nextFireDmg {
			s.damage(1, civquestv1.DamageSourceType_DAMAGE_SOURCE_TYPE_LAVA, 0)
			s.nextFireDmg = s.Tick + 20
		}
	}
	if s.InWater() {
		s.fire = 0
	}
	if s.fire > 0 {
		s.fire -= int32(dt)
		if s.fire < 0 {
			s.fire = 0
		}
		if s.Tick >= s.nextFireDmg {
			s.damage(1, civquestv1.DamageSourceType_DAMAGE_SOURCE_TYPE_LAVA, 0)
			s.nextFireDmg = s.Tick + 20
		}
	}
}

func (s *PlaytestSim) advanceZombies(dt int64) {
	for _, z := range s.Zombies {
		if z.Dead {
			continue
		}
		dx, dz := s.Pos.X-z.Pos.X, s.Pos.Z-z.Pos.Z
		d := math.Hypot(dx, dz)
		if d > 1.4 {
			step := math.Min(z.Speed*float64(dt), d-1.2)
			z.Pos.X += dx / d * step
			z.Pos.Z += dz / d * step
		}
		if d <= 1.8 && s.Tick >= z.nextHit {
			z.nextHit = s.Tick + 20
			s.damage(z.Damage, civquestv1.DamageSourceType_DAMAGE_SOURCE_TYPE_MOB, z.ID)
		}
	}
}

func (s *PlaytestSim) zombie(id int32) *SimZombie {
	for _, z := range s.Zombies {
		if z.ID == id {
			return z
		}
	}
	return nil
}

// ---- intent application ----

func (s *PlaytestSim) applyIntent(in *civquestv1.AgentIntent) {
	switch {
	case in.GetNavigate() != nil:
		n := in.GetNavigate()
		if n.Target == nil {
			return
		}
		s.navTarget = &civquestv1.Vec3{X: n.Target.X, Y: n.Target.Y, Z: n.Target.Z}
		m := float64(n.SpeedMultiplier)
		if m <= 0 {
			m = 1
		}
		if n.Sprint && m < 1.3 {
			m = 1.3
		}
		s.navSpeed = simWalkPerTick * m
		// The mod's navigator reports arrival on entering the goal box, which for a
		// one-cell target is about half the requested tolerance away (design assumption).
		tol := float64(n.ArrivalTolerance)
		if tol <= 0 {
			tol = 1.5
		}
		s.navTol = math.Max(tol*0.5, math.Min(tol, 0.75))
		s.navState = civquestv1.NavigationState_NAVIGATION_STATE_PATHING
	case in.GetAttack() != nil:
		a := in.GetAttack()
		s.AttackLog = append(s.AttackLog, AttackRecord{Tick: s.Tick, Target: a.TargetEntityId, Flee: a.FleeImmediately, HP: s.HP})
		z := s.zombie(a.TargetEntityId)
		if a.FleeImmediately {
			if z != nil && !z.Dead {
				dx, dz := s.Pos.X-z.Pos.X, s.Pos.Z-z.Pos.Z
				d := math.Max(math.Hypot(dx, dz), 0.1)
				s.navTarget = &civquestv1.Vec3{X: s.Pos.X + dx/d*12, Y: s.Pos.Y, Z: s.Pos.Z + dz/d*12}
				s.navSpeed = simWalkPerTick * 1.3
				s.navTol = 1
				s.navState = civquestv1.NavigationState_NAVIGATION_STATE_PATHING
			}
			return
		}
		if z != nil && !z.Dead && math.Hypot(z.Pos.X-s.Pos.X, z.Pos.Z-s.Pos.Z) <= 3.5 {
			dmg := float32(1)
			switch {
			case s.CountSuffix("_sword") > 0:
				dmg = 4
			case s.CountSuffix("_axe") > 0:
				dmg = 3
			}
			z.HP -= dmg
			if z.HP <= 0 {
				z.Dead = true
			}
		}
	case in.GetInteractBlock() != nil:
		s.applyInteract(in.GetInteractBlock())
	case in.GetBuildBlock() != nil:
		b := in.GetBuildBlock()
		if b.TargetVoxel != nil {
			s.placeBlock(BlockPosKey{X: b.TargetVoxel.X, Y: b.TargetVoxel.Y, Z: b.TargetVoxel.Z}, "minecraft:oak_planks")
		}
	case in.GetCraftItem() != nil:
		if s.cfg.CraftLatency > 0 {
			c := in.GetCraftItem()
			s.CraftIssued = append(s.CraftIssued, SimCraft{Tick: s.Tick, Recipe: strings.TrimPrefix(strings.ToLower(c.RecipeId), "minecraft:"), OK: true})
			s.deferred = append(s.deferred, deferredEffect{at: s.Tick + s.cfg.CraftLatency, fn: func() { s.applyCraft(c) }})
		} else {
			s.CraftIssued = append(s.CraftIssued, SimCraft{Tick: s.Tick, Recipe: strings.TrimPrefix(strings.ToLower(in.GetCraftItem().RecipeId), "minecraft:"), OK: true})
			s.applyCraft(in.GetCraftItem())
		}
	case in.GetUseItem() != nil:
		u := in.GetUseItem()
		if u.IsEatingOrDrinking && u.InventorySlot >= 0 && int(u.InventorySlot) < len(s.inventory) {
			it := s.inventory[u.InventorySlot]
			if strings.Contains(it.ItemId, "beef") || strings.Contains(it.ItemId, "bread") || strings.Contains(it.ItemId, "apple") || strings.Contains(it.ItemId, "carrot") {
				s.RemoveItem(it.ItemId, 1)
				s.Food = int32(math.Min(20, float64(s.Food+6)))
			}
		}
	case in.GetSurveyHorizon() != nil:
		s.pendingSurvey = true
	}
}

func isToolGatedStone(id string) bool {
	return id == "minecraft:stone" || id == "minecraft:cobblestone" || strings.HasSuffix(id, "_ore") || id == "minecraft:andesite" ||
		id == "minecraft:diorite" || id == "minecraft:granite" || id == "minecraft:sandstone"
}

func harvestDrop(id string, hasPick bool) string {
	switch {
	case isToolGatedStone(id):
		if !hasPick {
			return ""
		}
		if id == "minecraft:stone" {
			return "minecraft:cobblestone"
		}
		return id
	case strings.HasSuffix(id, "_leaves"):
		return ""
	case id == "minecraft:grass_block":
		return "minecraft:dirt"
	}
	return id
}

func (s *PlaytestSim) applyInteract(ib *civquestv1.InteractBlockIntent) {
	if ib.Pos == nil {
		return
	}
	k := BlockPosKey{X: ib.Pos.X, Y: ib.Pos.Y, Z: ib.Pos.Z}
	switch ib.InteractionType {
	case civquestv1.InteractBlockIntent_HARVEST_BREAK:
		id, exists := s.World.Blocks[k]
		reach := s.withinReach(k, simBlockReach)
		hasPick := s.CountSuffix("_pickaxe") > 0
		h := SimHarvest{Tick: s.Tick, Cell: k, BlockID: id, Existed: exists, InReach: reach, HadTool: hasPick, Inv: s.InventorySummary()}
		if exists && reach {
			drop := harvestDrop(id, hasPick)
			h.NoDrop = isToolGatedStone(id) && !hasPick
			delete(s.World.Blocks, k)
			delete(s.World.BlockStates, k)
			if drop != "" {
				s.AddItem(drop, 1)
			}
		}
		s.Harvests = append(s.Harvests, h)
	case civquestv1.InteractBlockIntent_PLACE_BLOCK:
		item := ib.BlockOrItemId
		if item == "" {
			item = "minecraft:oak_planks"
		}
		s.placeBlock(k, item)
	}
}

func (s *PlaytestSim) placeBlock(k BlockPosKey, item string) {
	item = normItem(item)
	if _, taken := s.World.Blocks[k]; taken {
		return
	}
	if !s.withinReach(k, simBlockReach+1.5) {
		return
	}
	if !s.RemoveItem(item, 1) {
		return
	}
	s.World.Blocks[k] = item
	s.Placed = append(s.Placed, k)
}

// applyCraft executes a craft intent against VanillaRecipeNodes: every ingredient slot must be
// satisfiable from the inventory, and 3x3 recipes need a crafting table within 4 cells.
func (s *PlaytestSim) applyCraft(c *civquestv1.CraftItemIntent) {
	var node *civquestv1.RecipeNode
	for _, n := range s.recipes {
		if n.RecipeId == c.RecipeId || n.RecipeId == "minecraft:"+strings.TrimPrefix(c.RecipeId, "minecraft:") {
			node = n
			break
		}
	}
	batch := c.BatchCount
	if batch <= 0 {
		batch = 1
	}
	ok := node != nil
	if ok && node.Requires_3X3 && !s.tableNearby() {
		ok = false
	}
	for i := int32(0); i < batch && ok; i++ {
		picks := make([]string, len(node.IngredientSlots))
		have := map[string]int32{}
		for _, it := range s.inventory {
			have[it.ItemId] += it.Count
		}
		for si, sl := range node.IngredientSlots {
			found := false
			for _, id := range sl.AcceptedItemIds {
				if have[id] >= sl.Count {
					picks[si] = id
					have[id] -= sl.Count
					found = true
					break
				}
			}
			if !found {
				ok = false
				break
			}
		}
		if !ok {
			break
		}
		for si, sl := range node.IngredientSlots {
			s.RemoveItem(picks[si], sl.Count)
		}
		s.AddItem(node.Result.ItemId, node.Result.Count)
	}
	s.Crafts = append(s.Crafts, SimCraft{Tick: s.Tick, Recipe: strings.TrimPrefix(strings.ToLower(c.RecipeId), "minecraft:"), OK: ok})
}

func (s *PlaytestSim) tableNearby() bool {
	f := cellOf(s.Pos.X, s.Pos.Y, s.Pos.Z)
	for dx := int32(-4); dx <= 4; dx++ {
		for dy := int32(-2); dy <= 3; dy++ {
			for dz := int32(-4); dz <= 4; dz++ {
				if s.World.Blocks[BlockPosKey{X: f.X + dx, Y: f.Y + dy, Z: f.Z + dz}] == "minecraft:crafting_table" {
					return true
				}
			}
		}
	}
	return false
}

// ---- analysis helpers ----

// EmergencyEntries counts transitions into the EMERGENCY stage over the recorded steps.
func (s *PlaytestSim) EmergencyEntries() int {
	n := 0
	prev := system1.Stage("")
	for _, st := range s.Steps {
		if st.Stage == system1.StageEmergency && prev != system1.StageEmergency {
			n++
		}
		prev = st.Stage
	}
	return n
}

// Intents returns every intent emitted so far, flattened in order.
func (s *PlaytestSim) Intents() []*civquestv1.AgentIntent {
	var out []*civquestv1.AgentIntent
	for _, st := range s.Steps {
		out = append(out, st.Intents...)
	}
	return out
}

// RevisitRatio returns the fraction of visited 2x2-block cells that were stepped into on more
// than one distinct frame run (a cell re-entered after leaving it counts as a revisit). It is the
// metric behind the no-retread scenario (cq-dcx.4/.6): 0 = never retraced, 1 = every cell revisited.
func RevisitRatio(path []SimVec) float64 {
	const cell = 2.0
	type key struct{ x, z int32 }
	seen := map[key]bool{}
	entries, revisits := 0, 0
	var last key
	have := false
	for _, p := range path {
		k := key{int32(math.Floor(p.X / cell)), int32(math.Floor(p.Z / cell))}
		if have && k == last {
			continue
		}
		entries++
		if seen[k] {
			revisits++
		}
		seen[k] = true
		last, have = k, true
	}
	if entries == 0 {
		return 0
	}
	return float64(revisits) / float64(entries)
}

// Path returns the recorded position of every step.
func (s *PlaytestSim) Path() []SimVec {
	out := make([]SimVec, 0, len(s.Steps))
	for _, st := range s.Steps {
		out = append(out, st.Pos)
	}
	return out
}

// withinReach is vanilla's interaction check, implemented independently of the production
// code under test: eye (feet + 1.62) to the NEAREST POINT of the block's unit AABB.
func (s *PlaytestSim) withinReach(k BlockPosKey, reach float64) bool {
	axis := func(p float64, lo int32) float64 {
		l := float64(lo)
		switch {
		case p < l:
			return l - p
		case p > l+1:
			return p - l - 1
		}
		return 0
	}
	dx, dy, dz := axis(s.Pos.X, k.X), axis(s.Pos.Y+simEyeHeight, k.Y), axis(s.Pos.Z, k.Z)
	return dx*dx+dy*dy+dz*dz <= reach*reach+1e-9
}
