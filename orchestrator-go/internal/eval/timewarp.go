package eval

import (
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/agent"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
	"github.com/btposey/civquest/orchestrator-go/internal/system2"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// BlockPosKey identifies a 3D discrete voxel block coordinate.
type BlockPosKey struct {
	X int32
	Y int32
	Z int32
}

func posToKey(pos *civquestv1.BlockPosProto) BlockPosKey {
	if pos == nil {
		return BlockPosKey{}
	}
	return BlockPosKey{X: pos.X, Y: pos.Y, Z: pos.Z}
}

func vecToKey(v *civquestv1.Vec3) BlockPosKey {
	if v == nil {
		return BlockPosKey{}
	}
	return BlockPosKey{
		X: int32(math.Floor(v.X)),
		Y: int32(math.Floor(v.Y)),
		Z: int32(math.Floor(v.Z)),
	}
}

// TimeWarpConfig configures the simulated time-warp physical world and cognitive agent harness.
type TimeWarpConfig struct {
	AgentID              string
	ClanID               string
	IdentityConfig       *xdg.AgentIdentityConfig
	InitialTick          int64
	InitialTimeOfDay     int64
	TickDelta            int64 // default: 4 ticks (5 Hz telemetry matching Netty gRPC)
	InitialPosition      *civquestv1.Vec3
	InitialInventory     []*civquestv1.ItemStackProto
	InitialBlocks        []*civquestv1.VisibleBlock
	InitialEntities      []*civquestv1.VisibleEntity
	Weather              civquestv1.Weather
	WalkSpeed            float64 // blocks per tick (default: 0.215 = 4.3 m/s)
	HungerDecayRateTicks int64   // ticks per hunger point lost (default: 800)
	Runtime              *agent.AgentRuntime
	ContemplationService *system2.ContemplationService
	AgentDB              *db.AgentDB
	WorkDir              string
}

// TimeWarpSimulation simulates the physical world feedback, environmental stimuli, and
// time progression (day/night, hunger, physics, voxel interactions) for CivQuest agent runtimes
// in milliseconds without needing a running Minecraft Java server.
type TimeWarpSimulation struct {
	cfg                       TimeWarpConfig
	runtime                   *agent.AgentRuntime
	tempDir                   string
	currentTick               int64
	timeOfDay                 int64
	weather                   civquestv1.Weather
	pos                       *civquestv1.Vec3
	yaw                       float32
	pitch                     float32
	health                    float32
	maxHealth                 float32
	foodLevel                 int32
	stamina                   float32
	isSleeping                bool
	isOnFire                  bool
	navState                  civquestv1.NavigationState
	navTarget                 *civquestv1.Vec3
	navSpeed                  float64
	navArrivalTol             float64
	inventory                 []*civquestv1.ItemStackProto
	blocks                    map[BlockPosKey]string
	blockStates               map[BlockPosKey]string
	entities                  []*civquestv1.VisibleEntity
	injectedChats             []*civquestv1.ChatEvent
	lastHarvestTick           int64
	lastCraftTick             int64
	hungerAcc                 float64
	pendingSurvey             *civquestv1.SurveyHorizonIntent
	pendingSurveyCompleteTick int64
	pendingSurveyOrigin       *civquestv1.Vec3

	// Detectors
	OscillationDetector *FSMOscillationDetector
	StallDetector       *NavigationStallDetector
	BlueprintDetector   *BlueprintAdvancementDetector
	AzimuthDetector     *SectorAzimuthLockDetector

	// Metrics
	AllIntents              []*civquestv1.AgentIntent
	IntentsThisStep         []*civquestv1.AgentIntent
	ActionOccurredThisStep  bool
	PavingIntentsCount      int
	DepositedContainerItems []*civquestv1.ItemStackProto
}

// NewTimeWarpSimulation initializes a high-speed time-warp virtual world and agent harness.
func NewTimeWarpSimulation(cfg TimeWarpConfig) (*TimeWarpSimulation, error) {
	if cfg.AgentID == "" {
		cfg.AgentID = "warp_agent"
	}
	if cfg.ClanID == "" {
		cfg.ClanID = "pioneers"
	}
	if cfg.IdentityConfig == nil {
		cfg.IdentityConfig = xdg.DefaultAgentIdentityConfig(cfg.AgentID)
		cfg.IdentityConfig.ClanID = cfg.ClanID
	}
	if cfg.TickDelta <= 0 {
		cfg.TickDelta = 4 // 5 Hz
	}
	if cfg.WalkSpeed <= 0 {
		cfg.WalkSpeed = 0.215 // 4.3 m/s / 20 tps
	}
	if cfg.HungerDecayRateTicks <= 0 {
		cfg.HungerDecayRateTicks = 800
	}
	if cfg.InitialPosition == nil {
		cfg.InitialPosition = &civquestv1.Vec3{X: 0.5, Y: 64.0, Z: 0.5}
	}

	var tempDir string
	rt := cfg.Runtime
	if rt == nil {
		var err error
		tempDir, err = os.MkdirTemp(cfg.WorkDir, "civquest-timewarp-"+cfg.AgentID+"-*")
		if err != nil {
			return nil, fmt.Errorf("failed creating temp dir for timewarp: %w", err)
		}
		resolver := xdg.NewResolver(xdg.WithCustomRoot(tempDir))
		_ = resolver.InitAgentDirectories(cfg.AgentID)

		agentDB := cfg.AgentDB
		if agentDB == nil {
			agentDB, err = db.OpenAgentDB(resolver.AgentDataDir(cfg.AgentID))
			if err != nil {
				_ = os.RemoveAll(tempDir)
				return nil, fmt.Errorf("failed opening AgentDB: %w", err)
			}
		}

		rt, err = agent.NewRuntime(agent.Config{
			AgentID:              cfg.AgentID,
			ClanID:               cfg.ClanID,
			IdentityConfig:       cfg.IdentityConfig,
			Resolver:             resolver,
			AgentDB:              agentDB,
			ContemplationService: cfg.ContemplationService,
			IntentQueueSize:      512,
		})
		if err != nil {
			_ = os.RemoveAll(tempDir)
			return nil, fmt.Errorf("failed creating AgentRuntime: %w", err)
		}
	}

	sim := &TimeWarpSimulation{
		cfg:                     cfg,
		runtime:                 rt,
		tempDir:                 tempDir,
		currentTick:             cfg.InitialTick,
		timeOfDay:               cfg.InitialTimeOfDay,
		weather:                 cfg.Weather,
		pos:                     &civquestv1.Vec3{X: cfg.InitialPosition.X, Y: cfg.InitialPosition.Y, Z: cfg.InitialPosition.Z},
		health:                  20.0,
		maxHealth:               20.0,
		foodLevel:               20,
		stamina:                 100.0,
		navState:                civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		navSpeed:                cfg.WalkSpeed,
		navArrivalTol:           2.0,
		inventory:               make([]*civquestv1.ItemStackProto, 0),
		blocks:                  make(map[BlockPosKey]string),
		blockStates:             make(map[BlockPosKey]string),
		entities:                make([]*civquestv1.VisibleEntity, 0),
		injectedChats:           make([]*civquestv1.ChatEvent, 0),
		AllIntents:              make([]*civquestv1.AgentIntent, 0),
		DepositedContainerItems: make([]*civquestv1.ItemStackProto, 0),
		OscillationDetector:     NewFSMOscillationDetector(1200, 2),
		StallDetector:           NewNavigationStallDetector(600, 1.0),
	}

	// Copy initial inventory
	for _, it := range cfg.InitialInventory {
		if it != nil {
			sim.inventory = append(sim.inventory, &civquestv1.ItemStackProto{
				Slot:       it.Slot,
				ItemId:     it.ItemId,
				Count:      it.Count,
				Durability: it.Durability,
			})
		}
	}

	// Copy initial blocks
	for _, b := range cfg.InitialBlocks {
		if b != nil && b.Pos != nil {
			sim.blocks[posToKey(b.Pos)] = b.BlockId
		}
	}

	// Copy initial entities
	for _, e := range cfg.InitialEntities {
		if e != nil {
			sim.entities = append(sim.entities, e)
		}
	}

	// Register FSM transition callback on runtime's AgentFSM to feed the oscillation detector
	if fsm := rt.AgentFSM(); fsm != nil {
		// AgentRuntime creates NewAgentFSM with internal transition logger;
		// wrap transition observer via helper
		sim.attachFSMObserver()
	}

	return sim, nil
}

func (sim *TimeWarpSimulation) attachFSMObserver() {
	if sim == nil || sim.runtime == nil {
		return
	}
	// We observe transitions after each Tick
}

// Close cleans up temporary storage and resources.
func (sim *TimeWarpSimulation) Close() error {
	if sim == nil {
		return nil
	}
	if sim.runtime != nil {
		sim.runtime.Stop()
	}
	if sim.tempDir != "" {
		_ = os.RemoveAll(sim.tempDir)
	}
	return nil
}

// Runtime returns the underlying AgentRuntime instance.
func (sim *TimeWarpSimulation) Runtime() *agent.AgentRuntime {
	return sim.runtime
}

// CurrentTick returns the simulation's current world tick.
func (sim *TimeWarpSimulation) CurrentTick() int64 {
	return sim.currentTick
}

// CurrentTimeOfDay returns the simulation's current time of day in [0, 24000).
func (sim *TimeWarpSimulation) CurrentTimeOfDay() int64 {
	return ((sim.timeOfDay % 24000) + 24000) % 24000
}

// CurrentPosition returns the virtual agent's current position.
func (sim *TimeWarpSimulation) CurrentPosition() *civquestv1.Vec3 {
	return &civquestv1.Vec3{X: sim.pos.X, Y: sim.pos.Y, Z: sim.pos.Z}
}

// CurrentFSMState returns the active FSM state of the agent.
func (sim *TimeWarpSimulation) CurrentFSMState() system1.State {
	if sim == nil || sim.runtime == nil || sim.runtime.AgentFSM() == nil {
		return system1.StateBootstrap
	}
	return sim.runtime.AgentFSM().CurrentState()
}

// SetPosition sets the virtual agent's position.
func (sim *TimeWarpSimulation) SetPosition(x, y, z float64) {
	sim.pos.X = x
	sim.pos.Y = y
	sim.pos.Z = z
}

// SetWeather sets the virtual world's weather.
func (sim *TimeWarpSimulation) SetWeather(w civquestv1.Weather) {
	sim.weather = w
}

// AddBlock places a block into the simulated voxel world.
func (sim *TimeWarpSimulation) AddBlock(x, y, z int32, blockID string) {
	sim.blocks[BlockPosKey{X: x, Y: y, Z: z}] = blockID
}

// AddBlockWithState places a block with an optional BlockStateString into the simulated voxel world.
func (sim *TimeWarpSimulation) AddBlockWithState(x, y, z int32, blockID, state string) {
	k := BlockPosKey{X: x, Y: y, Z: z}
	sim.blocks[k] = blockID
	if state != "" {
		sim.blockStates[k] = state
	} else {
		delete(sim.blockStates, k)
	}
}

// LoadSyntheticWorld loads a complete SyntheticStartingWorld layout into the simulation.
func (sim *TimeWarpSimulation) LoadSyntheticWorld(world *SyntheticStartingWorld) {
	if world == nil {
		return
	}
	sim.blocks = make(map[BlockPosKey]string, len(world.Blocks))
	sim.blockStates = make(map[BlockPosKey]string, len(world.BlockStates))
	for k, v := range world.Blocks {
		sim.blocks[k] = v
	}
	for k, v := range world.BlockStates {
		sim.blockStates[k] = v
	}
	if world.AssignedBeds != nil {
		if bed, ok := world.AssignedBeds[sim.cfg.AgentID]; ok && bed != nil {
			if sim.runtime != nil {
				sim.runtime.SetAssignedBed(bed)
			}
		}
	}
}

// RemoveBlock removes a block from the simulated voxel world.
func (sim *TimeWarpSimulation) RemoveBlock(x, y, z int32) {
	k := BlockPosKey{X: x, Y: y, Z: z}
	delete(sim.blocks, k)
	delete(sim.blockStates, k)
}

// HasBlock checks if a block exists at discrete coordinates.
func (sim *TimeWarpSimulation) HasBlock(x, y, z int32) (string, bool) {
	id, ok := sim.blocks[BlockPosKey{X: x, Y: y, Z: z}]
	return id, ok
}

// AddEntity adds a visible entity to the world.
func (sim *TimeWarpSimulation) AddEntity(e *civquestv1.VisibleEntity) {
	if e != nil {
		sim.entities = append(sim.entities, e)
	}
}

// ClearEntities clears all visible entities from the world.
func (sim *TimeWarpSimulation) ClearEntities() {
	sim.entities = make([]*civquestv1.VisibleEntity, 0)
}

// InjectChat stages an incoming chat message delivered in the next frame.
func (sim *TimeWarpSimulation) InjectChat(sender, text string) {
	sim.injectedChats = append(sim.injectedChats, &civquestv1.ChatEvent{
		SenderName: sender,
		Message:    text,
		Distance:   2.0,
	})
}

// InventoryItemCount returns the quantity of itemId carried in the virtual agent's inventory.
func (sim *TimeWarpSimulation) InventoryItemCount(itemId string) int32 {
	norm := strings.ToLower(strings.TrimSpace(itemId))
	var count int32
	for _, it := range sim.inventory {
		if it != nil && strings.ToLower(strings.TrimSpace(it.ItemId)) == norm {
			count += it.Count
		}
	}
	return count
}

// AddInventoryItem adds count units of itemId into the virtual agent's inventory.
func (sim *TimeWarpSimulation) AddInventoryItem(itemId string, count int32) {
	if count <= 0 || itemId == "" {
		return
	}
	norm := strings.ToLower(strings.TrimSpace(itemId))
	// Try to stack onto existing item stack (< 64)
	for _, it := range sim.inventory {
		if it != nil && strings.ToLower(strings.TrimSpace(it.ItemId)) == norm && it.Count < 64 {
			space := 64 - it.Count
			if count <= space {
				it.Count += count
				return
			}
			it.Count = 64
			count -= space
		}
	}
	// Add new stack into first free slot
	slot := int32(len(sim.inventory))
	sim.inventory = append(sim.inventory, &civquestv1.ItemStackProto{
		Slot:   slot,
		ItemId: itemId,
		Count:  count,
	})
}

// RemoveInventoryItem decrements count units of itemId from inventory.
func (sim *TimeWarpSimulation) RemoveInventoryItem(itemId string, count int32) bool {
	if count <= 0 {
		return true
	}
	if sim.InventoryItemCount(itemId) < count {
		return false
	}
	norm := strings.ToLower(strings.TrimSpace(itemId))
	rem := count
	for _, it := range sim.inventory {
		if it != nil && strings.ToLower(strings.TrimSpace(it.ItemId)) == norm {
			if it.Count <= rem {
				rem -= it.Count
				it.Count = 0
			} else {
				it.Count -= rem
				rem = 0
				break
			}
		}
	}
	// Compact inventory: remove empty stacks
	filtered := make([]*civquestv1.ItemStackProto, 0, len(sim.inventory))
	for _, it := range sim.inventory {
		if it != nil && it.Count > 0 {
			filtered = append(filtered, it)
		}
	}
	sim.inventory = filtered
	return true
}

// BuildTelemetryFrame constructs the sensory TelemetryFrame for the current simulated instant.
func (sim *TimeWarpSimulation) BuildTelemetryFrame() *civquestv1.TelemetryFrame {
	posCopy := &civquestv1.Vec3{X: sim.pos.X, Y: sim.pos.Y, Z: sim.pos.Z}

	// Filter interesting blocks within 32m radius
	var visibleBlocks []*civquestv1.VisibleBlock
	for k, blockID := range sim.blocks {
		bx := float64(k.X) + 0.5
		by := float64(k.Y) + 0.5
		bz := float64(k.Z) + 0.5
		dist := math.Hypot(bx-sim.pos.X, bz-sim.pos.Z)
		if dist <= 32.0 && math.Abs(by-sim.pos.Y) <= 16.0 {
			stateStr := sim.blockStates[k]
			visibleBlocks = append(visibleBlocks, &civquestv1.VisibleBlock{
				Pos:              &civquestv1.BlockPosProto{X: k.X, Y: k.Y, Z: k.Z},
				BlockId:          blockID,
				BlockStateString: stateStr,
			})
		}
	}

	// Filter visible entities within 32m radius
	var visibleEntities []*civquestv1.VisibleEntity
	for _, e := range sim.entities {
		if e == nil {
			continue
		}
		dist := float64(e.Distance)
		if e.Position != nil {
			dist = math.Hypot(e.Position.X-sim.pos.X, e.Position.Z-sim.pos.Z)
		}
		if dist <= 32.0 {
			eCopy := *e
			eCopy.Distance = float32(dist)
			visibleEntities = append(visibleEntities, &eCopy)
		}
	}

	// Copy inventory
	invCopy := make([]*civquestv1.ItemStackProto, 0, len(sim.inventory))
	for _, it := range sim.inventory {
		if it != nil && it.Count > 0 {
			invCopy = append(invCopy, &civquestv1.ItemStackProto{
				Slot:       it.Slot,
				ItemId:     it.ItemId,
				Count:      it.Count,
				Durability: it.Durability,
			})
		}
	}

	// Incoming chats
	var chats []*civquestv1.ChatEvent
	if len(sim.injectedChats) > 0 {
		chats = sim.injectedChats
		sim.injectedChats = nil
	}

	var horizonSurvey *civquestv1.HorizonSurveyReport
	if sim.pendingSurvey != nil && sim.currentTick >= sim.pendingSurveyCompleteTick {
		horizonSurvey = sim.buildSyntheticHorizonSurvey()
		sim.pendingSurvey = nil
		sim.pendingSurveyOrigin = nil
	}

	frame := &civquestv1.TelemetryFrame{
		WorldTick: sim.currentTick,
		TimeOfDay: sim.CurrentTimeOfDay(),
		Weather:   sim.weather,
		Status: &civquestv1.AgentStatus{
			AgentId:         sim.cfg.AgentID,
			Health:          sim.health,
			MaxHealth:       sim.maxHealth,
			FoodLevel:       sim.foodLevel,
			Stamina:         sim.stamina,
			IsSleeping:      sim.isSleeping,
			IsOnFire:        sim.isOnFire,
			Position:        posCopy,
			Yaw:             sim.yaw,
			Pitch:           sim.pitch,
			NavigationState: sim.navState,
			Inventory:       invCopy,
		},
		InterestingBlocks: visibleBlocks,
		VisibleEntities:   visibleEntities,
		IncomingChats:     chats,
		HorizonSurvey:     horizonSurvey,
	}

	return frame
}

func (sim *TimeWarpSimulation) buildSyntheticHorizonSurvey() *civquestv1.HorizonSurveyReport {
	numSectors := int32(36)
	maxRange := float32(64.0)
	if sim.pendingSurvey != nil {
		if sim.pendingSurvey.NumSectors > 0 {
			numSectors = sim.pendingSurvey.NumSectors
		}
		if sim.pendingSurvey.MaxRange > 0 {
			maxRange = sim.pendingSurvey.MaxRange
		}
	}
	ox, oy, oz := sim.pos.X, sim.pos.Y, sim.pos.Z
	if sim.pendingSurveyOrigin != nil {
		ox, oy, oz = sim.pendingSurveyOrigin.X, sim.pendingSurveyOrigin.Y, sim.pendingSurveyOrigin.Z
	}
	sectors := make([]*civquestv1.HorizonSector, 0, numSectors)
	stepDeg := 360.0 / float64(numSectors)
	hasBlocks := len(sim.blocks) > 0
	for i := int32(0); i < numSectors; i++ {
		bearing := float32(float64(i) * stepDeg)
		rad := float64(bearing) * math.Pi / 180.0
		dx := -math.Sin(rad)
		dz := math.Cos(rad)

		hitDist := float64(maxRange)
		feat := "OPEN_PLAINS"
		hitBlockID := "minecraft:air"
		hitX := ox + dx*hitDist
		hitY := oy
		hitZ := oz + dz*hitDist
		occluded := false
		openHorizon := true

		// Scan virtual blocks along this bearing out to maxRange.
		if hasBlocks {
			for k, blkID := range sim.blocks {
				if k.Y < int32(math.Floor(oy))-2 || k.Y > int32(math.Floor(oy))+10 {
					continue
				}
				bx := float64(k.X) + 0.5 - ox
				bz := float64(k.Z) + 0.5 - oz
				proj := bx*dx + bz*dz
				if proj <= 1.0 || proj > hitDist {
					continue
				}
				perp := math.Abs(bx*(-dz) + bz*dx)
				if perp > 1.5 {
					continue
				}
				lower := strings.ToLower(blkID)
				switch {
				case strings.Contains(lower, "_log") || strings.Contains(lower, "_leaves"):
					feat = "FOREST_CANOPY"
				case strings.Contains(lower, "water"):
					feat = "WATER"
				case strings.Contains(lower, "stone") || strings.Contains(lower, "ore"):
					feat = "STONE_OUTCROP"
				default:
					continue
				}
				hitDist = proj
				hitBlockID = blkID
				hitX = float64(k.X) + 0.5
				hitY = float64(k.Y)
				hitZ = float64(k.Z) + 0.5
				openHorizon = false
				occluded = hitDist < 14.0
			}
		}

		sectors = append(sectors, &civquestv1.HorizonSector{
			SectorIndex:    i,
			BearingDeg:     bearing,
			HitDistance:    float32(hitDist),
			FeatureType:    feat,
			HitBlockId:     hitBlockID,
			HitX:           hitX,
			HitY:           hitY,
			HitZ:           hitZ,
			ElevationDelta: float32(hitY - oy),
			Occluded:       occluded,
			OpenHorizon:    openHorizon,
		})
	}
	return &civquestv1.HorizonSurveyReport{
		OriginX:       ox,
		OriginY:       oy,
		OriginZ:       oz,
		CompletedTick: sim.currentTick,
		MaxRange:      maxRange,
		Sectors:       sectors,
	}
}

// Step advances the simulation by deltaTicks, processes telemetry through the agent runtime,
// intercepts emitted intents, and applies simulated physical world feedback.
func (sim *TimeWarpSimulation) Step(deltaTicks int64) error {
	if deltaTicks <= 0 {
		deltaTicks = sim.cfg.TickDelta
	}

	// 1. Advance physical motion along active path
	sim.advancePhysics(deltaTicks)

	// 2. Advance biological vitals (hunger decay)
	sim.advanceBiology(deltaTicks)

	// 3. Advance time
	sim.currentTick += deltaTicks
	sim.timeOfDay += deltaTicks

	// Record FSM state before tick
	stateBefore := sim.CurrentFSMState()

	// 4. Generate TelemetryFrame and feed to AgentRuntime
	frame := sim.BuildTelemetryFrame()
	sim.runtime.HandleTelemetryFrame(frame)

	// Record FSM state after tick and track transitions
	stateAfter := sim.CurrentFSMState()
	if stateAfter != stateBefore {
		sim.OscillationDetector.RecordTransition(stateBefore, stateAfter, sim.currentTick, "HFSM state tick")
	}

	// 5. Intercept emitted AgentIntents from runtime's IntentQueue
	sim.IntentsThisStep = sim.drainIntents()
	sim.ActionOccurredThisStep = false

	for _, intent := range sim.IntentsThisStep {
		sim.AllIntents = append(sim.AllIntents, intent)
		sim.applyIntentFeedback(intent)
	}

	if sim.pendingSurvey != nil {
		sim.ActionOccurredThisStep = true
	}

	// 6. Run detectors
	routine := system1.RoutineDawnAwakening
	if s1 := sim.runtime.System1State(); s1 != nil {
		routine = s1.GetCurrentRoutine()
	}
	sim.StallDetector.OnStep(
		sim.currentTick,
		sim.pos,
		sim.navState,
		sim.CurrentFSMState(),
		routine,
		sim.isSleeping,
		sim.ActionOccurredThisStep,
	)

	return nil
}

func (sim *TimeWarpSimulation) advancePhysics(deltaTicks int64) {
	if sim.navState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING && sim.navTarget != nil {
		dx := sim.navTarget.X - sim.pos.X
		dz := sim.navTarget.Z - sim.pos.Z
		dist := math.Hypot(dx, dz)

		stepDist := sim.navSpeed * float64(deltaTicks)
		if dist <= sim.navArrivalTol || dist <= stepDist {
			// Arrived at target waypoint
			sim.pos.X = sim.navTarget.X
			sim.pos.Z = sim.navTarget.Z
			if sim.navTarget.Y > 0 {
				sim.pos.Y = sim.navTarget.Y
			}
			sim.navState = civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED
			sim.navTarget = nil
			sim.ActionOccurredThisStep = true
		} else {
			// Move incrementally toward target
			sim.pos.X += (dx / dist) * stepDist
			sim.pos.Z += (dz / dist) * stepDist
			if sim.navTarget.Y > 0 && math.Abs(sim.navTarget.Y-sim.pos.Y) > 0.01 {
				dy := sim.navTarget.Y - sim.pos.Y
				yStep := (stepDist / dist) * dy
				if math.Abs(yStep) > math.Abs(dy) {
					sim.pos.Y = sim.navTarget.Y
				} else {
					sim.pos.Y += yStep
				}
			}
			sim.ActionOccurredThisStep = true
		}
	}
}

func (sim *TimeWarpSimulation) advanceBiology(deltaTicks int64) {
	if sim.cfg.HungerDecayRateTicks > 0 {
		sim.hungerAcc += float64(deltaTicks) / float64(sim.cfg.HungerDecayRateTicks)
		if sim.hungerAcc >= 1.0 {
			lost := int32(sim.hungerAcc)
			sim.foodLevel -= lost
			sim.hungerAcc -= float64(lost)
			if sim.foodLevel < 0 {
				sim.foodLevel = 0
			}
		}
	}
}

func (sim *TimeWarpSimulation) drainIntents() []*civquestv1.AgentIntent {
	var intents []*civquestv1.AgentIntent
	q := sim.runtime.IntentQueue()
	for {
		select {
		case intent := <-q:
			if intent != nil {
				intents = append(intents, intent)
			}
		default:
			return intents
		}
	}
}

func (sim *TimeWarpSimulation) applyIntentFeedback(intent *civquestv1.AgentIntent) {
	if intent == nil {
		return
	}

	// 0. Horizon Survey Intent (ADR-032)
	if sh := intent.GetSurveyHorizon(); sh != nil {
		numSectors := sh.NumSectors
		if numSectors <= 0 {
			numSectors = 36
		}
		ticksPerSec := sh.TicksPerSector
		if ticksPerSec <= 0 {
			ticksPerSec = 2
		}
		sim.pendingSurvey = sh
		sim.pendingSurveyCompleteTick = sim.currentTick + int64(numSectors*ticksPerSec)
		sim.pendingSurveyOrigin = &civquestv1.Vec3{X: sim.pos.X, Y: sim.pos.Y, Z: sim.pos.Z}
		sim.ActionOccurredThisStep = true
	}

	// 1. Navigation Intent
	if nav := intent.GetNavigate(); nav != nil && nav.Target != nil {
		sim.navTarget = nav.Target
		sim.navState = civquestv1.NavigationState_NAVIGATION_STATE_PATHING
		speed := float64(nav.SpeedMultiplier)
		if speed <= 0 {
			speed = 1.0
		}
		sim.navSpeed = speed * sim.cfg.WalkSpeed
		tol := float64(nav.ArrivalTolerance)
		if tol <= 0 {
			tol = 2.0
		}
		sim.navArrivalTol = tol

		if sim.AzimuthDetector != nil {
			sim.AzimuthDetector.OnNavigationWaypoint(nav.Target, sim.currentTick)
		}
	}

	// 2. Interact Block Intent
	if ib := intent.GetInteractBlock(); ib != nil {
		pos := ib.Pos
		switch ib.InteractionType {
		case civquestv1.InteractBlockIntent_HARVEST_BREAK:
			if pos != nil {
				k := posToKey(pos)
				blockID, ok := sim.blocks[k]
				if ok {
					delete(sim.blocks, k)
					dropItem := resolveHarvestDrop(blockID)
					sim.AddInventoryItem(dropItem, 1)
					sim.ActionOccurredThisStep = true
				}
			}

		case civquestv1.InteractBlockIntent_PLACE_BLOCK:
			if pos != nil {
				k := posToKey(pos)
				blockID := ib.BlockOrItemId
				if blockID == "" {
					blockID = "minecraft:oak_planks"
				}
				sim.blocks[k] = blockID
				sim.RemoveInventoryItem(blockID, 1)
				sim.ActionOccurredThisStep = true

				if sim.BlueprintDetector != nil {
					sim.BlueprintDetector.OnBlockPlaced(pos, blockID, sim.currentTick, sim.BlueprintDetector.Count())
				}
			}

		case civquestv1.InteractBlockIntent_SLEEP_IN_BED:
			sim.isSleeping = true
			sim.ActionOccurredThisStep = true

		case civquestv1.InteractBlockIntent_WAKE_UP:
			sim.isSleeping = false
			sim.ActionOccurredThisStep = true

		case civquestv1.InteractBlockIntent_PAVE_ROAD_PATH:
			if pos != nil {
				k := posToKey(pos)
				sim.blocks[k] = "minecraft:dirt_path"
				sim.PavingIntentsCount++
				sim.ActionOccurredThisStep = true
			}
		}
	}

	// 3. Craft Item Intent
	if craft := intent.GetCraftItem(); craft != nil {
		recipe := strings.ToLower(craft.RecipeId)
		switch {
		case strings.Contains(recipe, "oak_planks") || strings.Contains(recipe, "planks"):
			// 1 log -> 4 planks
			if sim.RemoveFirstMatchingLog() {
				sim.AddInventoryItem("minecraft:oak_planks", 4)
				sim.ActionOccurredThisStep = true
			}
		case strings.Contains(recipe, "crafting_table"):
			// 4 planks -> 1 crafting table
			if sim.RemoveInventoryItem("minecraft:oak_planks", 4) {
				sim.AddInventoryItem("minecraft:crafting_table", 1)
				sim.ActionOccurredThisStep = true
			}
		case strings.Contains(recipe, "campfire"):
			// 3 logs/planks/sticks -> 1 campfire
			if sim.RemoveInventoryItem("minecraft:oak_planks", 3) || sim.RemoveFirstMatchingLog() {
				sim.AddInventoryItem("minecraft:campfire", 1)
				sim.ActionOccurredThisStep = true
			}
		case strings.Contains(recipe, "wooden_axe"):
			// 3 planks + 2 sticks -> 1 wooden axe
			if sim.RemoveInventoryItem("minecraft:oak_planks", 3) {
				sim.AddInventoryItem("minecraft:wooden_axe", 1)
				sim.ActionOccurredThisStep = true
			}
		case strings.Contains(recipe, "wooden_sword"):
			// 2 planks + 1 stick -> 1 wooden sword
			if sim.RemoveInventoryItem("minecraft:oak_planks", 2) {
				sim.AddInventoryItem("minecraft:wooden_sword", 1)
				sim.ActionOccurredThisStep = true
			}
		case strings.Contains(recipe, "stone_sword"):
			// 2 cobblestone + 1 stick -> 1 stone sword
			if sim.RemoveInventoryItem("minecraft:cobblestone", 2) {
				sim.AddInventoryItem("minecraft:stone_sword", 1)
				sim.ActionOccurredThisStep = true
			}
		}
	}

	// 4. Use Item Intent (Eating)
	if ui := intent.GetUseItem(); ui != nil && ui.IsEatingOrDrinking {
		slot := ui.InventorySlot
		if slot >= 0 && int(slot) < len(sim.inventory) {
			it := sim.inventory[slot]
			if it != nil && it.Count > 0 {
				it.Count--
				sim.foodLevel += 8
				if sim.foodLevel > 20 {
					sim.foodLevel = 20
				}
				sim.ActionOccurredThisStep = true
			}
		}
	}

	// 5. Transfer Container Intent
	if tc := intent.GetTransferContainer(); tc != nil {
		if tc.DepositIntoContainer {
			for _, item := range tc.Items {
				if item != nil && item.Count > 0 {
					sim.RemoveInventoryItem(item.ItemId, item.Count)
					sim.DepositedContainerItems = append(sim.DepositedContainerItems, item)
				}
			}
			sim.ActionOccurredThisStep = true
		}
	}

	// 6. Create Written Book Intent
	if cwb := intent.GetCreateBook(); cwb != nil {
		sim.AddInventoryItem("minecraft:written_book", 1)
		sim.ActionOccurredThisStep = true
	}
}

// RemoveFirstMatchingLog removes 1 log of any wood type from inventory.
func (sim *TimeWarpSimulation) RemoveFirstMatchingLog() bool {
	for _, it := range sim.inventory {
		if it != nil && it.Count > 0 {
			id := strings.ToLower(it.ItemId)
			if strings.HasSuffix(id, "_log") || strings.HasSuffix(id, "_wood") || strings.HasSuffix(id, "_stem") {
				it.Count--
				return true
			}
		}
	}
	return false
}

func resolveHarvestDrop(blockID string) string {
	id := strings.ToLower(strings.TrimSpace(blockID))
	switch {
	case strings.HasSuffix(id, "_log") || strings.HasSuffix(id, "_wood") || strings.HasSuffix(id, "_stem"):
		return blockID
	case strings.HasSuffix(id, "_planks"):
		return blockID
	case id == "minecraft:stone":
		return "minecraft:cobblestone"
	case id == "minecraft:dirt_path" || id == "minecraft:grass_block":
		return "minecraft:dirt"
	case strings.Contains(id, "wheat"):
		return "minecraft:wheat"
	case strings.Contains(id, "carrot"):
		return "minecraft:carrot"
	default:
		return blockID
	}
}

// RunTicks advances the simulation by totalTicks using cfg.TickDelta steps.
func (sim *TimeWarpSimulation) RunTicks(totalTicks int64) error {
	delta := sim.cfg.TickDelta
	if delta <= 0 {
		delta = 4
	}
	for elapsed := int64(0); elapsed < totalTicks; elapsed += delta {
		if err := sim.Step(delta); err != nil {
			return err
		}
	}
	return nil
}

// RunUntil advances the simulation until predicate returns true or maxTicks is reached.
func (sim *TimeWarpSimulation) RunUntil(predicate func(sim *TimeWarpSimulation) bool, maxTicks int64) (bool, error) {
	delta := sim.cfg.TickDelta
	if delta <= 0 {
		delta = 4
	}
	for elapsed := int64(0); elapsed < maxTicks; elapsed += delta {
		if predicate(sim) {
			return true, nil
		}
		if err := sim.Step(delta); err != nil {
			return false, err
		}
	}
	return predicate(sim), nil
}
