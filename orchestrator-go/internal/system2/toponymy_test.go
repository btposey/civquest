package system2_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"unicode/utf8"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	"github.com/btposey/civquest/orchestrator-go/internal/system2"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
	"google.golang.org/protobuf/proto"
)

func TestSanitizeToponymName(t *testing.T) {
	t.Run("empty name fallback", func(t *testing.T) {
		if got := system2.SanitizeToponymName(""); got != "Unnamed Landmark" {
			t.Errorf("expected 'Unnamed Landmark', got %q", got)
		}
	})

	t.Run("short name untouched", func(t *testing.T) {
		name := "Ironwood Falls"
		if got := system2.SanitizeToponymName(name); got != name {
			t.Errorf("expected %q, got %q", name, got)
		}
	})

	t.Run("clamped to 24 runes without breaking multibyte utf8", func(t *testing.T) {
		longName := "Die Große Zauberhügelhöhe und das Tal"
		got := system2.SanitizeToponymName(longName)
		runeCount := utf8.RuneCountInString(got)
		if runeCount > system2.MaxToponymNameLen {
			t.Errorf("expected <= %d runes, got %d (%q)", system2.MaxToponymNameLen, runeCount, got)
		}
		// Confirm valid UTF-8
		if !utf8.ValidString(got) {
			t.Errorf("invalid UTF-8 in sanitized name: %q", got)
		}
	})
}

func TestSanitizeSignLine(t *testing.T) {
	t.Run("clamped to 15 runes without breaking multibyte utf8", func(t *testing.T) {
		longLine := "12345678901234äX"
		got := system2.SanitizeSignLine(longLine)
		runeCount := utf8.RuneCountInString(got)
		if runeCount > system2.MaxSignpostLineLen {
			t.Errorf("expected <= %d runes, got %d (%q)", system2.MaxSignpostLineLen, runeCount, got)
		}
		if !utf8.ValidString(got) {
			t.Errorf("invalid UTF-8 in sanitized sign line: %q", got)
		}
	})
}

func TestToponymyService_GenerateToponym_Fallback(t *testing.T) {
	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	clanID := "pioneers"

	landmarksDB, err := db.OpenLandmarksDB(resolver.ClanLandmarksDBPath(clanID), clanID)
	if err != nil {
		t.Fatalf("failed opening landmarks DB: %v", err)
	}
	defer landmarksDB.Close()

	svc := system2.NewToponymyService(nil)
	ctx := context.Background()

	req := system2.ToponymRequest{
		AgentID:  "scout_aria",
		ClanID:   clanID,
		Category: db.LandmarkCategoryMemorial,
		Coordinates: system2.Vec3Coords{
			X: 125.7,
			Y: 64.0,
			Z: -250.3,
		},
		ContextPrompt: "Sacred cairn where fallen defender stood",
		IsHangingSign: false,
		WorldTick:     4000,
		LandmarksDB:   landmarksDB,
	}

	result, err := svc.GenerateToponym(ctx, req)
	if err != nil {
		t.Fatalf("GenerateToponym failed: %v", err)
	}

	// 1. Verify DisplayName <= 24 runes
	if utf8.RuneCountInString(result.DisplayName) > 24 {
		t.Errorf("expected DisplayName <= 24 runes, got %d (%q)", utf8.RuneCountInString(result.DisplayName), result.DisplayName)
	}

	// 2. Verify all 4 sign lines <= 15 runes
	lines := []string{result.Line1, result.Line2, result.Line3, result.Line4}
	for i, l := range lines {
		count := utf8.RuneCountInString(l)
		if count > 15 {
			t.Errorf("line %d exceeds 15 runes: got %d (%q)", i+1, count, l)
		}
	}

	// 3. Verify landmark was persisted in landmarks.db
	savedLM, err := landmarksDB.GetLandmark(result.LandmarkID)
	if err != nil || savedLM == nil {
		t.Fatalf("failed retrieving saved landmark from DB: %v", err)
	}
	if savedLM.Name != result.DisplayName {
		t.Errorf("name mismatch in DB: %s vs %s", savedLM.Name, result.DisplayName)
	}
	if savedLM.Category != db.LandmarkCategoryMemorial {
		t.Errorf("category mismatch in DB: %s", savedLM.Category)
	}
	if savedLM.SignpostLine1 != result.Line1 || savedLM.SignpostLine2 != result.Line2 {
		t.Errorf("sign lines mismatch in DB: [%s, %s] vs [%s, %s]",
			savedLM.SignpostLine1, savedLM.SignpostLine2, result.Line1, result.Line2)
	}

	// 4. Verify PlaceSignpostIntent
	if result.Intent == nil {
		t.Fatalf("expected non-nil intent")
	}
	signIntent := result.Intent.GetPlaceSignpost()
	if signIntent == nil {
		t.Fatalf("expected PlaceSignpost intent action")
	}
	if signIntent.Pos.X != 125 || signIntent.Pos.Y != 64 || signIntent.Pos.Z != -251 {
		t.Errorf("unexpected integer block pos: %+v", signIntent.Pos)
	}
	if signIntent.Line1 != result.Line1 || signIntent.Line2 != result.Line2 ||
		signIntent.Line3 != result.Line3 || signIntent.Line4 != result.Line4 {
		t.Errorf("intent lines do not match result lines")
	}

	// 5. Verify protobuf serialization without UTF-8 error
	protoBytes, err := proto.Marshal(result.Intent)
	if err != nil {
		t.Fatalf("proto.Marshal failed: %v", err)
	}
	if len(protoBytes) == 0 {
		t.Fatal("expected non-empty protobuf bytes")
	}
}

func TestToponymyService_GenerateToponym_WithLLM(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req system2.ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.Temperature < 0.84 || req.Temperature > 0.86 {
			t.Errorf("expected sampling temperature ~0.85, got %.2f", req.Temperature)
		}

		synthesisResp := system2.ToponymSynthesisResponse{
			DisplayName:     "Frostpeak Crags",
			CulturalEpithet: "Frozen Spires",
			Line1:           "Frostpeak",
			Line2:           "Frozen Spires",
			Line3:           "Clan Trail 4",
			Line4:           "Travel Guarded",
			ChronicleLore:   "Towering crystalline mountain ridges towering over the tundra trail.",
		}
		jsonBytes, _ := json.Marshal(synthesisResp)

		resp := system2.ChatCompletionResponse{
			ID:    "chatcmpl-toponym-456",
			Model: "mock-model",
			Choices: []system2.ChatCompletionChoice{
				{
					Index: 0,
					Message: system2.ChatMessage{
						Role:    system2.RoleAssistant,
						Content: string(jsonBytes),
					},
					FinishReason: "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client, err := system2.NewClient(
		&xdg.GlobalConfig{
			LLMGateway: xdg.LLMGatewayConfig{
				BaseURL: server.URL + "/v1",
				Model:   "mock-model",
			},
		},
		system2.WithHTTPClient(server.Client()),
	)
	if err != nil {
		t.Fatalf("failed creating LLM client: %v", err)
	}
	defer client.Close()

	tmpDir := t.TempDir()
	landmarksDB, err := db.OpenLandmarksDB(filepath.Join(tmpDir, "landmarks.db"), "sentinels")
	if err != nil {
		t.Fatalf("failed opening landmarks DB: %v", err)
	}
	defer landmarksDB.Close()

	svc := system2.NewToponymyService(client)
	ctx := context.Background()

	result, err := svc.GenerateToponym(ctx, system2.ToponymRequest{
		AgentID:  "scout_elena",
		ClanID:   "sentinels",
		Category: db.LandmarkCategoryNatural,
		Coordinates: system2.Vec3Coords{
			X: 500.0,
			Y: 120.0,
			Z: -800.0,
		},
		ContextPrompt: "Jagged snowy icy mountain range",
		IsHangingSign: true,
		WorldTick:     7500,
		LandmarksDB:   landmarksDB,
	})
	if err != nil {
		t.Fatalf("GenerateToponym with LLM failed: %v", err)
	}

	if result.DisplayName != "Frostpeak Crags" {
		t.Errorf("expected 'Frostpeak Crags', got %q", result.DisplayName)
	}
	if result.Line1 != "Frostpeak" || result.Line2 != "Frozen Spires" {
		t.Errorf("unexpected sign lines: [%s, %s]", result.Line1, result.Line2)
	}
	if !result.IsHangingSign {
		t.Errorf("expected IsHangingSign to be true")
	}

	// Verify persistence in DB
	lm, err := landmarksDB.GetLandmark(result.LandmarkID)
	if err != nil || lm == nil {
		t.Fatalf("failed getting landmark from DB: %v", err)
	}
	if lm.LoreText != "Towering crystalline mountain ridges towering over the tundra trail." {
		t.Errorf("unexpected lore text: %q", lm.LoreText)
	}
}
