package system1

import (
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/vertical"
)

func trunk(x, z, h int) []vertical.Cell {
	var c []vertical.Cell
	for y := 0; y < h; y++ {
		c = append(c, vertical.Cell{X: x, Y: y + 1, Z: z})
	}
	return c
}

func reserveCtx(trees ...[]vertical.Cell) ReserveContext {
	logs := map[vertical.Cell]bool{}
	for _, t := range trees {
		for _, c := range t {
			logs[c] = true
		}
	}
	return ReserveContext{
		Agent: vertical.FeetAt(vertical.Cell{X: 5, Y: 1, Z: 0}),
		Solid: func(c vertical.Cell) bool { return c.Y <= 0 || logs[c] },
	}
}

func inv(items ...any) []*civquestv1.ItemStackProto {
	var out []*civquestv1.ItemStackProto
	for i := 0; i+1 < len(items); i += 2 {
		out = append(out, &civquestv1.ItemStackProto{ItemId: items[i].(string), Count: int32(items[i+1].(int))})
	}
	return out
}

func TestLadderReserveScalesWithTreeHeight(t *testing.T) {
	short, tall, huge := trunk(5, 3, 3), trunk(5, 3, 8), trunk(5, 3, 40)
	if r := LadderReserve([][]vertical.Cell{short}, reserveCtx(short)); r != 0 {
		t.Fatalf("short reserve = %d, want 0", r)
	}
	rt := LadderReserve([][]vertical.Cell{tall}, reserveCtx(tall))
	if rt <= 0 {
		t.Fatalf("tall reserve = %d, want > 0", rt)
	}
	// tallest tree wins
	if r := LadderReserve([][]vertical.Cell{short, tall}, reserveCtx(short, tall)); r != rt {
		t.Fatalf("mixed reserve = %d, want %d", r, rt)
	}
	if r := LadderReserve([][]vertical.Cell{huge}, reserveCtx(huge)); r > MaxLadderReserve {
		t.Fatalf("reserve %d exceeds cap", r)
	}
	if r := LadderReserve(nil, reserveCtx()); r != 0 {
		t.Fatalf("empty reserve = %d", r)
	}
}

func ladderGraph() *crafting.RecipeGraph {
	return crafting.NewRecipeGraph([]*civquestv1.RecipeNode{{
		RecipeId: "minecraft:ladder",
		IngredientSlots: []*civquestv1.RecipeIngredientSlot{
			{AcceptedItemIds: []string{"minecraft:stick"}, Count: 7},
		},
		Result: &civquestv1.ItemStackProto{ItemId: LadderItemID, Count: 3},
	}})
}

func TestLadderCraftTrigger(t *testing.T) {
	g := ladderGraph()
	sticks := inv("minecraft:stick", 7)
	if _, ok := LadderCraftStep(g, sticks, 0, true); ok {
		t.Fatal("reserve 0 must not craft")
	}
	if _, ok := LadderCraftStep(g, inv("minecraft:stick", 7, LadderItemID, 5), 5, true); ok {
		t.Fatal("at reserve must not craft")
	}
	if _, ok := LadderCraftStep(g, inv("minecraft:stick", 2), 4, true); ok {
		t.Fatal("insufficient sticks must not craft")
	}
	step, ok := LadderCraftStep(g, sticks, 4, true)
	if !ok || !step.CanExecuteNow || step.ResultItemID != LadderItemID {
		t.Fatalf("expected executable ladder craft, got %+v ok=%v", step, ok)
	}
	it := LadderCraftIntent("a", g, sticks, 4, true, nil)
	if it == nil || it.GetCraftItem() == nil || it.GetCraftItem().RecipeId != "minecraft:ladder" {
		t.Fatalf("bad intent %v", it)
	}
	if LadderCraftIntent("a", crafting.NewRecipeGraph(nil), sticks, 4, true, nil) != nil {
		t.Fatal("empty graph must not craft")
	}
}

func TestLaddersAvailableAndNeedsMore(t *testing.T) {
	i := inv(LadderItemID, 2, "minecraft:stick", 3)
	if LaddersAvailable(i) != 2 {
		t.Fatal("LaddersAvailable")
	}
	plan := vertical.ClimbPlan{LaddersNeeded: 5}
	if !NeedsMoreLadders(plan, i) || NeedsMoreLadders(plan, inv(LadderItemID, 5)) {
		t.Fatal("NeedsMoreLadders")
	}
	if NeedsMoreLadders(vertical.ClimbPlan{FromGround: true}, nil) {
		t.Fatal("ground plan needs none")
	}
}

func TestLaddersNeverDiscarded(t *testing.T) {
	i := inv(LadderItemID, 9, "minecraft:dirt", 64, "minecraft:cobblestone", 3)
	c := DiscardCandidates(i)
	if len(c) != 2 {
		t.Fatalf("candidates = %d, want 2", len(c))
	}
	for _, s := range c {
		if IsLadderItem(s.ItemId) {
			t.Fatal("ladder offered for discard")
		}
	}
	if !IsProtectedItem(LadderItemID) || IsProtectedItem("minecraft:dirt") {
		t.Fatal("IsProtectedItem")
	}
}

func TestCollectLaddersAfterUse(t *testing.T) {
	frame := &civquestv1.TelemetryFrame{
		Status: &civquestv1.AgentStatus{Position: &civquestv1.Vec3{X: 5.5, Y: 1, Z: 0.5}},
		VisibleEntities: []*civquestv1.VisibleEntity{
			{EntityId: 1, EntityType: "minecraft:item", DisplayName: "Oak Log", Position: &civquestv1.Vec3{X: 5.5, Y: 1, Z: 1.5}},
			{EntityId: 2, EntityType: "minecraft:item", DisplayName: "Ladder", Position: &civquestv1.Vec3{X: 6.5, Y: 1, Z: 3.5}},
			{EntityId: 3, EntityType: "minecraft:item", DisplayName: "Ladder", Position: &civquestv1.Vec3{X: 40, Y: 1, Z: 40}},
		},
	}
	d := FindNearestDroppedLadder(frame, 5, 3)
	if d == nil || d.EntityId != 2 {
		t.Fatalf("got %v, want entity 2", d)
	}
	it := CollectLaddersIntent("a", frame, 5, 3)
	if it == nil || it.GetNavigate() == nil || it.GetNavigate().Target.X != 6.5 {
		t.Fatalf("bad intent %v", it)
	}
	frame.VisibleEntities = frame.VisibleEntities[:1]
	if CollectLaddersIntent("a", frame, 5, 3) != nil {
		t.Fatal("no ladder drops -> nil")
	}
}
