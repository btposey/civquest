package system1

import (
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

func TestSurvivalKitEvaluator_Registration(t *testing.T) {
	eval := NewSurvivalKitEvaluator()
	if eval.Name() != "survival_kit" {
		t.Errorf("expected name 'survival_kit', got %q", eval.Name())
	}
	if eval.Category() != "bootstrap" {
		t.Errorf("expected category 'bootstrap', got %q", eval.Category())
	}
}

func TestSurvivalKitEvaluator_IncompleteKit(t *testing.T) {
	eval := NewSurvivalKitEvaluator()
	graph := NewFactGraph()

	frame := &civquestv1.TelemetryFrame{
		WorldTick: 50,
		Status: &civquestv1.AgentStatus{
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:oak_log", Count: 3},
				{ItemId: "minecraft:oak_planks", Count: 4},
			},
		},
	}
	ctx := &EvaluatorContext{
		AgentID: "agent-eval-1",
		Frame:   frame,
		State:   func() *AgentState { s := NewAgentState(); s.SetRecipeGraph(newTestRecipeGraph()); return s }(),
	}

	if err := eval.Evaluate(ctx, graph); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if graph.GetBool("bootstrap.has_kit") {
		t.Errorf("expected bootstrap.has_kit false without axe, weapon, and light")
	}
	if graph.GetBool("bootstrap.has_axe") {
		t.Errorf("expected bootstrap.has_axe false")
	}
	if graph.GetBool("bootstrap.has_sword") {
		t.Errorf("expected bootstrap.has_sword false")
	}
	if graph.GetBool("bootstrap.has_weapon") {
		t.Errorf("expected bootstrap.has_weapon false")
	}
	if graph.GetBool("bootstrap.has_light") {
		t.Errorf("expected bootstrap.has_light false")
	}
	logCount, _ := graph.GetInt("bootstrap.log_count")
	if logCount != 3 {
		t.Errorf("expected log_count 3, got %d", logCount)
	}
	plankCount, _ := graph.GetInt("bootstrap.plank_count")
	if plankCount != 4 {
		t.Errorf("expected plank_count 4, got %d", plankCount)
	}
	cobbleCount, _ := graph.GetInt("bootstrap.cobblestone_count")
	if cobbleCount != 0 {
		t.Errorf("expected cobblestone_count 0, got %d", cobbleCount)
	}
	stickCount, _ := graph.GetInt("bootstrap.stick_count")
	if stickCount != 0 {
		t.Errorf("expected stick_count 0, got %d", stickCount)
	}
	coalCount, _ := graph.GetInt("bootstrap.coal_count")
	if coalCount != 0 {
		t.Errorf("expected coal_count 0, got %d", coalCount)
	}
	if graph.GetBool("bootstrap.has_crafting_table") {
		t.Errorf("expected has_crafting_table false")
	}
	if !graph.GetBool("bootstrap.needs_tools") {
		t.Errorf("expected needs_tools true when tools missing")
	}
	if graph.GetBool("bootstrap.on_cooldown") {
		t.Errorf("expected on_cooldown false")
	}
}

func TestSurvivalKitEvaluator_CompleteKit(t *testing.T) {
	eval := NewSurvivalKitEvaluator()
	graph := NewFactGraph()

	frame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		Status: &civquestv1.AgentStatus{
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:wooden_axe", Count: 1},
				{ItemId: "minecraft:wooden_sword", Count: 1},
				{ItemId: "minecraft:torch", Count: 8},
				{ItemId: "minecraft:crafting_table", Count: 1},
				{ItemId: "minecraft:oak_log", Count: 5},
			},
		},
	}
	ctx := &EvaluatorContext{
		AgentID: "agent-eval-2",
		Frame:   frame,
		State:   NewAgentState(),
	}

	if err := eval.Evaluate(ctx, graph); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !graph.GetBool("bootstrap.has_kit") {
		t.Errorf("expected bootstrap.has_kit true")
	}
	if !graph.GetBool("bootstrap.has_axe") {
		t.Errorf("expected bootstrap.has_axe true")
	}
	if !graph.GetBool("bootstrap.has_sword") {
		t.Errorf("expected bootstrap.has_sword true")
	}
	if !graph.GetBool("bootstrap.has_weapon") {
		t.Errorf("expected bootstrap.has_weapon true")
	}
	if !graph.GetBool("bootstrap.has_light") {
		t.Errorf("expected bootstrap.has_light true")
	}
	if !graph.GetBool("bootstrap.has_crafting_table") {
		t.Errorf("expected bootstrap.has_crafting_table true")
	}
	if graph.GetBool("bootstrap.needs_tools") {
		t.Errorf("expected bootstrap.needs_tools false when wooden tools secured and no cobblestone")
	}
}

func TestSurvivalKitEvaluator_StoneUpgradeNeeds(t *testing.T) {
	eval := NewSurvivalKitEvaluator()
	graph := NewFactGraph()

	frame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		Status: &civquestv1.AgentStatus{
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:wooden_axe", Count: 1},
				{ItemId: "minecraft:wooden_sword", Count: 1},
				{ItemId: "minecraft:torch", Count: 4},
				{ItemId: "minecraft:cobblestone", Count: 8},
				{ItemId: "minecraft:stick", Count: 4},
				{ItemId: "minecraft:charcoal", Count: 2},
			},
		},
	}
	ctx := &EvaluatorContext{
		AgentID: "agent-eval-3",
		Frame:   frame,
		State:   NewAgentState(),
	}

	if err := eval.Evaluate(ctx, graph); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !graph.GetBool("bootstrap.has_kit") {
		t.Errorf("expected has_kit true")
	}
	cobble, _ := graph.GetInt("bootstrap.cobblestone_count")
	if cobble != 8 {
		t.Errorf("expected cobblestone_count 8, got %d", cobble)
	}
	sticks, _ := graph.GetInt("bootstrap.stick_count")
	if sticks != 4 {
		t.Errorf("expected stick_count 4, got %d", sticks)
	}
	coal, _ := graph.GetInt("bootstrap.coal_count")
	if coal != 2 {
		t.Errorf("expected coal_count 2, got %d", coal)
	}
	if !graph.GetBool("bootstrap.needs_tools") {
		t.Errorf("expected needs_tools true for available stone gear upgrade")
	}
}

func TestSurvivalKitEvaluator_Cooldown(t *testing.T) {
	eval := NewSurvivalKitEvaluator()
	state := NewAgentState()
	state.SetLastBootstrapTimeoutTick(100)

	// Within 600 ticks cooldown window
	frame1 := &civquestv1.TelemetryFrame{WorldTick: 200, Status: &civquestv1.AgentStatus{}}
	ctx1 := &EvaluatorContext{Frame: frame1, State: state}
	graph1 := NewFactGraph()
	_ = eval.Evaluate(ctx1, graph1)
	if !graph1.GetBool("bootstrap.on_cooldown") {
		t.Errorf("expected bootstrap.on_cooldown true at tick 200 (timeout at 100)")
	}

	// After 600 ticks
	frame2 := &civquestv1.TelemetryFrame{WorldTick: 800, Status: &civquestv1.AgentStatus{}}
	ctx2 := &EvaluatorContext{Frame: frame2, State: state}
	graph2 := NewFactGraph()
	_ = eval.Evaluate(ctx2, graph2)
	if graph2.GetBool("bootstrap.on_cooldown") {
		t.Errorf("expected bootstrap.on_cooldown false at tick 800 (timeout at 100)")
	}
}

func TestShelterEvaluator_Registration(t *testing.T) {
	eval := NewShelterEvaluator()
	if eval.Name() != "shelter" {
		t.Errorf("expected name 'shelter', got %q", eval.Name())
	}
	if eval.Category() != "shelter" {
		t.Errorf("expected category 'shelter', got %q", eval.Category())
	}
}

func TestShelterEvaluator_IncompleteAndComplete(t *testing.T) {
	eval := NewShelterEvaluator()
	state := NewAgentState()

	// Incomplete
	frame := &civquestv1.TelemetryFrame{WorldTick: 100}
	ctx := &EvaluatorContext{Frame: frame, State: state}
	graph := NewFactGraph()
	_ = eval.Evaluate(ctx, graph)

	if graph.GetBool("shelter.complete") {
		t.Errorf("expected shelter.complete false initially")
	}
	placed, _ := graph.GetInt("shelter.blocks_placed")
	if placed != 0 {
		t.Errorf("expected shelter.blocks_placed 0, got %d", placed)
	}
	total, _ := graph.GetInt("shelter.total_blocks")
	if total != blueprints.CabinTotalBlocks {
		t.Errorf("expected shelter.total_blocks %d, got %d", blueprints.CabinTotalBlocks, total)
	}

	// Complete via blocks placed
	state.SetShelterBlocksPlaced(blueprints.CabinTotalBlocks)
	graphComplete := NewFactGraph()
	_ = eval.Evaluate(ctx, graphComplete)
	if !graphComplete.GetBool("shelter.complete") {
		t.Errorf("expected shelter.complete true when blocks placed reaches total")
	}

	// Complete via build index
	state.SetShelterBlocksPlaced(0)
	state.SetShelterBuildIndex(blueprints.CabinTotalBlocks)
	graphCompleteIndex := NewFactGraph()
	_ = eval.Evaluate(ctx, graphCompleteIndex)
	if !graphCompleteIndex.GetBool("shelter.complete") {
		t.Errorf("expected shelter.complete true when build index reaches total")
	}
}

func TestShelterEvaluator_CampfireAndBedDetection(t *testing.T) {
	eval := NewShelterEvaluator()
	state := NewAgentState()
	state.SetAssignedBed(&civquestv1.BlockPosProto{X: 10, Y: 64, Z: 10})

	frame := &civquestv1.TelemetryFrame{
		WorldTick: 100,
		Status: &civquestv1.AgentStatus{
			Position: &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{
				BlockId: "minecraft:campfire",
				Pos:     &civquestv1.BlockPosProto{X: 12, Y: 64, Z: 10},
			},
		},
	}
	ctx := &EvaluatorContext{Frame: frame, State: state}
	graph := NewFactGraph()
	_ = eval.Evaluate(ctx, graph)

	if !graph.GetBool("shelter.has_campfire") {
		t.Errorf("expected shelter.has_campfire true with nearby campfire")
	}
	if !graph.GetBool("shelter.has_bed") {
		t.Errorf("expected shelter.has_bed true with assigned bed")
	}
}

func TestShelterEvaluator_Cooldown(t *testing.T) {
	eval := NewShelterEvaluator()
	state := NewAgentState()
	state.SetLastShelterTimeoutTick(100)

	frame1 := &civquestv1.TelemetryFrame{WorldTick: 300}
	ctx1 := &EvaluatorContext{Frame: frame1, State: state}
	graph1 := NewFactGraph()
	_ = eval.Evaluate(ctx1, graph1)
	if !graph1.GetBool("shelter.on_cooldown") {
		t.Errorf("expected shelter.on_cooldown true at tick 300 (timeout at 100)")
	}

	frame2 := &civquestv1.TelemetryFrame{WorldTick: 800}
	ctx2 := &EvaluatorContext{Frame: frame2, State: state}
	graph2 := NewFactGraph()
	_ = eval.Evaluate(ctx2, graph2)
	if graph2.GetBool("shelter.on_cooldown") {
		t.Errorf("expected shelter.on_cooldown false at tick 800 (timeout at 100)")
	}
}

func BenchmarkBootstrapEvaluators(b *testing.B) {
	survEval := NewSurvivalKitEvaluator()
	shelterEval := NewShelterEvaluator()

	frame := &civquestv1.TelemetryFrame{
		WorldTick: 500,
		Status: &civquestv1.AgentStatus{
			Position: &civquestv1.Vec3{X: 20, Y: 64, Z: 20},
			Inventory: []*civquestv1.ItemStackProto{
				{ItemId: "minecraft:oak_log", Count: 6},
				{ItemId: "minecraft:wooden_axe", Count: 1},
				{ItemId: "minecraft:torch", Count: 4},
				{ItemId: "minecraft:cobblestone", Count: 12},
			},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{BlockId: "minecraft:campfire", Pos: &civquestv1.BlockPosProto{X: 22, Y: 64, Z: 20}},
			{BlockId: "minecraft:oak_log", Pos: &civquestv1.BlockPosProto{X: 25, Y: 64, Z: 20}},
		},
	}
	state := NewAgentState()
	ctx := &EvaluatorContext{
		AgentID: "bench-agent",
		Frame:   frame,
		State:   state,
	}
	graph := NewFactGraph()

	b.ResetTimer()
	start := time.Now()
	for i := 0; i < b.N; i++ {
		_ = survEval.Evaluate(ctx, graph)
		_ = shelterEval.Evaluate(ctx, graph)
	}
	elapsed := time.Since(start)

	if b.N > 0 {
		perOp := elapsed / time.Duration(b.N)
		if perOp > 5*time.Millisecond {
			b.Fatalf("Bootstrap evaluators exceeded 5ms limit: %v per op", perOp)
		}
	}
}
