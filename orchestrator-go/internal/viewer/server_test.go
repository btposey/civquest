package viewer

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"

	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func TestViewerServer_LifecycleAndHealth(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithExplicitDirs(
		filepath.Join(tmpDir, "config"),
		filepath.Join(tmpDir, "data"),
		filepath.Join(tmpDir, "state"),
	))

	srv := NewServer(Config{
		Host:     "127.0.0.1",
		Port:     0, // dynamic port for testing
		Resolver: r,
	})

	if err := srv.Start(); err != nil {
		t.Fatalf("failed starting viewer server: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Stop(ctx)
	}()

	addr := srv.Addr()
	if addr == "" {
		t.Fatalf("expected non-empty bound address")
	}
	url := srv.URL()
	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Fatalf("expected url to start with http://127.0.0.1:, got %s", url)
	}

	// Test GET /api/health
	resp, err := http.Get(url + "/api/health")
	if err != nil {
		t.Fatalf("failed requesting health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var health map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("failed decoding health JSON: %v", err)
	}
	if health["status"] != "ok" {
		t.Fatalf("expected status 'ok', got %q", health["status"])
	}
	if health["time"] == "" {
		t.Fatalf("expected non-empty timestamp")
	}
}

func TestViewerServer_StaticFilesAndSPAFallback(t *testing.T) {
	tmpDir := t.TempDir()
	r := xdg.NewResolver(xdg.WithExplicitDirs(
		filepath.Join(tmpDir, "config"),
		filepath.Join(tmpDir, "data"),
		filepath.Join(tmpDir, "state"),
	))

	srv := NewServer(Config{
		Host:     "127.0.0.1",
		Port:     0,
		Resolver: r,
	})

	if err := srv.Start(); err != nil {
		t.Fatalf("failed starting server: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Stop(ctx)
	}()

	baseURL := srv.URL()

	// 1. Root / -> index.html
	resp, err := http.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("failed requesting root: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for root, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "CIVQUEST // AGENT VIEWER") {
		t.Fatalf("expected root to serve index.html, got: %s", string(body)[:200])
	}

	// 2. style.css
	respCss, err := http.Get(baseURL + "/style.css")
	if err != nil {
		t.Fatalf("failed requesting style.css: %v", err)
	}
	cssBody, _ := io.ReadAll(respCss.Body)
	respCss.Body.Close()
	if respCss.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for style.css, got %d", respCss.StatusCode)
	}
	if !strings.Contains(string(cssBody), "--bg-dark") {
		t.Fatalf("expected style.css content, got: %s", string(cssBody)[:100])
	}

	// 3. app.js
	respJs, err := http.Get(baseURL + "/app.js")
	if err != nil {
		t.Fatalf("failed requesting app.js: %v", err)
	}
	jsBody, _ := io.ReadAll(respJs.Body)
	respJs.Body.Close()
	if respJs.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for app.js, got %d", respJs.StatusCode)
	}
	if !strings.Contains(string(jsBody), "Tactical Agent Viewer SPA") {
		t.Fatalf("expected app.js content, got: %s", string(jsBody)[:100])
	}

	// 4. SPA Fallback for arbitrary route (e.g. /agent/artisan)
	respSpa, err := http.Get(baseURL + "/agent/artisan")
	if err != nil {
		t.Fatalf("failed requesting SPA route: %v", err)
	}
	spaBody, _ := io.ReadAll(respSpa.Body)
	respSpa.Body.Close()
	if respSpa.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for SPA route, got %d", respSpa.StatusCode)
	}
	if !strings.Contains(string(spaBody), "CIVQUEST // AGENT VIEWER") {
		t.Fatalf("expected SPA route to fallback to index.html")
	}
}

func TestViewerServer_AgentsListAndOfflineStatus(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	dataDir := filepath.Join(tmpDir, "data")
	stateDir := filepath.Join(tmpDir, "state")
	r := xdg.NewResolver(xdg.WithExplicitDirs(configDir, dataDir, stateDir))

	// Create a mock agent configuration
	agentsDir := filepath.Join(configDir, "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("creating agents dir: %v", err)
	}
	cfgData := `name: "artisan"
clan_id: "iron_clan"
profession: "Blacksmith"
secondary_hobby: "Fishing"
`
	if err := os.WriteFile(filepath.Join(agentsDir, "artisan.yaml"), []byte(cfgData), 0644); err != nil {
		t.Fatalf("writing agent config: %v", err)
	}

	srv := NewServer(Config{
		Host:     "127.0.0.1",
		Port:     0,
		Resolver: r,
	})

	if err := srv.Start(); err != nil {
		t.Fatalf("failed starting server: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Stop(ctx)
	}()

	baseURL := srv.URL()

	// 1. GET /api/agents
	resp, err := http.Get(baseURL + "/api/agents")
	if err != nil {
		t.Fatalf("failed requesting agents: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var agents []AgentInfo
	if err := json.NewDecoder(resp.Body).Decode(&agents); err != nil {
		t.Fatalf("decoding agents JSON: %v", err)
	}

	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
	if agents[0].ID != "artisan" || agents[0].Profession != "Blacksmith" {
		t.Fatalf("unexpected agent info: %+v", agents[0])
	}
	if agents[0].Active {
		t.Fatalf("expected agent to be inactive/offline without socket")
	}

	// 2. GET /api/agents/artisan/status (offline)
	respStatus, err := http.Get(baseURL + "/api/agents/artisan/status")
	if err != nil {
		t.Fatalf("failed requesting agent status: %v", err)
	}
	defer respStatus.Body.Close()

	if respStatus.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", respStatus.StatusCode)
	}

	var statusMap map[string]any
	if err := json.NewDecoder(respStatus.Body).Decode(&statusMap); err != nil {
		t.Fatalf("decoding status JSON: %v", err)
	}

	if statusMap["agent"] != "artisan" {
		t.Fatalf("expected agent 'artisan', got %v", statusMap["agent"])
	}
	if statusMap["active"] != false {
		t.Fatalf("expected active to be false")
	}
	if statusMap["profession"] != "Blacksmith" {
		t.Fatalf("expected profession 'Blacksmith', got %v", statusMap["profession"])
	}
}

func TestViewerServer_ActiveSocketAndWebSocketStreaming(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	dataDir := filepath.Join(tmpDir, "data")
	stateDir := filepath.Join(tmpDir, "state")
	r := xdg.NewResolver(xdg.WithExplicitDirs(configDir, dataDir, stateDir))

	// Create sockets directory
	socketsDir := filepath.Join(stateDir, "sockets")
	if err := os.MkdirAll(socketsDir, 0755); err != nil {
		t.Fatalf("creating sockets dir: %v", err)
	}

	// Start a mock agent UNIX domain socket listener
	mockSockPath := filepath.Join(socketsDir, "active_agent.sock")
	ln, err := net.Listen("unix", mockSockPath)
	if err != nil {
		t.Fatalf("failed listening on unix socket %s: %v", mockSockPath, err)
	}
	defer func() {
		_ = ln.Close()
		_ = os.Remove(mockSockPath)
	}()

	mockTelemetry := map[string]any{
		"agent":            "active_agent",
		"clan":             "wolf_clan",
		"status":           "RUNNING",
		"pid":              12345,
		"world_tick":       24800,
		"time_of_day":      6500,
		"health":           18.5,
		"max_health":       20.0,
		"food_level":       19,
		"saturation":       4.5,
		"stamina":          92.0,
		"armor_rating":     15,
		"stage":            "STAGE_SETTLEMENT",
		"routine":          "ROUTINE_PRIMARY_DUTY",
		"shift":            "FIRST",
		"weather":          "WEATHER_CLEAR",
		"biome_id":         "minecraft:plains",
		"navigation_state": "NAVIGATION_STATE_PATHING",
		"position": map[string]float64{
			"x": 102.4,
			"y": 64.0,
			"z": -250.8,
		},
		"activity_stack": []map[string]any{
			{
				"id":         7,
				"kind":       "emergency",
				"band":       3,
				"start_tick": 24750,
			},
		},
		"inventory": []map[string]any{
			{"slot": 0, "item_id": "minecraft:iron_sword", "count": 1, "durability": 200, "max_durability": 250},
			{"slot": 38, "item_id": "minecraft:iron_chestplate", "count": 1, "durability": 220, "max_durability": 240},
		},
		"blackboard": map[string]any{
			"threat.creeper_nearby": map[string]any{
				"key":        "threat.creeper_nearby",
				"category":   "Threat",
				"value":      map[string]any{"bool": true},
				"confidence": 1.0,
				"evidence":   "Creeper at distance 3.8m",
			},
		},
		"reflections": map[string]any{
			"latest_thought":   "Must maintain defensive perimeter around northern gate.",
			"latest_dialogue":  "Good morrow, blacksmith.",
			"latest_chronicle": "Repelled creeper incursion at twilight.",
		},
	}
	mockJSON, _ := json.Marshal(mockTelemetry)
	mockJSON = append(mockJSON, '\n')

	// Goroutine to serve incoming socket requests
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = conn.Write(mockJSON)
			_ = conn.Close()
		}
	}()

	srv := NewServer(Config{
		Host:     "127.0.0.1",
		Port:     0,
		Resolver: r,
	})

	if err := srv.Start(); err != nil {
		t.Fatalf("failed starting server: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Stop(ctx)
	}()

	baseURL := srv.URL()

	// 1. Verify GET /api/agents shows active_agent as active: true
	respAgents, err := http.Get(baseURL + "/api/agents")
	if err != nil {
		t.Fatalf("failed getting agents: %v", err)
	}
	var agents []AgentInfo
	_ = json.NewDecoder(respAgents.Body).Decode(&agents)
	respAgents.Body.Close()

	if len(agents) != 1 || !agents[0].Active || agents[0].ID != "active_agent" {
		t.Fatalf("expected active_agent to be active, got %+v", agents)
	}

	// 2. Verify GET /api/agents/active_agent/status returns real-time socket JSON
	respStatus, err := http.Get(baseURL + "/api/agents/active_agent/status")
	if err != nil {
		t.Fatalf("failed getting agent status: %v", err)
	}
	defer respStatus.Body.Close()

	if respStatus.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", respStatus.StatusCode)
	}

	var statusResp map[string]any
	if err := json.NewDecoder(respStatus.Body).Decode(&statusResp); err != nil {
		t.Fatalf("failed decoding socket response: %v", err)
	}

	if statusResp["agent"] != "active_agent" || statusResp["status"] != "RUNNING" {
		t.Fatalf("unexpected socket response: %+v", statusResp)
	}
	if statusResp["armor_rating"] != float64(15) {
		t.Fatalf("expected armor_rating 15, got %v", statusResp["armor_rating"])
	}

	// 3. Verify WebSocket streaming: /ws/agents/active_agent
	wsURL := strings.Replace(baseURL, "http://", "ws://", 1) + "/ws/agents/active_agent"
	origin := baseURL
	wsConn, err := websocket.Dial(wsURL, "", origin)
	if err != nil {
		t.Fatalf("failed connecting websocket: %v", err)
	}
	defer wsConn.Close()

	// Read first frame
	_ = wsConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var frameStr string
	if err := websocket.Message.Receive(wsConn, &frameStr); err != nil {
		t.Fatalf("failed receiving frame from websocket: %v", err)
	}

	var wsFrame map[string]any
	if err := json.Unmarshal([]byte(frameStr), &wsFrame); err != nil {
		t.Fatalf("failed unmarshaling ws frame: %v", err)
	}

	if wsFrame["agent"] != "active_agent" {
		t.Fatalf("expected agent 'active_agent', got %v", wsFrame["agent"])
	}
	if wsFrame["stage"] != "STAGE_SETTLEMENT" {
		t.Fatalf("expected stage STAGE_SETTLEMENT, got %v", wsFrame["stage"])
	}
	if wsFrame["routine"] != "ROUTINE_PRIMARY_DUTY" {
		t.Fatalf("expected routine ROUTINE_PRIMARY_DUTY, got %v", wsFrame["routine"])
	}

	// Verify state stack frame was received
	stack, ok := wsFrame["activity_stack"].([]any)
	if !ok || len(stack) != 1 {
		t.Fatalf("expected 1 activity stack frame, got: %+v", wsFrame["activity_stack"])
	}
	firstFrame := stack[0].(map[string]any)
	if firstFrame["kind"] != "emergency" {
		t.Fatalf("expected emergency frame, got %v", firstFrame["kind"])
	}

	// Verify blackboard was received
	bb, ok := wsFrame["blackboard"].(map[string]any)
	if !ok || bb["threat.creeper_nearby"] == nil {
		t.Fatalf("expected threat.creeper_nearby in blackboard, got %+v", wsFrame["blackboard"])
	}
}
