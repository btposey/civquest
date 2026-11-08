package system2_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system2"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
	"google.golang.org/protobuf/proto"
)

func TestPaginateProse_LimitsAndWordWrapping(t *testing.T) {
	t.Run("empty prose", func(t *testing.T) {
		pages := system2.PaginateProse("")
		if len(pages) != 1 || pages[0] != "" {
			t.Errorf("expected 1 empty page, got %v", pages)
		}
	})

	t.Run("short prose single line", func(t *testing.T) {
		text := "A short tale."
		pages := system2.PaginateProse(text)
		if len(pages) != 1 {
			t.Fatalf("expected 1 page, got %d", len(pages))
		}
		if pages[0] != text {
			t.Errorf("expected %q, got %q", text, pages[0])
		}
	})

	t.Run("line wrapping without word truncation", func(t *testing.T) {
		text := "The quick brown fox jumps over the lazy dog in the forest."
		pages := system2.PaginateProse(text)
		if len(pages) != 1 {
			t.Fatalf("expected 1 page, got %d", len(pages))
		}

		lines := strings.Split(pages[0], "\n")
		for i, line := range lines {
			if len(line) > system2.MaxMinecraftCharsPerLine {
				t.Errorf("line %d exceeds max line length %d: %q (len %d)",
					i, system2.MaxMinecraftCharsPerLine, line, len(line))
			}
		}
		// Confirm words are intact
		rejoined := strings.Join(strings.Fields(strings.ReplaceAll(pages[0], "\n", " ")), " ")
		if rejoined != text {
			t.Errorf("word corruption detected:\nexpected: %q\ngot:      %q", text, rejoined)
		}
	})

	t.Run("oversized word handling", func(t *testing.T) {
		// A word longer than 19 characters (e.g. 35 chars)
		longWord := "Supercalifragilisticexpialidocious"
		pages := system2.PaginateProse(longWord)
		if len(pages) < 1 {
			t.Fatal("expected at least 1 page")
		}
		lines := strings.Split(pages[0], "\n")
		for i, line := range lines {
			if len(line) > system2.MaxMinecraftCharsPerLine {
				t.Errorf("line %d exceeds max line length: %q (len %d)", i, line, len(line))
			}
		}
		combined := strings.Join(lines, "")
		if combined != longWord {
			t.Errorf("chunked word does not reconstruct original: %q vs %q", combined, longWord)
		}
	})

	t.Run("utf8 multi-byte rune boundary safety", func(t *testing.T) {
		// A word with multi-byte UTF-8 character spanning the 19th character boundary
		word := "123456789012345678äX"
		pages := system2.PaginateProse(word)
		if len(pages) < 1 {
			t.Fatal("expected at least 1 page")
		}

		// Verify protobuf serialization succeeds without invalid UTF-8 error
		intent := &civquestv1.CreateWrittenBookIntent{
			Title: system2.SanitizeBookTitle("Tome of Valhalla with runes: äöü—«»"),
			Pages: pages,
		}
		data, err := proto.Marshal(intent)
		if err != nil {
			t.Fatalf("proto.Marshal failed with UTF-8 error: %v", err)
		}
		if len(data) == 0 {
			t.Fatal("expected serialized protobuf bytes")
		}
	})

	t.Run("14 lines per page constraint", func(t *testing.T) {
		// Generate 20 distinct lines of 10 chars each
		var sb strings.Builder
		for i := 1; i <= 20; i++ {
			sb.WriteString("Line of text\n")
		}
		pages := system2.PaginateProse(sb.String())
		if len(pages) < 2 {
			t.Fatalf("expected at least 2 pages for 20 lines, got %d", len(pages))
		}

		for pIdx, page := range pages {
			lines := strings.Split(page, "\n")
			if len(lines) > system2.MaxMinecraftLinesPerPage {
				t.Errorf("page %d has %d lines, exceeding limit %d",
					pIdx, len(lines), system2.MaxMinecraftLinesPerPage)
			}
			if len(page) > system2.MaxMinecraftCharsPerPage {
				t.Errorf("page %d has %d chars, exceeding limit %d",
					pIdx, len(page), system2.MaxMinecraftCharsPerPage)
			}
		}
	})

	t.Run("250 characters per page constraint", func(t *testing.T) {
		// 13 lines of 19 chars = 247 chars + 12 newlines = 259 chars -> must split across pages!
		var sb strings.Builder
		for i := 1; i <= 15; i++ {
			sb.WriteString("1234567890123456789\n")
		}
		pages := system2.PaginateProse(sb.String())
		for pIdx, page := range pages {
			if len(page) > system2.MaxMinecraftCharsPerPage {
				t.Errorf("page %d has %d chars, exceeding limit %d",
					pIdx, len(page), system2.MaxMinecraftCharsPerPage)
			}
		}
	})

	t.Run("max 100 pages cap", func(t *testing.T) {
		var sb strings.Builder
		for i := 0; i < 2000; i++ {
			sb.WriteString("Long line of prose here.\n")
		}
		pages := system2.PaginateProse(sb.String())
		if len(pages) > system2.MaxMinecraftBookPages {
			t.Errorf("expected pages <= %d, got %d", system2.MaxMinecraftBookPages, len(pages))
		}
	})
}

func TestSanitizeBookTitle(t *testing.T) {
	if got := system2.SanitizeBookTitle(""); got != "Clan Chronicle" {
		t.Errorf("expected 'Clan Chronicle', got %q", got)
	}

	short := "The Rise of Pioneers"
	if got := system2.SanitizeBookTitle(short); got != short {
		t.Errorf("expected %q, got %q", short, got)
	}

	long := "This is an extremely long and grand title for a book that exceeds thirty two characters"
	got := system2.SanitizeBookTitle(long)
	if len(got) > 32 {
		t.Errorf("expected len <= 32, got %d (%q)", len(got), got)
	}
	if got != long[:32] {
		t.Errorf("expected %q, got %q", long[:32], got)
	}
}

func TestChroniclerService_ComposeChronicle_Fallback(t *testing.T) {
	tmpDir := t.TempDir()
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	agentID := "chronicler_fallback"
	_ = resolver.InitAgentDirectories(agentID)

	agentDB, err := db.OpenAgentDBForAgent(resolver, agentID)
	if err != nil {
		t.Fatalf("failed opening agent DB: %v", err)
	}
	defer agentDB.Close()

	ctx := context.Background()
	svc := system2.NewChroniclerService(nil)

	// 1. Should fail when no unchronicled memories exist
	_, err = svc.ComposeChronicle(ctx, agentDB, system2.ChronicleRequest{
		AgentID: agentID,
		ClanID:  "pioneers",
	})
	if err != system2.ErrNoUnchronicledMemories {
		t.Fatalf("expected ErrNoUnchronicledMemories, got: %v", err)
	}

	// 2. Insert high-saliency event (0.85) and permadeath event (1.0)
	mem1ID, err := agentDB.InsertEpisodicMemory(ctx, db.EpisodicMemory{
		WorldTick:      1000,
		EventCategory:  "DISCOVERY",
		SaliencyScore:  0.85,
		SummaryText:    "Discovered the Ironwood Valley and abundant mineral deposits.",
		ActorsJSON:     `["scout_1"]`,
		LocationCoords: "100.0,64.0,-200.0",
		Chronicled:     false,
	}, nil)
	if err != nil {
		t.Fatalf("inserting memory 1: %v", err)
	}

	mem2ID, err := agentDB.InsertEpisodicMemory(ctx, db.EpisodicMemory{
		WorldTick:      2500,
		EventCategory:  "DEATH",
		SaliencyScore:  1.0,
		SummaryText:    "Aria fell in valiant combat defending the frontier camp.",
		ActorsJSON:     `["aria", "Zombie"]`,
		LocationCoords: "120.0,65.0,-210.0",
		Chronicled:     false,
	}, nil)
	if err != nil {
		t.Fatalf("inserting memory 2: %v", err)
	}

	// Also insert a low-saliency event (0.20) that should NOT be included
	_, err = agentDB.InsertEpisodicMemory(ctx, db.EpisodicMemory{
		WorldTick:      1500,
		EventCategory:  "TRADE",
		SaliencyScore:  0.20,
		SummaryText:    "Traded 5 apples for 2 wheat.",
		ActorsJSON:     `["trader_1"]`,
		LocationCoords: "105.0,64.0,-202.0",
		Chronicled:     false,
	}, nil)
	if err != nil {
		t.Fatalf("inserting memory 3: %v", err)
	}

	// 3. Compose chronicle targeting a lectern
	targetLectern := &civquestv1.BlockPosProto{
		X: 10,
		Y: 64,
		Z: 20,
	}
	chronicle, err := svc.ComposeChronicle(ctx, agentDB, system2.ChronicleRequest{
		AgentID:              agentID,
		ClanID:               "pioneers",
		AuthorName:           "Elder Chronicler",
		MinSaliency:          0.70,
		Limit:                10,
		TargetLecternOrShelf: targetLectern,
	})
	if err != nil {
		t.Fatalf("ComposeChronicle failed: %v", err)
	}

	if chronicle.Title != "Chronicle of Pioneers" {
		t.Errorf("expected title 'Chronicle of Pioneers', got %q", chronicle.Title)
	}
	if chronicle.Author != "Elder Chronicler" {
		t.Errorf("expected author 'Elder Chronicler', got %q", chronicle.Author)
	}
	if len(chronicle.Pages) == 0 {
		t.Errorf("expected non-empty pages in chronicle book")
	}
	if len(chronicle.ChronicledIDs) != 2 {
		t.Fatalf("expected 2 chronicled IDs, got %d", len(chronicle.ChronicledIDs))
	}
	if chronicle.ChronicledIDs[0] != mem1ID || chronicle.ChronicledIDs[1] != mem2ID {
		t.Errorf("expected IDs [%d, %d], got %v", mem1ID, mem2ID, chronicle.ChronicledIDs)
	}

	// Verify CreateWrittenBookIntent
	if chronicle.Intent == nil {
		t.Fatalf("expected non-nil intent")
	}
	createBook := chronicle.Intent.GetCreateBook()
	if createBook == nil {
		t.Fatalf("expected non-nil CreateWrittenBook action")
	}
	if createBook.Title != chronicle.Title {
		t.Errorf("intent title mismatch: %s vs %s", createBook.Title, chronicle.Title)
	}
	if createBook.Author != "Elder Chronicler" {
		t.Errorf("intent author mismatch: %s", createBook.Author)
	}
	if createBook.TargetLecternOrShelf.X != 10 || createBook.TargetLecternOrShelf.Y != 64 || createBook.TargetLecternOrShelf.Z != 20 {
		t.Errorf("intent target lectern mismatch: %+v", createBook.TargetLecternOrShelf)
	}

	// 4. Verify memories were marked as chronicled in the database
	m1, _ := agentDB.GetEpisodicMemory(ctx, mem1ID)
	if m1 == nil || !m1.Chronicled {
		t.Errorf("expected mem1 to be marked chronicled")
	}
	m2, _ := agentDB.GetEpisodicMemory(ctx, mem2ID)
	if m2 == nil || !m2.Chronicled {
		t.Errorf("expected mem2 to be marked chronicled")
	}

	// 5. Subsequent call finds no unchronicled memories
	_, err = svc.ComposeChronicle(ctx, agentDB, system2.ChronicleRequest{
		AgentID: agentID,
		ClanID:  "pioneers",
	})
	if err != system2.ErrNoUnchronicledMemories {
		t.Fatalf("expected ErrNoUnchronicledMemories after batch chronicled, got: %v", err)
	}
}

func TestChroniclerService_ComposeChronicle_WithLLM(t *testing.T) {
	// Mock OpenAI LLM Server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req system2.ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Verify sampling temperature T=0.90
		if req.Temperature < 0.89 || req.Temperature > 0.91 {
			t.Errorf("expected temperature ~0.90, got %.2f", req.Temperature)
		}

		synthesisResp := system2.ChronicleSynthesisResponse{
			Title: "The Heroic Stand of Aria",
			Prose: "In the quiet dawn of the frontier, darkness crept upon the boundary...\n\nAria raised her shield against the swarm, giving her life so that the pioneers might endure.\n\nHer sacrifice is forever carved into the memory of our people.",
		}
		jsonBytes, _ := json.Marshal(synthesisResp)

		resp := system2.ChatCompletionResponse{
			ID:    "chatcmpl-chronicler-123",
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
	resolver := xdg.NewResolver(xdg.WithCustomRoot(tmpDir))
	agentID := "chronicler_llm"
	_ = resolver.InitAgentDirectories(agentID)

	agentDB, err := db.OpenAgentDBForAgent(resolver, agentID)
	if err != nil {
		t.Fatalf("failed opening agent DB: %v", err)
	}
	defer agentDB.Close()

	ctx := context.Background()
	_, err = agentDB.InsertEpisodicMemory(ctx, db.EpisodicMemory{
		WorldTick:     5000,
		EventCategory: "DEATH",
		SaliencyScore: 1.0,
		SummaryText:   "Aria sacrificed herself defending against a zombie siege.",
		Chronicled:    false,
	}, nil)
	if err != nil {
		t.Fatalf("inserting memory: %v", err)
	}

	svc := system2.NewChroniclerService(client)
	chronicle, err := svc.ComposeChronicle(ctx, agentDB, system2.ChronicleRequest{
		AgentID:       agentID,
		ClanID:        "sentinels",
		AuthorName:    "Chronicler Marcus",
		BookshelfSlot: 2,
	})
	if err != nil {
		t.Fatalf("ComposeChronicle with LLM failed: %v", err)
	}

	if chronicle.Title != "The Heroic Stand of Aria" {
		t.Errorf("expected title 'The Heroic Stand of Aria', got %q", chronicle.Title)
	}
	if len(chronicle.Pages) == 0 {
		t.Fatalf("expected non-empty pages")
	}
	for i, page := range chronicle.Pages {
		lines := strings.Split(page, "\n")
		if len(lines) > 14 {
			t.Errorf("page %d has %d lines, exceeding 14", i, len(lines))
		}
		if len(page) > 250 {
			t.Errorf("page %d has %d chars, exceeding 250", i, len(page))
		}
		for j, l := range lines {
			if len(l) > 19 {
				t.Errorf("page %d line %d has %d chars, exceeding 19: %q", i, j, len(l), l)
			}
		}
	}
}
