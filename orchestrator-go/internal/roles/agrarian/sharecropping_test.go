package agrarian

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

func TestGenerateProposalMessage(t *testing.T) {
	bounds := &civquestv1.BoundingBoxProto{
		Min: &civquestv1.BlockPosProto{X: 100, Y: 64, Z: 200},
		Max: &civquestv1.BlockPosProto{X: 120, Y: 64, Z: 220},
	}
	chestPos := &civquestv1.BlockPosProto{X: 105, Y: 65, Z: 205}

	msg := GenerateProposalMessage("Steve", bounds, chestPos, "minecraft:wheat")
	if !strings.Contains(msg, "Steve") {
		t.Error("expected message to address Steve")
	}
	if !strings.Contains(msg, "50/50") {
		t.Error("expected message to mention 50/50")
	}
	if !strings.Contains(msg, "105, 65, 205") {
		t.Error("expected message to mention chest coordinates")
	}
}

func TestIsTenancyAccepted(t *testing.T) {
	acceptedCases := []string{
		"I agree, let's do this!",
		"Deal!",
		"Yes, sounds good to me.",
		"Sure, shake on it.",
		"ok",
		"approved",
	}
	for _, c := range acceptedCases {
		if !IsTenancyAccepted(c) {
			t.Errorf("expected %q to be recognized as acceptance", c)
		}
	}

	rejectedCases := []string{
		"No thanks",
		"Get off my land",
		"Not interested",
		"Who are you?",
		"",
	}
	for _, c := range rejectedCases {
		if IsTenancyAccepted(c) {
			t.Errorf("expected %q to NOT be recognized as acceptance", c)
		}
	}
}

func TestSharecroppingEngine_NegotiationAndRegistration(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "contracts.db")

	contractsDB, err := db.OpenContractsDB(dbPath, "clan-pioneers")
	if err != nil {
		t.Fatalf("failed to open contracts DB: %v", err)
	}
	defer contractsDB.Close()

	engine := NewSharecroppingEngine("agent-farmer-1", "clan-pioneers", contractsDB)

	survey := &civquestv1.FarmlandSurveyEvent{
		Bounds: &civquestv1.BoundingBoxProto{
			Min: &civquestv1.BlockPosProto{X: 10, Y: 63, Z: 20},
			Max: &civquestv1.BlockPosProto{X: 30, Y: 65, Z: 40},
		},
		PrimaryCropId:       "minecraft:wheat",
		MatureCropCount:     15,
		TotalFarmlandBlocks: 20,
		InferredOwnerUuid:   "player-uuid-42",
	}
	chestPos := &civquestv1.BlockPosProto{X: 12, Y: 64, Z: 22}

	ctx := context.Background()
	proposal, err := engine.HandleSurveyEvent(ctx, survey, chestPos, 1000)
	if err != nil {
		t.Fatalf("unexpected error handling survey event: %v", err)
	}
	if proposal == nil || proposal.PlayerUUID != "player-uuid-42" {
		t.Fatalf("expected proposal for player-uuid-42, got %+v", proposal)
	}

	// Player accepts via chat
	chat := &civquestv1.ChatEvent{
		SenderUuid: "player-uuid-42",
		SenderName: "Steve",
		Message:    "Sounds good, I accept the deal!",
	}

	contract, speech, err := engine.ProcessChatNegotiation(ctx, chat, 1050)
	if err != nil {
		t.Fatalf("unexpected error processing negotiation: %v", err)
	}
	if contract == nil {
		t.Fatal("expected registered contract, got nil")
	}
	if contract.SplitPlayerPercent != 50.0 || contract.SplitClanPercent != 50.0 {
		t.Errorf("expected 50/50 split, got %f / %f", contract.SplitPlayerPercent, contract.SplitClanPercent)
	}
	if contract.ChestX != 12 || contract.ChestY != 64 || contract.ChestZ != 22 {
		t.Errorf("chest coordinates mismatch: got (%d, %d, %d)", contract.ChestX, contract.ChestY, contract.ChestZ)
	}
	if !strings.Contains(speech, "50%") {
		t.Errorf("expected speech to mention 50%%, got %q", speech)
	}

	// Verify contract in DB
	stored, err := contractsDB.GetContract(contract.ContractID)
	if err != nil {
		t.Fatalf("failed to query contract from DB: %v", err)
	}
	if stored == nil || stored.ContractID != contract.ContractID {
		t.Fatalf("contract was not found in DB")
	}
}

func TestCalculate5050YieldSplit(t *testing.T) {
	harvested := []*civquestv1.ItemStackProto{
		{ItemId: "minecraft:wheat", Count: 10},
		{ItemId: "minecraft:wheat_seeds", Count: 7},
		{ItemId: "farmersdelight:tomato", Count: 1},
	}

	playerShare, clanShare := Calculate5050YieldSplit(harvested)

	// Check wheat (10 total -> 5 player, 5 clan)
	if playerShare[0].Count != 5 || clanShare[0].Count != 5 {
		t.Errorf("expected 5/5 wheat split, got player=%d clan=%d", playerShare[0].Count, clanShare[0].Count)
	}

	// Check seeds (7 total -> 4 player, 3 clan)
	if playerShare[1].Count != 4 || clanShare[1].Count != 3 {
		t.Errorf("expected 4/3 seeds split, got player=%d clan=%d", playerShare[1].Count, clanShare[1].Count)
	}

	// Check tomato (1 total -> 1 player, 0 clan)
	if playerShare[2].Count != 1 {
		t.Errorf("expected 1 tomato for player, got %d", playerShare[2].Count)
	}
	if len(clanShare) != 2 {
		t.Errorf("expected clan to have 2 item entries, got %d", len(clanShare))
	}
}

func TestPlanDailyHarvestRoutine(t *testing.T) {
	contract := &db.SharecroppingContract{
		ContractID: "sc-test-1",
		PlotMinX:   10,
		PlotMinY:   60,
		PlotMinZ:   10,
		PlotMaxX:   20,
		PlotMaxY:   70,
		PlotMaxZ:   20,
	}

	visibleBlocks := []*civquestv1.VisibleBlock{
		// Mature wheat inside bounds
		{
			Pos:              &civquestv1.BlockPosProto{X: 12, Y: 64, Z: 12},
			BlockId:          "minecraft:wheat",
			BlockStateString: "minecraft:wheat[age=7]",
		},
		// Immature wheat inside bounds
		{
			Pos:              &civquestv1.BlockPosProto{X: 14, Y: 64, Z: 14},
			BlockId:          "minecraft:wheat",
			BlockStateString: "minecraft:wheat[age=3]",
		},
		// Mature wheat outside bounds
		{
			Pos:              &civquestv1.BlockPosProto{X: 35, Y: 64, Z: 35},
			BlockId:          "minecraft:wheat",
			BlockStateString: "minecraft:wheat[age=7]",
		},
	}

	intents := PlanDailyHarvestRoutine("agent-farmer", contract, visibleBlocks)
	// Should produce 3 intents for the one mature crop: navigate, break, replant
	if len(intents) != 3 {
		t.Fatalf("expected 3 intents for 1 mature crop, got %d", len(intents))
	}

	navIntent := intents[0].GetNavigate()
	if navIntent == nil || navIntent.Target.X != 12 || navIntent.Target.Z != 12 {
		t.Errorf("unexpected navigate intent: %+v", navIntent)
	}

	breakIntent := intents[1].GetInteractBlock()
	if breakIntent == nil || breakIntent.InteractionType != civquestv1.InteractBlockIntent_HARVEST_BREAK {
		t.Errorf("unexpected break intent: %+v", breakIntent)
	}

	replantIntent := intents[2].GetInteractBlock()
	if replantIntent == nil || replantIntent.InteractionType != civquestv1.InteractBlockIntent_PLACE_BLOCK ||
		replantIntent.BlockOrItemId != "minecraft:wheat_seeds" {
		t.Errorf("unexpected replant intent: %+v", replantIntent)
	}
}

func TestGenerateDepositIntents(t *testing.T) {
	contract := &db.SharecroppingContract{
		ChestX: 100,
		ChestY: 65,
		ChestZ: 200,
	}

	playerItems := []*civquestv1.ItemStackProto{
		{ItemId: "minecraft:wheat", Count: 16},
	}
	clanItems := []*civquestv1.ItemStackProto{
		{ItemId: "minecraft:wheat", Count: 16},
	}
	granaryChest := &civquestv1.BlockPosProto{X: 50, Y: 64, Z: 50}

	intents := GenerateDepositIntents("agent-farmer", contract, playerItems, clanItems, granaryChest)

	// Should have 4 intents:
	// 1. Navigate to player chest
	// 2. Transfer container to player chest
	// 3. Navigate to clan granary chest
	// 4. Transfer container to granary chest
	if len(intents) != 4 {
		t.Fatalf("expected 4 intents, got %d", len(intents))
	}

	// Verify player deposit
	playerTransfer := intents[1].GetTransferContainer()
	if playerTransfer == nil || !playerTransfer.DepositIntoContainer ||
		playerTransfer.ContainerPos.X != 100 || playerTransfer.Items[0].Count != 16 {
		t.Errorf("unexpected player transfer: %+v", playerTransfer)
	}

	// Verify granary deposit
	granaryTransfer := intents[3].GetTransferContainer()
	if granaryTransfer == nil || !granaryTransfer.DepositIntoContainer ||
		granaryTransfer.ContainerPos.X != 50 || granaryTransfer.Items[0].Count != 16 {
		t.Errorf("unexpected granary transfer: %+v", granaryTransfer)
	}
}
