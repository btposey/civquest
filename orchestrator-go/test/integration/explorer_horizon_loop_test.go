package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/agent"
	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
	"github.com/btposey/civquest/orchestrator-go/internal/system2"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
	_ "modernc.org/sqlite"
)

type worldBlockKey struct {
	X, Y, Z int32
}

type forestStand struct {
	Name    string
	CenterX float64
	CenterZ float64
	Radius  float64
	Species string
}

type waterPond struct {
	CenterX float64
	CenterZ float64
	Radius  float64
}

type ravineZone struct {
	MinX, MaxX float64
	MinZ, MaxZ float64
	RimZ       float64
}

type simWorld struct {
	mu               sync.Mutex
	blocks           map[worldBlockKey]string
	forests          []forestStand
	ponds            []waterPond
	ravine           *ravineZone
	zombie           *civquestv1.VisibleEntity
	hasPlaytestTraps bool
	biomeID          string
	entities         []*civquestv1.VisibleEntity
	customRaycast    func(ox, oy, oz float64, completedTick int64, numSectors int32, maxRange float32) *civquestv1.HorizonSurveyReport
	isBlocked        func(x, y, z float64) (blocked bool, reason string)
	isEnclosed       func(pos civquestv1.Vec3, blocks map[worldBlockKey]string) bool
}

func newPlaytest0849World(includeTraps, includeZombie, includeRavine bool) *simWorld {
	w := &simWorld{
		blocks:           make(map[worldBlockKey]string),
		hasPlaytestTraps: includeTraps,
	}

	if includeTraps {
		// 1. Unreachable elevated canopy oak log trap at (-36, 94, 12) (+8 blocks above Y=86 ridge).
		w.blocks[worldBlockKey{X: -36, Y: 94, Z: 12}] = "minecraft:oak_log"
		for dx := int32(-1); dx <= 1; dx++ {
			for dz := int32(-1); dz <= 1; dz++ {
				if dx == 0 && dz == 0 {
					continue
				}
				w.blocks[worldBlockKey{X: -36 + dx, Y: 94, Z: 12 + dz}] = "minecraft:oak_leaves"
			}
		}
		w.blocks[worldBlockKey{X: -36, Y: 95, Z: 12}] = "minecraft:oak_leaves"

		// 2. Unreachable hollow camp chest (-44, 79, 17) & beds (-46, 79, 17), (-47, 79, 17).
		w.blocks[worldBlockKey{X: -44, Y: 79, Z: 17}] = "minecraft:chest"
		w.blocks[worldBlockKey{X: -46, Y: 79, Z: 17}] = "minecraft:red_bed"
		w.blocks[worldBlockKey{X: -47, Y: 79, Z: 17}] = "minecraft:red_bed"
	}

	if includeZombie {
		// 3. Harmless distant zombie standing 18m East of spawn (-30, 86, 0).
		w.zombie = &civquestv1.VisibleEntity{
			EntityId:    9001,
			Uuid:        "00000000-0000-0000-0000-000000009001",
			EntityType:  "minecraft:zombie",
			DisplayName: "Zombie",
			Position:    &civquestv1.Vec3{X: -12, Y: 86, Z: 0},
			Health:      20,
			IsHostile:   true,
		}
	}

	if includeRavine {
		// 4. Wide impassable ravine blocking North sector across z in [-38, -24], x in [-45, -15].
		w.ravine = &ravineZone{
			MinX: -45.0,
			MaxX: -15.0,
			MinZ: -38.0,
			MaxZ: -24.0,
			RimZ: -22.0,
		}
	}

	// 5. Reachable forest stands at 40-55m away across multiple compass sectors + nearby ponds.
	w.forests = []forestStand{
		{Name: "east_oak", CenterX: 14, CenterZ: 0, Radius: 9.0, Species: "oak"},
		{Name: "se_oak", CenterX: 4, CenterZ: 34, Radius: 9.0, Species: "oak"},
		{Name: "south_birch", CenterX: -30, CenterZ: 46, Radius: 9.0, Species: "birch"},
		{Name: "sw_oak", CenterX: -64, CenterZ: 34, Radius: 9.0, Species: "oak"},
		{Name: "west_oak", CenterX: -76, CenterZ: -2, Radius: 9.0, Species: "oak"},
		{Name: "nw_birch", CenterX: -62, CenterZ: -34, Radius: 9.0, Species: "birch"},
		{Name: "ne_oak", CenterX: 2, CenterZ: -34, Radius: 9.0, Species: "oak"},
		{Name: "far_north_oak", CenterX: -30, CenterZ: -48, Radius: 9.0, Species: "oak"},
	}

	w.ponds = []waterPond{
		{CenterX: 22, CenterZ: 8, Radius: 6.5},
		{CenterX: 10, CenterZ: 42, Radius: 6.5},
		{CenterX: -24, CenterZ: 54, Radius: 6.5},
		{CenterX: -72, CenterZ: 40, Radius: 6.5},
		{CenterX: -84, CenterZ: -8, Radius: 6.5},
		{CenterX: -68, CenterZ: -42, Radius: 6.5},
		{CenterX: 10, CenterZ: -40, Radius: 6.5},
		{CenterX: -22, CenterZ: -56, Radius: 6.5},
	}

	for _, stand := range w.forests {
		w.populateForestTrees(stand)
	}
	for _, pond := range w.ponds {
		w.populatePond(pond)
	}

	return w
}

func (w *simWorld) populateForestTrees(stand forestStand) {
	logID := "minecraft:" + stand.Species + "_log"
	leavesID := "minecraft:" + stand.Species + "_leaves"
	offsets := [][2]int32{{0, 0}, {4, 2}, {-4, -2}}
	cx := int32(math.Round(stand.CenterX))
	cz := int32(math.Round(stand.CenterZ))
	for _, off := range offsets {
		tx := cx + off[0]
		tz := cz + off[1]
		for dy := int32(0); dy <= 3; dy++ {
			w.blocks[worldBlockKey{X: tx, Y: 86 + dy, Z: tz}] = logID
		}
		for dx := int32(-1); dx <= 1; dx++ {
			for dz := int32(-1); dz <= 1; dz++ {
				if dx == 0 && dz == 0 {
					continue
				}
				w.blocks[worldBlockKey{X: tx + dx, Y: 89, Z: tz + dz}] = leavesID
			}
		}
		w.blocks[worldBlockKey{X: tx, Y: 90, Z: tz}] = leavesID
	}
}

func (w *simWorld) populatePond(pond waterPond) {
	cx := int32(math.Round(pond.CenterX))
	cz := int32(math.Round(pond.CenterZ))
	for dx := int32(-3); dx <= 3; dx++ {
		for dz := int32(-3); dz <= 3; dz++ {
			if float64(dx*dx+dz*dz) <= pond.Radius*pond.Radius {
				w.blocks[worldBlockKey{X: cx + dx, Y: 85, Z: cz + dz}] = "minecraft:water"
			}
		}
	}
}

func (w *simWorld) isRavineBlockedPoint(x, z float64) bool {
	if w.ravine == nil {
		return false
	}
	return x >= w.ravine.MinX && x <= w.ravine.MaxX && z <= w.ravine.RimZ && z >= w.ravine.MinZ
}

func (w *simWorld) isUnreachableTrapTarget(target *civquestv1.Vec3) bool {
	if target == nil || !w.hasPlaytestTraps {
		return false
	}
	if target.Y >= 91.0 || target.Y <= 81.0 {
		return true
	}
	// Canopy oak log at (-36, 94, 12)
	if math.Hypot(target.X-(-36.5), target.Z-12.5) <= 2.5 && target.Y >= 90.0 {
		return true
	}
	// Hollow camp chest (-44, 79, 17) & beds (-46..-47, 79, 17)
	if math.Hypot(target.X-(-44.5), target.Z-17.5) <= 4.0 && target.Y <= 82.0 {
		return true
	}
	if math.Hypot(target.X-(-46.5), target.Z-17.5) <= 4.0 && target.Y <= 82.0 {
		return true
	}
	return false
}

func (w *simWorld) raycastHorizon(ox, oy, oz float64, completedTick int64, numSectors int32, maxRange float32) *civquestv1.HorizonSurveyReport {
	if w.customRaycast != nil {
		return w.customRaycast(ox, oy, oz, completedTick, numSectors, maxRange)
	}
	if numSectors <= 0 {
		numSectors = 36
	}
	if maxRange <= 0 {
		maxRange = 64.0
	}
	stepDeg := 360.0 / float64(numSectors)
	sectors := make([]*civquestv1.HorizonSector, 0, numSectors)

	for i := int32(0); i < numSectors; i++ {
		bearing := float32(float64(i) * stepDeg)
		rad := float64(bearing) * math.Pi / 180.0
		// Exact Minecraft yaw convention: dx = -sin(rad), dz = cos(rad)
		dx := -math.Sin(rad)
		dz := math.Cos(rad)

		bestDist := float64(maxRange)
		bestFeat := "OPEN_PLAINS"
		bestBlock := "minecraft:air"
		bestY := oy
		occluded := false
		openHorizon := true

		for dist := 2.0; dist <= float64(maxRange); dist += 1.5 {
			rx := ox + dx*dist
			rz := oz + dz*dist

			// Ravine rim drop-off / canyon wall when standing near the rim
			if w.ravine != nil && rx >= w.ravine.MinX && rx <= w.ravine.MaxX && rz <= w.ravine.MaxZ && rz >= w.ravine.MinZ {
				// Ravine spans across this ray; if close to rim, represent impassable canyon edge
				if dist < 12.0 && oz > w.ravine.RimZ-1.5 {
					bestDist = dist
					bestFeat = "RIDGE_OBSTRUCTION"
					bestBlock = "minecraft:stone"
					bestY = oy - 8.0
					occluded = true
					openHorizon = false
					break
				}
			}

			hitForest := false
			for _, f := range w.forests {
				if math.Hypot(rx-f.CenterX, rz-f.CenterZ) <= f.Radius {
					if dist < bestDist {
						bestDist = dist
						bestFeat = "FOREST_CANOPY"
						bestBlock = "minecraft:" + f.Species + "_leaves"
						bestY = oy + 4.0
						openHorizon = false
					}
					hitForest = true
					break
				}
			}
			if hitForest {
				break
			}

			hitWater := false
			for _, p := range w.ponds {
				if math.Hypot(rx-p.CenterX, rz-p.CenterZ) <= p.Radius {
					if dist < bestDist {
						bestDist = dist
						bestFeat = "WATER"
						bestBlock = "minecraft:water"
						bestY = oy - 1.0
						openHorizon = false
					}
					hitWater = true
					break
				}
			}
			if hitWater {
				break
			}
		}

		sectors = append(sectors, &civquestv1.HorizonSector{
			SectorIndex:    i,
			BearingDeg:     bearing,
			HitDistance:    float32(bestDist),
			FeatureType:    bestFeat,
			HitBlockId:     bestBlock,
			HitX:           ox + dx*bestDist,
			HitY:           bestY,
			HitZ:           oz + dz*bestDist,
			ElevationDelta: float32(bestY - oy),
			Occluded:       occluded,
			OpenHorizon:    openHorizon,
		})
	}

	return &civquestv1.HorizonSurveyReport{
		OriginX:       ox,
		OriginY:       oy,
		OriginZ:       oz,
		CompletedTick: completedTick,
		MaxRange:      maxRange,
		Sectors:       sectors,
	}
}

func vanillaTestRecipeNodes() []*civquestv1.RecipeNode {
	planks := []string{"minecraft:birch_planks", "minecraft:oak_planks", "minecraft:spruce_planks"}
	logs := []string{"minecraft:birch_log", "minecraft:oak_log", "minecraft:spruce_log"}
	coals := []string{"minecraft:charcoal", "minecraft:coal"}
	cobble := []string{"minecraft:cobbled_deepslate", "minecraft:cobblestone"}

	return []*civquestv1.RecipeNode{
		{
			RecipeId:     "minecraft:oak_planks",
			RecipeType:   "minecraft:crafting_shapeless",
			Requires_3X3: false,
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{
				{AcceptedItemIds: []string{"minecraft:oak_log"}, Count: 1},
			},
			Result: &civquestv1.ItemStackProto{ItemId: "minecraft:oak_planks", Count: 4},
		},
		{
			RecipeId:     "minecraft:birch_planks",
			RecipeType:   "minecraft:crafting_shapeless",
			Requires_3X3: false,
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{
				{AcceptedItemIds: []string{"minecraft:birch_log"}, Count: 1},
			},
			Result: &civquestv1.ItemStackProto{ItemId: "minecraft:birch_planks", Count: 4},
		},
		{
			RecipeId:     "minecraft:stick",
			RecipeType:   "minecraft:crafting_shaped",
			Requires_3X3: false,
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{
				{AcceptedItemIds: planks, Count: 2},
			},
			Result: &civquestv1.ItemStackProto{ItemId: "minecraft:stick", Count: 4},
		},
		{
			RecipeId:     "minecraft:crafting_table",
			RecipeType:   "minecraft:crafting_shaped",
			Requires_3X3: false,
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{
				{AcceptedItemIds: planks, Count: 4},
			},
			Result: &civquestv1.ItemStackProto{ItemId: "minecraft:crafting_table", Count: 1},
		},
		{
			RecipeId:         "minecraft:wooden_axe",
			RecipeType:       "minecraft:crafting_shaped",
			WorkstationBlock: "minecraft:crafting_table",
			Requires_3X3:     true,
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{
				{AcceptedItemIds: planks, Count: 3},
				{AcceptedItemIds: []string{"minecraft:stick"}, Count: 2},
			},
			Result: &civquestv1.ItemStackProto{ItemId: "minecraft:wooden_axe", Count: 1},
		},
		{
			RecipeId:         "minecraft:wooden_sword",
			RecipeType:       "minecraft:crafting_shaped",
			WorkstationBlock: "minecraft:crafting_table",
			Requires_3X3:     true,
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{
				{AcceptedItemIds: planks, Count: 2},
				{AcceptedItemIds: []string{"minecraft:stick"}, Count: 1},
			},
			Result: &civquestv1.ItemStackProto{ItemId: "minecraft:wooden_sword", Count: 1},
		},
		{
			RecipeId:         "minecraft:stone_sword",
			RecipeType:       "minecraft:crafting_shaped",
			WorkstationBlock: "minecraft:crafting_table",
			Requires_3X3:     true,
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{
				{AcceptedItemIds: cobble, Count: 2},
				{AcceptedItemIds: []string{"minecraft:stick"}, Count: 1},
			},
			Result: &civquestv1.ItemStackProto{ItemId: "minecraft:stone_sword", Count: 1},
		},
		{
			RecipeId:     "minecraft:torch",
			RecipeType:   "minecraft:crafting_shaped",
			Requires_3X3: false,
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{
				{AcceptedItemIds: coals, Count: 1},
				{AcceptedItemIds: []string{"minecraft:stick"}, Count: 1},
			},
			Result: &civquestv1.ItemStackProto{ItemId: "minecraft:torch", Count: 4},
		},
		{
			RecipeId:         "minecraft:campfire",
			RecipeType:       "minecraft:crafting_shaped",
			WorkstationBlock: "minecraft:crafting_table",
			Requires_3X3:     true,
			IngredientSlots: []*civquestv1.RecipeIngredientSlot{
				{AcceptedItemIds: []string{"minecraft:stick"}, Count: 3},
				{AcceptedItemIds: coals, Count: 1},
				{AcceptedItemIds: logs, Count: 3},
			},
			Result: &civquestv1.ItemStackProto{ItemId: "minecraft:campfire", Count: 1},
		},
	}
}

type surveyEmitRecord struct {
	Tick        int64
	X, Y, Z     float64
	InFlight    bool
	QueuedDepth int
}

type chatEmitRecord struct {
	Tick    int64
	Channel civquestv1.ChatChannel
	Message string
}

type navTargetRecord struct {
	Tick    int64
	Target  *civquestv1.Vec3
	Stuck   bool
	TrapHit bool
}

type attackEmitRecord struct {
	Tick            int64
	TargetEntityID  int32
	RaiseShield     bool
	FleeImmediately bool
}

type useItemEmitRecord struct {
	Tick               int64
	InventorySlot      int32
	IsEatingOrDrinking bool
}

type simAgent struct {
	id          string
	runtime     *agent.AgentRuntime
	agentDB     *db.AgentDB
	world       *simWorld
	recipeGraph *crafting.RecipeGraph
	recipesByID map[string]*civquestv1.RecipeNode

	pos       civquestv1.Vec3
	spawnPos  civquestv1.Vec3
	yaw       float32
	health    float32
	foodLevel int32
	inventory []*civquestv1.ItemStackProto

	navState       civquestv1.NavigationState
	navTarget      *civquestv1.Vec3
	navTol         float64
	navStallTicks  int64
	navStallTarget bool

	surveyQueue        []*civquestv1.SurveyHorizonIntent
	surveyActive       *civquestv1.SurveyHorizonIntent
	surveyCompleteTick int64
	surveyOrigin       civquestv1.Vec3
	pendingReport      *civquestv1.HorizonSurveyReport
	lastReceipt        *civquestv1.IntentReceipt
	nextBatchID        int64
	navFrame           uint64 // top activity frame when the in-flight Navigate was drained (receipt frame_id)

	// Telemetry & diagnostic counters for RED assertions
	surveyEmissions         []surveyEmitRecord
	surveysCompleted        int
	surveyWhileInFlight     int
	maxConsecSameSpotSurvey int
	curConsecSameSpotSurvey int
	lastSurveyEmitPos       *civquestv1.Vec3

	trapStuckCount        int
	ravineStuckCount      int
	ravineRimResurveyed   bool
	ravineFirstStuckTick  int64
	ravinePostRimBearings []float32
	ravineEscaped         bool

	navHistory        []navTargetRecord
	returnedNearSpawn bool
	maxDistFromSpawn  float64
	farthestPos       civquestv1.Vec3
	chatEmissions     []chatEmitRecord
	harvestedLogs     int
	craftedItems      []string
	observedPhases    []string

	lastDamageTaken   *civquestv1.DamageEvent
	attackEmissions   []attackEmitRecord
	useItemEmissions  []useItemEmitRecord
	barrierStuckCount int
	barrierRimPos     *civquestv1.Vec3
	barrierResurveyed bool
	visitedPositions  []civquestv1.Vec3
}

func newSimAgent(
	t *testing.T,
	id string,
	clanID string,
	spawn civquestv1.Vec3,
	res *xdg.Resolver,
	civicDB *db.CivicRegistryDB,
	contemplator *system2.ContemplationService,
	world *simWorld,
	preSeedTraps bool,
) *simAgent {
	t.Helper()

	if err := res.InitAgentDirectories(id); err != nil {
		t.Fatalf("InitAgentDirectories(%s): %v", id, err)
	}
	cfg := xdg.DefaultAgentIdentityConfig(id)
	cfg.ClanID = clanID
	cfg.Profession = "EXPLORER"
	cfg.Stage = "BOOTSTRAP"
	if err := res.SaveAgentConfig(cfg); err != nil {
		t.Fatalf("SaveAgentConfig(%s): %v", id, err)
	}

	agentDB, err := db.OpenAgentDB(res.AgentDataDir(id))
	if err != nil {
		t.Fatalf("OpenAgentDB(%s): %v", id, err)
	}

	if preSeedTraps {
		ctx := context.Background()
		trapPOIs := []db.AgentPOI{
			{
				POIID:            system1.FormatPOIID(system1.POICategoryContainer, -44, 79, 17),
				Category:         system1.POICategoryContainer,
				BlockID:          "minecraft:chest",
				X:                -43.5,
				Y:                79.0,
				Z:                17.5,
				AccessX:          -43.5,
				AccessY:          79.0,
				AccessZ:          16.5,
				ElevationLayer:   system1.ElevationDugout,
				SafetyScore:      1.0,
				DiscoveredTick:   0,
				LastVerifiedTick: 0,
				Reliability:      1.0,
				IsActive:         true,
			},
			{
				POIID:            system1.FormatPOIID(system1.POICategoryHabitation, -46, 79, 17),
				Category:         system1.POICategoryHabitation,
				BlockID:          "minecraft:red_bed",
				X:                -45.5,
				Y:                79.0,
				Z:                17.5,
				AccessX:          -45.5,
				AccessY:          79.0,
				AccessZ:          16.5,
				ElevationLayer:   system1.ElevationDugout,
				SafetyScore:      1.0,
				DiscoveredTick:   0,
				LastVerifiedTick: 0,
				Reliability:      1.0,
				IsActive:         true,
			},
			{
				POIID:            system1.FormatPOIID(system1.POICategoryHabitation, -47, 79, 17),
				Category:         system1.POICategoryHabitation,
				BlockID:          "minecraft:red_bed",
				X:                -46.5,
				Y:                79.0,
				Z:                17.5,
				AccessX:          -46.5,
				AccessY:          79.0,
				AccessZ:          16.5,
				ElevationLayer:   system1.ElevationDugout,
				SafetyScore:      1.0,
				DiscoveredTick:   0,
				LastVerifiedTick: 0,
				Reliability:      1.0,
				IsActive:         true,
			},
		}
		for _, p := range trapPOIs {
			if err := agentDB.AddOrUpdatePOI(ctx, p); err != nil {
				t.Fatalf("seed trap POI %s: %v", p.POIID, err)
			}
		}
	}

	rt, err := agent.NewRuntime(agent.Config{
		AgentID:              id,
		ClanID:               clanID,
		IdentityConfig:       cfg,
		Resolver:             res,
		AgentDB:              agentDB,
		CivicRegistryDB:      civicDB,
		ContemplationService: contemplator,
		SocketPath:           filepath.Join(res.AgentDataDir(id), id+".sock"),
	})
	if err != nil {
		t.Fatalf("NewRuntime(%s): %v", id, err)
	}

	nodes := vanillaTestRecipeNodes()
	rg := crafting.NewRecipeGraph(nodes)
	rt.System1State().SetRecipeGraph(rg)

	byID := make(map[string]*civquestv1.RecipeNode, len(nodes))
	for _, n := range nodes {
		byID[strings.ToLower(n.RecipeId)] = n
	}

	return &simAgent{
		id:          id,
		runtime:     rt,
		agentDB:     agentDB,
		world:       world,
		recipeGraph: rg,
		recipesByID: byID,
		pos:         spawn,
		spawnPos:    spawn,
		health:      20.0,
		foodLevel:   20,
		navState:    civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		farthestPos: spawn,
	}
}

func (a *simAgent) close() {
	if a.runtime != nil {
		a.runtime.AwaitPOIPersistence()
		a.runtime.Stop()
	}
	if a.agentDB != nil {
		_ = a.agentDB.Close()
	}
}

func (a *simAgent) buildFrame(worldTick, timeOfDay int64) *civquestv1.TelemetryFrame {
	a.world.mu.Lock()
	var blocks []*civquestv1.VisibleBlock
	for k, id := range a.world.blocks {
		bx := float64(k.X) + 0.5
		bz := float64(k.Z) + 0.5
		if math.Hypot(bx-a.pos.X, bz-a.pos.Z) <= 16.0 && math.Abs(float64(k.Y)+0.5-a.pos.Y) <= 16.0 {
			blocks = append(blocks, &civquestv1.VisibleBlock{
				Pos:     &civquestv1.BlockPosProto{X: k.X, Y: k.Y, Z: k.Z},
				BlockId: id,
			})
		}
	}
	sort.Slice(blocks, func(i, j int) bool {
		if blocks[i].Pos.X != blocks[j].Pos.X {
			return blocks[i].Pos.X < blocks[j].Pos.X
		}
		if blocks[i].Pos.Y != blocks[j].Pos.Y {
			return blocks[i].Pos.Y < blocks[j].Pos.Y
		}
		return blocks[i].Pos.Z < blocks[j].Pos.Z
	})

	var entities []*civquestv1.VisibleEntity
	if a.world.zombie != nil && a.world.zombie.Position != nil {
		dist := math.Hypot(a.world.zombie.Position.X-a.pos.X, a.world.zombie.Position.Z-a.pos.Z)
		if dist <= 24.0 {
			zCopy := *a.world.zombie
			zCopy.Distance = float32(dist)
			entities = append(entities, &zCopy)
		}
	}
	for _, ent := range a.world.entities {
		if ent != nil && ent.Position != nil {
			dist := math.Hypot(ent.Position.X-a.pos.X, ent.Position.Z-a.pos.Z)
			if dist <= 24.0 {
				eCopy := *ent
				eCopy.Distance = float32(dist)
				entities = append(entities, &eCopy)
			}
		}
	}
	enclosed := false
	if a.world.isEnclosed != nil {
		enclosed = a.world.isEnclosed(a.pos, a.world.blocks)
	}
	biomeID := a.world.biomeID
	if biomeID == "" {
		biomeID = "minecraft:plains"
	}
	a.world.mu.Unlock()

	invCopy := make([]*civquestv1.ItemStackProto, 0, len(a.inventory))
	for _, it := range a.inventory {
		if it != nil && it.Count > 0 {
			invCopy = append(invCopy, &civquestv1.ItemStackProto{
				Slot:   it.Slot,
				ItemId: it.ItemId,
				Count:  it.Count,
			})
		}
	}

	frame := &civquestv1.TelemetryFrame{
		WorldTick: worldTick,
		TimeOfDay: ((timeOfDay % 24000) + 24000) % 24000,
		Weather:   civquestv1.Weather_WEATHER_CLEAR,
		BiomeId:   biomeID,
		Status: &civquestv1.AgentStatus{
			AgentId:            a.id,
			Health:             a.health,
			MaxHealth:          20.0,
			FoodLevel:          a.foodLevel,
			Stamina:            100.0,
			Position:           &civquestv1.Vec3{X: a.pos.X, Y: a.pos.Y, Z: a.pos.Z},
			Yaw:                a.yaw,
			NavigationState:    a.navState,
			NavigationEnclosed: enclosed,
			Inventory:          invCopy,
		},
		VisibleEntities:   entities,
		InterestingBlocks: blocks,
		Receipt:           a.lastReceipt,
		HorizonSurvey:     a.pendingReport,
		LastDamageTaken:   a.lastDamageTaken,
	}
	a.lastReceipt = nil
	a.pendingReport = nil
	a.lastDamageTaken = nil
	return frame
}

func (a *simAgent) stepPhysics(worldTick, dt int64) {
	// 1. Advance active 360-degree HorizonSurvey if one is executing (72 ticks default).
	if a.surveyActive != nil {
		a.yaw = float32(math.Mod(float64(a.yaw)+20.0, 360.0))
		if worldTick >= a.surveyCompleteTick {
			a.pendingReport = a.world.raycastHorizon(
				a.surveyOrigin.X, a.surveyOrigin.Y, a.surveyOrigin.Z,
				worldTick, a.surveyActive.NumSectors, a.surveyActive.MaxRange,
			)
			a.surveysCompleted++
			a.surveyActive = nil
			a.nextBatchID++
			a.lastReceipt = &civquestv1.IntentReceipt{
				BatchId: a.nextBatchID,
				Status:  civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED,
			}
			// Start next queued duplicate if Go spammed SurveyHorizonIntent!
			if len(a.surveyQueue) > 0 {
				next := a.surveyQueue[0]
				a.surveyQueue = a.surveyQueue[1:]
				a.startSurvey(next, worldTick)
			}
		}
		return
	}
	if len(a.surveyQueue) > 0 {
		next := a.surveyQueue[0]
		a.surveyQueue = a.surveyQueue[1:]
		a.startSurvey(next, worldTick)
		return
	}

	// 2. Advance navigation at ~0.25 blocks/tick (1.0 block per 4-tick frame).
	if a.navState == civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED {
		a.navState = civquestv1.NavigationState_NAVIGATION_STATE_IDLE
	}
	if a.navState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING && a.navTarget != nil {
		if a.navStallTarget {
			a.navStallTicks += dt
			if a.navStallTicks >= 16 {
				a.lastReceipt = a.stuckReceipt("Navigation stuck", false)
				a.navState = civquestv1.NavigationState_NAVIGATION_STATE_STUCK
				a.navTarget = nil
				a.navStallTicks = 0
				a.navStallTarget = false
				a.trapStuckCount++
				if len(a.navHistory) > 0 {
					a.navHistory[len(a.navHistory)-1].Stuck = true
				}
			}
			return
		}

		dx := a.navTarget.X - a.pos.X
		dz := a.navTarget.Z - a.pos.Z
		dist := math.Hypot(dx, dz)
		stepDist := 0.25 * float64(dt)
		if dist <= math.Max(a.navTol, 1.5) || dist <= stepDist {
			if a.world.isRavineBlockedPoint(a.navTarget.X, a.navTarget.Z) {
				a.handleRavineRimHit(worldTick)
				return
			}
			if a.world.isBlocked != nil {
				if blocked, reason := a.world.isBlocked(a.navTarget.X, a.navTarget.Y, a.navTarget.Z); blocked {
					a.handleCustomBarrierHit(worldTick, reason)
					return
				}
			}
			a.pos.X = a.navTarget.X
			a.pos.Y = a.navTarget.Y
			a.pos.Z = a.navTarget.Z
			a.navState = civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED
			a.navTarget = nil
			a.nextBatchID++
			a.lastReceipt = &civquestv1.IntentReceipt{
				BatchId: a.nextBatchID,
				Status:  civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED,
			}
		} else {
			nx := a.pos.X + (dx/dist)*stepDist
			nz := a.pos.Z + (dz/dist)*stepDist
			if a.world.isRavineBlockedPoint(nx, nz) {
				a.navStallTicks += dt
				if a.navStallTicks >= 16 {
					a.handleRavineRimHit(worldTick)
				}
				return
			}
			if a.world.isBlocked != nil {
				if blocked, reason := a.world.isBlocked(nx, a.pos.Y, nz); blocked {
					a.navStallTicks += dt
					if a.navStallTicks >= 16 {
						a.handleCustomBarrierHit(worldTick, reason)
					}
					return
				}
			}
			a.pos.X = nx
			a.pos.Z = nz
			a.yaw = float32(math.Mod(math.Atan2(-dx, dz)*180.0/math.Pi+360.0, 360.0))
		}
	}

	a.visitedPositions = append(a.visitedPositions, a.pos)
	dSpawn := math.Hypot(a.pos.X-a.spawnPos.X, a.pos.Z-a.spawnPos.Z)
	if dSpawn > a.maxDistFromSpawn {
		a.maxDistFromSpawn = dSpawn
		a.farthestPos = a.pos
	}
	if a.ravineFirstStuckTick > 0 && a.world.ravine != nil {
		if a.pos.X < a.world.ravine.MinX-2.0 || a.pos.X > a.world.ravine.MaxX+2.0 || a.pos.Z < a.world.ravine.MinZ-2.0 || a.pos.Z > a.world.ravine.RimZ+10.0 {
			a.ravineEscaped = true
		}
	}
}

// stuckReceipt mirrors the NeoForge mod after cq-2sr.14: a stuck or enclosed Move fails
// immediately with a FAILED receipt ("Navigation stuck") that carries MotorFacts and the
// frame_id of the frame that issued it (Go's Recover decides what to do about it).
func (a *simAgent) stuckReceipt(reason string, enclosed bool) *civquestv1.IntentReceipt {
	a.nextBatchID++
	facts := &civquestv1.MotorFacts{Stuck: true, Enclosed: enclosed, StuckTicks: 16}
	if a.navTarget != nil {
		dx, dz := a.navTarget.X-a.pos.X, a.navTarget.Z-a.pos.Z
		facts.BlockedBearingDeg = float32(math.Mod(math.Atan2(-dx, dz)*180.0/math.Pi+360.0, 360.0))
		facts.Progress = math.Hypot(dx, dz)
	}
	return &civquestv1.IntentReceipt{
		BatchId:       a.nextBatchID,
		FrameId:       a.navFrame,
		Status:        civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED,
		FailureReason: reason,
		Facts:         facts,
	}
}

func (a *simAgent) handleRavineRimHit(worldTick int64) {
	a.lastReceipt = a.stuckReceipt("Navigation stuck", false)
	a.navState = civquestv1.NavigationState_NAVIGATION_STATE_STUCK
	a.navTarget = nil
	a.navStallTicks = 0
	a.navStallTarget = false
	a.ravineStuckCount++
	if a.ravineFirstStuckTick == 0 {
		a.ravineFirstStuckTick = worldTick
	}
	if len(a.navHistory) > 0 {
		a.navHistory[len(a.navHistory)-1].Stuck = true
	}
}

func (a *simAgent) handleCustomBarrierHit(worldTick int64, reason string) {
	a.lastReceipt = a.stuckReceipt("Navigation stuck", false)
	a.navState = civquestv1.NavigationState_NAVIGATION_STATE_STUCK
	a.navTarget = nil
	a.navStallTicks = 0
	a.navStallTarget = false
	a.barrierStuckCount++
	if a.barrierRimPos == nil {
		a.barrierRimPos = &civquestv1.Vec3{X: a.pos.X, Y: a.pos.Y, Z: a.pos.Z}
	}
	if len(a.navHistory) > 0 {
		a.navHistory[len(a.navHistory)-1].Stuck = true
	}
}

func (a *simAgent) startSurvey(intent *civquestv1.SurveyHorizonIntent, worldTick int64) {
	numSec := intent.NumSectors
	if numSec <= 0 {
		numSec = 36
	}
	tps := intent.TicksPerSector
	if tps <= 0 {
		tps = 2
	}
	a.surveyActive = intent
	a.surveyOrigin = a.pos
	a.surveyCompleteTick = worldTick + int64(numSec)*int64(tps)
	a.navState = civquestv1.NavigationState_NAVIGATION_STATE_IDLE
	a.navTarget = nil
}

func (a *simAgent) applyIntents(intents []*civquestv1.AgentIntent, worldTick int64) {
	for _, in := range intents {
		if in == nil {
			continue
		}
		switch act := in.Action.(type) {
		case *civquestv1.AgentIntent_SurveyHorizon:
			sh := act.SurveyHorizon
			inFlight := a.surveyActive != nil
			if inFlight {
				a.surveyWhileInFlight++
				a.surveyQueue = append(a.surveyQueue, sh)
			} else {
				a.startSurvey(sh, worldTick)
			}
			a.surveyEmissions = append(a.surveyEmissions, surveyEmitRecord{
				Tick:        worldTick,
				X:           a.pos.X,
				Y:           a.pos.Y,
				Z:           a.pos.Z,
				InFlight:    inFlight,
				QueuedDepth: len(a.surveyQueue),
			})
			if a.lastSurveyEmitPos != nil && math.Hypot(a.pos.X-a.lastSurveyEmitPos.X, a.pos.Z-a.lastSurveyEmitPos.Z) < 6.0 {
				a.curConsecSameSpotSurvey++
			} else {
				a.curConsecSameSpotSurvey = 1
				a.lastSurveyEmitPos = &civquestv1.Vec3{X: a.pos.X, Y: a.pos.Y, Z: a.pos.Z}
			}
			if a.curConsecSameSpotSurvey > a.maxConsecSameSpotSurvey {
				a.maxConsecSameSpotSurvey = a.curConsecSameSpotSurvey
			}
			if a.ravineFirstStuckTick > 0 && a.world.ravine != nil && math.Abs(a.pos.Z-(a.world.ravine.RimZ)) <= 4.0 {
				a.ravineRimResurveyed = true
			}
			if a.barrierRimPos != nil && math.Hypot(a.pos.X-a.barrierRimPos.X, a.pos.Z-a.barrierRimPos.Z) <= 6.0 {
				a.barrierResurveyed = true
			}

		case *civquestv1.AgentIntent_Navigate:
			nav := act.Navigate
			if nav == nil || nav.Target == nil {
				continue
			}
			// Do not preempt an active survey unless motor cancels; track nav request.
			tgt := &civquestv1.Vec3{X: nav.Target.X, Y: nav.Target.Y, Z: nav.Target.Z}
			isTrap := a.world.isUnreachableTrapTarget(tgt)
			a.navHistory = append(a.navHistory, navTargetRecord{
				Tick:    worldTick,
				Target:  tgt,
				TrapHit: isTrap,
			})
			if a.maxDistFromSpawn > 25.0 && math.Hypot(tgt.X-a.spawnPos.X, tgt.Z-a.spawnPos.Z) <= 15.0 {
				a.returnedNearSpawn = true
			}
			if a.ravineFirstStuckTick > 0 && worldTick > a.ravineFirstStuckTick {
				b := float32(math.Mod(math.Atan2(-(tgt.X-a.pos.X), tgt.Z-a.pos.Z)*180.0/math.Pi+360.0, 360.0))
				a.ravinePostRimBearings = append(a.ravinePostRimBearings, b)
			}
			a.navTarget = tgt
			a.navTol = float64(nav.ArrivalTolerance)
			if a.navTol <= 0 {
				a.navTol = 2.0
			}
			a.navState = civquestv1.NavigationState_NAVIGATION_STATE_PATHING
			a.navStallTicks = 0
			a.navStallTarget = isTrap
			if a.runtime != nil {
				if fr := a.runtime.AgentFSM().ActivityFrames(); len(fr) > 0 {
					a.navFrame = fr[len(fr)-1].ID
				}
			}
			// Enclosed in a pocket: the mod fails the Move immediately (it no longer digs out).
			enclosed := false
			if a.world.isEnclosed != nil {
				a.world.mu.Lock()
				enclosed = a.world.isEnclosed(a.pos, a.world.blocks)
				a.world.mu.Unlock()
			}
			if enclosed && a.world.isBlocked != nil {
				// The mod only fails the Move when its first step is walled in; once the
				// way out is dug the Move proceeds even though the pocket is not yet left.
				dx, dz := tgt.X-a.pos.X, tgt.Z-a.pos.Z
				if d := math.Hypot(dx, dz); d > 0 {
					step := math.Min(1.0, d)
					if blocked, _ := a.world.isBlocked(a.pos.X+dx/d*step, a.pos.Y, a.pos.Z+dz/d*step); !blocked {
						enclosed = false
					}
				}
			}
			if enclosed {
				a.lastReceipt = a.stuckReceipt("Navigation stuck", true)
				a.navState = civquestv1.NavigationState_NAVIGATION_STATE_STUCK
				a.navTarget = nil
			}

		case *civquestv1.AgentIntent_InteractBlock:
			ib := act.InteractBlock
			if ib == nil || ib.Pos == nil {
				continue
			}
			switch ib.InteractionType {
			case civquestv1.InteractBlockIntent_HARVEST_BREAK:
				bx := float64(ib.Pos.X) + 0.5
				by := float64(ib.Pos.Y) + 0.5
				bz := float64(ib.Pos.Z) + 0.5
				dist3D := math.Sqrt(math.Pow(bx-a.pos.X, 2) + math.Pow(by-a.pos.Y, 2) + math.Pow(bz-a.pos.Z, 2))
				a.world.mu.Lock()
				key := worldBlockKey{X: ib.Pos.X, Y: ib.Pos.Y, Z: ib.Pos.Z}
				blkID, exists := a.world.blocks[key]
				if exists && dist3D <= 4.5 && math.Abs(float64(ib.Pos.Y)-a.pos.Y) <= 4.5 {
					delete(a.world.blocks, key)
					a.world.mu.Unlock()
					a.addInventoryItem(blkID, 1)
					if strings.HasSuffix(blkID, "_log") {
						a.harvestedLogs++
					}
					a.nextBatchID++
					a.lastReceipt = &civquestv1.IntentReceipt{
						BatchId: a.nextBatchID,
						Status:  civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED,
					}
				} else {
					a.world.mu.Unlock()
					a.nextBatchID++
					a.lastReceipt = &civquestv1.IntentReceipt{
						BatchId:       a.nextBatchID,
						Status:        civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED,
						FailureReason: "block_unreachable_or_missing",
					}
				}
			case civquestv1.InteractBlockIntent_PLACE_BLOCK:
				itemID := strings.ToLower(strings.TrimSpace(ib.BlockOrItemId))
				if a.consumeInventoryItem(itemID, 1) {
					a.world.mu.Lock()
					a.world.blocks[worldBlockKey{X: ib.Pos.X, Y: ib.Pos.Y, Z: ib.Pos.Z}] = itemID
					a.world.mu.Unlock()
					a.nextBatchID++
					a.lastReceipt = &civquestv1.IntentReceipt{
						BatchId: a.nextBatchID,
						Status:  civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED,
					}
				}
			}

		case *civquestv1.AgentIntent_CraftItem:
			ci := act.CraftItem
			if ci == nil {
				continue
			}
			if a.executeCraft(ci) {
				a.nextBatchID++
				a.lastReceipt = &civquestv1.IntentReceipt{
					BatchId: a.nextBatchID,
					Status:  civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED,
				}
			} else {
				a.nextBatchID++
				a.lastReceipt = &civquestv1.IntentReceipt{
					BatchId:       a.nextBatchID,
					Status:        civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED,
					FailureReason: "craft_missing_ingredients_or_table",
				}
			}

		case *civquestv1.AgentIntent_Attack:
			atk := act.Attack
			if atk != nil {
				a.attackEmissions = append(a.attackEmissions, attackEmitRecord{
					Tick:            worldTick,
					TargetEntityID:  atk.TargetEntityId,
					RaiseShield:     atk.RaiseShield,
					FleeImmediately: atk.FleeImmediately,
				})
				if atk.FleeImmediately {
					// Preempt active motor survey upon emergency flee interrupt (matches NeoForge AgentMotorFSM flush)
					a.surveyActive = nil
					a.surveyQueue = nil
				}
			}

		case *civquestv1.AgentIntent_UseItem:
			ui := act.UseItem
			if ui != nil {
				a.useItemEmissions = append(a.useItemEmissions, useItemEmitRecord{
					Tick:               worldTick,
					InventorySlot:      ui.InventorySlot,
					IsEatingOrDrinking: ui.IsEatingOrDrinking,
				})
			}

		case *civquestv1.AgentIntent_Chat:
			if ch := act.Chat; ch != nil {
				a.chatEmissions = append(a.chatEmissions, chatEmitRecord{
					Tick:    worldTick,
					Channel: ch.TargetChannel,
					Message: ch.Message,
				})
			}
		}
	}
}

func (a *simAgent) addInventoryItem(itemID string, count int32) {
	if count <= 0 || itemID == "" {
		return
	}
	norm := strings.ToLower(strings.TrimSpace(itemID))
	for _, it := range a.inventory {
		if it != nil && strings.ToLower(it.ItemId) == norm && it.Count < 64 {
			add := count
			if it.Count+add > 64 {
				add = 64 - it.Count
			}
			it.Count += add
			count -= add
			if count == 0 {
				return
			}
		}
	}
	for count > 0 {
		add := count
		if add > 64 {
			add = 64
		}
		a.inventory = append(a.inventory, &civquestv1.ItemStackProto{
			Slot:   int32(len(a.inventory)),
			ItemId: norm,
			Count:  add,
		})
		count -= add
	}
}

func (a *simAgent) consumeInventoryItem(itemID string, count int32) bool {
	norm := strings.ToLower(strings.TrimSpace(itemID))
	total := int32(0)
	for _, it := range a.inventory {
		if it != nil && strings.ToLower(it.ItemId) == norm {
			total += it.Count
		}
	}
	if total < count {
		return false
	}
	rem := count
	for _, it := range a.inventory {
		if it != nil && strings.ToLower(it.ItemId) == norm && rem > 0 {
			take := it.Count
			if take > rem {
				take = rem
			}
			it.Count -= take
			rem -= take
		}
	}
	filtered := a.inventory[:0]
	for _, it := range a.inventory {
		if it != nil && it.Count > 0 {
			filtered = append(filtered, it)
		}
	}
	a.inventory = filtered
	return true
}

func (a *simAgent) executeCraft(ci *civquestv1.CraftItemIntent) bool {
	node := a.recipesByID[strings.ToLower(strings.TrimSpace(ci.RecipeId))]
	if node == nil || node.Result == nil {
		return false
	}
	batches := ci.BatchCount
	if batches <= 0 {
		batches = 1
	}
	if node.Requires_3X3 {
		hasTable := false
		a.world.mu.Lock()
		for k, id := range a.world.blocks {
			if id == "minecraft:crafting_table" &&
				math.Hypot(float64(k.X)+0.5-a.pos.X, float64(k.Z)+0.5-a.pos.Z) <= 4.5 {
				hasTable = true
				break
			}
		}
		a.world.mu.Unlock()
		if !hasTable {
			return false
		}
	}
	for b := int32(0); b < batches; b++ {
		for _, slot := range node.IngredientSlots {
			needed := slot.Count
			var chosenID string
			for _, cand := range slot.AcceptedItemIds {
				for _, it := range a.inventory {
					if it != nil && strings.EqualFold(it.ItemId, cand) && it.Count >= needed {
						chosenID = cand
						break
					}
				}
				if chosenID != "" {
					break
				}
			}
			if chosenID == "" || !a.consumeInventoryItem(chosenID, needed) {
				return false
			}
		}
		a.addInventoryItem(node.Result.ItemId, node.Result.Count)
		a.craftedItems = append(a.craftedItems, node.Result.ItemId)
	}
	return true
}

func newStubContemplationServer(action, target, goal string) (*httptest.Server, *system2.Client, *system2.ContemplationService) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := json.Marshal(system2.StrategicContemplationResponse{
			ImmediateGoal:    goal,
			ShortTermGoal:    "EXPLORE_AND_ESTABLISH_OUTPOST",
			LongTermGoal:     "CHART_REGIONAL_ATLAS",
			ReflectiveMemory: "Synthesized expedition directive.",
			Directive: &system2.StrategicDirective{
				Action: action,
				Target: target,
				Reason: "integration test stub",
			},
			Rationale: "Test deterministic directive flow.",
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(system2.ChatCompletionResponse{
			ID: "stub-contemplation",
			Choices: []system2.ChatCompletionChoice{{
				Message:      system2.ChatMessage{Role: "assistant", Content: string(payload)},
				FinishReason: "stop",
			}},
		})
	}))

	client, err := system2.NewClient(
		&xdg.GlobalConfig{LLMGateway: xdg.LLMGatewayConfig{BaseURL: srv.URL + "/v1", Model: "stub-model"}},
		system2.WithHTTPClient(srv.Client()),
	)
	if err != nil {
		srv.Close()
		panic(err)
	}
	return srv, client, system2.NewContemplationService(client)
}

func compassQuadrant(dx, dz float64) string {
	switch {
	case dx >= 0 && dz <= 0:
		return "NE"
	case dx >= 0 && dz > 0:
		return "SE"
	case dx < 0 && dz > 0:
		return "SW"
	default:
		return "NW"
	}
}

func countClanHorizonRows(t *testing.T, civicDBPath string) int {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", civicDBPath)
	if err != nil {
		t.Fatalf("sql.Open(%s): %v", civicDBPath, err)
	}
	defer sqlDB.Close()
	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM clan_horizon_sectors;`).Scan(&count); err != nil {
		t.Fatalf("query clan_horizon_sectors count: %v", err)
	}
	return count
}

func TestIntegration_SixNightExplorers_SurveyDisperseBlacklistAndRendezvous(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	res := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	srv, llmClient, contemplator := newStubContemplationServer(
		system2.DirectiveForageFood,
		"wild berries and plains",
		"FORAGE_FOOD: Secure food reserves at dawn",
	)
	defer srv.Close()
	defer llmClient.Close()

	clanID := "pioneers"
	civicDB, err := db.OpenCivicRegistryDBForClan(res, clanID)
	if err != nil {
		t.Fatalf("OpenCivicRegistryDBForClan: %v", err)
	}
	defer civicDB.Close()

	world := newPlaytest0849World(true, true, true)
	names := []string{"able", "bert", "carl", "dave", "earl", "fred"}
	agents := make([]*simAgent, 0, len(names))
	for i, name := range names {
		offsetX := float64(i%3) - 1.0
		offsetZ := float64(i/3) - 0.5
		sa := newSimAgent(
			t, name, clanID,
			civquestv1.Vec3{X: -30.0 + offsetX, Y: 86.0, Z: 0.0 + offsetZ},
			res, civicDB, contemplator, world, true,
		)
		sa.runtime.System1State().SetRendezvousOrigin(-30.0, 86.0, 0.0)
		agents = append(agents, sa)
	}
	defer func() {
		for _, sa := range agents {
			sa.close()
		}
	}()

	const (
		startTick = int64(4)
		startTOD  = int64(19213)
		tickDelta = int64(4)
		todDelta  = int64(16) // advances 19213 (night) -> 23500 (dawn) -> 3213 (daytime) across 500 frames
		numFrames = 500       // 2,000 world_ticks at 4 ticks per 5 Hz telemetry frame
	)

	directiveFired := false
	for frameIdx := 0; frameIdx < numFrames; frameIdx++ {
		worldTick := startTick + int64(frameIdx)*tickDelta
		tod := (startTOD + int64(frameIdx)*todDelta) % 24000

		for _, sa := range agents {
			sa.stepPhysics(worldTick, tickDelta)
			frame := sa.buildFrame(worldTick, tod)
			sa.runtime.HandleTelemetryFrame(frame)
			sa.observedPhases = append(sa.observedPhases, sa.runtime.AgentFSM().ActiveActivityKind())
			intents := sa.runtime.DrainIntents()
			sa.applyIntents(intents, worldTick)
		}

		// Simulate nighttime System 2 contemplation firing around tick 120 (tod = 19333)
		// returning FORAGE_FOOD directive, matching the 08:49 playtest interaction.
		if !directiveFired && worldTick >= 120 {
			directiveFired = true
			for _, sa := range agents {
				f := sa.buildFrame(worldTick, tod)
				if _, err := sa.runtime.TriggerContemplation(ctx, f); err != nil {
					t.Fatalf("TriggerContemplation(%s): %v", sa.id, err)
				}
			}
		}
	}

	for _, sa := range agents {
		sa.runtime.AwaitPOIPersistence()
	}

	// Diagnostic summary table
	for _, sa := range agents {
		sectors, _ := sa.agentDB.GetRecentHorizonSectors(ctx, 200)
		dir, dirActive := sa.runtime.System1State().ActiveStrategicDirective(startTick + int64(numFrames)*tickDelta)
		firstNav := ""
		if len(sa.navHistory) > 0 && sa.navHistory[0].Target != nil {
			t0 := sa.navHistory[0].Target
			firstNav = fmt.Sprintf("(%.1f,%.1f,%.1f)", t0.X, t0.Y, t0.Z)
		}
		lastNav := ""
		if len(sa.navHistory) > 0 && sa.navHistory[len(sa.navHistory)-1].Target != nil {
			tn := sa.navHistory[len(sa.navHistory)-1].Target
			lastNav = fmt.Sprintf("(%.1f,%.1f,%.1f)", tn.X, tn.Y, tn.Z)
		}
		t.Logf("[RED-DIAG] agent=%s clanSize=%d stage=%s routine=%s phase=%s surveysEmitted=%d surveysCompleted=%d inFlightDup=%d maxConsecSameSpot=%d dbSectors=%d trapStuck=%d navCount=%d firstNav=%s lastNav=%s maxDist=%.1fm pos=(%.1f,%.1f) chats=%d logs=%d dir=(%s,%v) goal=%q",
			sa.id,
			sa.runtime.System1State().GetClanSize(),
			sa.runtime.FSMState(),
			sa.runtime.Routine(),
			sa.runtime.AgentFSM().ActiveActivityKind(),
			len(sa.surveyEmissions),
			sa.surveysCompleted,
			sa.surveyWhileInFlight,
			sa.maxConsecSameSpotSurvey,
			len(sectors),
			sa.trapStuckCount,
			len(sa.navHistory),
			firstNav,
			lastNav,
			sa.maxDistFromSpawn,
			sa.pos.X, sa.pos.Z,
			len(sa.chatEmissions),
			sa.harvestedLogs,
			dir.Action, dirActive,
			sa.runtime.System1State().GetGoals().ImmediateGoal,
		)
	}

	// Assertion 1: All 6 agents emit & complete >= 1 SurveyHorizonIntent and persist >= 36 horizon_sectors.
	for _, sa := range agents {
		if sa.surveysCompleted < 1 {
			t.Errorf("Assertion 1 FAIL: agent %s completed %d surveys, want >= 1", sa.id, sa.surveysCompleted)
		}
		sectors, err := sa.agentDB.GetRecentHorizonSectors(ctx, 200)
		if err != nil {
			t.Errorf("Assertion 1 FAIL: agent %s GetRecentHorizonSectors error: %v", sa.id, err)
		} else if len(sectors) < 36 {
			t.Errorf("Assertion 1 FAIL: agent %s persisted %d horizon_sectors, want >= 36", sa.id, len(sectors))
		}
	}

	// Assertion 2: Zero duplicate survey storms (no >2 SurveyHorizonIntents while in-flight or <6m consecutively).
	for _, sa := range agents {
		if sa.surveyWhileInFlight > 2 {
			t.Errorf("Assertion 2 FAIL: agent %s emitted %d SurveyHorizonIntents while survey already in-flight (want <= 2)",
				sa.id, sa.surveyWhileInFlight)
		}
		if sa.maxConsecSameSpotSurvey > 2 {
			t.Errorf("Assertion 2 FAIL: agent %s emitted %d consecutive SurveyHorizonIntents within <6m of same spot (want <= 2)",
				sa.id, sa.maxConsecSameSpotSurvey)
		}
	}

	// Assertion 3: Unreachable target blacklisting (no agent stuck > 6 times on Y=94 log or Y=79 chest/beds).
	for _, sa := range agents {
		if sa.trapStuckCount > 6 {
			t.Errorf("Assertion 3 FAIL: agent %s stuck %d times on unreachable Y=94/Y=79 traps (want <= 6)",
				sa.id, sa.trapStuckCount)
		}
	}

	// Assertion 4: Real spatial dispersion (>= 4 of 6 travel > 30m along >= 3 distinct compass quadrants).
	dispersedCount := 0
	quadrants := make(map[string]bool)
	for _, sa := range agents {
		if sa.maxDistFromSpawn > 30.0 {
			dispersedCount++
			q := compassQuadrant(sa.farthestPos.X-sa.spawnPos.X, sa.farthestPos.Z-sa.spawnPos.Z)
			quadrants[q] = true
		}
	}
	if dispersedCount < 4 {
		t.Errorf("Assertion 4 FAIL: only %d/6 agents traveled > 30m from spawn (-30, 86, 0), want >= 4", dispersedCount)
	}
	if len(quadrants) < 3 {
		t.Errorf("Assertion 4 FAIL: dispersed across only %d compass quadrants (%v), want >= 3", len(quadrants), quadrants)
	}

	// Assertion 5: Clan rendezvous & collective map (>= 2 return <=20m after outbound, merge >0 rows into clan_horizon_sectors, emit local chat).
	clanRows := countClanHorizonRows(t, res.ClanCivicRegistryDBPath(clanID))
	rendezvousChatAgents := 0
	for _, sa := range agents {
		for _, ch := range sa.chatEmissions {
			if ch.Channel == civquestv1.ChatChannel_CHAT_CHANNEL_LOCAL && strings.Contains(strings.ToLower(ch.Message), "expedition") {
				rendezvousChatAgents++
				break
			}
		}
	}
	if clanRows == 0 {
		t.Errorf("Assertion 5 FAIL: civic_registry.db clan_horizon_sectors has 0 rows, want > 0 from >= 2 returning explorers")
	}
	if rendezvousChatAgents < 2 {
		t.Errorf("Assertion 5 FAIL: only %d agents emitted local rendezvous SendChatIntent, want >= 2", rendezvousChatAgents)
	}
	cands, err := civicDB.RankCollectiveSettlementCandidates(ctx)
	if err != nil {
		t.Errorf("Assertion 5 FAIL: RankCollectiveSettlementCandidates error: %v", err)
	} else {
		maxExplorers := 0
		for _, c := range cands {
			if c.ExplorerCount > maxExplorers {
				maxExplorers = c.ExplorerCount
			}
		}
		if maxExplorers < 2 {
			t.Errorf("Assertion 5 FAIL: max ExplorerCount in collective settlement candidates = %d, want >= 2 (candidates=%+v)",
				maxExplorers, cands)
		}
	}
}

func TestIntegration_ExplorerRavineRim_PauseResurveyAndFlank(t *testing.T) {
	tmpDir := t.TempDir()
	res := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	// World with ravine at z in [-38, -24], x in [-45, -15], and only the far North forest at (-30, 86, -48)
	// plus flanking forests at NW (-62, 86, -34) and NE (2, 86, -34).
	world := &simWorld{
		blocks: make(map[worldBlockKey]string),
		ravine: &ravineZone{
			MinX: -45.0,
			MaxX: -15.0,
			MinZ: -38.0,
			MaxZ: -24.0,
			RimZ: -22.0,
		},
		forests: []forestStand{
			{Name: "far_north_oak", CenterX: -30, CenterZ: -48, Radius: 9.0, Species: "oak"},
			{Name: "nw_birch", CenterX: -62, CenterZ: -34, Radius: 9.0, Species: "birch"},
			{Name: "ne_oak", CenterX: 2, CenterZ: -34, Radius: 9.0, Species: "oak"},
		},
		ponds: []waterPond{
			{CenterX: -68, CenterZ: -42, Radius: 6.5},
			{CenterX: 10, CenterZ: -40, Radius: 6.5},
		},
	}
	for _, f := range world.forests {
		world.populateForestTrees(f)
	}
	for _, p := range world.ponds {
		world.populatePond(p)
	}

	sa := newSimAgent(
		t, "scout", "pioneers",
		civquestv1.Vec3{X: -30.0, Y: 86.0, Z: -10.0},
		res, nil, nil, world, false,
	)
	defer sa.close()

	// Seed an initial survey aimed straight North across the ravine toward (-30, 86, -48)
	// so the explorer commits to heading North into the ravine rim at z = -22.
	const tickDelta = int64(4)
	for frameIdx := 0; frameIdx < 350; frameIdx++ {
		worldTick := int64(4) + int64(frameIdx)*tickDelta
		tod := (2000 + int64(frameIdx)*tickDelta) % 24000
		sa.stepPhysics(worldTick, tickDelta)
		frame := sa.buildFrame(worldTick, tod)
		sa.runtime.HandleTelemetryFrame(frame)
		intents := sa.runtime.DrainIntents()
		sa.applyIntents(intents, worldTick)
	}

	hazards := sa.runtime.System1State().SpatialIndex().FindPOIsInRadius(&sa.pos, system1.POICategoryHazard, 128.0)
	if len(hazards) == 0 {
		// Also check around rim coordinate (-30, 86, -22)
		hazards = sa.runtime.System1State().SpatialIndex().FindPOIsInRadius(&civquestv1.Vec3{X: -30, Y: 86, Z: -22}, system1.POICategoryHazard, 64.0)
	}

	t.Logf("[RED-DIAG] ravine scout: stuckCount=%d hazards=%d rimResurveyed=%v surveys=%d postRimBearings=%v escaped=%v finalPos=(%.1f,%.1f) phase=%s",
		sa.ravineStuckCount, len(hazards), sa.ravineRimResurveyed, len(sa.surveyEmissions),
		sa.ravinePostRimBearings, sa.ravineEscaped, sa.pos.X, sa.pos.Z, sa.runtime.AgentFSM().ActiveActivityKind())

	if sa.ravineStuckCount == 0 {
		t.Fatalf("Test setup error: scout never hit the ravine rim (finalPos=(%.1f,%.1f))", sa.pos.X, sa.pos.Z)
	}
	if len(hazards) == 0 {
		t.Errorf("FAIL: expected HAZARD barrier POI recorded in SpatialIndex upon hitting ravine rim, got 0")
	}
	if !sa.ravineRimResurveyed {
		t.Errorf("FAIL: expected explorer to pause at ravine rim and emit a fresh SurveyHorizonIntent (rimResurveyed=false)")
	}
	if !sa.ravineEscaped {
		t.Errorf("FAIL: expected explorer to select an unobstructed bearing and escape the ravine rim (finalPos=(%.1f,%.1f))", sa.pos.X, sa.pos.Z)
	}
	if sa.ravineStuckCount > 4 {
		t.Errorf("FAIL: explorer paced back and forth hitting ravine rim %d times (want <= 4)", sa.ravineStuckCount)
	}
}

func TestIntegration_SoloExplorer_NeverReturnsToSpawnAndClaimsHomestead(t *testing.T) {
	tmpDir := t.TempDir()
	res := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))

	// Suitable forested + water + open plains valley 48m East & SE from (-30, 86, 0), no trees within 16m.
	world := newPlaytest0849World(false, false, false)

	sa := newSimAgent(
		t, "nomad", "soloists",
		civquestv1.Vec3{X: -30.0, Y: 86.0, Z: 0.0},
		res, nil, nil, world, false,
	)
	defer sa.close()

	const tickDelta = int64(4)
	for frameIdx := 0; frameIdx < 450; frameIdx++ {
		worldTick := int64(4) + int64(frameIdx)*tickDelta
		tod := (2000 + int64(frameIdx)*tickDelta) % 24000
		sa.stepPhysics(worldTick, tickDelta)
		frame := sa.buildFrame(worldTick, tod)
		sa.runtime.HandleTelemetryFrame(frame)
		sa.observedPhases = append(sa.observedPhases, sa.runtime.AgentFSM().ActiveActivityKind())
		intents := sa.runtime.DrainIntents()
		sa.applyIntents(intents, worldTick)
	}

	st := sa.runtime.System1State()
	_, hops, _, _ := sa.runtime.AgentFSM().ExpeditionStatus()
	distHomestead := math.Hypot(st.HomesteadPos[0]-(-30.0), st.HomesteadPos[2]-0.0)
	t.Logf("[RED-DIAG] solo nomad: clanSize=%d surveys=%d/%d hops=%d phase=%s homesteadClaimed=%v homesteadPos=(%.1f,%.1f) distFromSpawn=%.1fm maxDist=%.1fm returnedNearSpawn=%v finalPos=(%.1f,%.1f)",
		st.GetClanSize(), len(sa.surveyEmissions), sa.surveysCompleted, hops,
		sa.runtime.AgentFSM().ActiveActivityKind(), st.HomesteadClaimed, st.HomesteadPos[0], st.HomesteadPos[2],
		distHomestead, sa.maxDistFromSpawn, sa.returnedNearSpawn, sa.pos.X, sa.pos.Z)

	if sa.surveysCompleted < 2 {
		t.Errorf("FAIL: expected chained SURVEY -> TRAVERSE -> SURVEY (>= 2 completed surveys), got %d", sa.surveysCompleted)
	}
	if sa.returnedNearSpawn {
		t.Errorf("FAIL: solo explorer turned back toward spawn (-30, 86, 0) after traveling >25m away")
	}
	if !st.HomesteadClaimed {
		t.Errorf("FAIL: expected HomesteadClaimed == true for solo explorer")
	} else if distHomestead <= 30.0 {
		t.Errorf("FAIL: expected claimed homestead > 30m from spawn (-30, 86, 0), got %.1fm", distHomestead)
	}
}
