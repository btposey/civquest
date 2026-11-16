package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	"github.com/btposey/civquest/orchestrator-go/internal/system2"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func setupTestServer(t *testing.T) (*Server, string) {
	t.Helper()

	tmpRoot := t.TempDir()
	configDir := filepath.Join(tmpRoot, "config")
	dataDir := filepath.Join(tmpRoot, "data")
	stateDir := filepath.Join(tmpRoot, "state")

	t.Setenv("XDG_CONFIG_HOME", configDir)
	t.Setenv("XDG_DATA_HOME", dataDir)
	t.Setenv("XDG_STATE_HOME", stateDir)

	_ = os.MkdirAll(filepath.Join(configDir, "civquest", "manifests"), 0755)
	_ = os.MkdirAll(filepath.Join(dataDir, "civquest", "blueprints"), 0755)
	_ = os.MkdirAll(filepath.Join(stateDir, "civquest", "sockets"), 0755)

	resolver := xdg.NewResolver(
		xdg.WithConfigHome(configDir),
		xdg.WithDataHome(dataDir),
		xdg.WithStateHome(stateDir),
	)

	// Use port 0 for dynamic ephemeral port allocation in tests
	srv := NewServer(Config{
		Host:     "127.0.0.1",
		Port:     0,
		Resolver: resolver,
	})

	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start test server: %v", err)
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Stop(ctx)
	})

	return srv, srv.URL()
}

func TestServer_Health(t *testing.T) {
	_, srvURL := setupTestServer(t)

	resp, err := http.Get(srvURL + "/api/health")
	if err != nil {
		t.Fatalf("GET /api/health failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var data map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("failed to decode health response: %v", err)
	}

	if data["status"] != "ok" {
		t.Errorf("expected status 'ok', got %q", data["status"])
	}
	if data["time"] == "" {
		t.Error("expected non-empty timestamp in health response")
	}
}

func TestServer_BlueprintsCRUD(t *testing.T) {
	_, srvURL := setupTestServer(t)

	// 1. Initial list should be empty
	resp, err := http.Get(srvURL + "/api/blueprints")
	if err != nil {
		t.Fatalf("GET /api/blueprints failed: %v", err)
	}
	var list []*blueprints.Blueprint
	_ = json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if len(list) != 0 {
		t.Fatalf("expected empty blueprints list initially, got %d", len(list))
	}

	// 2. POST create new blueprint
	newBP := &blueprints.Blueprint{
		ID:      "test_shack",
		Name:    "Test Survival Shack",
		Purpose: "HOME",
		Style:   "rustic",
		Dimensions: blueprints.Dimensions{
			Width:  3,
			Height: 3,
			Length: 3,
			MinY:   0,
		},
		Palette: map[int]string{
			1: "minecraft:oak_planks",
			2: "minecraft:oak_log[axis=y]",
		},
		Layers: []blueprints.Layer{
			{
				Y: 0,
				Matrix: [][]int{
					{2, 1, 2},
					{1, 1, 1},
					{2, 1, 2},
				},
			},
		},
	}

	payload, _ := json.Marshal(newBP)
	postResp, err := http.Post(srvURL+"/api/blueprints", "application/json", strings.NewReader(string(payload)))
	if err != nil {
		t.Fatalf("POST /api/blueprints failed: %v", err)
	}
	defer postResp.Body.Close()

	if postResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(postResp.Body)
		t.Fatalf("expected status 201 Created, got %d: %s", postResp.StatusCode, string(body))
	}

	var createdBP blueprints.Blueprint
	if err := json.NewDecoder(postResp.Body).Decode(&createdBP); err != nil {
		t.Fatalf("failed decoding created blueprint: %v", err)
	}
	if createdBP.ID != "test_shack" {
		t.Errorf("expected created ID 'test_shack', got %q", createdBP.ID)
	}
	if createdBP.BOM["minecraft:oak_planks"] != 5 {
		t.Errorf("expected 5 oak_planks in BOM, got %d", createdBP.BOM["minecraft:oak_planks"])
	}
	if createdBP.BOM["minecraft:oak_log"] != 4 {
		t.Errorf("expected 4 oak_log in BOM, got %d", createdBP.BOM["minecraft:oak_log"])
	}

	// 3. GET by ID
	getResp, err := http.Get(srvURL + "/api/blueprints/test_shack")
	if err != nil {
		t.Fatalf("GET /api/blueprints/test_shack failed: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", getResp.StatusCode)
	}
	var fetchedBP blueprints.Blueprint
	_ = json.NewDecoder(getResp.Body).Decode(&fetchedBP)
	if fetchedBP.Name != "Test Survival Shack" {
		t.Errorf("expected name 'Test Survival Shack', got %q", fetchedBP.Name)
	}

	// 4. PUT update blueprint
	fetchedBP.Description = "An updated cozy wooden outpost"
	putPayload, _ := json.Marshal(fetchedBP)
	req, _ := http.NewRequest(http.MethodPut, srvURL+"/api/blueprints/test_shack", strings.NewReader(string(putPayload)))
	req.Header.Set("Content-Type", "application/json")
	putResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT /api/blueprints/test_shack failed: %v", err)
	}
	defer putResp.Body.Close()
	if putResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(putResp.Body)
		t.Fatalf("expected status 200 OK, got %d: %s", putResp.StatusCode, string(body))
	}

	// 5. Verify update persisted
	verifyResp, _ := http.Get(srvURL + "/api/blueprints/test_shack")
	var updatedBP blueprints.Blueprint
	_ = json.NewDecoder(verifyResp.Body).Decode(&updatedBP)
	verifyResp.Body.Close()
	if updatedBP.Description != "An updated cozy wooden outpost" {
		t.Errorf("expected updated description, got %q", updatedBP.Description)
	}

	// 6. DELETE blueprint
	delReq, _ := http.NewRequest(http.MethodDelete, srvURL+"/api/blueprints/test_shack", nil)
	delResp, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatalf("DELETE /api/blueprints/test_shack failed: %v", err)
	}
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK on delete, got %d", delResp.StatusCode)
	}

	// 7. Verify 404 after deletion
	get404Resp, err := http.Get(srvURL + "/api/blueprints/test_shack")
	if err != nil {
		t.Fatalf("GET after delete failed: %v", err)
	}
	defer get404Resp.Body.Close()
	if get404Resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 Not Found after delete, got %d", get404Resp.StatusCode)
	}
}

func TestServer_BlueprintValidate(t *testing.T) {
	_, srvURL := setupTestServer(t)

	// 1. Valid subterranean blueprint
	validSubterranean := &blueprints.Blueprint{
		ID:      "miner_cellar",
		Name:    "Miner Cellar",
		Purpose: "STOREHOUSE",
		Dimensions: blueprints.Dimensions{
			Width:  3,
			Height: 3,
			Length: 3,
			MinY:   -2,
		},
		Palette: map[int]string{
			1: "minecraft:stone_bricks",
		},
		Layers: []blueprints.Layer{
			{
				Y: -2,
				Matrix: [][]int{
					{1, 1, 1},
					{1, 1, 1},
					{1, 1, 1},
				},
			},
			{
				Y: -1,
				Matrix: [][]int{
					{1, 0, 1},
					{0, 0, 0},
					{1, 0, 1},
				},
			},
			{
				Y: 0,
				Matrix: [][]int{
					{1, 1, 1},
					{1, 0, 1},
					{1, 1, 1},
				},
			},
		},
	}

	data, _ := json.Marshal(validSubterranean)
	resp, err := http.Post(srvURL+"/api/blueprints/validate", "application/json", strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("POST /api/blueprints/validate failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var res ValidationResult
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("failed decoding validate response: %v", err)
	}

	if !res.Valid {
		t.Errorf("expected valid blueprint, got errors: %+v", res.Errors)
	}
	if res.Excavation <= 0 {
		t.Errorf("expected subterranean excavation volume > 0, got %d", res.Excavation)
	}
	if res.BOM["minecraft:stone_bricks"] != 21 {
		t.Errorf("expected 21 stone_bricks in BOM, got %d", res.BOM["minecraft:stone_bricks"])
	}

	// 2. Invalid blueprint (layer row dimension mismatch)
	invalidBP := &blueprints.Blueprint{
		ID:      "broken_structure",
		Name:    "Broken Structure",
		Purpose: "HOME",
		Dimensions: blueprints.Dimensions{
			Width:  3,
			Height: 2,
			Length: 3,
			MinY:   0,
		},
		Palette: map[int]string{
			1: "minecraft:cobblestone",
		},
		Layers: []blueprints.Layer{
			{
				Y: 0,
				Matrix: [][]int{
					{1, 1}, // only 2 columns instead of 3
				},
			},
		},
	}

	dataInvalid, _ := json.Marshal(invalidBP)
	respInv, err := http.Post(srvURL+"/api/blueprints/validate", "application/json", strings.NewReader(string(dataInvalid)))
	if err != nil {
		t.Fatalf("POST validate invalid failed: %v", err)
	}
	defer respInv.Body.Close()

	var resInv ValidationResult
	_ = json.NewDecoder(respInv.Body).Decode(&resInv)
	if resInv.Valid {
		t.Errorf("expected invalid blueprint to fail validation")
	}
	if len(resInv.Errors) == 0 {
		t.Errorf("expected validation errors, got none")
	}
}

func TestServer_PaletteAndTaxonomy(t *testing.T) {
	_, srvURL := setupTestServer(t)

	// 1. Palette endpoint
	palResp, err := http.Get(srvURL + "/api/palette")
	if err != nil {
		t.Fatalf("GET /api/palette failed: %v", err)
	}
	defer palResp.Body.Close()

	if palResp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", palResp.StatusCode)
	}

	var pal PaletteResponse
	if err := json.NewDecoder(palResp.Body).Decode(&pal); err != nil {
		t.Fatalf("failed decoding palette: %v", err)
	}

	if len(pal.Blocks) == 0 {
		t.Fatal("expected palette blocks, got empty list")
	}
	if len(pal.Categories) != 6 {
		t.Errorf("expected 6 orientation categories, got %d", len(pal.Categories))
	}

	// Verify categories present in catalog
	categoryFound := make(map[blueprints.OrientationCategory]bool)
	for _, b := range pal.Blocks {
		categoryFound[b.OrientationCategory] = true
	}
	for _, expectedCat := range pal.Categories {
		if !categoryFound[expectedCat] {
			t.Errorf("expected category %q in palette catalog", expectedCat)
		}
	}

	// 2. Taxonomy endpoint
	taxResp, err := http.Get(srvURL + "/api/taxonomy")
	if err != nil {
		t.Fatalf("GET /api/taxonomy failed: %v", err)
	}
	defer taxResp.Body.Close()

	if taxResp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", taxResp.StatusCode)
	}

	var tax TaxonomyResponse
	if err := json.NewDecoder(taxResp.Body).Decode(&tax); err != nil {
		t.Fatalf("failed decoding taxonomy: %v", err)
	}

	if len(tax.Tags) == 0 {
		t.Fatal("expected taxonomy tags, got none")
	}
	if len(tax.Taxonomy) == 0 {
		t.Fatal("expected taxonomy map, got empty")
	}

	// Verify key tags exist
	requiredTags := []string{
		blueprints.TagPlanks,
		blueprints.TagLogs,
		blueprints.TagStairs,
		blueprints.TagStones,
	}
	for _, tag := range requiredTags {
		if len(tax.Taxonomy[tag]) == 0 {
			t.Errorf("expected non-empty mappings for taxonomy tag %s", tag)
		}
	}
}

func TestServer_AgentsAndStatus(t *testing.T) {
	srv, srvURL := setupTestServer(t)

	// 1. Setup mock agent manifest in $XDG_CONFIG_HOME/civquest/manifests/
	r := srv.cfg.Resolver
	manifestDir := filepath.Join(r.ConfigDir(), "manifests")
	manifestJSON := `{"name":"solas","clan_id":"iron_covenant","profession":"blacksmith"}`
	_ = os.WriteFile(filepath.Join(manifestDir, "solas.json"), []byte(manifestJSON), 0644)

	// 2. Set up mock agent unix socket listener in $XDG_STATE_HOME/civquest/sockets/
	socketsDir := filepath.Join(r.StateDir(), "sockets")
	sockPath := filepath.Join(socketsDir, "solas.sock")
	_ = os.Remove(sockPath)

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("failed creating mock agent socket: %v", err)
	}
	defer func() {
		_ = ln.Close()
		_ = os.Remove(sockPath)
	}()

	mockAgentStatus := map[string]any{
		"agent":            "solas",
		"clan":             "iron_covenant",
		"status":           "PATROLLING",
		"pid":              12345,
		"world_tick":       84200,
		"health":           20.0,
		"food_level":       18,
		"navigation_state": "NAVIGATING",
		"position":         map[string]float64{"x": 102.5, "y": 64.0, "z": -250.5},
	}
	statusBytes, _ := json.Marshal(mockAgentStatus)
	statusBytes = append(statusBytes, '\n')

	// Handle socket connections in background
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = conn.Write(statusBytes)
			_ = conn.Close()
		}
	}()

	// 3. GET /api/agents
	agentsResp, err := http.Get(srvURL + "/api/agents")
	if err != nil {
		t.Fatalf("GET /api/agents failed: %v", err)
	}
	defer agentsResp.Body.Close()

	if agentsResp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", agentsResp.StatusCode)
	}

	var agents []AgentInfo
	if err := json.NewDecoder(agentsResp.Body).Decode(&agents); err != nil {
		t.Fatalf("failed decoding agents list: %v", err)
	}

	if len(agents) == 0 {
		t.Fatal("expected at least 1 agent, got none")
	}

	var solasAgent *AgentInfo
	for i := range agents {
		if agents[i].ID == "solas" {
			solasAgent = &agents[i]
			break
		}
	}
	if solasAgent == nil {
		t.Fatal("solas agent not discovered")
	}
	if !solasAgent.Active {
		t.Errorf("expected solas agent to be active, got inactive")
	}

	// 4. GET /api/agents/solas/status
	statusResp, err := http.Get(srvURL + "/api/agents/solas/status")
	if err != nil {
		t.Fatalf("GET /api/agents/solas/status failed: %v", err)
	}
	defer statusResp.Body.Close()

	if statusResp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", statusResp.StatusCode)
	}

	var receivedStatus map[string]any
	if err := json.NewDecoder(statusResp.Body).Decode(&receivedStatus); err != nil {
		t.Fatalf("failed decoding agent socket status: %v", err)
	}

	if receivedStatus["agent"] != "solas" {
		t.Errorf("expected agent 'solas', got %v", receivedStatus["agent"])
	}
	if receivedStatus["status"] != "PATROLLING" {
		t.Errorf("expected status 'PATROLLING', got %v", receivedStatus["status"])
	}
}

func TestServer_WebSockets(t *testing.T) {
	srv, srvURL := setupTestServer(t)

	// Replace "http" with "ws"
	u, _ := url.Parse(srvURL)
	wsURL := fmt.Sprintf("ws://%s", u.Host)

	// 1. Studio WebSocket collaboration test
	ws1, err := websocket.Dial(wsURL+"/ws/studio", "", srvURL)
	if err != nil {
		t.Fatalf("websocket.Dial /ws/studio client 1 failed: %v", err)
	}
	defer ws1.Close()

	// Read welcome frame
	var welcomeMsg string
	if err := websocket.Message.Receive(ws1, &welcomeMsg); err != nil {
		t.Fatalf("failed receiving welcome frame: %v", err)
	}
	if !strings.Contains(welcomeMsg, "welcome") {
		t.Errorf("expected welcome message, got %q", welcomeMsg)
	}

	// Connect second studio client
	ws2, err := websocket.Dial(wsURL+"/ws/studio", "", srvURL)
	if err != nil {
		t.Fatalf("websocket.Dial /ws/studio client 2 failed: %v", err)
	}
	defer ws2.Close()

	// Drain welcome on ws2
	var welcome2 string
	_ = websocket.Message.Receive(ws2, &welcome2)

	// Broadcast test message from ws1
	broadcastPayload := `{"type":"block_place","x":10,"y":64,"z":-5,"block":"minecraft:stone"}`
	if err := websocket.Message.Send(ws1, broadcastPayload); err != nil {
		t.Fatalf("failed sending broadcast message: %v", err)
	}

	// ws2 should receive the broadcast
	var receivedMsg string
	_ = ws2.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := websocket.Message.Receive(ws2, &receivedMsg); err != nil {
		t.Fatalf("ws2 failed to receive broadcast from ws1: %v", err)
	}
	if receivedMsg != broadcastPayload {
		t.Errorf("expected received msg %q, got %q", broadcastPayload, receivedMsg)
	}

	// 2. Real-time agent streaming WebSocket test
	r := srv.cfg.Resolver
	socketsDir := filepath.Join(r.StateDir(), "sockets")
	sockPath := filepath.Join(socketsDir, "aria.sock")
	_ = os.Remove(sockPath)

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("failed creating aria mock socket: %v", err)
	}
	defer func() {
		_ = ln.Close()
		_ = os.Remove(sockPath)
	}()

	ariaSnapshot := `{"agent":"aria","status":"WOODCUTTING","health":20.0,"food":20}` + "\n"
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = conn.Write([]byte(ariaSnapshot))
			_ = conn.Close()
		}
	}()

	wsAgent, err := websocket.Dial(wsURL+"/ws/agents/aria", "", srvURL)
	if err != nil {
		t.Fatalf("websocket.Dial /ws/agents/aria failed: %v", err)
	}
	defer wsAgent.Close()

	// Expect periodic status stream
	_ = wsAgent.SetReadDeadline(time.Now().Add(2 * time.Second))
	var agentStreamMsg string
	if err := websocket.Message.Receive(wsAgent, &agentStreamMsg); err != nil {
		t.Fatalf("failed to receive agent telemetry stream: %v", err)
	}

	if !strings.Contains(agentStreamMsg, "aria") || !strings.Contains(agentStreamMsg, "WOODCUTTING") {
		t.Errorf("expected telemetry frame for aria, got %q", agentStreamMsg)
	}
}

func TestServer_StaticAndSPA(t *testing.T) {
	_, srvURL := setupTestServer(t)

	// 1. Root path returns index.html
	resp, err := http.Get(srvURL + "/")
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "CivQuest Blueprint Studio") {
		t.Errorf("expected index.html content at /, got: %s", string(body)[:min(100, len(body))])
	}

	// 2. CSS asset
	cssResp, err := http.Get(srvURL + "/style.css")
	if err != nil {
		t.Fatalf("GET /style.css failed: %v", err)
	}
	defer cssResp.Body.Close()
	if cssResp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200 for style.css, got %d", cssResp.StatusCode)
	}

	// 3. JS assets
	for _, jsAsset := range []string{"/app.js", "/canvas.js", "/grid.js"} {
		jsResp, err := http.Get(srvURL + jsAsset)
		if err != nil {
			t.Fatalf("GET %s failed: %v", jsAsset, err)
		}
		if jsResp.StatusCode != http.StatusOK {
			t.Errorf("expected status 200 for %s, got %d", jsAsset, jsResp.StatusCode)
		}
		body, _ := io.ReadAll(jsResp.Body)
		jsResp.Body.Close()
		if len(body) == 0 {
			t.Errorf("expected non-empty body for %s", jsAsset)
		}
	}

	// 4. Unknown route falls back to index.html for SPA client-side routing
	spaResp, err := http.Get(srvURL + "/studio/blueprints/viewer/abc")
	if err != nil {
		t.Fatalf("GET /studio/blueprints/viewer/abc failed: %v", err)
	}
	defer spaResp.Body.Close()
	spaBody, _ := io.ReadAll(spaResp.Body)
	if !strings.Contains(string(spaBody), "CivQuest Blueprint Studio") {
		t.Errorf("expected SPA fallback to index.html, got: %s", string(spaBody)[:min(100, len(spaBody))])
	}
}

func TestServer_AssistantDraft(t *testing.T) {
	srv, srvURL := setupTestServer(t)

	// 1. Invalid method (GET)
	getResp, err := http.Get(srvURL + "/api/assistant/draft")
	if err != nil {
		t.Fatalf("GET /api/assistant/draft failed: %v", err)
	}
	getResp.Body.Close()
	if getResp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed, got %d", getResp.StatusCode)
	}

	// 2. Empty prompt
	emptyReq := map[string]string{"prompt": ""}
	emptyBody, _ := json.Marshal(emptyReq)
	badResp, err := http.Post(srvURL+"/api/assistant/draft", "application/json", strings.NewReader(string(emptyBody)))
	if err != nil {
		t.Fatalf("POST empty prompt failed: %v", err)
	}
	badResp.Body.Close()
	if badResp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for empty prompt, got %d", badResp.StatusCode)
	}

	// 3. Fallback procedural synthesis of frontier tavern with cellar & porch
	req := AssistantDraftRequest{
		Prompt: "Build a cozy 7x5 frontier tavern with oak planks, spruce log pillars, a cellar, and a front porch",
		Style:  "frontier",
	}
	reqBytes, _ := json.Marshal(req)
	resp, err := http.Post(srvURL+"/api/assistant/draft", "application/json", strings.NewReader(string(reqBytes)))
	if err != nil {
		t.Fatalf("POST /api/assistant/draft failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 OK, got %d: %s", resp.StatusCode, string(body))
	}

	var drafted blueprints.Blueprint
	if err := json.NewDecoder(resp.Body).Decode(&drafted); err != nil {
		t.Fatalf("failed to decode drafted blueprint: %v", err)
	}

	if err := drafted.Validate(); err != nil {
		t.Fatalf("drafted blueprint failed validation: %v", err)
	}

	if drafted.Purpose != blueprints.PurposeTavern {
		t.Errorf("expected purpose TAVERN, got %q", drafted.Purpose)
	}
	if drafted.Dimensions.Width != 7 || drafted.Dimensions.Length != 5 {
		t.Errorf("expected dimensions 7x5, got %dx%d", drafted.Dimensions.Width, drafted.Dimensions.Length)
	}
	if drafted.Dimensions.MinY != -1 {
		t.Errorf("expected MinY -1 for tavern with cellar, got %d", drafted.Dimensions.MinY)
	}
	if drafted.Excavation <= 0 {
		t.Errorf("expected excavation > 0 for cellar level, got %d", drafted.Excavation)
	}

	// Check that cellar layer exists
	foundCellar := false
	for _, l := range drafted.Layers {
		if l.Y == -1 {
			foundCellar = true
			break
		}
	}
	if !foundCellar {
		t.Error("expected layer with Y=-1 in drafted blueprint")
	}

	// 4. Test with System 2 LLM Client attached to mock gateway
	mockGateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mockResp := system2.ChatCompletionResponse{
			Choices: []system2.ChatCompletionChoice{
				{
					Message: system2.ChatMessage{
						Role: "assistant",
						Content: `{
							"name": "LLM Nordic Mead Hall",
							"purpose": "TAVERN",
							"style": "nordic",
							"dimensions": { "width": 5, "height": 3, "length": 5, "min_y": 0 },
							"palette": {
								"p": "minecraft:oak_planks",
								"l": "minecraft:spruce_log[axis=y]"
							},
							"layers": [
								{ "y": 0, "grid": ["lpppl", "ppppp", "ppppp", "ppppp", "lpppl"] }
							]
						}`,
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(mockResp)
	}))
	defer mockGateway.Close()

	llmClient, err := system2.NewClientWithConfig(
		xdg.LLMGatewayConfig{BaseURL: mockGateway.URL},
		xdg.InferenceGovernorConfig{MaxConcurrentLLMRequests: 2},
	)
	if err != nil {
		t.Fatalf("creating test LLM client: %v", err)
	}

	srv.cfg.LLMClient = llmClient
	srv.cfg.ArchitectService = system2.NewArchitectService(llmClient)

	llmReq := AssistantDraftRequest{
		Prompt: "Design an authentic Nordic mead hall",
		Style:  "nordic",
	}
	llmReqBytes, _ := json.Marshal(llmReq)
	llmResp, err := http.Post(srvURL+"/api/assistant/draft", "application/json", strings.NewReader(string(llmReqBytes)))
	if err != nil {
		t.Fatalf("POST /api/assistant/draft with LLM failed: %v", err)
	}
	defer llmResp.Body.Close()

	if llmResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(llmResp.Body)
		t.Fatalf("expected status 200 from LLM draft, got %d: %s", llmResp.StatusCode, string(body))
	}

	var llmBP blueprints.Blueprint
	if err := json.NewDecoder(llmResp.Body).Decode(&llmBP); err != nil {
		t.Fatalf("failed decoding LLM blueprint: %v", err)
	}

	if llmBP.Name != "LLM Nordic Mead Hall" {
		t.Errorf("expected name 'LLM Nordic Mead Hall', got %q", llmBP.Name)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
