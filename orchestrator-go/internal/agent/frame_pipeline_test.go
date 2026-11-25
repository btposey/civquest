package agent

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
	"github.com/btposey/civquest/orchestrator-go/internal/system2"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func newPipelineTestRuntime(t *testing.T, agentID string) (*AgentRuntime, *db.AgentDB, *xdg.Resolver) {
	t.Helper()
	tmpDir := t.TempDir()
	res := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	if err := res.InitAgentDirectories(agentID); err != nil {
		t.Fatalf("InitAgentDirectories: %v", err)
	}
	idCfg := xdg.DefaultAgentIdentityConfig(agentID)
	idCfg.ClanID = "pioneers"
	if err := res.SaveAgentConfig(idCfg); err != nil {
		t.Fatalf("SaveAgentConfig: %v", err)
	}
	agentDB, err := db.OpenAgentDB(res.AgentDataDir(agentID))
	if err != nil {
		t.Fatalf("OpenAgentDB: %v", err)
	}
	t.Cleanup(func() { _ = agentDB.Close() })

	rt, err := NewRuntime(Config{
		AgentID:        agentID,
		ClanID:         "pioneers",
		IdentityConfig: idCfg,
		Resolver:       res,
		AgentDB:        agentDB,
		SocketPath:     filepath.Join(tmpDir, agentID+".sock"),
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	t.Cleanup(func() { rt.Stop() })
	return rt, agentDB, res
}

func TestFramePipeline_OrderedStageExecution(t *testing.T) {
	rt, _, _ := newPipelineTestRuntime(t, "pipe_order")

	wantStages := []string{
		stageNameReceiptIngest,
		stageNameSpatialAndSurvey,
		stageNameFactGraphSystem1,
		stageNameIdleAndGoalLogging,
		stageNameIntentDispatch,
		stageNameSystem2Dispatchers,
	}
	if got := rt.ensureFramePipeline().Stages(); !reflect.DeepEqual(got, wantStages) {
		t.Fatalf("Stages() = %v, want %v", got, wantStages)
	}

	var executed []string
	pipeline := NewDefaultFramePipeline()
	pipeline.onStage = func(name string, ctx *FrameContext) {
		executed = append(executed, name)
	}
	rt.framePipeline = pipeline

	frame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		TimeOfDay: 1000,
		Status: &civquestv1.AgentStatus{
			AgentId:         "pipe_order",
			Health:          20,
			MaxHealth:       20,
			FoodLevel:       20,
			Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
	}

	rt.HandleTelemetryFrame(frame)
	if !reflect.DeepEqual(executed, wantStages) {
		t.Fatalf("executed stages = %v, want %v", executed, wantStages)
	}
}

func TestFramePipeline_ShortCircuitsSafely(t *testing.T) {
	// Nil runtime and nil pipeline/context safety
	var nilRT *AgentRuntime
	nilRT.HandleTelemetryFrame(&civquestv1.TelemetryFrame{WorldTick: 1})

	var nilPipe *FramePipeline
	nilPipe.Run(nilRT, nil)

	rt, _, _ := newPipelineTestRuntime(t, "pipe_shortcircuit")
	var executed []string
	pipeline := NewDefaultFramePipeline()
	pipeline.onStage = func(name string, _ *FrameContext) {
		executed = append(executed, name)
	}
	rt.framePipeline = pipeline

	// 1. Nil frame short-circuits without running stages
	rt.HandleTelemetryFrame(nil)
	if len(executed) != 0 {
		t.Fatalf("expected 0 stages on nil frame, got %v", executed)
	}
	if rt.LatestFrame() != nil {
		t.Fatalf("expected LatestFrame to remain nil")
	}

	// 2. Nil FrameContext short-circuits in Run
	pipeline.Run(rt, nil)
	pipeline.Run(rt, &FrameContext{})
	if len(executed) != 0 {
		t.Fatalf("expected 0 stages on nil FrameContext/Frame, got %v", executed)
	}

	// 3. DeathEvent runs permadeath teardown and skips all 6 frame stages
	rt.HandleTelemetryFrame(&civquestv1.TelemetryFrame{
		WorldTick: 200,
		DeathEvent: &civquestv1.DeathEvent{
			AgentId:      "pipe_shortcircuit",
			WorldTick:    200,
			DamageSource: "minecraft:fall",
		},
	})
	if !rt.IsDead() {
		t.Fatalf("expected IsDead() == true after DeathEvent")
	}
	if len(executed) != 0 {
		t.Fatalf("expected 0 stages on DeathEvent frame, got %v", executed)
	}

	// 4. Subsequent frames while dead short-circuit cleanly
	rt.HandleTelemetryFrame(&civquestv1.TelemetryFrame{
		WorldTick: 205,
		Status: &civquestv1.AgentStatus{
			AgentId:   "pipe_shortcircuit",
			Health:    20,
			MaxHealth: 20,
			FoodLevel: 20,
		},
	})
	if len(executed) != 0 {
		t.Fatalf("expected 0 stages when already dead, got %v", executed)
	}
	if rt.LatestFrame().GetWorldTick() != 200 {
		t.Fatalf("expected LatestFrame to remain at death tick 200, got %d", rt.LatestFrame().GetWorldTick())
	}
}

func TestFramePipeline_StageReceiptIngest(t *testing.T) {
	rt, _, res := newPipelineTestRuntime(t, "pipe_receipt")
	si := rt.system1State.SpatialIndex()
	si.UpsertPOI(db.AgentPOI{
		POIID:       "poi_resource_10_64_0",
		Category:    system1.POICategoryResource,
		BlockID:     "minecraft:oak_log",
		X:           10,
		Y:           64,
		Z:           0,
		Reliability: 1.0,
		IsActive:    true,
	})
	rt.mu.Lock()
	rt.lastHarvestPos = &civquestv1.BlockPosProto{X: 10, Y: 64, Z: 0}
	rt.mu.Unlock()

	ctx := &FrameContext{
		Frame: &civquestv1.TelemetryFrame{
			WorldTick: 50,
			Receipt: &civquestv1.IntentReceipt{
				BatchId: 7,
				Status:  civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED,
			},
			Status: &civquestv1.AgentStatus{
				AgentId: "pipe_receipt",
				Inventory: []*civquestv1.ItemStackProto{
					{ItemId: "minecraft:oak_log", Count: 3},
				},
			},
		},
	}

	stageReceiptIngest(rt, ctx)

	if ctx.Receipt == nil || ctx.Receipt.BatchId != 7 {
		t.Fatalf("expected ctx.Receipt BatchId=7, got %+v", ctx.Receipt)
	}
	gotRec := rt.system1State.GetLastReceipt()
	if gotRec == nil || gotRec.Status != civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED {
		t.Fatalf("expected system1State lastReceipt COMPLETED, got %+v", gotRec)
	}
	rt.mu.RLock()
	hpos := rt.lastHarvestPos
	rt.mu.RUnlock()
	if hpos != nil {
		t.Fatalf("expected lastHarvestPos cleared after COMPLETED receipt, got %+v", hpos)
	}
	if _, ok := si.GetPOI("poi_resource_10_64_0"); ok {
		t.Fatalf("expected harvested POI pruned from SpatialIndex")
	}
	if _, err := os.Stat(res.AgentInventoryCachePath("pipe_receipt")); err != nil {
		t.Fatalf("expected inventory cache written: %v", err)
	}
}

func TestFramePipeline_StageSpatialAndSurvey(t *testing.T) {
	rt, _, _ := newPipelineTestRuntime(t, "pipe_spatial")
	si := rt.system1State.SpatialIndex()
	si.UpsertPOI(db.AgentPOI{
		POIID:       "poi_resource_5_64_5",
		Category:    system1.POICategoryResource,
		BlockID:     "minecraft:oak_log",
		X:           5,
		Y:           64,
		Z:           5,
		Reliability: 1.0,
		IsActive:    true,
	})
	rt.mu.Lock()
	rt.navTargetPOIID = "poi_resource_5_64_5"
	rt.navDestination = &SocketPosition{X: 5, Y: 64, Z: 5}
	rt.mu.Unlock()

	survey := &civquestv1.HorizonSurveyReport{
		OriginX:       0,
		OriginY:       64,
		OriginZ:       0,
		CompletedTick: 80,
		MaxRange:      64,
		Sectors: []*civquestv1.HorizonSector{
			{
				SectorIndex: 0,
				BearingDeg:  0,
				HitDistance: 40,
				FeatureType: "FOREST_CANOPY",
				HitBlockId:  "minecraft:oak_leaves",
			},
		},
	}

	ctx := &FrameContext{
		Frame: &civquestv1.TelemetryFrame{
			WorldTick:     80,
			HorizonSurvey: survey,
			InterestingBlocks: []*civquestv1.VisibleBlock{
				{Pos: &civquestv1.BlockPosProto{X: 20, Y: 64, Z: 0}, BlockId: "minecraft:oak_log"},
			},
			Status: &civquestv1.AgentStatus{
				AgentId:         "pipe_spatial",
				Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
				NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_STUCK,
			},
		},
	}

	stageSpatialAndSurvey(rt, ctx)
	rt.AwaitPOIPersistence()

	if rt.NavTargetPOIID() != "" {
		t.Fatalf("expected NavTargetPOIID cleared on stuck navigation, got %q", rt.NavTargetPOIID())
	}
	stuckPOI, ok := si.GetPOI("poi_resource_5_64_5")
	if !ok || stuckPOI.Reliability >= 1.0 {
		t.Fatalf("expected stuck POI reliability decayed below 1.0, got ok=%v poi=%+v", ok, stuckPOI)
	}
	if _, ok := si.GetPOI("poi_resource_20_64_0"); !ok {
		t.Fatalf("expected visible block poi_resource_20_64_0 ingested into SpatialIndex")
	}
	if summary := rt.System1State().LatestSurveySummary(); !strings.Contains(summary, "forest canopy") {
		t.Fatalf("expected horizon survey summary recorded, got %q", summary)
	}
}

func TestFramePipeline_StageFactGraphAndSystem1(t *testing.T) {
	rt, _, _ := newPipelineTestRuntime(t, "pipe_s1")
	rt.system1State.PendingRendezvousSync = true
	rt.system1State.PendingRendezvousSummary = "Explored 36 sectors."

	ctx := &FrameContext{
		Frame: &civquestv1.TelemetryFrame{
			WorldTick: 120,
			TimeOfDay: 14000,
			VisibleEntities: []*civquestv1.VisibleEntity{
				{
					EntityId:    777,
					EntityType:  "minecraft:zombie",
					DisplayName: "Zombie",
					Position:    &civquestv1.Vec3{X: 3, Y: 64, Z: 0},
					Health:      20,
					IsHostile:   true,
				},
			},
			Status: &civquestv1.AgentStatus{
				AgentId:         "pipe_s1",
				Health:          20,
				MaxHealth:       20,
				FoodLevel:       20,
				Position:        &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
				NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			},
		},
	}

	stageFactGraphAndSystem1(rt, ctx)

	if !ctx.HasFSMResult || ctx.FSMContext == nil {
		t.Fatalf("expected HasFSMResult=true and non-nil FSMContext")
	}
	if ctx.FSMResult.Stage != system1.StageEmergency {
		t.Fatalf("expected StageEmergency with nearby zombie, got %q", ctx.FSMResult.Stage)
	}
	if synced, _ := rt.system1State.ConsumePendingRendezvousSync(); synced {
		t.Fatalf("expected PendingRendezvousSync to be consumed during stageFactGraphAndSystem1")
	}
	intents := rt.DrainIntents()
	if len(intents) != 1 || intents[0].GetChat() == nil {
		t.Fatalf("expected 1 rendezvous chat intent from syncPendingRendezvous, got %+v", intents)
	}
}

func TestFramePipeline_StageIdleAndGoalLogging(t *testing.T) {
	rt, _, res := newPipelineTestRuntime(t, "pipe_idle_goal")

	// Safe no-op when FSMResult is absent
	stageIdleAndGoalLogging(rt, &FrameContext{
		Frame: &civquestv1.TelemetryFrame{WorldTick: 10},
	})
	rt.mu.RLock()
	if rt.hasIdleStartTick || rt.lastLoggedGoal != "" {
		rt.mu.RUnlock()
		t.Fatalf("expected no-op when HasFSMResult is false")
	}
	rt.mu.RUnlock()

	ctx := &FrameContext{
		Frame: &civquestv1.TelemetryFrame{
			WorldTick: 250,
			TimeOfDay: 1500,
			Status: &civquestv1.AgentStatus{
				AgentId:  "pipe_idle_goal",
				Position: &civquestv1.Vec3{X: 4, Y: 64, Z: 4},
			},
		},
		HasFSMResult: true,
		FSMResult: system1.FSMResult{
			Stage: system1.StageBootstrap,
			GoalHierarchy: &system1.GoalHierarchy{
				ImmediateGoal: "GATHER_TIMBER: Harvest nearby tree logs",
				ShortTermGoal: "Build starter shelter",
				LongTermGoal:  "Found pioneer settlement",
			},
		},
	}

	stageIdleAndGoalLogging(rt, ctx)

	rt.mu.RLock()
	hasIdle := rt.hasIdleStartTick
	idleTick := rt.idleStartTick
	loggedGoal := rt.lastLoggedGoal
	rt.mu.RUnlock()

	if !hasIdle || idleTick != 250 {
		t.Fatalf("expected idleStartTick=250 (hasIdle=%v), got %d", hasIdle, idleTick)
	}
	if loggedGoal != "GATHER_TIMBER: Harvest nearby tree logs" {
		t.Fatalf("unexpected lastLoggedGoal: %q", loggedGoal)
	}
	rawLog, err := os.ReadFile(res.AgentDebugJSONLPath("pipe_idle_goal"))
	if err != nil {
		t.Fatalf("read debug log: %v", err)
	}
	if !strings.Contains(string(rawLog), `"GOAL_TRANSITION"`) {
		t.Fatalf("expected GOAL_TRANSITION in debug log")
	}
}

func TestFramePipeline_StageIntentDispatch(t *testing.T) {
	rt, _, _ := newPipelineTestRuntime(t, "pipe_dispatch")

	// Safe no-op when HasFSMResult is false
	stageIntentDispatch(rt, &FrameContext{
		Frame: &civquestv1.TelemetryFrame{WorldTick: 10},
	})
	if rt.slotQueue.Len() != 0 || rt.slotQueue.FlushPending() {
		t.Fatalf("expected empty slotQueue when HasFSMResult is false")
	}

	eatIntent := &civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_UseItem{
			UseItem: &civquestv1.UseItemIntent{
				InventorySlot:      0,
				IsEatingOrDrinking: true,
			},
		},
	}
	fleeNav := &civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_Navigate{
			Navigate: &civquestv1.NavigateIntent{
				Target: &civquestv1.Vec3{X: 30, Y: 64, Z: 30},
				Sprint: true,
			},
		},
	}
	normalCraft := &civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_CraftItem{
			CraftItem: &civquestv1.CraftItemIntent{
				RecipeId:   "minecraft:oak_planks",
				BatchCount: 1,
			},
		},
	}

	ctx := &FrameContext{
		Frame:        &civquestv1.TelemetryFrame{WorldTick: 300},
		HasFSMResult: true,
		FSMResult: system1.FSMResult{
			State:          system1.StateEmergency,
			FlushPeriphery: true,
			Intents: []system1.FSMIntent{
				{Intent: normalCraft, Priority: false},
				{Intent: eatIntent, Priority: true},
				{Intent: fleeNav, Priority: true},
			},
		},
	}

	stageIntentDispatch(rt, ctx)

	if !rt.slotQueue.ConsumeFlushPeriphery() {
		t.Fatalf("expected FlushPeriphery to be marked on slotQueue")
	}
	rt.mu.RLock()
	eatingTick := rt.lastEatingTick
	fleeTarget := rt.lastFleeTarget
	rt.mu.RUnlock()
	if eatingTick != 300 {
		t.Fatalf("expected lastEatingTick=300, got %d", eatingTick)
	}
	if fleeTarget == nil || fleeTarget.X != 30 {
		t.Fatalf("expected lastFleeTarget recorded at X=30, got %+v", fleeTarget)
	}

	drained := rt.DrainIntents()
	if len(drained) != 3 {
		t.Fatalf("expected 3 drained intents, got %d", len(drained))
	}
	// Priority intents drain ahead of normal craft intent.
	if drained[2].GetCraftItem() == nil {
		t.Fatalf("expected normal craft intent to drain after priority intents, got %+v", drained)
	}

	// Repeated flee nav within 10 ticks & <2m is deduplicated; non-emergency state clears lastFleeTarget.
	ctx2 := &FrameContext{
		Frame:        &civquestv1.TelemetryFrame{WorldTick: 305},
		HasFSMResult: true,
		FSMResult: system1.FSMResult{
			State: system1.StateBootstrap,
			Intents: []system1.FSMIntent{
				{Intent: fleeNav, Priority: true},
			},
		},
	}
	stageIntentDispatch(rt, ctx2)
	if got := rt.DrainIntents(); len(got) != 0 {
		t.Fatalf("expected duplicate flee navigate to be deduplicated, got %+v", got)
	}
	rt.mu.RLock()
	clearedFlee := rt.lastFleeTarget
	rt.mu.RUnlock()
	if clearedFlee != nil {
		t.Fatalf("expected lastFleeTarget cleared outside StateEmergency, got %+v", clearedFlee)
	}
}

func TestFramePipeline_StageSystem2Dispatchers(t *testing.T) {
	rt, agentDB, _ := newPipelineTestRuntime(t, "pipe_s2")
	rt.mu.Lock()
	rt.status = "RUNNING"
	rt.mu.Unlock()

	if err := agentDB.UpsertRelationship(context.Background(), db.Relationship{
		EntityUUID:          "player-1",
		EntityName:          "Alice",
		EntityType:          "PLAYER",
		KinshipRole:         "NONE",
		DispositionScore:    50,
		Familiarity:         0.5,
		LastInteractionTick: 10,
	}); err != nil {
		t.Fatalf("UpsertRelationship: %v", err)
	}

	ctx := &FrameContext{
		Frame: &civquestv1.TelemetryFrame{
			WorldTick: 400,
			TimeOfDay: 4000,
			Status: &civquestv1.AgentStatus{
				AgentId:   "pipe_s2",
				Health:    20,
				MaxHealth: 20,
				FoodLevel: 20,
				Position:  &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
			},
			IncomingChats: []*civquestv1.ChatEvent{
				{
					SenderUuid: "player-1",
					SenderName: "Alice",
					Message:    "follow me",
					Distance:   3.0,
				},
			},
		},
	}

	stageSystem2Dispatchers(rt, ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if rt.CompanionService().GetState() == system2.CompanionStateFollow {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := rt.CompanionService().GetState(); got != system2.CompanionStateFollow {
		t.Fatalf("expected CompanionStateFollow after stageSystem2Dispatchers, got %s", got)
	}
}
