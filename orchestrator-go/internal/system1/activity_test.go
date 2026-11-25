package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

type testMockActivity struct {
	id             string
	kind           string
	privateCounter int
	doneAfterTicks int
	startCalls     int
	tickCalls      int
	suspendReasons []string
	resumeCalls    int
	exitCalls      int
	events         *[]string
}

func (a *testMockActivity) ID() string   { return a.id }
func (a *testMockActivity) Kind() string { return a.kind }

func (a *testMockActivity) record(ev string) {
	if a.events != nil {
		*a.events = append(*a.events, a.id+":"+ev)
	}
}

func (a *testMockActivity) OnStart(_ *FSMContext) {
	a.startCalls++
	a.record("start")
}

func (a *testMockActivity) OnTick(ctx *FSMContext) ([]FSMIntent, bool) {
	a.tickCalls++
	a.privateCounter++
	a.record("tick")
	intent := &civquestv1.AgentIntent{
		AgentId: ctx.AgentID,
		Action: &civquestv1.AgentIntent_CraftItem{
			CraftItem: &civquestv1.CraftItemIntent{RecipeId: "activity:" + a.id},
		},
	}
	done := a.doneAfterTicks > 0 && a.tickCalls >= a.doneAfterTicks
	return []FSMIntent{{Intent: intent, Priority: false}}, done
}

func (a *testMockActivity) OnSuspend(_ *FSMContext, reason string) {
	a.suspendReasons = append(a.suspendReasons, reason)
	a.record("suspend")
}

func (a *testMockActivity) OnResume(_ *FSMContext) {
	a.resumeCalls++
	a.record("resume")
}

func (a *testMockActivity) OnExit(_ *FSMContext) {
	a.exitCalls++
	a.record("exit")
}

func makeActivityTestCtx(tick int64, health float32) *FSMContext {
	return &FSMContext{
		AgentID: "activity_agent",
		State:   NewAgentState(),
		Frame: &civquestv1.TelemetryFrame{
			WorldTick: tick,
			TimeOfDay: 3000,
			Status: &civquestv1.AgentStatus{
				AgentId:   "activity_agent",
				Health:    health,
				MaxHealth: 20.0,
				FoodLevel: 20,
				Position:  &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
				Inventory: []*civquestv1.ItemStackProto{
					{ItemId: "minecraft:wooden_axe", Count: 1},
					{ItemId: "minecraft:wooden_sword", Count: 1},
					{ItemId: "minecraft:torch", Count: 4},
				},
			},
		},
	}
}

func TestActivityStack_EmergencyPreemptSuspendsAndResumes(t *testing.T) {
	fsm := NewAgentFSM(StageSettlement, nil)
	ctx := makeActivityTestCtx(100, 20.0)

	var events []string
	act := &testMockActivity{id: "mine-1", kind: "mine_stone", events: &events}
	fsm.PushActivity(act, ctx)

	if fsm.ActivityDepth() != 1 || fsm.ActiveActivity() != act {
		t.Fatalf("expected ActivityDepth=1 and ActiveActivity=act, got depth=%d active=%v",
			fsm.ActivityDepth(), fsm.ActiveActivity())
	}
	if act.startCalls != 1 {
		t.Fatalf("expected OnStart called once, got %d", act.startCalls)
	}

	// Normal Tier 1 tick runs act.OnTick
	res1 := fsm.Tick(ctx)
	if act.tickCalls != 1 || act.privateCounter != 1 {
		t.Fatalf("expected act.tickCalls=1, privateCounter=1, got (%d, %d)", act.tickCalls, act.privateCounter)
	}
	if len(res1.Intents) != 1 || res1.Intents[0].Intent.GetCraftItem().GetRecipeId() != "activity:mine-1" {
		t.Fatalf("expected only activity intent on Tier 1 tick, got %+v", res1.Intents)
	}

	// Tier 0 Emergency strikes: creeper nearby
	ctx.Frame.WorldTick = 105
	ctx.Frame.VisibleEntities = []*civquestv1.VisibleEntity{
		{
			EntityId:   77,
			EntityType: "minecraft:creeper",
			IsHostile:  true,
			Distance:   3.0,
			Position:   &civquestv1.Vec3{X: 3, Y: 64, Z: 0},
		},
	}
	resEmg := fsm.Tick(ctx)
	if resEmg.Stage != StageEmergency {
		t.Fatalf("expected StageEmergency on hostile preemption, got %s", resEmg.Stage)
	}
	if !resEmg.FlushPeriphery {
		t.Fatalf("expected FlushPeriphery=true on emergency preemption")
	}
	if len(act.suspendReasons) != 1 {
		t.Fatalf("expected OnSuspend called once on preemption, got %d (%v)", len(act.suspendReasons), act.suspendReasons)
	}
	if act.tickCalls != 1 {
		t.Fatalf("expected suspended activity OnTick NOT to run during emergency, got %d", act.tickCalls)
	}

	// Subsequent emergency tick must NOT re-invoke OnSuspend or run act.OnTick
	ctx.Frame.WorldTick = 110
	_ = fsm.Tick(ctx)
	if len(act.suspendReasons) != 1 {
		t.Fatalf("expected OnSuspend idempotent across emergency ticks, got %d", len(act.suspendReasons))
	}
	if act.tickCalls != 1 {
		t.Fatalf("expected suspended activity OnTick NOT to run while in emergency, got %d", act.tickCalls)
	}

	// Threats cleared -> pops Emergency, calls OnResume on exact suspended Activity, preserves private state, and ticks it
	ctx.Frame.WorldTick = 120
	ctx.Frame.VisibleEntities = nil
	resClear := fsm.Tick(ctx)
	if resClear.Stage != StageSettlement {
		t.Fatalf("expected restoration to StageSettlement, got %s", resClear.Stage)
	}
	if fsm.ActiveActivity() != act || fsm.ActivityDepth() != 1 {
		t.Fatalf("expected exact activity preserved on stack, got depth=%d active=%v",
			fsm.ActivityDepth(), fsm.ActiveActivity())
	}
	if act.resumeCalls != 1 {
		t.Fatalf("expected OnResume called once when threats cleared, got %d", act.resumeCalls)
	}
	if act.tickCalls != 2 || act.privateCounter != 2 {
		t.Fatalf("expected resumed activity state preserved (privateCounter=2, tickCalls=2), got (%d, %d)",
			act.privateCounter, act.tickCalls)
	}
}

func TestActivityStack_Tier1ExclusivityNestedPushAndDonePop(t *testing.T) {
	fsm := NewAgentFSM(StageSettlement, nil)
	ctx := makeActivityTestCtx(200, 20.0)
	// Give inventory items that would otherwise cause StageSettlementHandler to emit armory or patrol intents
	ctx.Frame.Status.Inventory = append(ctx.Frame.Status.Inventory,
		&civquestv1.ItemStackProto{ItemId: "minecraft:feather", Count: 5},
		&civquestv1.ItemStackProto{ItemId: "minecraft:stick", Count: 5},
		&civquestv1.ItemStackProto{ItemId: "minecraft:flint", Count: 5},
	)

	var events []string
	parent := &testMockActivity{id: "parent", kind: "build_cabin", events: &events}
	child := &testMockActivity{id: "child", kind: "unstuck_step", doneAfterTicks: 2, events: &events}

	fsm.PushActivity(parent, ctx)
	fsm.PushActivity(child, ctx)

	if fsm.ActivityDepth() != 2 || fsm.ActiveActivity() != child {
		t.Fatalf("expected depth=2 and active=child, got depth=%d active=%v", fsm.ActivityDepth(), fsm.ActiveActivity())
	}
	if len(parent.suspendReasons) != 1 || parent.suspendReasons[0] != "preempted by activity: unstuck_step" {
		t.Fatalf("expected parent suspended with child kind reason, got %v", parent.suspendReasons)
	}

	// Tick 1: ONLY child runs (not parent, not StageSettlementHandler)
	res1 := fsm.Tick(ctx)
	if child.tickCalls != 1 || parent.tickCalls != 0 {
		t.Fatalf("expected only child OnTick to run (child=%d, parent=%d)", child.tickCalls, parent.tickCalls)
	}
	if len(res1.Intents) != 1 || res1.Intents[0].Intent.GetCraftItem().GetRecipeId() != "activity:child" {
		t.Fatalf("expected exclusively child intent, got %+v", res1.Intents)
	}

	// Tick 2: child completes (done == true) -> OnExit on child, pop, OnResume on parent
	ctx.Frame.WorldTick = 201
	res2 := fsm.Tick(ctx)
	if child.tickCalls != 2 || child.exitCalls != 1 {
		t.Fatalf("expected child tickCalls=2 exitCalls=1, got (%d, %d)", child.tickCalls, child.exitCalls)
	}
	if len(res2.Intents) != 1 || res2.Intents[0].Intent.GetCraftItem().GetRecipeId() != "activity:child" {
		t.Fatalf("expected child intent on completion tick, got %+v", res2.Intents)
	}
	if fsm.ActivityDepth() != 1 || fsm.ActiveActivity() != parent {
		t.Fatalf("expected parent restored to top (depth=1), got depth=%d active=%v", fsm.ActivityDepth(), fsm.ActiveActivity())
	}
	if parent.resumeCalls != 1 {
		t.Fatalf("expected parent.OnResume called once upon child completion, got %d", parent.resumeCalls)
	}

	// Tick 3: parent runs
	ctx.Frame.WorldTick = 202
	res3 := fsm.Tick(ctx)
	if parent.tickCalls != 1 {
		t.Fatalf("expected parent.OnTick to run after child popped, got %d", parent.tickCalls)
	}
	if len(res3.Intents) != 1 || res3.Intents[0].Intent.GetCraftItem().GetRecipeId() != "activity:parent" {
		t.Fatalf("expected parent intent, got %+v", res3.Intents)
	}
}

func TestActivityStack_StageDeadClearsAllActivitiesTopDown(t *testing.T) {
	fsm := NewAgentFSM(StageSettlement, nil)
	ctx := makeActivityTestCtx(300, 20.0)

	var events []string
	bottom := &testMockActivity{id: "bottom", kind: "patrol", events: &events}
	top := &testMockActivity{id: "top", kind: "forage", events: &events}

	fsm.PushActivity(bottom, ctx)
	fsm.PushActivity(top, ctx)
	if fsm.ActivityDepth() != 2 {
		t.Fatalf("expected depth=2 before death, got %d", fsm.ActivityDepth())
	}

	// Fatal health triggers StageDead in Tick
	ctx.Frame.WorldTick = 305
	ctx.Frame.Status.Health = 0
	res := fsm.Tick(ctx)
	if res.Stage != StageDead || res.Routine != RoutineDead {
		t.Fatalf("expected (StageDead, RoutineDead), got (%s, %s)", res.Stage, res.Routine)
	}
	if fsm.ActivityDepth() != 0 || fsm.ActiveActivity() != nil {
		t.Fatalf("expected empty activity stack on StageDead, got depth=%d active=%v",
			fsm.ActivityDepth(), fsm.ActiveActivity())
	}
	if top.exitCalls != 1 || bottom.exitCalls != 1 {
		t.Fatalf("expected OnExit called once on all stacked activities, got top=%d bottom=%d",
			top.exitCalls, bottom.exitCalls)
	}
	if bottom.resumeCalls != 0 {
		t.Fatalf("expected bottom.OnResume NOT called during terminal clear, got %d", bottom.resumeCalls)
	}

	// Verify top-down exit order: top:exit precedes bottom:exit
	topExitIdx, bottomExitIdx := -1, -1
	for i, ev := range events {
		if ev == "top:exit" {
			topExitIdx = i
		} else if ev == "bottom:exit" {
			bottomExitIdx = i
		}
	}
	if topExitIdx == -1 || bottomExitIdx == -1 || topExitIdx > bottomExitIdx {
		t.Fatalf("expected top-down OnExit order, got events %v", events)
	}

	// TransitionTo(StageDead) path also clears stacked activities cleanly
	fsm2 := NewAgentFSM(StageSettlement, nil)
	a1 := &testMockActivity{id: "a1", kind: "build"}
	a2 := &testMockActivity{id: "a2", kind: "harvest"}
	fsm2.PushActivity(a1, ctx)
	fsm2.PushActivity(a2, ctx)
	if !fsm2.TransitionTo(StageDead, "killed by test", ctx) {
		t.Fatalf("expected TransitionTo(StageDead) to succeed")
	}
	if fsm2.ActivityDepth() != 0 || a1.exitCalls != 1 || a2.exitCalls != 1 {
		t.Fatalf("expected TransitionTo(StageDead) to clear activities and invoke OnExit, got depth=%d a1=%d a2=%d",
			fsm2.ActivityDepth(), a1.exitCalls, a2.exitCalls)
	}
}
