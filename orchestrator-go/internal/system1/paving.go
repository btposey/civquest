package system1

import (
	"math"
	"strings"
	"sync"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// BlockPosKey identifies a 3D discrete block coordinate.
type BlockPosKey struct {
	X int32
	Y int32
	Z int32
}

// PavingEntry represents a tracked voxel location in the bounded LRU cache.
type PavingEntry struct {
	Pos   BlockPosKey
	Steps int
	Paved bool
	prev  *PavingEntry
	next  *PavingEntry
}

// RoadPavingTracker tracks foot traffic over terrain blocks and emits PAVE_ROAD_PATH
// when repeated traversals cross a configurable threshold (default: 15).
// It maintains an in-memory frequency map bounded by an LRU cache to prevent memory leaks,
// enforces a radial exclusion zone around the spawn centroid/campfire to prevent concentric clearing bloat,
// and gates paving behind directional corridor movement.
const (
	DefaultPavingCapacity   = 1000
	DefaultPavingThreshold  = 15
	DefaultExclusionRadius  = 4.0
	DefaultRecentStepsLimit = 16
)

type RoadPavingTracker struct {
	mu               sync.Mutex
	capacity         int
	threshold        int
	exclusionRadius  float64
	homeOrigin       *civquestv1.Vec3
	outpostEntrances map[BlockPosKey]bool
	corridorGating   bool
	recentSteps      []BlockPosKey
	entries          map[BlockPosKey]*PavingEntry
	head             *PavingEntry // Most recently used
	tail             *PavingEntry // Least recently used
	lastStepped      *BlockPosKey
}

// NewRoadPavingTracker creates a RoadPavingTracker with specified capacity and step threshold.
func NewRoadPavingTracker(capacity, threshold int) *RoadPavingTracker {
	if capacity <= 0 {
		capacity = DefaultPavingCapacity
	}
	if threshold <= 0 {
		threshold = DefaultPavingThreshold
	}
	return &RoadPavingTracker{
		capacity:         capacity,
		threshold:        threshold,
		exclusionRadius:  DefaultExclusionRadius,
		corridorGating:   true,
		outpostEntrances: make(map[BlockPosKey]bool),
		recentSteps:      make([]BlockPosKey, 0, DefaultRecentStepsLimit),
		entries:          make(map[BlockPosKey]*PavingEntry, capacity),
	}
}

// isPaveable checks whether a block ID is eligible for conversion to dirt path.
// Harmonized strictly to grass_block and dirt matching IntentActuator.java:258.
func isPaveable(blockID string) bool {
	id := strings.ToLower(strings.TrimSpace(blockID))
	if idx := strings.Index(id, ":"); idx != -1 {
		id = id[idx+1:]
	}
	return id == "grass_block" || id == "dirt"
}

// isAlreadyPaved checks whether a block is already a path.
func isAlreadyPaved(blockID string) bool {
	id := strings.ToLower(strings.TrimSpace(blockID))
	if idx := strings.Index(id, ":"); idx != -1 {
		id = id[idx+1:]
	}
	return id == "dirt_path" || id == "grass_path"
}

// isAdjacent returns true if blocks a and b are directly adjacent horizontally or vertically (Chebyshev distance <= 1).
func isAdjacent(a, b BlockPosKey) bool {
	dx := a.X - b.X
	if dx < -1 || dx > 1 {
		return false
	}
	dz := a.Z - b.Z
	if dz < -1 || dz > 1 {
		return false
	}
	dy := a.Y - b.Y
	if dy < -1 || dy > 1 {
		return false
	}
	return !(dx == 0 && dy == 0 && dz == 0)
}

// hasPavedNeighborLocked checks if any adjacent block has already been paved in the tracker.
func (t *RoadPavingTracker) hasPavedNeighborLocked(key BlockPosKey) bool {
	for dx := int32(-1); dx <= 1; dx++ {
		for dz := int32(-1); dz <= 1; dz++ {
			if dx == 0 && dz == 0 {
				continue
			}
			for dy := int32(-1); dy <= 1; dy++ {
				neighborKey := BlockPosKey{X: key.X + dx, Y: key.Y + dy, Z: key.Z + dz}
				if neighbor, exists := t.entries[neighborKey]; exists && neighbor.Paved {
					return true
				}
			}
		}
	}
	return false
}

// hasSequentialLineOfTravelLocked checks if the recent movement history contains sequential
// adjacent steps ending at key that form a directional line of travel rather than omnidirectional milling.
func (t *RoadPavingTracker) hasSequentialLineOfTravelLocked(key BlockPosKey) bool {
	n := len(t.recentSteps)
	if n < 3 {
		return false
	}

	// Recent steps must end at the current key
	if t.recentSteps[n-1] != key {
		return false
	}

	s0 := t.recentSteps[n-3]
	s1 := t.recentSteps[n-2]
	s2 := t.recentSteps[n-1]

	// Each step must be adjacent to the next
	if !isAdjacent(s0, s1) || !isAdjacent(s1, s2) {
		return false
	}

	// Must not be immediately backtracking (e.g. oscillating between 2 blocks)
	if s2 == s0 {
		return false
	}

	// Horizontal Euclidean net displacement over the 3 steps
	dx := float64(s2.X - s0.X)
	dz := float64(s2.Z - s0.Z)
	dist := math.Hypot(dx, dz)

	// In a straight line: dist is 2.0. Diagonal: ~2.83. Slight jog: ~2.24.
	// Milling within a 2x2 box or oscillating gives dist <= 1.41.
	if dist >= 1.9 {
		return true
	}

	// Check 4-step window if available
	if n >= 4 {
		sPrev := t.recentSteps[n-4]
		if isAdjacent(sPrev, s0) && isAdjacent(s0, s1) && isAdjacent(s1, s2) {
			dx4 := float64(s2.X - sPrev.X)
			dz4 := float64(s2.Z - sPrev.Z)
			if math.Hypot(dx4, dz4) >= 2.5 {
				return true
			}
		}
	}

	return false
}

// isDirectionalCorridorLocked evaluates whether stepping on key represents an intentional corridor.
func (t *RoadPavingTracker) isDirectionalCorridorLocked(key BlockPosKey, navState civquestv1.NavigationState) bool {
	// 1. Actively navigating between distinct nodes/waypoints via Baritone pathing
	if navState == civquestv1.NavigationState_NAVIGATION_STATE_PATHING {
		return true
	}

	// 2. Directly adjacent to an already paved road block (extending an existing paved corridor)
	if t.hasPavedNeighborLocked(key) {
		return true
	}

	// 3. Sustained sequential adjacent steps along a forward line of travel
	if t.hasSequentialLineOfTravelLocked(key) {
		return true
	}

	return false
}

// isInsideExclusionZoneLocked checks if key lies within the radial exclusion zone around homeOrigin/campfire.
func (t *RoadPavingTracker) isInsideExclusionZoneLocked(key BlockPosKey) bool {
	if t.homeOrigin == nil || t.exclusionRadius <= 0 {
		return false
	}
	if t.outpostEntrances != nil && t.outpostEntrances[key] {
		return false // Explicit outpost entrance exemption
	}
	dx := float64(key.X) + 0.5 - t.homeOrigin.X
	dz := float64(key.Z) + 0.5 - t.homeOrigin.Z
	return math.Hypot(dx, dz) <= t.exclusionRadius
}

// RecordStep records a traversal over a specific block coordinate with UNSPECIFIED navigation state.
// Delegated to RecordStepWithNav for backward compatibility.
func (t *RoadPavingTracker) RecordStep(agentID string, pos *civquestv1.BlockPosProto, blockID string) *civquestv1.AgentIntent {
	return t.RecordStepWithNav(agentID, pos, blockID, civquestv1.NavigationState_NAVIGATION_STATE_UNSPECIFIED)
}

// RecordStepWithNav records a traversal over a specific block coordinate with navigation state.
// If the block is eligible grass/dirt, traversals reach the threshold, and passes corridor/exclusion gating,
// it emits an InteractBlockIntent with interaction_type = PAVE_ROAD_PATH.
func (t *RoadPavingTracker) RecordStepWithNav(agentID string, pos *civquestv1.BlockPosProto, blockID string, navState civquestv1.NavigationState) *civquestv1.AgentIntent {
	if pos == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	key := BlockPosKey{X: pos.X, Y: pos.Y, Z: pos.Z}

	// If already paved in world, register as paved only if already tracked, but never pollute cache with new entries
	if isAlreadyPaved(blockID) {
		if entry, exists := t.entries[key]; exists {
			entry.Paved = true
		}
		return nil
	}

	// Only paveable blocks (grass_block, dirt) accumulate steps
	if !isPaveable(blockID) {
		return nil
	}

	// Track recent unique stepped blocks for directional corridor detection
	if len(t.recentSteps) == 0 || t.recentSteps[len(t.recentSteps)-1] != key {
		t.recentSteps = append(t.recentSteps, key)
		if len(t.recentSteps) > DefaultRecentStepsLimit {
			t.recentSteps = t.recentSteps[len(t.recentSteps)-DefaultRecentStepsLimit:]
		}
	}

	entry, exists := t.entries[key]
	if !exists {
		entry = &PavingEntry{
			Pos:   key,
			Steps: 1,
			Paved: false,
		}
		t.addEntry(entry)
	} else {
		entry.Steps++
		t.moveToHead(entry)
	}

	if entry.Steps >= t.threshold && !entry.Paved {
		// Radial exclusion zone check around homeOrigin / campfire
		if t.isInsideExclusionZoneLocked(key) {
			return nil
		}

		// Directional corridor gating check
		if t.corridorGating && !t.isDirectionalCorridorLocked(key, navState) {
			return nil
		}

		entry.Paved = true
		return &civquestv1.AgentIntent{
			AgentId: agentID,
			Action: &civquestv1.AgentIntent_InteractBlock{
				InteractBlock: &civquestv1.InteractBlockIntent{
					Pos: &civquestv1.BlockPosProto{
						X: key.X,
						Y: key.Y,
						Z: key.Z,
					},
					InteractionType: civquestv1.InteractBlockIntent_PAVE_ROAD_PATH,
					BlockOrItemId:   "minecraft:dirt_path",
				},
			},
		}
	}

	return nil
}

// EvaluatePaving examines the agent's current position and below-foot terrain block from telemetry.
// If the agent stepped onto a new block and crosses the frequency threshold and corridor criteria,
// it emits a PAVE_ROAD_PATH intent.
func (t *RoadPavingTracker) EvaluatePaving(agentID string, frame *civquestv1.TelemetryFrame) *civquestv1.AgentIntent {
	if t == nil || frame == nil || frame.Status == nil || frame.Status.Position == nil {
		return nil
	}

	// Auto-detect campfire in interesting blocks if homeOrigin is not yet established
	t.mu.Lock()
	if t.homeOrigin == nil {
		for _, b := range frame.InterestingBlocks {
			if b != nil && b.Pos != nil && strings.Contains(strings.ToLower(b.BlockId), "campfire") {
				t.homeOrigin = &civquestv1.Vec3{
					X: float64(b.Pos.X) + 0.5,
					Y: float64(b.Pos.Y),
					Z: float64(b.Pos.Z) + 0.5,
				}
				break
			}
		}
	}
	t.mu.Unlock()

	pos := frame.Status.Position
	belowPos := civquestv1.BlockPosProto{
		X: int32(math.Floor(pos.X)),
		Y: int32(math.Floor(pos.Y - 0.2)),
		Z: int32(math.Floor(pos.Z)),
	}

	key := BlockPosKey{X: belowPos.X, Y: belowPos.Y, Z: belowPos.Z}
	t.mu.Lock()
	if t.lastStepped != nil && *t.lastStepped == key {
		t.mu.Unlock()
		return nil // Agent did not move to a new block
	}
	t.mu.Unlock()

	// Locate block ID from interesting_blocks if present
	blockID := ""
	for _, b := range frame.InterestingBlocks {
		if b != nil && b.Pos != nil && b.Pos.X == belowPos.X && b.Pos.Y == belowPos.Y && b.Pos.Z == belowPos.Z {
			blockID = b.BlockId
			break
		}
	}

	// Fallback: check if block is reported at feet level
	if blockID == "" {
		for _, b := range frame.InterestingBlocks {
			if b != nil && b.Pos != nil && b.Pos.X == belowPos.X && b.Pos.Y == belowPos.Y+1 && b.Pos.Z == belowPos.Z {
				if isPaveable(b.BlockId) || isAlreadyPaved(b.BlockId) {
					blockID = b.BlockId
					belowPos.Y = b.Pos.Y
					key = BlockPosKey{X: belowPos.X, Y: belowPos.Y, Z: belowPos.Z}
					break
				}
			}
		}
	}

	// If block ID is not found in telemetry frame, do NOT update lastStepped to avoid locking out future checks
	if blockID == "" {
		return nil
	}

	t.mu.Lock()
	t.lastStepped = &key
	t.mu.Unlock()

	navState := frame.Status.NavigationState
	return t.RecordStepWithNav(agentID, &belowPos, blockID, navState)
}

// SetHomeOrigin sets the spawn/campfire origin for the radial exclusion zone.
func (t *RoadPavingTracker) SetHomeOrigin(origin *civquestv1.Vec3) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if origin == nil {
		t.homeOrigin = nil
		return
	}
	t.homeOrigin = &civquestv1.Vec3{
		X: origin.X,
		Y: origin.Y,
		Z: origin.Z,
	}
}

// GetHomeOrigin returns a defensive copy of the current home origin.
func (t *RoadPavingTracker) GetHomeOrigin() *civquestv1.Vec3 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.homeOrigin == nil {
		return nil
	}
	return &civquestv1.Vec3{
		X: t.homeOrigin.X,
		Y: t.homeOrigin.Y,
		Z: t.homeOrigin.Z,
	}
}

// SetExclusionRadius sets the radial exclusion radius in blocks.
func (t *RoadPavingTracker) SetExclusionRadius(radius float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if radius < 0 {
		radius = 0
	}
	t.exclusionRadius = radius
}

// GetExclusionRadius returns the current exclusion radius.
func (t *RoadPavingTracker) GetExclusionRadius() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.exclusionRadius
}

// AddOutpostEntrance registers a block position as an explicit outpost entrance,
// exempting it from the radial exclusion zone.
func (t *RoadPavingTracker) AddOutpostEntrance(pos *civquestv1.BlockPosProto) {
	if pos == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.outpostEntrances == nil {
		t.outpostEntrances = make(map[BlockPosKey]bool)
	}
	t.outpostEntrances[BlockPosKey{X: pos.X, Y: pos.Y, Z: pos.Z}] = true
}

// IsOutpostEntrance checks whether a block position is registered as an outpost entrance.
func (t *RoadPavingTracker) IsOutpostEntrance(pos *civquestv1.BlockPosProto) bool {
	if pos == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.outpostEntrances == nil {
		return false
	}
	return t.outpostEntrances[BlockPosKey{X: pos.X, Y: pos.Y, Z: pos.Z}]
}

// SetThreshold updates the traversal threshold required before emitting a pave intent.
func (t *RoadPavingTracker) SetThreshold(threshold int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if threshold <= 0 {
		threshold = DefaultPavingThreshold
	}
	t.threshold = threshold
}

// GetThreshold returns the current traversal threshold.
func (t *RoadPavingTracker) GetThreshold() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.threshold
}

// SetCorridorGating enables or disables directional corridor gating.
func (t *RoadPavingTracker) SetCorridorGating(enabled bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.corridorGating = enabled
}

// IsCorridorGating returns whether directional corridor gating is enabled.
func (t *RoadPavingTracker) IsCorridorGating() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.corridorGating
}

// GetEntry returns inspection data for a block coordinate.
func (t *RoadPavingTracker) GetEntry(pos *civquestv1.BlockPosProto) (steps int, paved bool, found bool) {
	if pos == nil {
		return 0, false, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	key := BlockPosKey{X: pos.X, Y: pos.Y, Z: pos.Z}
	if entry, exists := t.entries[key]; exists {
		return entry.Steps, entry.Paved, true
	}
	return 0, false, false
}

// Count returns the number of currently tracked block positions in the LRU cache.
func (t *RoadPavingTracker) Count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.entries)
}

// Clear resets the tracker and cache.
func (t *RoadPavingTracker) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.entries = make(map[BlockPosKey]*PavingEntry, t.capacity)
	t.head = nil
	t.tail = nil
	t.lastStepped = nil
	t.recentSteps = nil
}

func (t *RoadPavingTracker) addEntry(entry *PavingEntry) {
	t.entries[entry.Pos] = entry
	entry.next = t.head
	entry.prev = nil
	if t.head != nil {
		t.head.prev = entry
	}
	t.head = entry
	if t.tail == nil {
		t.tail = entry
	}

	if len(t.entries) > t.capacity {
		t.removeTail()
	}
}

func (t *RoadPavingTracker) moveToHead(entry *PavingEntry) {
	if t.head == entry {
		return
	}
	if entry.prev != nil {
		entry.prev.next = entry.next
	}
	if entry.next != nil {
		entry.next.prev = entry.prev
	}
	if t.tail == entry {
		t.tail = entry.prev
	}

	entry.prev = nil
	entry.next = t.head
	if t.head != nil {
		t.head.prev = entry
	}
	t.head = entry
}

func (t *RoadPavingTracker) removeTail() {
	if t.tail == nil {
		return
	}
	oldTail := t.tail
	delete(t.entries, oldTail.Pos)
	if oldTail.prev != nil {
		oldTail.prev.next = nil
		t.tail = oldTail.prev
	} else {
		t.head = nil
		t.tail = nil
	}
	oldTail.prev = nil
	oldTail.next = nil
}
