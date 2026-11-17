package studio

import (
	"bufio"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	"github.com/btposey/civquest/orchestrator-go/internal/system2"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

//go:embed static/*
var staticFS embed.FS

// Config specifies the runtime configuration for the CivQuest Studio HTTP server.
type Config struct {
	Host             string
	Port             int
	Resolver         *xdg.Resolver
	ArchitectService *system2.ArchitectService
	LLMClient        *system2.Client
}

// StudioHub manages bidirectional WebSocket client connections for collaborative live sync.
type StudioHub struct {
	mu      sync.Mutex
	clients map[*websocket.Conn]bool
}

// newStudioHub constructs a thread-safe WebSocket broadcast hub.
func newStudioHub() *StudioHub {
	return &StudioHub{
		clients: make(map[*websocket.Conn]bool),
	}
}

func (h *StudioHub) register(conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[conn] = true
}

func (h *StudioHub) unregister(conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, conn)
}

func (h *StudioHub) broadcast(sender *websocket.Conn, message string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for client := range h.clients {
		if client != sender {
			_ = websocket.Message.Send(client, message)
		}
	}
}

func (h *StudioHub) clientCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// PaletteBlockEntry models a catalog block with orientation and canonical defaults.
type PaletteBlockEntry struct {
	ID                  string                         `json:"id"`
	OrientationCategory blueprints.OrientationCategory `json:"orientation_category"`
	Category            blueprints.OrientationCategory `json:"category"`
	DefaultProperties   map[string]string              `json:"default_properties"`
	Properties          map[string]string              `json:"properties"`
	DirectionGlyph      string                         `json:"direction_glyph"`
	Glyph               string                         `json:"glyph"`
}

// PaletteResponse holds the standard catalog palette list.
type PaletteResponse struct {
	Blocks     []PaletteBlockEntry              `json:"blocks"`
	Categories []blueprints.OrientationCategory `json:"categories"`
}

// TaxonomyResponse describes known taxonomy tags and material mappings.
type TaxonomyResponse struct {
	Tags     []string            `json:"tags"`
	Taxonomy map[string][]string `json:"taxonomy"`
	Map      map[string][]string `json:"map"`
}

// AgentInfo encapsulates detected agent worker state.
type AgentInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ClanID     string `json:"clan_id,omitempty"`
	Profession string `json:"profession,omitempty"`
	Active     bool   `json:"active"`
	SocketPath string `json:"socket_path,omitempty"`
}

// ValidationResult represents the output of POST /api/blueprints/validate.
type ValidationResult struct {
	Valid       bool                  `json:"valid"`
	Errors      []string              `json:"errors"`
	Error       string                `json:"error,omitempty"`
	Blueprint   *blueprints.Blueprint `json:"blueprint,omitempty"`
	BOM         map[string]int        `json:"bom,omitempty"`
	Excavation  int                   `json:"excavation"`
	Dimensions  blueprints.Dimensions `json:"dimensions"`
	TotalBlocks int                   `json:"total_blocks"`
}

// Server encapsulates the embedded HTTP and WebSocket server for CivQuest Blueprint Studio.
type Server struct {
	cfg      Config
	httpSrv  *http.Server
	listener net.Listener
	hub      *StudioHub
	addr     string
	mu       sync.Mutex
	running  bool
}

// NewServer initializes a new Studio Server with the provided configuration.
func NewServer(cfg Config) *Server {
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if cfg.Port < 0 {
		cfg.Port = 8765
	}
	if cfg.Resolver == nil {
		cfg.Resolver = xdg.DefaultResolver()
	}

	s := &Server{
		cfg: cfg,
		hub: newStudioHub(),
	}

	mux := http.NewServeMux()

	// REST API Endpoints
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/assistant/draft", s.handleAssistantDraft)
	mux.HandleFunc("/api/blueprints/validate", s.handleBlueprintValidate)
	mux.HandleFunc("/api/blueprints", s.handleBlueprints)
	mux.HandleFunc("/api/blueprints/", s.handleBlueprintByID)
	mux.HandleFunc("/api/palette", s.handlePalette)
	mux.HandleFunc("/api/taxonomy", s.handleTaxonomy)
	mux.HandleFunc("/api/agents", s.handleAgents)
	mux.HandleFunc("/api/agents/", s.handleAgentStatus)

	// WebSocket Endpoints
	mux.Handle("/ws/agents/", websocket.Handler(s.handleAgentWS))
	mux.Handle("/ws/studio", websocket.Handler(s.handleStudioWS))

	// Static Web UI & SPA Fallback
	mux.HandleFunc("/", s.handleStatic)

	s.httpSrv = &http.Server{
		Handler:      mux,
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 0, // No timeout to prevent dropping connection on long LLM inference
	}

	return s
}

// Start binds the TCP listener and starts serving HTTP requests.
func (s *Server) Start() error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return errors.New("studio server is already running")
	}

	addr := fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("binding studio listener on %s: %w", addr, err)
	}

	s.listener = ln
	s.addr = ln.Addr().String()
	s.running = true
	s.mu.Unlock()

	go func() {
		if err := s.httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(os.Stderr, "[studio] http serve error: %v\n", err)
		}
	}()

	return nil
}

// Stop gracefully shuts down the HTTP server.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = false
	s.mu.Unlock()

	return s.httpSrv.Shutdown(ctx)
}

// Addr returns the bound TCP host:port string.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addr
}

// URL returns the full HTTP URL to access the studio.
func (s *Server) URL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fmt.Sprintf("http://%s", s.addr)
}

// -----------------------------------------------------------------------------
// REST Handlers
// -----------------------------------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleBlueprints(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		bps, err := blueprints.ListLibraryBlueprints()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if bps == nil {
			bps = make([]*blueprints.Blueprint, 0)
		}
		writeJSON(w, http.StatusOK, bps)

	case http.MethodPost:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "failed reading request body")
			return
		}
		defer r.Body.Close()

		var bp blueprints.Blueprint
		if err := json.Unmarshal(body, &bp); err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid json: %v", err))
			return
		}

		if err := bp.Validate(); err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("validation failed: %v", err))
			return
		}

		if err := blueprints.SaveLibraryBlueprint(&bp); err != nil {
			writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("persisting blueprint: %v", err))
			return
		}

		writeJSON(w, http.StatusCreated, &bp)

	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleBlueprintByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/blueprints/")
	id = strings.Trim(id, "/")

	if id == "" {
		s.handleBlueprints(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet:
		bp, err := blueprints.GetLibraryBlueprint(id)
		if err != nil {
			if errors.Is(err, blueprints.ErrBlueprintNotFound) {
				writeJSONError(w, http.StatusNotFound, "blueprint not found")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, bp)

	case http.MethodPut:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "failed reading request body")
			return
		}
		defer r.Body.Close()

		var bp blueprints.Blueprint
		if err := json.Unmarshal(body, &bp); err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid json: %v", err))
			return
		}

		if bp.ID == "" {
			bp.ID = id
		}

		if err := bp.Validate(); err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("validation failed: %v", err))
			return
		}

		if err := blueprints.SaveLibraryBlueprint(&bp); err != nil {
			writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("saving blueprint: %v", err))
			return
		}

		writeJSON(w, http.StatusOK, &bp)

	case http.MethodDelete:
		if err := blueprints.DeleteLibraryBlueprint(id); err != nil {
			if errors.Is(err, blueprints.ErrBlueprintNotFound) {
				writeJSONError(w, http.StatusNotFound, "blueprint not found")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "deleted",
			"id":     id,
		})

	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleBlueprintValidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "failed reading request body")
		return
	}
	defer r.Body.Close()

	var bp blueprints.Blueprint
	if err := json.Unmarshal(body, &bp); err != nil {
		writeJSON(w, http.StatusOK, ValidationResult{
			Valid:  false,
			Errors: []string{fmt.Sprintf("invalid JSON specification: %v", err)},
			Error:  err.Error(),
		})
		return
	}

	bom := bp.CalculateBOM()
	excavation := bp.ComputeExcavation()
	totalBlocks := 0
	for _, count := range bom {
		totalBlocks += count
	}

	if err := bp.Validate(); err != nil {
		writeJSON(w, http.StatusOK, ValidationResult{
			Valid:       false,
			Errors:      []string{err.Error()},
			Error:       err.Error(),
			Blueprint:   &bp,
			BOM:         bom,
			Excavation:  excavation,
			Dimensions:  bp.Dimensions,
			TotalBlocks: totalBlocks,
		})
		return
	}

	writeJSON(w, http.StatusOK, ValidationResult{
		Valid:       true,
		Errors:      make([]string, 0),
		Blueprint:   &bp,
		BOM:         bom,
		Excavation:  excavation,
		Dimensions:  bp.Dimensions,
		TotalBlocks: totalBlocks,
	})
}

// AssistantDraftRequest encapsulates a user prompt and optional base blueprint context
// for the AI Architectural Assistant.
type AssistantDraftRequest struct {
	Prompt          string `json:"prompt"`
	BaseBlueprintID string `json:"base_blueprint_id,omitempty"`
	Style           string `json:"style,omitempty"`
}

func (s *Server) handleAssistantDraft(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "failed reading request body")
		return
	}
	defer r.Body.Close()

	var req AssistantDraftRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid json payload: %v", err))
		return
	}

	if strings.TrimSpace(req.Prompt) == "" {
		writeJSONError(w, http.StatusBadRequest, "prompt cannot be empty")
		return
	}

	var baseBP *blueprints.Blueprint
	if req.BaseBlueprintID != "" {
		baseBP, _ = blueprints.GetLibraryBlueprint(req.BaseBlueprintID)
	}

	// 1. If an ArchitectService or LLMClient is available, attempt System 2 LLM inference
	archSrv := s.cfg.ArchitectService
	if archSrv == nil && s.cfg.LLMClient != nil {
		archSrv = system2.NewArchitectService(s.cfg.LLMClient)
	}
	if archSrv == nil && s.cfg.Resolver != nil {
		if globalCfg, err := s.cfg.Resolver.LoadGlobalConfig(); err == nil && globalCfg != nil && globalCfg.LLMGateway.BaseURL != "" {
			if client, err := system2.NewClient(globalCfg); err == nil {
				archSrv = system2.NewArchitectService(client)
			}
		}
	}

	if archSrv != nil {
		baseForReq := baseBP
		if baseForReq == nil {
			baseForReq = &blueprints.Blueprint{
				ID:      "base_draft",
				Name:    "Draft Base",
				Purpose: blueprints.PurposeHome,
				Dimensions: blueprints.Dimensions{
					Width:  5,
					Height: 3,
					Length: 5,
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
							{2, 1, 1, 1, 2},
							{1, 1, 1, 1, 1},
							{1, 1, 1, 1, 1},
							{1, 1, 1, 1, 1},
							{2, 1, 1, 1, 2},
						},
					},
				},
			}
		}

		ctx, cancel := context.WithTimeout(r.Context(), 75*time.Second)
		defer cancel()

		bp, err := archSrv.GenerateDerivative(ctx, &system2.ArchitecturalDerivativeRequest{
			BaseBlueprint:           baseForReq,
			ModificationDescription: req.Prompt,
			TargetStyle:             req.Style,
		})
		if err == nil && bp != nil && bp.Validate() == nil {
			writeJSON(w, http.StatusOK, bp)
			return
		}
		if err != nil {
			log.Printf("[studio] LLM synthesis failed or timed out: %v; falling back to procedural architect\n", err)
		} else if bp != nil {
			if vErr := bp.Validate(); vErr != nil {
				log.Printf("[studio] LLM blueprint validation failed: %v; falling back to procedural architect\n", vErr)
			}
		}
	}

	// 2. Procedural Fallback Architect: Deterministic synthesis fulfilling prompt requirements
	bp, err := generateFallbackBlueprint(req, baseBP)
	if err != nil {
		log.Printf("[studio] Fallback blueprint generation failed: %v\n", err)
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("blueprint generation failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, bp)
}

var dimRegex = regexp.MustCompile(`(?i)(\d+)\s*[xX*]\s*(\d+)`)

func generateFallbackBlueprint(req AssistantDraftRequest, baseBP *blueprints.Blueprint) (*blueprints.Blueprint, error) {
	promptLower := strings.ToLower(req.Prompt)

	// Determine Purpose & Name
	purpose := blueprints.PurposeHome
	name := "Frontier Residence"

	switch {
	case strings.Contains(promptLower, "tavern"):
		purpose = blueprints.PurposeTavern
		name = "Cozy Frontier Tavern"
	case strings.Contains(promptLower, "armory"):
		purpose = blueprints.PurposeArmory
		name = "Settlement Armory"
	case strings.Contains(promptLower, "tower") || strings.Contains(promptLower, "watchtower") || strings.Contains(promptLower, "barracks"):
		purpose = blueprints.PurposeBarracks
		name = "Defense Watchtower"
	case strings.Contains(promptLower, "granary") || strings.Contains(promptLower, "storehouse"):
		purpose = blueprints.PurposeStorehouse
		name = "Settlement Storehouse"
	case strings.Contains(promptLower, "library") || strings.Contains(promptLower, "archive"):
		purpose = blueprints.PurposeLibrary
		name = "Settlement Archive"
	case strings.Contains(promptLower, "townhall") || strings.Contains(promptLower, "hall"):
		purpose = blueprints.PurposeTownhall
		name = "Settlement Townhall"
	case strings.Contains(promptLower, "farm"):
		purpose = blueprints.PurposeFarm
		name = "Settlement Farmhouse"
	case strings.Contains(promptLower, "cabin"):
		purpose = blueprints.PurposeHome
		name = "Log Cabin"
	}

	// Style
	style := strings.TrimSpace(req.Style)
	if style == "" {
		switch {
		case strings.Contains(promptLower, "nordic"):
			style = "nordic"
		case strings.Contains(promptLower, "stone"):
			style = "stone"
		case strings.Contains(promptLower, "rustic"):
			style = "rustic"
		default:
			style = "frontier"
		}
	}

	// Feature flags from prompt analysis
	hasDivision := strings.Contains(promptLower, "two room") ||
		strings.Contains(promptLower, "two-room") ||
		strings.Contains(promptLower, "divided") ||
		strings.Contains(promptLower, "interior wall") ||
		strings.Contains(promptLower, "partition") ||
		strings.Contains(promptLower, "divider") ||
		strings.Contains(promptLower, "bedroom") ||
		strings.Contains(promptLower, "wall between")

	hasSlopedRoof := strings.Contains(promptLower, "sloped roof") ||
		strings.Contains(promptLower, "sloped") ||
		strings.Contains(promptLower, "pitched") ||
		strings.Contains(promptLower, "gable") ||
		strings.Contains(promptLower, "roof")

	hasCellar := strings.Contains(promptLower, "cellar") ||
		strings.Contains(promptLower, "basement") ||
		strings.Contains(promptLower, "subterranean")

	hasPorch := strings.Contains(promptLower, "porch")

	// Dimensions
	width := 7
	length := 5
	if hasDivision {
		width = 9
		length = 7
	}
	if baseBP != nil {
		width = baseBP.Dimensions.Width
		length = baseBP.Dimensions.Length
	}

	if match := dimRegex.FindStringSubmatch(promptLower); len(match) >= 3 {
		var w, l int
		if _, err := fmt.Sscanf(match[1], "%d", &w); err == nil && w >= 3 && w <= 32 {
			width = w
		}
		if _, err := fmt.Sscanf(match[2], "%d", &l); err == nil && l >= 3 && l <= 32 {
			length = l
		}
	}
	if hasDivision && width < 7 {
		width = 7
	}
	if hasDivision && length < 5 {
		length = 5
	}

	minY := 0
	if hasCellar {
		minY = -2
	} else if baseBP != nil && baseBP.Dimensions.MinY < 0 {
		minY = baseBP.Dimensions.MinY
	}

	wallHeight := 4 // Y=0 floor, Y=1, 2, 3 interior walls (comfortable 3 vertical blocks clearance)
	if strings.Contains(promptLower, "tower") || strings.Contains(promptLower, "2-story") || strings.Contains(promptLower, "two-story") {
		wallHeight += 2
	}

	roofLayersCount := 1
	if hasSlopedRoof && length >= 5 {
		roofLayersCount = 2
	}

	subterraneanCount := 0
	if minY < 0 {
		subterraneanCount = -minY
	}

	totalHeight := subterraneanCount + wallHeight + roofLayersCount

	// Build Palette
	logBlock := "minecraft:oak_log[axis=y]"
	if strings.Contains(promptLower, "spruce log") || strings.Contains(promptLower, "spruce") {
		logBlock = "minecraft:spruce_log[axis=y]"
	}

	plankBlock := "minecraft:oak_planks"
	if strings.Contains(promptLower, "spruce plank") {
		plankBlock = "minecraft:spruce_planks"
	}

	stairsNorth := "minecraft:oak_stairs[facing=north]"
	stairsSouth := "minecraft:oak_stairs[facing=south]"
	doorLower := "minecraft:oak_door[facing=south,half=lower]"
	doorUpper := "minecraft:oak_door[facing=south,half=upper]"
	if strings.Contains(promptLower, "spruce") {
		stairsNorth = "minecraft:spruce_stairs[facing=north]"
		stairsSouth = "minecraft:spruce_stairs[facing=south]"
		doorLower = "minecraft:spruce_door[facing=south,half=lower]"
		doorUpper = "minecraft:spruce_door[facing=south,half=upper]"
	}

	palette := map[int]string{
		1:  plankBlock,
		2:  logBlock,
		3:  "minecraft:cobblestone",
		4:  stairsNorth,
		5:  "minecraft:glass_pane",
		6:  "minecraft:barrel",
		7:  "minecraft:torch",
		8:  "minecraft:crafting_table",
		9:  "minecraft:chest",
		10: "minecraft:stone_bricks",
		11: "minecraft:red_bed[facing=north,part=foot]",
		12: "minecraft:furnace",
		13: doorLower,
		14: stairsSouth,
		15: "minecraft:ladder[facing=south]",
		16: doorUpper,
		17: "minecraft:red_bed[facing=north,part=head]",
	}

	dividerX := width / 2
	doorZ := length / 2

	frontWallZ := length - 1
	if hasPorch {
		frontWallZ = length - 2
	}

	ladderX := 1
	ladderZ := length - 2
	if hasPorch && ladderZ >= frontWallZ {
		ladderZ = frontWallZ - 1
	}
	if ladderZ < 1 {
		ladderZ = 1
	}

	layers := make([]blueprints.Layer, 0, totalHeight)

	for y := minY; y < minY+totalHeight; y++ {
		matrix := make([][]int, length)
		for z := 0; z < length; z++ {
			matrix[z] = make([]int, width)
		}

		switch {
		case y < 0:
			// Subterranean Cellar level: at least 2 vertical blocks standing clearance (Y=-2 and Y=-1)
			for z := 0; z < length; z++ {
				for x := 0; x < width; x++ {
					if z == 0 || z == length-1 || x == 0 || x == width-1 {
						matrix[z][x] = 10 // stone_bricks walls
					} else if hasCellar && x == ladderX && z == ladderZ {
						matrix[z][x] = 15 // ladder descending shaft
					} else if y == minY && ((x == 1 && z == 1) || (x == width-2 && z == 1) || (x == width-2 && z == length-2)) {
						matrix[z][x] = 6 // storage barrel on cellar floor
					} else if y == -1 && x == width-2 && z == length/2 {
						matrix[z][x] = 7 // wall torch lighting
					} else {
						matrix[z][x] = 0 // air for excavation and 2-block standing clearance
					}
				}
			}

		case y == 0:
			// Ground Floor: foundation / planks, corner log pillars, divider pillar if multi-room
			for z := 0; z < length; z++ {
				for x := 0; x < width; x++ {
					if hasCellar && x == ladderX && z == ladderZ {
						matrix[z][x] = 15 // dedicated 1-block descent opening with ladder
						continue
					}
					isCorner := (x == 0 || x == width-1) && (z == 0 || z == length-1)
					isDivPillar := hasDivision && (x == dividerX) && (z == 0 || z == length-1)
					if isCorner || isDivPillar {
						matrix[z][x] = 2 // log pillar
					} else {
						matrix[z][x] = 1 // planks floor
					}
				}
			}

		case y >= 1 && y < wallHeight:
			// Wall Layers: Y=1 lower wall with doors & windows; Y=2 mid wall; Y=3 upper wall/lintels
			for z := 0; z < length; z++ {
				for x := 0; x < width; x++ {
					// 2 vertical blocks of clearance over the ladder/shaft at Y=1 and Y=2 (and air at Y=3)
					if hasCellar && x == ladderX && z == ladderZ {
						matrix[z][x] = 0 // unobstructed standing clearance over ladder opening
						continue
					}

					isCorner := (x == 0 || x == width-1) && (z == 0 || z == length-1)
					if isCorner {
						matrix[z][x] = 2 // log pillar
						continue
					}

					// Front porch open area
					if hasPorch && z == length-1 {
						matrix[z][x] = 0
						continue
					}

					isOuterWall := z == 0 || z == length-1 || x == 0 || x == width-1
					if hasPorch && z == length-2 {
						isOuterWall = true
					}

					if isOuterWall {
						mainDoorX := width / 2
						if hasDivision {
							mainDoorX = dividerX + (width-dividerX)/2
						}

						if z == frontWallZ && x == mainDoorX {
							if y == 1 {
								matrix[z][x] = 13 // oak_door lower half
							} else if y == 2 {
								matrix[z][x] = 16 // oak_door upper half (2-block clearance)
							} else if y == 3 {
								matrix[z][x] = 1 // plank lintel above 2-block doorway
							} else {
								matrix[z][x] = 1 // plank wall
							}
						} else if (x == 0 || x == width-1) && z == length/2 {
							if y == 1 || y == 2 {
								matrix[z][x] = 5 // glass pane
							} else {
								matrix[z][x] = 1 // plank lintel above window
							}
						} else if z == 0 && (x == width/4 || x == 3*width/4) {
							if y == 1 || y == 2 {
								matrix[z][x] = 5 // glass pane
							} else {
								matrix[z][x] = 1 // plank lintel above window
							}
						} else {
							matrix[z][x] = 1 // plank wall
						}
						continue
					}

					// Interior divider wall
					if hasDivision && x == dividerX {
						if z == doorZ {
							if y == 1 {
								matrix[z][x] = 13 // interior door lower half
							} else if y == 2 {
								matrix[z][x] = 16 // interior door upper half (2-block clearance)
							} else if y == 3 {
								matrix[z][x] = 1 // plank lintel above interior doorway
							} else {
								matrix[z][x] = 1 // plank divider wall
							}
						} else {
							matrix[z][x] = 1 // plank divider wall
						}
						continue
					}

					// Furnishings
					if y == 1 {
						if hasDivision {
							// Room 1 (Left / Bedroom): bed occupying 2 contiguous voxels oriented NS (foot at z=2, head at z=1), chest
							if x == 1 && z == 2 && length > 3 {
								matrix[z][x] = 11 // red_bed[facing=north,part=foot]
							} else if x == 1 && z == 1 {
								matrix[z][x] = 17 // red_bed[facing=north,part=head]
							} else if x == dividerX-1 && z == 1 {
								matrix[z][x] = 9 // chest
							}

							// Room 2 (Right / Workshop): crafting table, furnace, barrel/crates, chest
							if x == width-2 && z == 1 {
								matrix[z][x] = 8 // crafting table
							} else if x == width-2 && z == 2 && length > 4 {
								matrix[z][x] = 12 // furnace
							} else if x == width-2 && z == length-2 && (!hasPorch || z < frontWallZ) {
								matrix[z][x] = 6 // barrel
							} else if x == dividerX+1 && z == 1 {
								matrix[z][x] = 9 // chest
							}
						} else {
							// Single room furnishings
							if x == 1 && z == 1 {
								matrix[z][x] = 8 // crafting table
							} else if x == width-2 && z == 1 {
								matrix[z][x] = 9 // chest
							} else if x == width-2 && z == length-2 && (!hasPorch || z < frontWallZ) {
								matrix[z][x] = 6 // barrel
							} else if hasCellar && ladderX == 1 && (ladderZ == length-2 || ladderZ == length-3) {
								// When cellar ladder is in the back corner, place bed in available contiguous interior voxels
								if x == 1 && z == 2 && length > 3 {
									matrix[z][x] = 11 // red_bed foot
								} else if x == 2 && z == 2 {
									matrix[z][x] = 17 // red_bed head
								}
							} else {
								if x == 1 && z == length-2 && (!hasPorch || z < frontWallZ) {
									matrix[z][x] = 11 // red_bed foot
								} else if x == 1 && z == length-3 && length > 4 {
									matrix[z][x] = 17 // red_bed head
								}
							}
						}
					} else if y == 2 {
						// Torches on interior walls
						if hasDivision {
							if x == dividerX/2 && z == 1 {
								matrix[z][x] = 7 // torch in bedroom
							} else if x == dividerX+(width-dividerX)/2 && z == 1 {
								matrix[z][x] = 7 // torch in workshop
							}
						} else if x == width/2 && z == 1 {
							matrix[z][x] = 7 // torch
						}
					}
					// Note: At y == 3, interior blocks remain 0 (air) providing comfortable 3-block ceiling height
				}
			}

		default:
			// Roof layers
			roofRelY := y - wallHeight
			if hasSlopedRoof && roofLayersCount == 2 {
				if roofRelY == 0 {
					// Base roof layer
					for z := 0; z < length; z++ {
						for x := 0; x < width; x++ {
							if z == 0 {
								matrix[z][x] = 14 // stairs facing south
							} else if z == length-1 {
								matrix[z][x] = 4 // stairs facing north
							} else if x == 0 || x == width-1 {
								matrix[z][x] = 1 // gable wall planks
							} else {
								matrix[z][x] = 1 // ceiling planks
							}
						}
					}
				} else {
					// Upper ridge roof layer
					for z := 0; z < length; z++ {
						for x := 0; x < width; x++ {
							if z == 0 || z == length-1 {
								matrix[z][x] = 0 // open air (slope recedes inward)
							} else if z == 1 {
								matrix[z][x] = 14 // stairs facing south
							} else if z == length-2 {
								matrix[z][x] = 4 // stairs facing north
							} else if x == 0 || x == width-1 {
								matrix[z][x] = 1 // gable wall planks
							} else {
								matrix[z][x] = 1 // center ridge planks
							}
						}
					}
				}
			} else {
				// Single roof layer (stairs trim with ceiling planks)
				for z := 0; z < length; z++ {
					for x := 0; x < width; x++ {
						if hasPorch && z == length-1 {
							matrix[z][x] = 4 // stairs overhang
						} else if z == 0 {
							matrix[z][x] = 14 // stairs facing south
						} else if z == length-1 {
							matrix[z][x] = 4 // stairs facing north
						} else if x == 0 || x == width-1 {
							matrix[z][x] = 4 // stairs roof trim
						} else {
							matrix[z][x] = 1 // ceiling planks
						}
					}
				}
			}
		}

		layers = append(layers, blueprints.Layer{
			Y:      y,
			Matrix: matrix,
		})
	}

	slug := blueprints.Slugify(name + "_" + fmt.Sprintf("%d", time.Now().UnixNano()%100000))
	bp := &blueprints.Blueprint{
		ID:          slug,
		Name:        name,
		Version:     1,
		Purpose:     purpose,
		Type:        strings.ToLower(purpose),
		Style:       style,
		Author:      "System 2 Master Architect",
		Description: fmt.Sprintf("Architecturally synthesized blueprint: %s", req.Prompt),
		Dimensions: blueprints.Dimensions{
			Width:  width,
			Height: totalHeight,
			Length: length,
			MinY:   minY,
		},
		Palette: palette,
		Layers:  layers,
	}

	if err := bp.Validate(); err != nil {
		return nil, fmt.Errorf("validating synthesized fallback blueprint: %w", err)
	}

	return bp, nil
}

var standardBlockCatalog = []string{
	// Stones & Masonry
	"minecraft:stone",
	"minecraft:cobblestone",
	"minecraft:stone_bricks",
	"minecraft:mossy_stone_bricks",
	"minecraft:deepslate",
	"minecraft:cobbled_deepslate",
	"minecraft:deepslate_bricks",
	"minecraft:sandstone",
	"minecraft:smooth_stone",
	"minecraft:granite",
	"minecraft:diorite",
	"minecraft:andesite",
	"minecraft:tuff",
	"minecraft:brick",

	// Woods & Planks
	"minecraft:oak_planks",
	"minecraft:spruce_planks",
	"minecraft:birch_planks",
	"minecraft:jungle_planks",
	"minecraft:acacia_planks",
	"minecraft:dark_oak_planks",
	"minecraft:mangrove_planks",
	"minecraft:cherry_planks",
	"minecraft:bamboo_planks",

	// 3-Axis Directional (Logs & Pillars)
	"minecraft:oak_log",
	"minecraft:spruce_log",
	"minecraft:birch_log",
	"minecraft:dark_oak_log",
	"minecraft:hay_block",
	"minecraft:basalt",
	"minecraft:bone_block",

	// Horizontal Directional (Stairs)
	"minecraft:oak_stairs",
	"minecraft:spruce_stairs",
	"minecraft:birch_stairs",
	"minecraft:cobblestone_stairs",
	"minecraft:stone_brick_stairs",
	"minecraft:sandstone_stairs",

	// Slabs
	"minecraft:oak_slab",
	"minecraft:spruce_slab",
	"minecraft:cobblestone_slab",
	"minecraft:stone_brick_slab",
	"minecraft:stone_slab",

	// Doors & Gates
	"minecraft:oak_door",
	"minecraft:spruce_door",
	"minecraft:oak_trapdoor",
	"minecraft:oak_fence_gate",
	"minecraft:oak_fence",

	// Utilities & Containers
	"minecraft:chest",
	"minecraft:furnace",
	"minecraft:crafting_table",
	"minecraft:barrel",
	"minecraft:ladder",
	"minecraft:red_bed",

	// 6-Way Directional
	"minecraft:piston",
	"minecraft:sticky_piston",
	"minecraft:dispenser",
	"minecraft:dropper",
	"minecraft:observer",
	"minecraft:hopper",

	// Rotational (16-step)
	"minecraft:oak_sign",
	"minecraft:white_banner",

	// Light & Glass
	"minecraft:torch",
	"minecraft:lantern",
	"minecraft:glass",
	"minecraft:glass_pane",
}

func (s *Server) handlePalette(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	entries := make([]PaletteBlockEntry, 0, len(standardBlockCatalog))
	for _, blockID := range standardBlockCatalog {
		category := blueprints.GetOrientationCategory(blockID)
		defaultProps := blueprints.CanonicalDefaultProperties(blockID)
		if defaultProps == nil {
			defaultProps = make(map[string]string)
		}
		glyph := blueprints.DirectionGlyph(blueprints.BlockState{
			Identifier: blockID,
			Properties: defaultProps,
		})

		entries = append(entries, PaletteBlockEntry{
			ID:                  blockID,
			OrientationCategory: category,
			Category:            category,
			DefaultProperties:   defaultProps,
			Properties:          defaultProps,
			DirectionGlyph:      glyph,
			Glyph:               glyph,
		})
	}

	categories := []blueprints.OrientationCategory{
		blueprints.OrientationNone,
		blueprints.OrientationAxis,
		blueprints.OrientationHorizontal,
		blueprints.Orientation6Way,
		blueprints.OrientationSlab,
		blueprints.OrientationRotation,
	}

	writeJSON(w, http.StatusOK, PaletteResponse{
		Blocks:     entries,
		Categories: categories,
	})
}

func (s *Server) handleTaxonomy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	tags := blueprints.KnownTaxonomyTags()
	taxonomyMap := blueprints.KnownTaxonomyMap()

	writeJSON(w, http.StatusOK, TaxonomyResponse{
		Tags:     tags,
		Taxonomy: taxonomyMap,
		Map:      taxonomyMap,
	})
}

// resolveAgentSocket returns the active/configured socket path for an agent ID.
func (s *Server) resolveAgentSocket(agentID string) string {
	r := s.cfg.Resolver
	if r == nil {
		r = xdg.DefaultResolver()
	}

	// 1. Check $XDG_STATE_HOME/civquest/sockets/{agent_id}.sock
	cand1 := filepath.Join(r.StateDir(), "sockets", agentID+".sock")
	if _, err := os.Stat(cand1); err == nil {
		return cand1
	}

	// 2. Check resolver's AgentSocketPath
	cand2 := r.AgentSocketPath(agentID)
	if _, err := os.Stat(cand2); err == nil {
		return cand2
	}

	// 3. Check $XDG_STATE_HOME/civquest/agents/{agent_id}.sock
	cand3 := filepath.Join(r.StateDir(), "agents", agentID+".sock")
	if _, err := os.Stat(cand3); err == nil {
		return cand3
	}

	// Default fallback to cand1
	return cand1
}

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	resolver := s.cfg.Resolver
	if resolver == nil {
		resolver = xdg.DefaultResolver()
	}

	agentsMap := make(map[string]*AgentInfo)

	// 1. Scan manifests directory ($XDG_CONFIG_HOME/civquest/manifests/)
	manifestsDir := filepath.Join(resolver.ConfigDir(), "manifests")
	if entries, err := os.ReadDir(manifestsDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			ext := strings.ToLower(filepath.Ext(name))
			if ext != ".json" && ext != ".yaml" && ext != ".yml" {
				continue
			}
			agentID := strings.TrimSuffix(name, ext)
			fullPath := filepath.Join(manifestsDir, name)

			info := &AgentInfo{
				ID:   agentID,
				Name: agentID,
			}

			if data, err := os.ReadFile(fullPath); err == nil {
				var parsed struct {
					Name       string `json:"name" yaml:"name"`
					ClanID     string `json:"clan_id" yaml:"clan_id"`
					Profession string `json:"profession" yaml:"profession"`
				}
				if json.Unmarshal(data, &parsed) == nil && parsed.Name != "" {
					info.Name = parsed.Name
					info.ClanID = parsed.ClanID
					info.Profession = parsed.Profession
				}
			}

			agentsMap[agentID] = info
		}
	}

	// 2. Scan agents config directory ($XDG_CONFIG_HOME/civquest/agents/)
	agentsConfigDir := resolver.AgentsConfigDir()
	if entries, err := os.ReadDir(agentsConfigDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			ext := strings.ToLower(filepath.Ext(name))
			if ext != ".yaml" && ext != ".yml" && ext != ".json" {
				continue
			}
			agentID := strings.TrimSuffix(name, ext)
			if _, exists := agentsMap[agentID]; !exists {
				agentsMap[agentID] = &AgentInfo{
					ID:   agentID,
					Name: agentID,
				}
			}
			if cfg, err := resolver.LoadAgentConfig(agentID); err == nil {
				agentsMap[agentID].Name = cfg.Name
				agentsMap[agentID].ClanID = cfg.ClanID
				agentsMap[agentID].Profession = cfg.Profession
			}
		}
	}

	// 3. Scan sockets directory ($XDG_STATE_HOME/civquest/sockets/)
	socketsDir := filepath.Join(resolver.StateDir(), "sockets")
	if entries, err := os.ReadDir(socketsDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if strings.HasSuffix(name, ".sock") {
				agentID := strings.TrimSuffix(name, ".sock")
				if _, exists := agentsMap[agentID]; !exists {
					agentsMap[agentID] = &AgentInfo{
						ID:   agentID,
						Name: agentID,
					}
				}
			}
		}
	}

	// 4. Scan agents state directory ($XDG_STATE_HOME/civquest/agents/)
	agentsStateDir := resolver.AgentsStateDir()
	if entries, err := os.ReadDir(agentsStateDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if strings.HasSuffix(name, ".sock") {
				agentID := strings.TrimSuffix(name, ".sock")
				if _, exists := agentsMap[agentID]; !exists {
					agentsMap[agentID] = &AgentInfo{
						ID:   agentID,
						Name: agentID,
					}
				}
			}
		}
	}

	// 5. Test socket responsiveness for each detected agent
	result := make([]AgentInfo, 0, len(agentsMap))
	for agentID, info := range agentsMap {
		sockPath := s.resolveAgentSocket(agentID)
		info.SocketPath = sockPath

		if _, err := os.Stat(sockPath); err == nil {
			conn, err := net.DialTimeout("unix", sockPath, 50*time.Millisecond)
			if err == nil {
				info.Active = true
				_ = conn.Close()
			}
		}

		result = append(result, *info)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})

	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleAgentStatus(w http.ResponseWriter, r *http.Request) {
	sub := strings.TrimPrefix(r.URL.Path, "/api/agents/")
	sub = strings.Trim(sub, "/")

	if sub == "" {
		s.handleAgents(w, r)
		return
	}

	agentID := sub
	if strings.HasSuffix(sub, "/status") {
		agentID = strings.TrimSuffix(sub, "/status")
	}

	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	sockPath := s.resolveAgentSocket(agentID)
	conn, err := net.DialTimeout("unix", sockPath, 500*time.Millisecond)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":       fmt.Sprintf("agent socket unavailable: %v", err),
			"agent":       agentID,
			"active":      false,
			"socket_path": sockPath,
		})
		return
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(1 * time.Second))
	scanner := bufio.NewScanner(conn)
	if scanner.Scan() {
		raw := scanner.Bytes()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
		return
	}

	writeJSONError(w, http.StatusInternalServerError, "empty response from agent socket")
}

// -----------------------------------------------------------------------------
// WebSocket Handlers
// -----------------------------------------------------------------------------

func (s *Server) handleAgentWS(ws *websocket.Conn) {
	defer ws.Close()

	agentID := strings.TrimPrefix(ws.Request().URL.Path, "/ws/agents/")
	agentID = strings.Trim(agentID, "/")

	if agentID == "" {
		_ = websocket.Message.Send(ws, `{"error":"missing agent id"}`)
		return
	}

	disconnectChan := make(chan struct{})
	go func() {
		var dummy string
		for {
			if err := websocket.Message.Receive(ws, &dummy); err != nil {
				close(disconnectChan)
				return
			}
		}
	}()

	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-disconnectChan:
			return
		case <-ticker.C:
			sockPath := s.resolveAgentSocket(agentID)
			conn, err := net.DialTimeout("unix", sockPath, 200*time.Millisecond)
			if err != nil {
				msg, _ := json.Marshal(map[string]any{
					"agent":       agentID,
					"active":      false,
					"error":       fmt.Sprintf("socket unavailable: %v", err),
					"socket_path": sockPath,
					"time":        time.Now().UTC().Format(time.RFC3339),
				})
				if err := websocket.Message.Send(ws, string(msg)); err != nil {
					return
				}
				continue
			}

			_ = conn.SetDeadline(time.Now().Add(250 * time.Millisecond))
			scanner := bufio.NewScanner(conn)
			if scanner.Scan() {
				line := scanner.Text()
				if err := websocket.Message.Send(ws, line); err != nil {
					_ = conn.Close()
					return
				}
			}
			_ = conn.Close()
		}
	}
}

func (s *Server) handleStudioWS(ws *websocket.Conn) {
	s.hub.register(ws)
	defer s.hub.unregister(ws)
	defer ws.Close()

	// Initial welcome message
	welcome, _ := json.Marshal(map[string]any{
		"type":    "welcome",
		"message": "Connected to CivQuest Studio Collaboration Hub",
		"clients": s.hub.clientCount(),
		"time":    time.Now().UTC().Format(time.RFC3339),
	})
	_ = websocket.Message.Send(ws, string(welcome))

	for {
		var msg string
		if err := websocket.Message.Receive(ws, &msg); err != nil {
			break
		}
		s.hub.broadcast(ws, msg)
	}
}

// -----------------------------------------------------------------------------
// Static Content Handler (SPA)
// -----------------------------------------------------------------------------

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	subFS, err := fs.Sub(staticFS, "static")
	if err != nil {
		http.Error(w, "Static asset filesystem error", http.StatusInternalServerError)
		return
	}

	cleanPath := strings.TrimPrefix(r.URL.Path, "/")
	if cleanPath == "" {
		cleanPath = "index.html"
	}

	file, err := subFS.Open(cleanPath)
	if err != nil {
		// SPA Fallback: serve index.html
		file, err = subFS.Open("index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		cleanPath = "index.html"
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil || stat.IsDir() {
		// If directory, fallback to index.html
		indexFile, err := subFS.Open("index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer indexFile.Close()
		http.ServeContent(w, r, "index.html", time.Time{}, indexFile.(io.ReadSeeker))
		return
	}

	http.ServeContent(w, r, cleanPath, stat.ModTime(), file.(io.ReadSeeker))
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, code int, val any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(val)
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
