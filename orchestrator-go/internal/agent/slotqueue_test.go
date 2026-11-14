package agent_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/agent"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// TestSlotQueue_SlotOverwrite verifies that a second intent of the same slot type
// overwrites the pending one before it is dispatched.
func TestSlotQueue_SlotOverwrite(t *testing.T) {
	sq := agent.NewSlotQueue()

	// 1. AttackIntent overwrite
	a1 := &civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_Attack{
			Attack: &civquestv1.AttackIntent{TargetEntityId: 10, RaiseShield: false},
		},
	}
	a2 := &civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_Attack{
			Attack: &civquestv1.AttackIntent{TargetEntityId: 20, RaiseShield: true},
		},
	}

	sq.Put(false, a1)
	if sq.SlotCount() != 1 {
		t.Fatalf("expected 1 slot, got %d", sq.SlotCount())
	}

	sq.Put(false, a2)
	if sq.SlotCount() != 1 {
		t.Fatalf("expected 1 slot after overwrite, got %d", sq.SlotCount())
	}

	drained := sq.Drain()
	if len(drained) != 1 {
		t.Fatalf("expected 1 drained intent, got %d", len(drained))
	}
	if drained[0].GetAttack().TargetEntityId != 20 {
		t.Errorf("expected target 20, got %d", drained[0].GetAttack().TargetEntityId)
	}
	if !drained[0].GetAttack().RaiseShield {
		t.Errorf("expected RaiseShield=true on overwritten intent")
	}

	// 2. NavigateIntent overwrite
	n1 := &civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_Navigate{
			Navigate: &civquestv1.NavigateIntent{Target: &civquestv1.Vec3{X: 1, Y: 1, Z: 1}},
		},
	}
	n2 := &civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_Navigate{
			Navigate: &civquestv1.NavigateIntent{Target: &civquestv1.Vec3{X: 2, Y: 2, Z: 2}},
		},
	}

	sq.Put(false, n1)
	sq.Put(false, n2)

	drainedNav := sq.Drain()
	if len(drainedNav) != 1 {
		t.Fatalf("expected 1 drained intent, got %d", len(drainedNav))
	}
	if drainedNav[0].GetNavigate().Target.X != 2 {
		t.Errorf("expected target X=2, got %f", drainedNav[0].GetNavigate().Target.X)
	}

	// 3. Verify all 5 slot keys coalesce independently
	sq.Put(false, &civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_Navigate{Navigate: &civquestv1.NavigateIntent{Sprint: false}},
	})
	sq.Put(false, &civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_Attack{Attack: &civquestv1.AttackIntent{TargetEntityId: 1}},
	})
	sq.Put(false, &civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_UseItem{UseItem: &civquestv1.UseItemIntent{InventorySlot: 0}},
	})
	sq.Put(false, &civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_CraftItem{CraftItem: &civquestv1.CraftItemIntent{RecipeId: "minecraft:torch"}},
	})
	sq.Put(false, &civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_InteractBlock{InteractBlock: &civquestv1.InteractBlockIntent{InteractionType: civquestv1.InteractBlockIntent_HARVEST_BREAK}},
	})

	if sq.SlotCount() != 5 {
		t.Fatalf("expected 5 distinct slots occupied, got %d", sq.SlotCount())
	}

	// Overwrite all 5
	sq.Put(false, &civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_Navigate{Navigate: &civquestv1.NavigateIntent{Sprint: true}},
	})
	sq.Put(false, &civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_Attack{Attack: &civquestv1.AttackIntent{TargetEntityId: 2}},
	})
	sq.Put(false, &civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_UseItem{UseItem: &civquestv1.UseItemIntent{InventorySlot: 1}},
	})
	sq.Put(false, &civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_CraftItem{CraftItem: &civquestv1.CraftItemIntent{RecipeId: "minecraft:campfire"}},
	})
	sq.Put(false, &civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_InteractBlock{InteractBlock: &civquestv1.InteractBlockIntent{InteractionType: civquestv1.InteractBlockIntent_SLEEP_IN_BED}},
	})

	if sq.SlotCount() != 5 {
		t.Fatalf("expected 5 slots after overwrite, got %d", sq.SlotCount())
	}

	drainedAll := sq.Drain()
	if len(drainedAll) != 5 {
		t.Fatalf("expected 5 drained intents, got %d", len(drainedAll))
	}
	if drainedAll[0].GetNavigate().Sprint != true &&
		drainedAll[1].GetNavigate() == nil { // find the navigate intent
		var navFound bool
		for _, in := range drainedAll {
			if in.GetNavigate() != nil && in.GetNavigate().Sprint {
				navFound = true
			}
		}
		if !navFound {
			t.Errorf("expected overwritten navigate intent with Sprint=true")
		}
	}
}

// TestSlotQueue_PriorityOrdering verifies that priority-slot intents drain
// before normal-slot intents.
func TestSlotQueue_PriorityOrdering(t *testing.T) {
	sq := agent.NewSlotQueue()

	// Enqueue normal navigate first
	normalNav := &civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_Navigate{
			Navigate: &civquestv1.NavigateIntent{Target: &civquestv1.Vec3{X: 10, Y: 64, Z: 10}},
		},
	}
	sq.Put(false, normalNav)

	// Enqueue priority attack reflex second
	priorityAttack := &civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_Attack{
			Attack: &civquestv1.AttackIntent{TargetEntityId: 999, RaiseShield: true},
		},
	}
	sq.Put(true, priorityAttack)

	// Drain should return priority attack first, then normal navigate
	drained := sq.Drain()
	if len(drained) != 2 {
		t.Fatalf("expected 2 drained intents, got %d", len(drained))
	}
	if drained[0].GetAttack() == nil {
		t.Errorf("expected first intent to be priority Attack, got %+v", drained[0].Action)
	}
	if drained[0].GetAttack().TargetEntityId != 999 {
		t.Errorf("expected target 999, got %d", drained[0].GetAttack().TargetEntityId)
	}
	if drained[1].GetNavigate() == nil {
		t.Errorf("expected second intent to be normal Navigate, got %+v", drained[1].Action)
	}

	// Test multiple priority and normal intents
	sq.Put(false, &civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_UseItem{UseItem: &civquestv1.UseItemIntent{InventorySlot: 0}},
	})
	sq.Put(true, &civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_Attack{Attack: &civquestv1.AttackIntent{TargetEntityId: 50}},
	})
	sq.Put(false, &civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_Navigate{Navigate: &civquestv1.NavigateIntent{SpeedMultiplier: 1.0}},
	})
	sq.Put(true, &civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_CraftItem{CraftItem: &civquestv1.CraftItemIntent{RecipeId: "minecraft:shield"}},
	})

	drainedMulti := sq.Drain()
	if len(drainedMulti) != 4 {
		t.Fatalf("expected 4 drained intents, got %d", len(drainedMulti))
	}

	// First 2 must be the priority intents (Attack, Craft) in arrival order
	if drainedMulti[0].GetAttack() == nil {
		t.Errorf("expected slot 0 to be Attack, got %+v", drainedMulti[0].Action)
	}
	if drainedMulti[1].GetCraftItem() == nil {
		t.Errorf("expected slot 1 to be CraftItem, got %+v", drainedMulti[1].Action)
	}
	// Next 2 must be the normal intents (UseItem, Navigate) in arrival order
	if drainedMulti[2].GetUseItem() == nil {
		t.Errorf("expected slot 2 to be UseItem, got %+v", drainedMulti[2].Action)
	}
	if drainedMulti[3].GetNavigate() == nil {
		t.Errorf("expected slot 3 to be Navigate, got %+v", drainedMulti[3].Action)
	}
}

// TestSlotQueue_PlaceBlock_Passthrough verifies that PlaceBlockIntents bypass the
// slot queue, are never coalesced, and pass through in arrival order.
func TestSlotQueue_PlaceBlock_Passthrough(t *testing.T) {
	sq := agent.NewSlotQueue()

	// 1. Both InteractBlock with PLACE_BLOCK and BuildBlock are PlaceBlockIntents
	pb1 := &civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_InteractBlock{
			InteractBlock: &civquestv1.InteractBlockIntent{
				Pos:             &civquestv1.BlockPosProto{X: 10, Y: 64, Z: 10},
				InteractionType: civquestv1.InteractBlockIntent_PLACE_BLOCK,
				BlockOrItemId:   "minecraft:oak_planks",
			},
		},
	}
	pb2 := &civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_BuildBlock{
			BuildBlock: &civquestv1.BuildBlockIntent{
				TargetVoxel:      &civquestv1.BlockPosProto{X: 10, Y: 65, Z: 10},
				BlockStateString: "minecraft:oak_log",
			},
		},
	}
	pb3 := &civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_InteractBlock{
			InteractBlock: &civquestv1.InteractBlockIntent{
				Pos:             &civquestv1.BlockPosProto{X: 10, Y: 66, Z: 10},
				InteractionType: civquestv1.InteractBlockIntent_PLACE_BLOCK,
				BlockOrItemId:   "minecraft:torch",
			},
		},
	}

	sq.Put(false, pb1)
	sq.Put(false, pb2)
	sq.Put(false, pb3)

	// SlotCount must be 0 because PlaceBlocks bypass the slot map!
	if sq.SlotCount() != 0 {
		t.Errorf("expected SlotCount=0 for PlaceBlocks, got %d", sq.SlotCount())
	}
	if sq.PlaceBlockCount() != 3 {
		t.Fatalf("expected PlaceBlockCount=3, got %d", sq.PlaceBlockCount())
	}
	if sq.Len() != 3 {
		t.Fatalf("expected Len=3, got %d", sq.Len())
	}

	// Drain must return all 3 in exact arrival order
	drained := sq.Drain()
	if len(drained) != 3 {
		t.Fatalf("expected 3 drained intents, got %d", len(drained))
	}
	if drained[0] != pb1 || drained[1] != pb2 || drained[2] != pb3 {
		t.Errorf("expected PlaceBlocks in arrival order pb1, pb2, pb3")
	}

	// 2. Test PopPlaceBlock individual popping
	sq.Put(false, pb1)
	sq.Put(false, pb2)

	popped1 := sq.PopPlaceBlock()
	if popped1 != pb1 {
		t.Errorf("expected popped1 to be pb1")
	}
	if sq.PlaceBlockCount() != 1 {
		t.Errorf("expected 1 remaining place block, got %d", sq.PlaceBlockCount())
	}

	popped2 := sq.PopPlaceBlock()
	if popped2 != pb2 {
		t.Errorf("expected popped2 to be pb2")
	}
	if sq.PopPlaceBlock() != nil {
		t.Errorf("expected nil after popping empty pipeline")
	}
}

// TestSlotQueue_CombatFlood verifies that under a simulated 50-intent combat flood,
// at most 2 intents (1 attack + 1 navigate) are pending in the slot queue at any time.
func TestSlotQueue_CombatFlood(t *testing.T) {
	sq := agent.NewSlotQueue()

	for i := 1; i <= 50; i++ {
		if i%2 == 1 {
			sq.Put(true, &civquestv1.AgentIntent{
				Action: &civquestv1.AgentIntent_Attack{
					Attack: &civquestv1.AttackIntent{
						TargetEntityId: int32(100 + i),
						RaiseShield:    true,
					},
				},
			})
		} else {
			sq.Put(false, &civquestv1.AgentIntent{
				Action: &civquestv1.AgentIntent_Navigate{
					Navigate: &civquestv1.NavigateIntent{
						Target: &civquestv1.Vec3{X: float64(i), Y: 64, Z: float64(i)},
					},
				},
			})
		}

		// After the first 2 intents, pending count must NEVER exceed 2!
		pending := sq.PendingCount()
		if i == 1 {
			if pending != 1 {
				t.Fatalf("iteration %d: expected pending 1, got %d", i, pending)
			}
		} else {
			if pending > 2 {
				t.Fatalf("iteration %d: combat flood invariant violated: pending %d > 2", i, pending)
			}
			if sq.SlotCount() > 2 {
				t.Fatalf("iteration %d: slot count %d > 2", i, sq.SlotCount())
			}
		}
	}

	if sq.PendingCount() != 2 {
		t.Fatalf("expected exactly 2 pending intents after 50-intent flood, got %d", sq.PendingCount())
	}

	drained := sq.Drain()
	if len(drained) != 2 {
		t.Fatalf("expected 2 drained intents, got %d", len(drained))
	}

	// Priority attack should be first
	if drained[0].GetAttack() == nil || drained[0].GetAttack().TargetEntityId != 149 {
		t.Errorf("expected latest attack intent (target 149), got %+v", drained[0].Action)
	}
	// Navigate should be second
	if drained[1].GetNavigate() == nil || drained[1].GetNavigate().Target.X != 50 {
		t.Errorf("expected latest navigate intent (X=50), got %+v", drained[1].Action)
	}
}

// TestSlotQueue_ConcurrentSafety runs high-concurrency Put and Drain loops under race detection.
func TestSlotQueue_ConcurrentSafety(t *testing.T) {
	sq := agent.NewSlotQueue()

	var wg sync.WaitGroup
	const goroutines = 10
	const iterations = 500

	// Producers: Attack & Navigate flood
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				priority := (i % 3) == 0
				if i%2 == 0 {
					sq.Put(priority, &civquestv1.AgentIntent{
						Action: &civquestv1.AgentIntent_Attack{
							Attack: &civquestv1.AttackIntent{TargetEntityId: int32(id*1000 + i)},
						},
					})
				} else {
					sq.Put(priority, &civquestv1.AgentIntent{
						Action: &civquestv1.AgentIntent_Navigate{
							Navigate: &civquestv1.NavigateIntent{
								Target: &civquestv1.Vec3{X: float64(id), Y: 64, Z: float64(i)},
							},
						},
					})
				}
			}
		}(g)
	}

	// Producers: PlaceBlock passthrough
	for g := 0; g < 3; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations/2; i++ {
				sq.Put(false, &civquestv1.AgentIntent{
					Action: &civquestv1.AgentIntent_InteractBlock{
						InteractBlock: &civquestv1.InteractBlockIntent{
							Pos:             &civquestv1.BlockPosProto{X: int32(id), Y: int32(i), Z: 0},
							InteractionType: civquestv1.InteractBlockIntent_PLACE_BLOCK,
							BlockOrItemId:   fmt.Sprintf("block_%d_%d", id, i),
						},
					},
				})
			}
		}(g)
	}

	// Consumers: Concurrent Drain and Len calls
	stopConsumers := make(chan struct{})
	var consumerWg sync.WaitGroup
	for c := 0; c < 4; c++ {
		consumerWg.Add(1)
		go func() {
			defer consumerWg.Done()
			for {
				select {
				case <-stopConsumers:
					return
				default:
					_ = sq.Len()
					_ = sq.SlotCount()
					_ = sq.PlaceBlockCount()
					_ = sq.DrainSlots()
					_ = sq.PopPlaceBlock()
					_ = sq.Drain()
				}
			}
		}()
	}

	wg.Wait()
	close(stopConsumers)
	consumerWg.Wait()

	// Final drain should leave queue completely empty
	final := sq.Drain()
	_ = final
	if sq.Len() != 0 {
		t.Fatalf("expected empty queue after completion, got %d", sq.Len())
	}
}
