package agent

import (
	"sort"
	"sync"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// SlotKey identifies a typed coalescing slot.
type SlotKey string

const (
	SlotNavigate      SlotKey = "navigate"
	SlotAttack        SlotKey = "attack"
	SlotUseItem       SlotKey = "use_item"
	SlotCraft         SlotKey = "craft"
	SlotInteractBlock SlotKey = "interact_block"
)

// IsPlaceBlockIntent returns true if the intent represents block placement
// (either BuildBlockIntent or InteractBlockIntent with PLACE_BLOCK interaction type).
func IsPlaceBlockIntent(intent *civquestv1.AgentIntent) bool {
	if intent == nil {
		return false
	}
	if intent.GetBuildBlock() != nil {
		return true
	}
	if ib := intent.GetInteractBlock(); ib != nil {
		if ib.InteractionType == civquestv1.InteractBlockIntent_PLACE_BLOCK {
			return true
		}
	}
	return false
}

// SlotKeyFor returns the slot key for an intent and true if it belongs in a coalesced slot.
// If the intent is a PlaceBlockIntent, it returns ("", false) to indicate it must bypass the slot map.
func SlotKeyFor(intent *civquestv1.AgentIntent) (SlotKey, bool) {
	if intent == nil {
		return "", false
	}
	if IsPlaceBlockIntent(intent) {
		return "", false
	}
	switch {
	case intent.GetNavigate() != nil:
		return SlotNavigate, true
	case intent.GetAttack() != nil:
		return SlotAttack, true
	case intent.GetUseItem() != nil:
		return SlotUseItem, true
	case intent.GetCraftItem() != nil:
		return SlotCraft, true
	case intent.GetInteractBlock() != nil:
		return SlotInteractBlock, true
	default:
		return "", false
	}
}

// slotEntry stores a queued intent, its priority flag, and an insertion order ticket.
type slotEntry struct {
	intent   *civquestv1.AgentIntent
	priority bool
	order    uint64
}

// SlotQueue enforces typed coalescing: one pending intent per slot key.
// High-frequency duplicate intents overwrite older pending intents in the same slot.
// PlaceBlockIntents bypass the slot map and pass through an ordered FIFO pipeline.
type SlotQueue struct {
	mu           sync.Mutex
	slots        map[SlotKey]*slotEntry
	placeBlocks  []*civquestv1.AgentIntent
	passthrough  []*slotEntry
	orderCounter uint64
	notifyCh     chan struct{}
}

// NewSlotQueue creates an initialized, thread-safe SlotQueue.
func NewSlotQueue() *SlotQueue {
	return &SlotQueue{
		slots:    make(map[SlotKey]*slotEntry),
		notifyCh: make(chan struct{}, 1),
	}
}

// Put enqueues an intent into the SlotQueue.
// If it is a PlaceBlockIntent, it bypasses the slot map and is appended to the ordered PlaceBlock pipeline.
// If it matches a typed slot key, it overwrites any pending intent in that slot.
// Otherwise, it is preserved in arrival order in the passthrough queue.
func (sq *SlotQueue) Put(priority bool, intent *civquestv1.AgentIntent) {
	if intent == nil {
		return
	}

	sq.mu.Lock()
	defer sq.mu.Unlock()

	sq.orderCounter++
	curOrder := sq.orderCounter

	if IsPlaceBlockIntent(intent) {
		sq.placeBlocks = append(sq.placeBlocks, intent)
		sq.notify()
		return
	}

	key, ok := SlotKeyFor(intent)
	if !ok {
		sq.passthrough = append(sq.passthrough, &slotEntry{
			intent:   intent,
			priority: priority,
			order:    curOrder,
		})
		sq.notify()
		return
	}

	existing, found := sq.slots[key]
	effectivePriority := priority
	if found && existing.priority {
		effectivePriority = true
	}

	sq.slots[key] = &slotEntry{
		intent:   intent,
		priority: effectivePriority,
		order:    curOrder,
	}

	sq.notify()
}

// Drain drains all pending intents from the SlotQueue and resets pending state.
// Intents are returned with priority-first dispatch ordering:
// all priority-slot intents drain before normal-slot intents, preserving arrival order within each tier.
// PlaceBlockIntents are never coalesced and drain in arrival order.
func (sq *SlotQueue) Drain() []*civquestv1.AgentIntent {
	sq.mu.Lock()
	defer sq.mu.Unlock()

	if len(sq.slots) == 0 && len(sq.placeBlocks) == 0 && len(sq.passthrough) == 0 {
		return nil
	}

	var priorityEntries []*slotEntry
	var normalEntries []*slotEntry

	for _, entry := range sq.slots {
		if entry.priority {
			priorityEntries = append(priorityEntries, entry)
		} else {
			normalEntries = append(normalEntries, entry)
		}
	}

	for _, entry := range sq.passthrough {
		if entry.priority {
			priorityEntries = append(priorityEntries, entry)
		} else {
			normalEntries = append(normalEntries, entry)
		}
	}

	sort.Slice(priorityEntries, func(i, j int) bool {
		return priorityEntries[i].order < priorityEntries[j].order
	})
	sort.Slice(normalEntries, func(i, j int) bool {
		return normalEntries[i].order < normalEntries[j].order
	})

	result := make([]*civquestv1.AgentIntent, 0, len(priorityEntries)+len(normalEntries)+len(sq.placeBlocks))
	for _, entry := range priorityEntries {
		result = append(result, entry.intent)
	}
	for _, entry := range normalEntries {
		result = append(result, entry.intent)
	}
	for _, pb := range sq.placeBlocks {
		result = append(result, pb)
	}

	sq.slots = make(map[SlotKey]*slotEntry)
	sq.placeBlocks = nil
	sq.passthrough = nil

	select {
	case <-sq.notifyCh:
	default:
	}

	return result
}

// DrainSlots drains only the coalesced slots and passthrough intents (priority first),
// leaving the ordered PlaceBlock pipeline untouched for rate-limited dispatch.
func (sq *SlotQueue) DrainSlots() []*civquestv1.AgentIntent {
	sq.mu.Lock()
	defer sq.mu.Unlock()

	if len(sq.slots) == 0 && len(sq.passthrough) == 0 {
		return nil
	}

	var priorityEntries []*slotEntry
	var normalEntries []*slotEntry

	for _, entry := range sq.slots {
		if entry.priority {
			priorityEntries = append(priorityEntries, entry)
		} else {
			normalEntries = append(normalEntries, entry)
		}
	}

	for _, entry := range sq.passthrough {
		if entry.priority {
			priorityEntries = append(priorityEntries, entry)
		} else {
			normalEntries = append(normalEntries, entry)
		}
	}

	sort.Slice(priorityEntries, func(i, j int) bool {
		return priorityEntries[i].order < priorityEntries[j].order
	})
	sort.Slice(normalEntries, func(i, j int) bool {
		return normalEntries[i].order < normalEntries[j].order
	})

	result := make([]*civquestv1.AgentIntent, 0, len(priorityEntries)+len(normalEntries))
	for _, entry := range priorityEntries {
		result = append(result, entry.intent)
	}
	for _, entry := range normalEntries {
		result = append(result, entry.intent)
	}

	sq.slots = make(map[SlotKey]*slotEntry)
	sq.passthrough = nil

	if len(sq.placeBlocks) == 0 {
		select {
		case <-sq.notifyCh:
		default:
		}
	}

	return result
}

// PopPlaceBlock removes and returns the head of the PlaceBlock pipeline, or nil if empty.
func (sq *SlotQueue) PopPlaceBlock() *civquestv1.AgentIntent {
	sq.mu.Lock()
	defer sq.mu.Unlock()

	if len(sq.placeBlocks) == 0 {
		return nil
	}
	first := sq.placeBlocks[0]
	sq.placeBlocks = sq.placeBlocks[1:]

	if len(sq.placeBlocks) == 0 && len(sq.slots) == 0 && len(sq.passthrough) == 0 {
		select {
		case <-sq.notifyCh:
		default:
		}
	}

	return first
}

// SlotCount returns the number of currently occupied coalescing slots.
func (sq *SlotQueue) SlotCount() int {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	return len(sq.slots)
}

// PlaceBlockCount returns the number of pending PlaceBlockIntents in the ordered pipeline.
func (sq *SlotQueue) PlaceBlockCount() int {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	return len(sq.placeBlocks)
}

// Len returns the total number of pending intents across slots, place blocks, and passthrough.
func (sq *SlotQueue) Len() int {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	return len(sq.slots) + len(sq.placeBlocks) + len(sq.passthrough)
}

// PendingCount is an alias for Len.
func (sq *SlotQueue) PendingCount() int {
	return sq.Len()
}

// HasPending reports whether any intents are waiting to be drained.
func (sq *SlotQueue) HasPending() bool {
	return sq.Len() > 0
}

// Ready returns a notification channel that receives a signal when a new intent is queued.
func (sq *SlotQueue) Ready() <-chan struct{} {
	return sq.notifyCh
}

func (sq *SlotQueue) notify() {
	select {
	case sq.notifyCh <- struct{}{}:
	default:
	}
}
