package agrarian

import (
	"context"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestEvaluateSpecialization_Vintner(t *testing.T) {
	ctx := SpecializationContext{
		AgentID:           "settler-vin",
		CurrentProfession: "SETTLER",
		Inventory: []*civquestv1.ItemStackProto{
			{ItemId: "vinery:red_grape", Count: 5},
			{ItemId: "vinery:savanna_grape_seeds", Count: 2},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{BlockId: "vinery:grape_bush"},
		},
		WorldTick: 2500,
	}

	res, ok := EvaluateSpecialization(ctx)
	if !ok || !res.Eligible {
		t.Fatalf("expected eligible specialization for vintner, got ok=%v, eligible=%v", ok, res.Eligible)
	}

	if res.TargetProfession != ProfessionVintner {
		t.Errorf("expected TargetProfession %s, got %s", ProfessionVintner, res.TargetProfession)
	}
	if res.SuggestedHobby != "VITICULTURIST" {
		t.Errorf("expected hobby VITICULTURIST, got %s", res.SuggestedHobby)
	}
	if res.Confidence < 0.5 {
		t.Errorf("expected confidence >= 0.5, got %f", res.Confidence)
	}
}

func TestEvaluateSpecialization_Rancher(t *testing.T) {
	ctx := SpecializationContext{
		AgentID:           "settler-ran",
		CurrentProfession: "UNASSIGNED",
		Inventory: []*civquestv1.ItemStackProto{
			{ItemId: "minecraft:lead", Count: 2},
			{ItemId: "minecraft:saddle", Count: 1},
			{ItemId: "minecraft:wheat", Count: 10},
		},
		NearbyEntities: []*civquestv1.VisibleEntity{
			{DisplayName: "Cow", EntityType: "minecraft:cow"},
		},
		WorldTick: 3000,
	}

	res, ok := EvaluateSpecialization(ctx)
	if !ok || !res.Eligible {
		t.Fatalf("expected eligible specialization for rancher, got ok=%v, eligible=%v", ok, res.Eligible)
	}

	if res.TargetProfession != ProfessionRancher {
		t.Errorf("expected TargetProfession %s, got %s", ProfessionRancher, res.TargetProfession)
	}
	if res.SuggestedHobby != "ANIMAL_BREEDER" {
		t.Errorf("expected hobby ANIMAL_BREEDER, got %s", res.SuggestedHobby)
	}
}

func TestEvaluateSpecialization_Chef(t *testing.T) {
	ctx := SpecializationContext{
		AgentID:           "settler-chef",
		CurrentProfession: "FARMER",
		Inventory: []*civquestv1.ItemStackProto{
			{ItemId: "farmersdelight:tomato", Count: 4},
			{ItemId: "farmersdelight:onion", Count: 3},
			{ItemId: "farmersdelight:cabbage", Count: 2},
		},
		InterestingBlocks: []*civquestv1.VisibleBlock{
			{BlockId: "farmersdelight:cooking_pot"},
		},
		WorldTick: 3500,
	}

	res, ok := EvaluateSpecialization(ctx)
	if !ok || !res.Eligible {
		t.Fatalf("expected eligible specialization for chef, got ok=%v, eligible=%v", ok, res.Eligible)
	}

	if res.TargetProfession != ProfessionChef {
		t.Errorf("expected TargetProfession %s, got %s", ProfessionChef, res.TargetProfession)
	}
	if res.SuggestedHobby != "CULINARY_ARTIST" {
		t.Errorf("expected hobby CULINARY_ARTIST, got %s", res.SuggestedHobby)
	}
}

func TestEvaluateSpecialization_IneligibleProfession(t *testing.T) {
	ctx := SpecializationContext{
		AgentID:           "guard-1",
		CurrentProfession: "GUARD", // Established military role, should not transition
		Inventory: []*civquestv1.ItemStackProto{
			{ItemId: "vinery:red_grape", Count: 10},
		},
		WorldTick: 4000,
	}

	res, ok := EvaluateSpecialization(ctx)
	if ok || res.Eligible {
		t.Errorf("expected non-eligible for GUARD, got ok=%v, res=%+v", ok, res)
	}
}

func TestApplySpecialization(t *testing.T) {
	identity := &xdg.AgentIdentityConfig{
		Name:       "aria",
		Profession: "SETTLER",
	}

	res := SpecializationResult{
		Eligible:          true,
		CurrentProfession: "SETTLER",
		TargetProfession:  ProfessionChef,
		SuggestedHobby:    "CULINARY_ARTIST",
		Diction:           "aromatic culinary phrasing",
	}

	if err := ApplySpecialization(identity, res); err != nil {
		t.Fatalf("unexpected error applying specialization: %v", err)
	}

	if identity.Profession != ProfessionChef {
		t.Errorf("expected profession %s, got %s", ProfessionChef, identity.Profession)
	}
	if identity.SecondaryHobby != "CULINARY_ARTIST" {
		t.Errorf("expected hobby CULINARY_ARTIST, got %s", identity.SecondaryHobby)
	}
	if identity.Voice.Diction != "aromatic culinary phrasing" {
		t.Errorf("expected updated diction, got %s", identity.Voice.Diction)
	}
}

func TestRecordSpecializationMemory(t *testing.T) {
	agentDataDir := t.TempDir()

	agentDB, err := db.OpenAgentDB(agentDataDir)
	if err != nil {
		t.Fatalf("failed to open agent db: %v", err)
	}
	defer agentDB.Close()

	res := SpecializationResult{
		Eligible:          true,
		CurrentProfession: "SETTLER",
		TargetProfession:  ProfessionVintner,
		KeyTriggerItem:    "vinery:red_grape",
	}

	ctx := context.Background()
	if err := RecordSpecializationMemory(ctx, agentDB, "agent-test-1", res, 1200); err != nil {
		t.Fatalf("failed to record memory: %v", err)
	}

	// Query memories
	mems, err := agentDB.GetRecentMemories(ctx, 10)
	if err != nil {
		t.Fatalf("failed to query episodic memories: %v", err)
	}
	if len(mems) != 1 {
		t.Fatalf("expected 1 episodic memory, got %d", len(mems))
	}
	if mems[0].EventCategory != "DISCOVERY" {
		t.Errorf("expected DISCOVERY event category, got %s", mems[0].EventCategory)
	}

	// Query skill
	skill, err := agentDB.GetSkill(ctx, ProfessionVintner)
	if err != nil {
		t.Fatalf("failed to get skill: %v", err)
	}
	if skill == nil || skill.SkillName != ProfessionVintner {
		t.Fatalf("expected skill %s recorded, got %+v", ProfessionVintner, skill)
	}
}
