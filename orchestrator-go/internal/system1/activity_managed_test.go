package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func makeArbiterTestFrame(tick, tod int64, food int32, inv []*civquestv1.ItemStackProto, blocks []*civquestv1.VisibleBlock) *civquestv1.TelemetryFrame {
	return &civquestv1.TelemetryFrame{
		WorldTick: tick,
		TimeOfDay: tod,
		Status: &civquestv1.AgentStatus{
			AgentId:         "arbiter_agent",
			Health:          20,
			MaxHealth:       20,
			FoodLevel:       food,
			Position:        &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
			Inventory:       inv,
		},
		InterestingBlocks: blocks,
	}
}

func fullSurvivalKitInv() []*civquestv1.ItemStackProto {
	return []*civquestv1.ItemStackProto{
		{Slot: 0, ItemId: "minecraft:wooden_axe", Count: 1},
		{Slot: 1, ItemId: "minecraft:wooden_sword", Count: 1},
		{Slot: 2, ItemId: "minecraft:torch", Count: 8},
	}
}

func TestActivityArbiter_UnifiedSelectionAndSkillRegistry(t *testing.T) {
	reg := NewDefaultEngineRegistry()
	requiredSkills := []string{
		"foraging",
		"shelter_construction",
		"task_subroutine",
		"strategic_directive",
		"survey_explorer",
		"bootstrap_gather",
		"bootstrap_craft",
		"civic_patrol",
		"frontier_scouting",
		"patrol_exploration",
	}
	for _, name := range requiredSkills {
		if sk, ok := reg.GetSkill(name); !ok || sk == nil {
			t.Fatalf("expected skill %q registered in NewDefaultEngineRegistry()", name)
		}
	}

	cfg := xdg.DefaultAgentIdentityConfig("arbiter_agent")
	cfg.Profession = "FARMER"

	// 1. foraging
	t.Run("foraging", func(t *testing.T) {
		fsm := NewAgentFSMWithRegistry(StageSettlement, nil, reg)
		st := NewAgentState()
		ctx := &FSMContext{
			AgentID: "arbiter_agent",
			State:   st,
			Config:  cfg,
			Frame: makeArbiterTestFrame(100, 3000, 6, fullSurvivalKitInv(), []*civquestv1.VisibleBlock{
				{Pos: &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 0}, BlockId: "minecraft:sweet_berry_bush"},
			}),
		}
		res := fsm.Tick(ctx)
		if fsm.ActiveActivityKind() != activityKindForaging {
			t.Fatalf("expected active foraging activity, got %v (intents=%+v)", fsm.ActiveActivityKind(), res.Intents)
		}
		if st.ActiveActivityKind() != activityKindForaging {
			t.Fatalf("expected state kind foraging, got %q", st.ActiveActivityKind())
		}
	})

	// 2. shelter
	t.Run("shelter", func(t *testing.T) {
		fsm := NewAgentFSMWithRegistry(StageBootstrap, nil, reg)
		st := NewAgentState()
		inv := []*civquestv1.ItemStackProto{
			{Slot: 0, ItemId: "minecraft:oak_planks", Count: 16},
			{Slot: 1, ItemId: "minecraft:torch", Count: 4},
		}
		ctx := &FSMContext{
			AgentID: "arbiter_agent",
			State:   st,
			Config:  cfg,
			Frame:   makeArbiterTestFrame(100, 14000, 20, inv, nil),
		}
		res := fsm.Tick(ctx)
		if len(res.Intents) == 0 {
			t.Fatalf("expected shelter intent")
		}
		if fsm.ActiveActivityKind() != activityKindShelter {
			t.Fatalf("expected active shelter activity, got %v", fsm.ActiveActivityKind())
		}
		if st.ActiveActivityKind() != activityKindShelter {
			t.Fatalf("expected state kind shelter, got %q", st.ActiveActivityKind())
		}
	})

	// 3. task_subroutine
	t.Run("task_subroutine", func(t *testing.T) {
		fsm := NewAgentFSMWithRegistry(StageSettlement, nil, reg)
		st := NewAgentState()
		fsm.StartTask(st, NewHarvestTreeTask(2, 64, 0, StageSettlement, 90))
		ctx := &FSMContext{
			AgentID: "arbiter_agent",
			State:   st,
			Config:  cfg,
			Frame: makeArbiterTestFrame(100, 3000, 20, fullSurvivalKitInv(), []*civquestv1.VisibleBlock{
				{Pos: &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 0}, BlockId: "minecraft:oak_log"},
				{Pos: &civquestv1.BlockPosProto{X: 2, Y: 65, Z: 0}, BlockId: "minecraft:oak_log"},
			}),
		}
		_ = fsm.Tick(ctx)
		if fsm.ActiveActivityKind() != activityKindTaskSubroutine {
			t.Fatalf("expected active task_subroutine activity, got %v", fsm.ActiveActivityKind())
		}
		if st.ActiveActivityKind() != activityKindTaskSubroutine {
			t.Fatalf("expected state kind task_subroutine, got %q", st.ActiveActivityKind())
		}
	})

	// 4. strategic_directive
	t.Run("strategic_directive", func(t *testing.T) {
		fsm := NewAgentFSMWithRegistry(StageSettlement, nil, reg)
		st := NewAgentState()
		st.SetStrategicDirective(DirectiveActionMineStone, "stone", "MINE_STONE: Gather cobble", 90, 1000)
		ctx := &FSMContext{
			AgentID: "arbiter_agent",
			State:   st,
			Config:  cfg,
			Frame: makeArbiterTestFrame(100, 3000, 20, append(fullSurvivalKitInv(), &civquestv1.ItemStackProto{Slot: 8, ItemId: "minecraft:wooden_pickaxe", Count: 1}), []*civquestv1.VisibleBlock{
				{Pos: &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 0}, BlockId: "minecraft:stone"},
			}),
		}
		res := fsm.Tick(ctx)
		if len(res.Intents) == 0 {
			t.Fatalf("expected strategic_directive intent")
		}
		if fsm.ActiveActivityKind() != activityKindStrategicDirective {
			t.Fatalf("expected active strategic_directive activity, got %v", fsm.ActiveActivityKind())
		}
		if st.ActiveActivityKind() != activityKindStrategicDirective {
			t.Fatalf("expected state kind strategic_directive, got %q", st.ActiveActivityKind())
		}
	})

	// 5. expedition: navigate frame over expedition over the directive that started it
	t.Run("expedition_navigate", func(t *testing.T) {
		fsm := NewAgentFSMWithRegistry(StageSettlement, nil, reg)
		st := NewAgentState()
		st.SetStrategicDirective(DirectiveActionScoutFrontier, "east", "SCOUT_FRONTIER: scout", 50, 60000)
		st.IngestHorizonSurveyReport(uniformSurvey(0, 0, 90), 90)
		expCfg := xdg.DefaultAgentIdentityConfig("arbiter_agent")
		expCfg.Profession = "EXPLORER"
		ctx := &FSMContext{
			AgentID: "arbiter_agent",
			State:   st,
			Config:  expCfg,
			Frame:   makeArbiterTestFrame(100, 3000, 20, fullSurvivalKitInv(), nil),
		}
		res := fsm.Tick(ctx)
		if len(res.Intents) == 0 || res.Intents[0].Intent.GetNavigate() == nil {
			t.Fatalf("expected expedition navigate intent, got %+v", res.Intents)
		}
		if fsm.ActiveActivityKind() != activityKindNavigate {
			t.Fatalf("expected active navigate frame, got %v", fsm.ActiveActivityKind())
		}
		if st.ActiveActivityKind() != activityKindNavigate {
			t.Fatalf("expected state kind navigate, got %q", st.ActiveActivityKind())
		}
	})

	// 6. bootstrap
	t.Run("bootstrap", func(t *testing.T) {
		fsm := NewAgentFSMWithRegistry(StageBootstrap, nil, reg)
		st := NewAgentState()
		ctx := &FSMContext{
			AgentID: "arbiter_agent",
			State:   st,
			Config:  cfg,
			Frame: makeArbiterTestFrame(100, 3000, 20, nil, []*civquestv1.VisibleBlock{
				{Pos: &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 0}, BlockId: "minecraft:oak_log"},
				{Pos: &civquestv1.BlockPosProto{X: 2, Y: 65, Z: 0}, BlockId: "minecraft:oak_log"},
			}),
		}
		res := fsm.Tick(ctx)
		if len(res.Intents) == 0 {
			t.Fatalf("expected bootstrap intent")
		}
		// The harvest bootstrap started runs as its child in the same tick.
		if kinds := stackKinds(fsm); len(kinds) != 2 || kinds[0] != activityKindBootstrap || kinds[1] != activityKindTaskSubroutine {
			t.Fatalf("expected a harvest task under bootstrap, got %v", kinds)
		}
		if st.ActiveActivityKind() != activityKindTaskSubroutine {
			t.Fatalf("expected state kind task_subroutine, got %q", st.ActiveActivityKind())
		}
	})

	// 7. exploration
	t.Run("exploration", func(t *testing.T) {
		fsm := NewAgentFSMWithRegistry(StageSettlement, nil, reg)
		st := NewAgentState()
		for i := int64(0); i < 2; i++ {
			ctx := &FSMContext{
				AgentID: "arbiter_agent",
				State:   st,
				Config:  cfg,
				Frame:   makeArbiterTestFrame(100+i*150, 3000, 20, fullSurvivalKitInv(), nil),
			}
			_ = fsm.Tick(ctx)
		}
		if fsm.ActiveActivityKind() != activityKindExploration {
			t.Fatalf("expected active exploration activity, got %v", fsm.ActiveActivityKind())
		}
		if st.ActiveActivityKind() != activityKindExploration {
			t.Fatalf("expected state kind exploration, got %q", st.ActiveActivityKind())
		}
	})
}

// Receipts are routed by frame ID (ADR-036 §3): only the top frame named by the receipt
// receives it; a receipt for a suspended frame, a frame_id of 0, an older batch or a
// re-delivery is dropped. Replaces the ActivityReceiptScope counter test.
func TestActivityKernel_ReceiptsRoutedByFrameID(t *testing.T) {
	fsm := NewAgentFSMWithRegistry(StageSettlement, nil, NewDefaultEngineRegistry())
	st := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("arbiter_agent")
	cfg.Profession = "FARMER"
	logs := []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 0}, BlockId: "minecraft:oak_log"},
		{Pos: &civquestv1.BlockPosProto{X: 2, Y: 65, Z: 0}, BlockId: "minecraft:oak_log"},
	}
	fsm.StartTask(st, NewHarvestTreeTask(2, 64, 0, StageSettlement, 100))
	tick := func(wt int64, rc *civquestv1.IntentReceipt, threat bool) FSMResult {
		f := makeArbiterTestFrame(wt, 3000, 20, fullSurvivalKitInv(), logs)
		f.Receipt = rc
		if threat {
			f.Status.Health = 6
			f.VisibleEntities = []*civquestv1.VisibleEntity{{EntityId: 1, EntityType: "minecraft:creeper", IsHostile: true, Distance: 2.5, Position: &civquestv1.Vec3{X: 2.5, Y: 64, Z: 0.5}}}
		}
		return fsm.Tick(&FSMContext{AgentID: "arbiter_agent", State: st, Config: cfg, Frame: f})
	}
	failures := func() int {
		if _, tk := topTaskLocal(t, fsm); tk != nil {
			return tk.ConsecutiveFailures
		}
		return -1
	}

	res := tick(100, nil, false)
	if fsm.ActiveActivityKind() != activityKindTaskSubroutine || len(res.Intents) == 0 {
		t.Fatalf("expected the task frame to emit, top=%q intents=%+v", fsm.ActiveActivityKind(), res.Intents)
	}
	taskFrame := res.Intents[len(res.Intents)-1].Frame
	if taskFrame == 0 {
		t.Fatalf("expected task intents stamped with their frame ID")
	}

	// Emergency pushes over the task: a FAILED receipt for the (now suspended) task frame is stale.
	tick(110, &civquestv1.IntentReceipt{BatchId: 5, FrameId: taskFrame, Status: civquestv1.IntentReceipt_RECEIPT_STATUS_EXECUTING}, true)
	if fsm.ActiveActivityKind() != activityKindEmergency {
		t.Fatalf("expected emergency frame on top, got %q", fsm.ActiveActivityKind())
	}
	tick(115, &civquestv1.IntentReceipt{BatchId: 6, FrameId: taskFrame, Status: civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED, FailureReason: "x"}, true)
	if failures() != 0 {
		t.Fatalf("receipt for a suspended frame must be dropped, failures=%d", failures())
	}

	// Cleared: the task frame is top again.
	tick(140, nil, false)
	if fsm.ActiveActivityKind() != activityKindTaskSubroutine {
		t.Fatalf("expected task frame resumed, got %q", fsm.ActiveActivityKind())
	}
	tick(141, &civquestv1.IntentReceipt{BatchId: 7, FrameId: 0, Status: civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED}, false)
	if failures() != 0 {
		t.Fatalf("frame_id 0 receipt must be dropped, failures=%d", failures())
	}
	tick(142, &civquestv1.IntentReceipt{BatchId: 8, FrameId: taskFrame, Status: civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED, FailureReason: "no path"}, false)
	if failures() != 1 {
		t.Fatalf("receipt for the top frame must be delivered, failures=%d", failures())
	}
	tick(143, &civquestv1.IntentReceipt{BatchId: 8, FrameId: taskFrame, Status: civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED, FailureReason: "no path"}, false)
	tick(144, &civquestv1.IntentReceipt{BatchId: 6, FrameId: taskFrame, Status: civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED, FailureReason: "late"}, false)
	if failures() != 1 {
		t.Fatalf("re-delivered or older-batch receipts must be dropped, failures=%d", failures())
	}
}

func TestActivityArbiter_ForagingPreemptsAndResumesParentActivity(t *testing.T) {
	reg := NewDefaultEngineRegistry()
	fsm := NewAgentFSMWithRegistry(StageSettlement, nil, reg)
	st := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("arbiter_agent")
	cfg.Profession = "FARMER"

	// Tick 1 & 2: establish parent exploration activity on stack
	for i := int64(0); i < 2; i++ {
		_ = fsm.Tick(&FSMContext{
			AgentID: "arbiter_agent",
			State:   st,
			Config:  cfg,
			Frame:   makeArbiterTestFrame(100+i*150, 3000, 20, fullSurvivalKitInv(), nil),
		})
	}
	if fsm.ActivityDepth() != 1 || fsm.ActiveActivityKind() != activityKindExploration {
		t.Fatalf("expected parent exploration activity at depth 1, got depth=%d active=%v", fsm.ActivityDepth(), fsm.ActiveActivityKind())
	}
	parentFrame := fsm.ActivityFrames()[0].ID

	// Tick 3: Hunger drops without edible food -> foraging preempts exploration (OnSuspend)
	hungryFrame := makeArbiterTestFrame(270, 3000, 8, fullSurvivalKitInv(), []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 0}, BlockId: "minecraft:sweet_berry_bush"},
	})
	resForage := fsm.Tick(&FSMContext{
		AgentID: "arbiter_agent",
		State:   st,
		Config:  cfg,
		Frame:   hungryFrame,
	})
	if fsm.ActivityDepth() != 2 || fsm.ActiveActivityKind() != activityKindForaging {
		t.Fatalf("expected foraging stacked above exploration (depth=2), got depth=%d active=%v", fsm.ActivityDepth(), fsm.ActiveActivityKind())
	}
	if len(resForage.Intents) == 0 || resForage.Intents[0].Intent.GetInteractBlock() == nil {
		t.Fatalf("expected foraging harvest intent, got %+v", resForage.Intents)
	}

	// Tick 4: Food satisfied and receipt batch 50 arrives -> foraging pops (OnExit), exploration resumes (OnResume)
	fedInv := append(fullSurvivalKitInv(), &civquestv1.ItemStackProto{Slot: 3, ItemId: "minecraft:bread", Count: 2})
	fedFrame := makeArbiterTestFrame(400, 3000, 20, fedInv, nil)
	fedFrame.Status.NavigationState = civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED
	fedFrame.Receipt = &civquestv1.IntentReceipt{BatchId: 50, Status: civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED}
	_ = fsm.Tick(&FSMContext{
		AgentID: "arbiter_agent",
		State:   st,
		Config:  cfg,
		Frame:   fedFrame,
	})

	if fsm.ActivityDepth() != 1 || fsm.ActivityFrames()[0].ID != parentFrame {
		t.Fatalf("expected parent exploration resumed at depth 1, got depth=%d active=%v", fsm.ActivityDepth(), fsm.ActiveActivityKind())
	}
	if st.ActiveActivityKind() != activityKindExploration {
		t.Fatalf("expected kind restored to exploration, got %q", st.ActiveActivityKind())
	}
}
