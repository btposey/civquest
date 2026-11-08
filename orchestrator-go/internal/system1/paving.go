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
// when repeated traversals cross a configurable threshold (default: 3).
// It maintains an in-memory frequency map bounded by an LRU cache to prevent memory leaks.
type RoadPavingTracker struct {
	mu          sync.Mutex
	capacity    int
	threshold   int
	entries     map[BlockPosKey]*PavingEntry
	head        *PavingEntry // Most recently used
	tail        *PavingEntry // Least recently used
	lastStepped *BlockPosKey
}

// NewRoadPavingTracker creates a RoadPavingTracker with specified capacity and step threshold.
func NewRoadPavingTracker(capacity, threshold int) *RoadPavingTracker {
	if capacity <= 0 {
		capacity = 1000
	}
	if threshold <= 0 {
		threshold = 3
	}
	return &RoadPavingTracker{
		capacity:  capacity,
		threshold: threshold,
		entries:   make(map[BlockPosKey]*PavingEntry, capacity),
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

// RecordStep records a traversal over a specific block coordinate.
// If the block is eligible grass/dirt and traversals reach the threshold,
// it emits an InteractBlockIntent with interaction_type = PAVE_ROAD_PATH.
// If the block is already paved (dirt_path), or not paveable, no intent is emitted.
// Cache pollution mitigation: if already paved, return nil immediately without adding new LRU entries.
func (t *RoadPavingTracker) RecordStep(agentID string, pos civquestv1.BlockPosProto, blockID string) *civquestv1.AgentIntent {
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
// If the agent stepped onto a new block and crosses the frequency threshold, it emits a PAVE_ROAD_PATH intent.
func (t *RoadPavingTracker) EvaluatePaving(agentID string, frame *civquestv1.TelemetryFrame) *civquestv1.AgentIntent {
	if t == nil || frame == nil || frame.Status == nil || frame.Status.Position == nil {
		return nil
	}

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

	return t.RecordStep(agentID, belowPos, blockID)
}

// GetEntry returns inspection data for a block coordinate.
func (t *RoadPavingTracker) GetEntry(pos civquestv1.BlockPosProto) (steps int, paved bool, found bool) {
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
