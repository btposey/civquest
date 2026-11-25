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
		if fsm.ActiveActivity() == nil || fsm.ActiveActivity().Kind() != activityKindForaging {
			t.Fatalf("expected active foraging activity, got %v (intents=%+v)", fsm.ActiveActivity(), res.Intents)
		}
		if st.ActiveActivityKind() != activityKindForaging || !st.ActiveActivityOwnsStuck() {
			t.Fatalf("expected state ownership=(foraging, true), got (%q, %t)", st.ActiveActivityKind(), st.ActiveActivityOwnsStuck())
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
		if fsm.ActiveActivity() == nil || fsm.ActiveActivity().Kind() != activityKindShelter {
			t.Fatalf("expected active shelter activity, got %v", fsm.ActiveActivity())
		}
		if st.ActiveActivityKind() != activityKindShelter || !st.ActiveActivityOwnsStuck() {
			t.Fatalf("expected state ownership=(shelter, true), got (%q, %t)", st.ActiveActivityKind(), st.ActiveActivityOwnsStuck())
		}
	})

	// 3. task_subroutine
	t.Run("task_subroutine", func(t *testing.T) {
		fsm := NewAgentFSMWithRegistry(StageSettlement, nil, reg)
		st := NewAgentState()
		st.StartTask(NewHarvestTreeTask(2, 64, 0, StageSettlement, 90))
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
		if fsm.ActiveActivity() == nil || fsm.ActiveActivity().Kind() != activityKindTaskSubroutine {
			t.Fatalf("expected active task_subroutine activity, got %v", fsm.ActiveActivity())
		}
		if st.ActiveActivityKind() != activityKindTaskSubroutine || !st.ActiveActivityOwnsStuck() {
			t.Fatalf("expected state ownership=(task_subroutine, true), got (%q, %t)", st.ActiveActivityKind(), st.ActiveActivityOwnsStuck())
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
			Frame: makeArbiterTestFrame(100, 3000, 20, fullSurvivalKitInv(), []*civquestv1.VisibleBlock{
				{Pos: &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 0}, BlockId: "minecraft:stone"},
			}),
		}
		res := fsm.Tick(ctx)
		if len(res.Intents) == 0 {
			t.Fatalf("expected strategic_directive intent")
		}
		if fsm.ActiveActivity() == nil || fsm.ActiveActivity().Kind() != activityKindStrategicDirective {
			t.Fatalf("expected active strategic_directive activity, got %v", fsm.ActiveActivity())
		}
		if st.ActiveActivityKind() != activityKindStrategicDirective || st.ActiveActivityOwnsStuck() {
			t.Fatalf("expected state ownership=(strategic_directive, false), got (%q, %t)", st.ActiveActivityKind(), st.ActiveActivityOwnsStuck())
		}
	})

	// 5. survey_explorer
	t.Run("survey_explorer", func(t *testing.T) {
		fsm := NewAgentFSMWithRegistry(StageSettlement, nil, reg)
		st := NewAgentState()
		st.mu.Lock()
		st.ExpeditionPhase = ExpeditionTraversing
		st.ExpeditionTarget = [3]float64{48, 64, 0}
		st.mu.Unlock()
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
			t.Fatalf("expected survey_explorer navigate intent, got %+v", res.Intents)
		}
		if fsm.ActiveActivity() == nil || fsm.ActiveActivity().Kind() != activityKindSurveyExplorer {
			t.Fatalf("expected active survey_explorer activity, got %v", fsm.ActiveActivity())
		}
		if st.ActiveActivityKind() != activityKindSurveyExplorer || !st.ActiveActivityOwnsStuck() {
			t.Fatalf("expected state ownership=(survey_explorer, true), got (%q, %t)", st.ActiveActivityKind(), st.ActiveActivityOwnsStuck())
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
		if fsm.ActiveActivity() == nil || fsm.ActiveActivity().Kind() != activityKindBootstrap {
			t.Fatalf("expected active bootstrap activity, got %v", fsm.ActiveActivity())
		}
		if st.ActiveActivityKind() != activityKindBootstrap || st.ActiveActivityOwnsStuck() {
			t.Fatalf("expected state ownership=(bootstrap, false), got (%q, %t)", st.ActiveActivityKind(), st.ActiveActivityOwnsStuck())
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
		if fsm.ActiveActivity() == nil || fsm.ActiveActivity().Kind() != activityKindExploration {
			t.Fatalf("expected active exploration activity, got %v", fsm.ActiveActivity())
		}
		if st.ActiveActivityKind() != activityKindExploration || st.ActiveActivityOwnsStuck() {
			t.Fatalf("expected state ownership=(exploration, false), got (%q, %t)", st.ActiveActivityKind(), st.ActiveActivityOwnsStuck())
		}
	})
}

func TestActivityArbiter_ScopedReceiptRejectsStaleAndPreemptedBatches(t *testing.T) {
	scope := &ActivityReceiptScope{ActivityID: "survey_explorer-1"}
	scope.Anchor(&civquestv1.IntentReceipt{BatchId: 10})
	if scope.StartReceiptBatchID != 10 || scope.IssuedBatchCount != 0 || scope.LastHandledBatchID != 0 {
		t.Fatalf("unexpected initial anchor state: %+v", scope)
	}

	// Reject any receipt before an intent is emitted by this activity
	if scope.PeekValid(&civquestv1.IntentReceipt{BatchId: 15}) || scope.Accept(&civquestv1.IntentReceipt{BatchId: 15}) {
		t.Fatalf("expected receipt rejected when IssuedBatchCount == 0")
	}

	scope.RecordEmit()
	if scope.Accept(nil) {
		t.Fatalf("expected nil receipt rejected")
	}
	if scope.Accept(&civquestv1.IntentReceipt{BatchId: 9}) || scope.Accept(&civquestv1.IntentReceipt{BatchId: 10}) {
		t.Fatalf("expected receipts <= StartReceiptBatchID rejected")
	}
	if !scope.PeekValid(&civquestv1.IntentReceipt{BatchId: 11}) {
		t.Fatalf("expected PeekValid true for fresh batch 11")
	}
	if scope.LastHandledBatchID != 0 {
		t.Fatalf("expected PeekValid not to mutate LastHandledBatchID")
	}
	if !scope.Accept(&civquestv1.IntentReceipt{BatchId: 11}) {
		t.Fatalf("expected Accept true for fresh batch 11")
	}
	if scope.Accept(&civquestv1.IntentReceipt{BatchId: 11}) {
		t.Fatalf("expected duplicate batch 11 rejected")
	}

	// Integration with AgentFSM emergency preemption -> combat receipt -> resume re-anchoring
	reg := NewDefaultEngineRegistry()
	fsm := NewAgentFSMWithRegistry(StageSettlement, nil, reg)
	st := NewAgentState()
	st.mu.Lock()
	st.ExpeditionPhase = ExpeditionTraversing
	st.ExpeditionTarget = [3]float64{60, 64, 0}
	st.mu.Unlock()
	cfg := xdg.DefaultAgentIdentityConfig("arbiter_agent")
	cfg.Profession = "EXPLORER"

	ctx := &FSMContext{
		AgentID: "arbiter_agent",
		State:   st,
		Config:  cfg,
		Frame:   makeArbiterTestFrame(100, 3000, 20, fullSurvivalKitInv(), nil),
	}
	_ = fsm.Tick(ctx)

	act, ok := fsm.ActiveActivity().(ScopedActivity)
	if !ok || act.Kind() != activityKindSurveyExplorer {
		t.Fatalf("expected ScopedActivity survey_explorer active, got %v", fsm.ActiveActivity())
	}
	actScope := act.ReceiptScope()
	if actScope.IssuedBatchCount != 1 {
		t.Fatalf("expected IssuedBatchCount=1 after initial emit, got %d", actScope.IssuedBatchCount)
	}

	// Hostile triggers emergency suspension; combat emits and produces batch 25 receipt
	ctx.Frame = makeArbiterTestFrame(110, 3000, 20, fullSurvivalKitInv(), nil)
	ctx.Frame.VisibleEntities = []*civquestv1.VisibleEntity{
		{EntityId: 1, EntityType: "minecraft:creeper", IsHostile: true, Distance: 3.0, Position: &civquestv1.Vec3{X: 3, Y: 64, Z: 0}},
	}
	resEmg := fsm.Tick(ctx)
	if resEmg.Stage != StageEmergency {
		t.Fatalf("expected StageEmergency, got %s", resEmg.Stage)
	}

	// Threats clear while frame carries combat receipt batch 25 -> OnResume anchors to 25
	ctx.Frame = makeArbiterTestFrame(140, 3000, 20, fullSurvivalKitInv(), nil)
	ctx.Frame.Receipt = &civquestv1.IntentReceipt{BatchId: 25, Status: civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED}
	resResumed := fsm.Tick(ctx)
	if resResumed.Stage != StageSettlement {
		t.Fatalf("expected restoration to StageSettlement, got %s", resResumed.Stage)
	}

	if actScope.StartReceiptBatchID != 25 {
		t.Fatalf("expected StartReceiptBatchID re-anchored to 25 on resume, got %d", actScope.StartReceiptBatchID)
	}
	if actScope.Accept(&civquestv1.IntentReceipt{BatchId: 25}) || actScope.Accept(&civquestv1.IntentReceipt{BatchId: 12}) {
		t.Fatalf("expected stale pre-resume/combat batch IDs rejected")
	}
	if !actScope.Accept(&civquestv1.IntentReceipt{BatchId: 26}) {
		t.Fatalf("expected fresh post-resume batch 26 accepted after resumed emit")
	}
}

func TestActivityArbiter_AmbientUnstuckStealsZeroTicksFromActiveActivities(t *testing.T) {
	reg := NewDefaultEngineRegistry()
	cfg := xdg.DefaultAgentIdentityConfig("arbiter_agent")
	cfg.Profession = "EXPLORER"

	stuckActivities := []struct {
		name  string
		setup func() (*AgentFSM, *AgentState)
	}{
		{
			name: activityKindSurveyExplorer,
			setup: func() (*AgentFSM, *AgentState) {
				fsm := NewAgentFSMWithRegistry(StageSettlement, nil, reg)
				st := NewAgentState()
				st.mu.Lock()
				st.ExpeditionPhase = ExpeditionTraversing
				st.ExpeditionTarget = [3]float64{50, 64, 0}
				st.mu.Unlock()
				_ = fsm.Tick(&FSMContext{
					AgentID: "arbiter_agent",
					State:   st,
					Config:  cfg,
					Frame:   makeArbiterTestFrame(100, 3000, 20, fullSurvivalKitInv(), nil),
				})
				return fsm, st
			},
		},
		{
			name: activityKindShelter,
			setup: func() (*AgentFSM, *AgentState) {
				fsm := NewAgentFSMWithRegistry(StageBootstrap, nil, reg)
				st := NewAgentState()
				inv := []*civquestv1.ItemStackProto{
					{Slot: 0, ItemId: "minecraft:oak_planks", Count: 16},
				}
				_ = fsm.Tick(&FSMContext{
					AgentID: "arbiter_agent",
					State:   st,
					Config:  cfg,
					Frame:   makeArbiterTestFrame(100, 14000, 20, inv, nil),
				})
				return fsm, st
			},
		},
		{
			name: activityKindForaging,
			setup: func() (*AgentFSM, *AgentState) {
				fsm := NewAgentFSMWithRegistry(StageSettlement, nil, reg)
				st := NewAgentState()
				_ = fsm.Tick(&FSMContext{
					AgentID: "arbiter_agent",
					State:   st,
					Config:  cfg,
					Frame: makeArbiterTestFrame(100, 3000, 6, fullSurvivalKitInv(), []*civquestv1.VisibleBlock{
						{Pos: &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 0}, BlockId: "minecraft:sweet_berry_bush"},
					}),
				})
				return fsm, st
			},
		},
		{
			name: activityKindTaskSubroutine,
			setup: func() (*AgentFSM, *AgentState) {
				fsm := NewAgentFSMWithRegistry(StageSettlement, nil, reg)
				st := NewAgentState()
				st.StartTask(NewHarvestTreeTask(2, 64, 0, StageSettlement, 90))
				_ = fsm.Tick(&FSMContext{
					AgentID: "arbiter_agent",
					State:   st,
					Config:  cfg,
					Frame: makeArbiterTestFrame(100, 3000, 20, fullSurvivalKitInv(), []*civquestv1.VisibleBlock{
						{Pos: &civquestv1.BlockPosProto{X: 2, Y: 64, Z: 0}, BlockId: "minecraft:oak_log"},
					}),
				})
				return fsm, st
			},
		},
	}

	pos := &civquestv1.Vec3{X: 10.5, Y: 64.0, Z: 10.5}
	blocks := []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 11, Y: 65, Z: 10}, BlockId: "minecraft:dirt"},
		{Pos: &civquestv1.BlockPosProto{X: 11, Y: 64, Z: 10}, BlockId: "minecraft:dirt"},
	}

	for _, tc := range stuckActivities {
		t.Run(tc.name, func(t *testing.T) {
			fsm, st := tc.setup()
			if fsm.ActiveActivity() == nil || fsm.ActiveActivity().Kind() != tc.name {
				t.Fatalf("expected active activity %s, got %v", tc.name, fsm.ActiveActivity())
			}
			if !st.ActiveActivityOwnsStuck() {
				t.Fatalf("expected ActiveActivityOwnsStuck() == true for %s", tc.name)
			}

			// STUCK but NOT enclosed -> EvaluateUnstuck MUST return nil
			stuckFrame := &civquestv1.TelemetryFrame{
				WorldTick: 200,
				Status: &civquestv1.AgentStatus{
					NavigationState:    civquestv1.NavigationState_NAVIGATION_STATE_STUCK,
					NavigationEnclosed: false,
					Position:           pos,
				},
				InterestingBlocks: blocks,
			}
			if intent := EvaluateUnstuck("arbiter_agent", stuckFrame, pos, st); intent != nil {
				t.Fatalf("expected nil EvaluateUnstuck when %s owns stuck recovery, got %+v", tc.name, intent)
			}

			// Enclosed pit entrapment -> EvaluateUnstuck MUST still escape cleanly
			enclosedFrame := &civquestv1.TelemetryFrame{
				WorldTick: 230,
				Status: &civquestv1.AgentStatus{
					NavigationState:    civquestv1.NavigationState_NAVIGATION_STATE_STUCK,
					NavigationEnclosed: true,
					Position:           pos,
				},
				InterestingBlocks: blocks,
			}
			if intent := EvaluateUnstuck("arbiter_agent", enclosedFrame, pos, st); intent == nil {
				t.Fatalf("expected pit-escape intent when NavigationEnclosed=true for %s", tc.name)
			}
		})
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
	if fsm.ActivityDepth() != 1 || fsm.ActiveActivity() == nil || fsm.ActiveActivity().Kind() != activityKindExploration {
		t.Fatalf("expected parent exploration activity at depth 1, got depth=%d active=%v", fsm.ActivityDepth(), fsm.ActiveActivity())
	}
	parent := fsm.ActiveActivity().(ScopedActivity)
	parentScope := parent.ReceiptScope()
	if parentScope.IssuedBatchCount == 0 {
		t.Fatalf("expected parent exploration to have emitted a motor batch")
	}

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
	if fsm.ActivityDepth() != 2 || fsm.ActiveActivity().Kind() != activityKindForaging {
		t.Fatalf("expected foraging stacked above exploration (depth=2), got depth=%d active=%v", fsm.ActivityDepth(), fsm.ActiveActivity())
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

	if fsm.ActivityDepth() != 1 || fsm.ActiveActivity() != parent {
		t.Fatalf("expected parent exploration resumed at depth 1, got depth=%d active=%v", fsm.ActivityDepth(), fsm.ActiveActivity())
	}
	if st.ActiveActivityKind() != activityKindExploration || st.ActiveActivityOwnsStuck() {
		t.Fatalf("expected ownership restored to (exploration, false), got (%q, %t)", st.ActiveActivityKind(), st.ActiveActivityOwnsStuck())
	}
	if parentScope.StartReceiptBatchID != 50 {
		t.Fatalf("expected parent ReceiptScope re-anchored to 50 on resume, got %d", parentScope.StartReceiptBatchID)
	}
	if parentScope.Accept(&civquestv1.IntentReceipt{BatchId: 50}) {
		t.Fatalf("expected stale batch 50 rejected by resumed parent")
	}
	if !parentScope.Accept(&civquestv1.IntentReceipt{BatchId: 51}) {
		t.Fatalf("expected fresh batch 51 accepted after resumed parent emit")
	}
}
