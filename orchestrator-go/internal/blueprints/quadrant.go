package blueprints

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// Spatial quadrant identifiers per ADR-010.
type Quadrant string

const (
	QuadrantNW Quadrant = Quadrant(db.QuadrantNW)
	QuadrantNE Quadrant = Quadrant(db.QuadrantNE)
	QuadrantSW Quadrant = Quadrant(db.QuadrantSW)
	QuadrantSE Quadrant = Quadrant(db.QuadrantSE)
)

// DefaultScaffoldBlock is the default Minecraft block identifier used for temporary construction scaffolding.
const DefaultScaffoldBlock = "minecraft:scaffolding"

var (
	ErrAgentAlreadyAssigned   = errors.New("agent already assigned to a different quadrant")
	ErrQuadrantAlreadyClaimed = errors.New("quadrant is already assigned to another agent")
	ErrInvalidQuadrant        = errors.New("invalid spatial quadrant")
	ErrNilVoxelMatrix         = errors.New("voxel matrix cannot be nil")
	ErrEmptyZoneID            = errors.New("zone_id cannot be empty")
	ErrEmptyZoneType          = errors.New("zone_type cannot be empty")
	ErrNilCivicDB             = errors.New("civic registry database cannot be nil")
)

// AllQuadrants returns the 4 standard spatial quadrants in deterministic clockwise order.
func AllQuadrants() []Quadrant {
	return []Quadrant{QuadrantNW, QuadrantNE, QuadrantSE, QuadrantSW}
}

// IsValidQuadrant returns true if q is one of NW, NE, SW, or SE.
func IsValidQuadrant(q Quadrant) bool {
	switch q {
	case QuadrantNW, QuadrantNE, QuadrantSW, QuadrantSE:
		return true
	default:
		return false
	}
}

// QuadrantBounds holds the inclusive coordinate ranges for a quadrant within matrix dimensions.
type QuadrantBounds struct {
	MinX int
	MaxX int
	MinZ int
	MaxZ int
}

// GetQuadrantBounds returns the bounding box [minX..maxX, minZ..maxZ] for a quadrant.
// The blueprint footprint is split at midX = width / 2 and midZ = length / 2:
// - NW: [0 .. midX-1], [0 .. midZ-1]
// - NE: [midX .. width-1], [0 .. midZ-1]
// - SW: [0 .. midX-1], [midZ .. length-1]
// - SE: [midX .. width-1], [midZ .. length-1]
func GetQuadrantBounds(q Quadrant, width, length int) (QuadrantBounds, error) {
	if width <= 0 || length <= 0 {
		return QuadrantBounds{}, ErrInvalidDimensions
	}
	midX := width / 2
	midZ := length / 2

	switch q {
	case QuadrantNW:
		return QuadrantBounds{MinX: 0, MaxX: midX - 1, MinZ: 0, MaxZ: midZ - 1}, nil
	case QuadrantNE:
		return QuadrantBounds{MinX: midX, MaxX: width - 1, MinZ: 0, MaxZ: midZ - 1}, nil
	case QuadrantSW:
		return QuadrantBounds{MinX: 0, MaxX: midX - 1, MinZ: midZ, MaxZ: length - 1}, nil
	case QuadrantSE:
		return QuadrantBounds{MinX: midX, MaxX: width - 1, MinZ: midZ, MaxZ: length - 1}, nil
	default:
		return QuadrantBounds{}, fmt.Errorf("%w: %s", ErrInvalidQuadrant, q)
	}
}

// GetQuadrantForCoord determines which quadrant contains the matrix coordinate (x, z).
// midX = width / 2, midZ = length / 2
// - NW: x < midX && z < midZ
// - NE: x >= midX && z < midZ
// - SW: x < midX && z >= midZ
// - SE: x >= midX && z >= midZ
func GetQuadrantForCoord(x, z, width, length int) Quadrant {
	midX := width / 2
	midZ := length / 2

	if x < midX {
		if z < midZ {
			return QuadrantNW
		}
		return QuadrantSW
	}

	if z < midZ {
		return QuadrantNE
	}
	return QuadrantSE
}

// PartitionBlueprint subdivides a VoxelMatrix into 4 non-overlapping spatial quadrants: NW, NE, SW, SE.
// Blocks within each quadrant are sorted layer-by-layer in strict ascending Y coordinates (Y_min -> Y_max).
// Air blocks are excluded since they do not require placement.
func PartitionBlueprint(vm *VoxelMatrix) map[Quadrant][]*civquestv1.BuildBlockIntent {
	return PartitionBlueprintWithAnchor(vm, 0, 0, 0)
}

// PartitionBlueprintWithAnchor subdivides a VoxelMatrix into 4 quadrants applying an anchor offset to TargetVoxel.
func PartitionBlueprintWithAnchor(vm *VoxelMatrix, anchorX, anchorY, anchorZ int) map[Quadrant][]*civquestv1.BuildBlockIntent {
	result := map[Quadrant][]*civquestv1.BuildBlockIntent{
		QuadrantNW: make([]*civquestv1.BuildBlockIntent, 0),
		QuadrantNE: make([]*civquestv1.BuildBlockIntent, 0),
		QuadrantSW: make([]*civquestv1.BuildBlockIntent, 0),
		QuadrantSE: make([]*civquestv1.BuildBlockIntent, 0),
	}

	if vm == nil || vm.Dimensions.Width <= 0 || vm.Dimensions.Height <= 0 || vm.Dimensions.Length <= 0 {
		return result
	}

	width := vm.Dimensions.Width
	height := vm.Dimensions.Height
	length := vm.Dimensions.Length

	// Enforce strict bottom-up layer-by-layer ordering (Y_min -> Y_max)
	for y := 0; y < height; y++ {
		for z := 0; z < length; z++ {
			for x := 0; x < width; x++ {
				state, err := vm.Get(x, y, z)
				if err != nil || state.IsAir() {
					continue
				}

				q := GetQuadrantForCoord(x, z, width, length)
				intent := &civquestv1.BuildBlockIntent{
					TargetVoxel: &civquestv1.BlockPosProto{
						X: int32(anchorX + x),
						Y: int32(anchorY + y),
						Z: int32(anchorZ + z),
					},
					BlockStateString:    state.String(),
					IsTemporaryScaffold: false,
				}
				result[q] = append(result[q], intent)
			}
		}
	}

	return result
}

// ScaffoldingPlan encapsulates temporary scaffolding placements (bottom-up) and removal sequence (top-down).
type ScaffoldingPlan struct {
	Quadrant   Quadrant                       `json:"quadrant"`
	Placements []*civquestv1.BuildBlockIntent `json:"placements"`
	Removals   []*civquestv1.BlockPosProto    `json:"removals"`
}

// RemovalIntents transforms Removals into HARVEST_BREAK InteractBlockIntents in top-down order.
func (sp *ScaffoldingPlan) RemovalIntents() []*civquestv1.InteractBlockIntent {
	if sp == nil || len(sp.Removals) == 0 {
		return nil
	}
	intents := make([]*civquestv1.InteractBlockIntent, len(sp.Removals))
	for i, pos := range sp.Removals {
		intents[i] = &civquestv1.InteractBlockIntent{
			Pos:             pos,
			InteractionType: civquestv1.InteractBlockIntent_HARVEST_BREAK,
		}
	}
	return intents
}

// ScaffoldConfig provides optional customization when generating scaffolding.
type ScaffoldConfig struct {
	ScaffoldBlock string
	AnchorX       int
	AnchorY       int
	AnchorZ       int
	CustomX       *int
	CustomZ       *int
}

// GenerateScaffoldingPlan generates scaffolding placements (bottom-up: Y_min -> Y_max) and removals (top-down: Y_max -> Y_min)
// for vertical reach when structure height > 2.
func GenerateScaffoldingPlan(vm *VoxelMatrix, q Quadrant, scaffoldBlock string) *ScaffoldingPlan {
	return GenerateScaffoldingPlanWithConfig(vm, q, ScaffoldConfig{ScaffoldBlock: scaffoldBlock})
}

// GenerateScaffoldingPlanWithAnchor generates scaffolding for a quadrant applying world anchor offsets.
func GenerateScaffoldingPlanWithAnchor(vm *VoxelMatrix, q Quadrant, scaffoldBlock string, anchorX, anchorY, anchorZ int) *ScaffoldingPlan {
	return GenerateScaffoldingPlanWithConfig(vm, q, ScaffoldConfig{
		ScaffoldBlock: scaffoldBlock,
		AnchorX:       anchorX,
		AnchorY:       anchorY,
		AnchorZ:       anchorZ,
	})
}

// GenerateScaffoldingPlanWithConfig generates scaffolding according to a custom ScaffoldConfig.
func GenerateScaffoldingPlanWithConfig(vm *VoxelMatrix, q Quadrant, cfg ScaffoldConfig) *ScaffoldingPlan {
	plan := &ScaffoldingPlan{
		Quadrant:   q,
		Placements: make([]*civquestv1.BuildBlockIntent, 0),
		Removals:   make([]*civquestv1.BlockPosProto, 0),
	}

	if vm == nil || vm.Dimensions.Height <= 2 {
		return plan
	}

	blockState := cfg.ScaffoldBlock
	if strings.TrimSpace(blockState) == "" {
		blockState = DefaultScaffoldBlock
	}

	width := vm.Dimensions.Width
	height := vm.Dimensions.Height
	length := vm.Dimensions.Length

	// Determine the highest block elevation in this quadrant
	maxBlockY := -1
	bounds, err := GetQuadrantBounds(q, width, length)
	if err == nil && bounds.MaxX >= bounds.MinX && bounds.MaxZ >= bounds.MinZ {
		for y := 0; y < height; y++ {
			for z := bounds.MinZ; z <= bounds.MaxZ; z++ {
				for x := bounds.MinX; x <= bounds.MaxX; x++ {
					st, err := vm.Get(x, y, z)
					if err == nil && !st.IsAir() {
						if y > maxBlockY {
							maxBlockY = y
						}
					}
				}
			}
		}
	}

	// If no blocks are placed in this quadrant, default to height-1 if height > 2
	targetReachY := maxBlockY
	if targetReachY < 0 {
		targetReachY = height - 1
	}

	// Height <= 2 requires no scaffolding (agents reach Y=0 and Y=1 directly)
	if targetReachY < 2 {
		return plan
	}

	// Determine scaffolding coordinate (scaffoldX, scaffoldZ)
	var scaffoldX, scaffoldZ int
	if cfg.CustomX != nil && cfg.CustomZ != nil {
		scaffoldX = *cfg.CustomX
		scaffoldZ = *cfg.CustomZ
	} else if err == nil && bounds.MaxX >= bounds.MinX && bounds.MaxZ >= bounds.MinZ {
		// Try to find an air column inside quadrant bounds
		foundAir := false
		for z := bounds.MinZ; z <= bounds.MaxZ; z++ {
			for x := bounds.MinX; x <= bounds.MaxX; x++ {
				allAir := true
				for y := 0; y < targetReachY; y++ {
					st, err := vm.Get(x, y, z)
					if err != nil || !st.IsAir() {
						allAir = false
						break
					}
				}
				if allAir {
					scaffoldX = x
					scaffoldZ = z
					foundAir = true
					break
				}
			}
			if foundAir {
				break
			}
		}

		if !foundAir {
			// Default to quadrant outer corner
			switch q {
			case QuadrantNW:
				scaffoldX, scaffoldZ = bounds.MinX, bounds.MinZ
			case QuadrantNE:
				scaffoldX, scaffoldZ = bounds.MaxX, bounds.MinZ
			case QuadrantSW:
				scaffoldX, scaffoldZ = bounds.MinX, bounds.MaxZ
			case QuadrantSE:
				scaffoldX, scaffoldZ = bounds.MaxX, bounds.MaxZ
			}
		}
	}

	worldX := int32(cfg.AnchorX + scaffoldX)
	worldZ := int32(cfg.AnchorZ + scaffoldZ)

	// Scaffolding rises from Y=0 up to targetReachY - 1 (providing standing height to place blocks at targetReachY)
	scaffoldMaxY := targetReachY - 1

	// Bottom-up placement: Y_min -> Y_max
	for y := 0; y <= scaffoldMaxY; y++ {
		worldY := int32(cfg.AnchorY + y)
		placement := &civquestv1.BuildBlockIntent{
			TargetVoxel: &civquestv1.BlockPosProto{
				X: worldX,
				Y: worldY,
				Z: worldZ,
			},
			BlockStateString:    blockState,
			IsTemporaryScaffold: true,
		}
		plan.Placements = append(plan.Placements, placement)
	}

	// Top-down removal: Y_max -> Y_min
	for y := scaffoldMaxY; y >= 0; y-- {
		worldY := int32(cfg.AnchorY + y)
		pos := &civquestv1.BlockPosProto{
			X: worldX,
			Y: worldY,
			Z: worldZ,
		}
		plan.Removals = append(plan.Removals, pos)
	}

	return plan
}

// CoordinatorConfig configures a multi-agent ConstructionCoordinator.
type CoordinatorConfig struct {
	ProjectID string
	Blueprint *Blueprint
	Matrix    *VoxelMatrix
	AnchorX   int
	AnchorY   int
	AnchorZ   int
	CivicDB   *db.CivicRegistryDB
	Tracker   *ProjectTracker
}

// ConstructionCoordinator coordinates collaborative multi-agent building across non-overlapping spatial quadrants.
type ConstructionCoordinator struct {
	mu                 sync.RWMutex
	projectID          string
	blueprint          *Blueprint
	matrix             *VoxelMatrix
	anchorX            int
	anchorY            int
	anchorZ            int
	civicDB            *db.CivicRegistryDB
	tracker            *ProjectTracker
	assignments        map[Quadrant]string
	agentQuadrants     map[string]Quadrant
	quadrantIntents    map[Quadrant][]*civquestv1.BuildBlockIntent
	scaffoldingPlans   map[Quadrant]*ScaffoldingPlan
	completedQuadrants map[Quadrant]bool
}

// NewConstructionCoordinator creates and initializes a multi-agent construction coordinator.
func NewConstructionCoordinator(cfg CoordinatorConfig) (*ConstructionCoordinator, error) {
	vm := cfg.Matrix
	if vm == nil && cfg.Blueprint != nil {
		var err error
		vm, err = cfg.Blueprint.ToVoxelMatrix()
		if err != nil {
			return nil, fmt.Errorf("converting blueprint to voxel matrix: %w", err)
		}
	}

	if vm == nil {
		return nil, ErrNilVoxelMatrix
	}

	intents := PartitionBlueprintWithAnchor(vm, cfg.AnchorX, cfg.AnchorY, cfg.AnchorZ)

	cc := &ConstructionCoordinator{
		projectID:          cfg.ProjectID,
		blueprint:          cfg.Blueprint,
		matrix:             vm,
		anchorX:            cfg.AnchorX,
		anchorY:            cfg.AnchorY,
		anchorZ:            cfg.AnchorZ,
		civicDB:            cfg.CivicDB,
		tracker:            cfg.Tracker,
		assignments:        make(map[Quadrant]string),
		agentQuadrants:     make(map[string]Quadrant),
		quadrantIntents:    intents,
		scaffoldingPlans:   make(map[Quadrant]*ScaffoldingPlan),
		completedQuadrants: make(map[Quadrant]bool),
	}

	// Pre-generate scaffolding plans for any quadrant with height > 2
	for _, q := range AllQuadrants() {
		cc.scaffoldingPlans[q] = GenerateScaffoldingPlanWithAnchor(
			vm, q, DefaultScaffoldBlock, cfg.AnchorX, cfg.AnchorY, cfg.AnchorZ,
		)
	}

	return cc, nil
}

// AssignAgent assigns an independent builder agent exclusively to a quadrant.
// Enforces strict agent isolation to prevent physical collisions:
// - An agent cannot be assigned to more than one quadrant simultaneously.
// - A quadrant cannot be assigned to more than one agent at a time.
func (cc *ConstructionCoordinator) AssignAgent(q Quadrant, agentID string) error {
	if !IsValidQuadrant(q) {
		return fmt.Errorf("%w: %s", ErrInvalidQuadrant, q)
	}
	if strings.TrimSpace(agentID) == "" {
		return errors.New("agentID cannot be empty")
	}

	cc.mu.Lock()
	defer cc.mu.Unlock()

	// Check if agent is already assigned elsewhere
	if existingQ, exists := cc.agentQuadrants[agentID]; exists {
		if existingQ != q {
			return fmt.Errorf("%w: agent %s already assigned to %s", ErrAgentAlreadyAssigned, agentID, existingQ)
		}
		return nil // Idempotent
	}

	// Check if quadrant already has an agent
	if currentAgent, exists := cc.assignments[q]; exists && currentAgent != "" {
		if currentAgent != agentID {
			return fmt.Errorf("%w: quadrant %s already assigned to agent %s", ErrQuadrantAlreadyClaimed, q, currentAgent)
		}
		return nil // Idempotent
	}

	cc.assignments[q] = agentID
	cc.agentQuadrants[agentID] = q
	return nil
}

// UnassignAgent releases an agent from their assigned quadrant.
func (cc *ConstructionCoordinator) UnassignAgent(agentID string) {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	if q, exists := cc.agentQuadrants[agentID]; exists {
		delete(cc.assignments, q)
		delete(cc.agentQuadrants, agentID)
	}
}

// GetAssignedAgent returns the agent ID assigned to quadrant q, or "" if unassigned.
func (cc *ConstructionCoordinator) GetAssignedAgent(q Quadrant) string {
	cc.mu.RLock()
	defer cc.mu.RUnlock()
	return cc.assignments[q]
}

// GetAssignedQuadrant returns the quadrant assigned to agentID, if any.
func (cc *ConstructionCoordinator) GetAssignedQuadrant(agentID string) (Quadrant, bool) {
	cc.mu.RLock()
	defer cc.mu.RUnlock()
	q, ok := cc.agentQuadrants[agentID]
	return q, ok
}

// GetIntents returns a copy of the ordered BuildBlockIntents for a quadrant.
func (cc *ConstructionCoordinator) GetIntents(q Quadrant) []*civquestv1.BuildBlockIntent {
	cc.mu.RLock()
	defer cc.mu.RUnlock()

	src := cc.quadrantIntents[q]
	result := make([]*civquestv1.BuildBlockIntent, len(src))
	copy(result, src)
	return result
}

// GetScaffoldingPlan returns the ScaffoldingPlan for quadrant q.
func (cc *ConstructionCoordinator) GetScaffoldingPlan(q Quadrant) *ScaffoldingPlan {
	cc.mu.RLock()
	defer cc.mu.RUnlock()
	return cc.scaffoldingPlans[q]
}

// SetScaffoldingPlan sets or overrides the ScaffoldingPlan for quadrant q.
func (cc *ConstructionCoordinator) SetScaffoldingPlan(q Quadrant, plan *ScaffoldingPlan) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.scaffoldingPlans[q] = plan
}

// MarkQuadrantComplete records that construction and scaffolding disassembly for quadrant q are finished.
func (cc *ConstructionCoordinator) MarkQuadrantComplete(q Quadrant) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.completedQuadrants[q] = true
}

// IsQuadrantComplete returns true if quadrant q is completed.
func (cc *ConstructionCoordinator) IsQuadrantComplete(q Quadrant) bool {
	cc.mu.RLock()
	defer cc.mu.RUnlock()
	return cc.completedQuadrants[q]
}

// IsComplete returns true when all 4 quadrants have completed construction.
func (cc *ConstructionCoordinator) IsComplete() bool {
	cc.mu.RLock()
	defer cc.mu.RUnlock()

	for _, q := range AllQuadrants() {
		if !cc.completedQuadrants[q] {
			return false
		}
	}
	return true
}

// RegisterCompletedZoneRequest contains parameters to register a completed structure zone.
type RegisterCompletedZoneRequest struct {
	ZoneID       string
	ZoneType     string // e.g. "TAVERN", "GRANARY", "MARKET", "RESIDENTIAL"
	Name         string
	OwnerAgentID string
	WorldTick    int64
}

// RegisterCompletedZone registers the completed structure bounding box into civic_registry.db (functional_zones).
// Also advances the associated construction project to COMPLETED if a ProjectTracker is present.
func (cc *ConstructionCoordinator) RegisterCompletedZone(ctx context.Context, req RegisterCompletedZoneRequest) (*db.FunctionalZone, error) {
	if cc.civicDB == nil {
		return nil, ErrNilCivicDB
	}

	dims := cc.matrix.Dimensions
	zoneID := req.ZoneID
	if strings.TrimSpace(zoneID) == "" {
		zoneID = fmt.Sprintf("zone-%s-%s", strings.ToLower(req.ZoneType), cc.projectID)
	}

	params := RegisterZoneParams{
		ZoneID:       zoneID,
		ZoneType:     req.ZoneType,
		Name:         req.Name,
		OwnerAgentID: req.OwnerAgentID,
		AnchorX:      cc.anchorX,
		AnchorY:      cc.anchorY,
		AnchorZ:      cc.anchorZ,
		Dimensions:   dims,
		WorldTick:    req.WorldTick,
	}

	zone, err := RegisterCompletedZone(ctx, cc.civicDB, params)
	if err != nil {
		return nil, err
	}

	// Advance project lifecycle if tracker is configured
	if cc.tracker != nil && cc.projectID != "" {
		_, _ = cc.tracker.TransitionProject(ctx, cc.projectID, StateCompleted, req.WorldTick, cc.blueprint)
	}

	return zone, nil
}

// RegisterZoneParams defines the full parameters to register a functional zone in civic_registry.db.
type RegisterZoneParams struct {
	ZoneID       string
	ZoneType     string
	Name         string
	OwnerAgentID string
	AnchorX      int
	AnchorY      int
	AnchorZ      int
	Dimensions   Dimensions
	WorldTick    int64
}

// NormalizeZoneType maps blueprint types (e.g. "tavern", "granary", "market") to valid civic registry zone types.
func NormalizeZoneType(raw string) string {
	norm := strings.ToUpper(strings.TrimSpace(raw))
	switch norm {
	case "HOUSE", "RESIDENCE":
		return db.ZoneTypeResidential
	default:
		return norm
	}
}

// RegisterCompletedZone registers a structure's bounding box into civic_registry.db in the functional_zones table
// via RegisterFunctionalZone to unlock civic functionality (e.g., Tavern, Granary, Market).
func RegisterCompletedZone(ctx context.Context, civicDB *db.CivicRegistryDB, params RegisterZoneParams) (*db.FunctionalZone, error) {
	if civicDB == nil {
		return nil, ErrNilCivicDB
	}
	if strings.TrimSpace(params.ZoneID) == "" {
		return nil, ErrEmptyZoneID
	}
	zoneType := NormalizeZoneType(params.ZoneType)
	if zoneType == "" {
		return nil, ErrEmptyZoneType
	}
	if !db.IsValidZoneType(zoneType) {
		return nil, fmt.Errorf("invalid zone type: %q", zoneType)
	}

	dims := params.Dimensions
	if dims.Width <= 0 || dims.Height <= 0 || dims.Length <= 0 {
		return nil, ErrInvalidDimensions
	}

	zone := &db.FunctionalZone{
		ZoneID:       params.ZoneID,
		ZoneType:     zoneType,
		Name:         params.Name,
		MinX:         params.AnchorX,
		MinY:         params.AnchorY,
		MinZ:         params.AnchorZ,
		MaxX:         params.AnchorX + dims.Width - 1,
		MaxY:         params.AnchorY + dims.Height - 1,
		MaxZ:         params.AnchorZ + dims.Length - 1,
		OwnerAgentID: params.OwnerAgentID,
		CreatedTick:  params.WorldTick,
	}

	if err := civicDB.RegisterFunctionalZoneContext(ctx, zone); err != nil {
		return nil, fmt.Errorf("registering functional zone in civic registry: %w", err)
	}

	return zone, nil
}
