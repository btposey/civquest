package system2

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestArchitect_PromptGeneration(t *testing.T) {
	baseBP := &blueprints.Blueprint{
		ID:      "pioneer_tavern",
		Name:    "Pioneer Tavern",
		Purpose: blueprints.PurposeTavern,
		Dimensions: blueprints.Dimensions{
			Width:  3,
			Height: 2,
			Length: 3,
			MinY:   0,
		},
		Palette: map[int]string{
			1: "#minecraft:logs[axis=y]",
			2: "#minecraft:planks",
		},
		Layers: []blueprints.Layer{
			{
				Y: 0,
				Matrix: [][]int{
					{1, 1, 1},
					{1, 0, 1},
					{1, 1, 1},
				},
			},
			{
				Y: 1,
				Matrix: [][]int{
					{2, 2, 2},
					{2, 0, 2},
					{2, 2, 2},
				},
			},
		},
	}

	facts := system1.NewFactGraph()
	facts.SetString("blueprint.missing:pioneer_tavern", "Blueprint", "minecraft:iron_sword:2", "Missing swords")

	req := &ArchitecturalDerivativeRequest{
		BaseBlueprint:           baseBP,
		ModificationDescription: "Add an armory wing to the east side of the tavern.",
		TargetPurpose:           blueprints.PurposeArmory,
		TargetStyle:             "pioneer",
		Facts:                   facts,
	}

	sysPrompt := BuildArchitectDerivativeSystemPrompt()
	if !strings.Contains(sysPrompt, "Master Architect") {
		t.Errorf("expected Master Architect in system prompt")
	}
	if !strings.Contains(sysPrompt, "Player Clearance") || !strings.Contains(sysPrompt, "1.8 blocks") {
		t.Errorf("expected Rule 7 (Player Clearance: 1.8 blocks) in system prompt")
	}
	if !strings.Contains(sysPrompt, "2 vertical blocks") {
		t.Errorf("expected 2 vertical blocks standing clearance in system prompt")
	}

	userPrompt, err := BuildArchitectDerivativeUserPrompt(req)
	if err != nil {
		t.Fatalf("BuildArchitectDerivativeUserPrompt failed: %v", err)
	}

	if !strings.Contains(userPrompt, "pioneer_tavern") {
		t.Errorf("expected base blueprint ID in user prompt")
	}
	if !strings.Contains(userPrompt, "Add an armory wing") {
		t.Errorf("expected modification description in user prompt")
	}
	if !strings.Contains(userPrompt, "ARMORY") {
		t.Errorf("expected target purpose in user prompt")
	}
}

func TestArchitect_ParseArchitecturalDerivative_ASCIISlices(t *testing.T) {
	jsonResponse := `
` + "```json" + `
{
  "name": "Tavern Armory Annex",
  "purpose": "ARMORY",
  "style": "pioneer",
  "description": "An expanded tavern with reinforced weapon armory",
  "dimensions": {
    "width": 4,
    "height": 2,
    "length": 4,
    "min_y": 0
  },
  "palette": {
    "L": "#minecraft:logs[axis=y]",
    "P": "#minecraft:planks",
    "C": "minecraft:cobblestone",
    ".": "minecraft:air"
  },
  "layers": [
    {
      "y": 0,
      "grid": [
        "LLLL",
        "L..L",
        "L..L",
        "LLLL"
      ]
    },
    {
      "y": 1,
      "grid": [
        "CCCC",
        "C..C",
        "C..C",
        "CCCC"
      ]
    }
  ]
}
` + "```"

	bp, err := ParseArchitecturalDerivative(jsonResponse)
	if err != nil {
		t.Fatalf("ParseArchitecturalDerivative failed: %v", err)
	}

	if bp.Name != "Tavern Armory Annex" {
		t.Errorf("unexpected name: %s", bp.Name)
	}
	if bp.Purpose != blueprints.PurposeArmory {
		t.Errorf("unexpected purpose: %s, want %s", bp.Purpose, blueprints.PurposeArmory)
	}
	if bp.Dimensions.Width != 4 || bp.Dimensions.Length != 4 || bp.Dimensions.Height != 2 {
		t.Errorf("unexpected dimensions: %+v", bp.Dimensions)
	}
	if len(bp.BOM) == 0 {
		t.Errorf("expected computed BOM, got empty")
	}
}

func TestArchitect_ParseArchitecturalDerivative_SubterraneanBasement(t *testing.T) {
	jsonResponse := `{
  "name": "Storehouse Vault",
  "purpose": "STOREHOUSE",
  "style": "stone",
  "description": "Storehouse with subterranean food cellar",
  "dimensions": {
    "width": 3,
    "height": 2,
    "length": 3,
    "min_y": -1
  },
  "palette": {
    "C": "minecraft:stone_bricks",
    ".": "minecraft:air"
  },
  "layers": [
    {
      "y": -1,
      "grid": [
        "CCC",
        "C.C",
        "CCC"
      ]
    },
    {
      "y": 0,
      "grid": [
        "CCC",
        "C.C",
        "CCC"
      ]
    }
  ]
}`

	bp, err := ParseArchitecturalDerivative(jsonResponse)
	if err != nil {
		t.Fatalf("ParseArchitecturalDerivative failed: %v", err)
	}

	if bp.Dimensions.MinY != -1 {
		t.Errorf("expected MinY=-1, got %d", bp.Dimensions.MinY)
	}
	if bp.Excavation <= 0 {
		t.Errorf("expected positive subterranean excavation volume, got %d", bp.Excavation)
	}
}

func TestArchitect_Service_GenerateDerivative(t *testing.T) {
	mockResponse := ChatCompletionResponse{
		Choices: []ChatCompletionChoice{
			{
				Message: ChatMessage{
					Role: "assistant",
					Content: `{
  "name": "Timber Armory Extension",
  "purpose": "ARMORY",
  "dimensions": {"width": 3, "height": 2, "length": 3, "min_y": 0},
  "palette": {"L": "#minecraft:logs[axis=y]", "P": "#minecraft:planks"},
  "layers": [
    {"y": 0, "grid": ["LLL", "L.L", "LLL"]},
    {"y": 1, "grid": ["PPP", "P.P", "PPP"]}
  ]
}`,
				},
				FinishReason: "stop",
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mockResponse)
	}))
	defer server.Close()

	client, err := NewClientWithConfig(
		xdg.LLMGatewayConfig{BaseURL: server.URL},
		xdg.InferenceGovernorConfig{MaxConcurrentLLMRequests: 1},
	)
	if err != nil {
		t.Fatalf("failed creating client: %v", err)
	}
	defer client.Close()

	svc := NewArchitectService(client)
	baseBP := &blueprints.Blueprint{
		ID:      "base_cabin",
		Name:    "Base Cabin",
		Purpose: blueprints.PurposeShelter,
		Dimensions: blueprints.Dimensions{
			Width:  3,
			Height: 1,
			Length: 3,
		},
		Palette: map[int]string{1: "#minecraft:logs[axis=y]"},
		Layers: []blueprints.Layer{
			{Y: 0, Matrix: [][]int{{1, 1, 1}, {1, 0, 1}, {1, 1, 1}}},
		},
	}

	bp, err := svc.GenerateDerivative(context.Background(), &ArchitecturalDerivativeRequest{
		BaseBlueprint:           baseBP,
		ModificationDescription: "Add armory roof",
	})
	if err != nil {
		t.Fatalf("GenerateDerivative failed: %v", err)
	}

	if bp.Name != "Timber Armory Extension" {
		t.Errorf("unexpected name: %s", bp.Name)
	}
	if bp.Purpose != blueprints.PurposeArmory {
		t.Errorf("unexpected purpose: %s", bp.Purpose)
	}
}
