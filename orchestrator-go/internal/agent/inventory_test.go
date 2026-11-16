package agent

import (
	"testing"
)

func TestCalculateArmorRating(t *testing.T) {
	// Test full iron armor in standard armor slots 36-39
	items := []CachedItem{
		{Slot: 36, ItemID: "minecraft:iron_boots", Count: 1},      // 2
		{Slot: 37, ItemID: "minecraft:iron_leggings", Count: 1},   // 5
		{Slot: 38, ItemID: "minecraft:iron_chestplate", Count: 1}, // 6
		{Slot: 39, ItemID: "minecraft:iron_helmet", Count: 1},     // 2
		{Slot: 0, ItemID: "minecraft:iron_sword", Count: 1},
	}

	rating := CalculateArmorRating(items)
	if rating != 15 {
		t.Errorf("expected iron armor rating 15, got %d", rating)
	}

	// Test diamond chestplate only in slot 38
	diamondChest := []CachedItem{
		{Slot: 38, ItemID: "minecraft:diamond_chestplate", Count: 1}, // 8
	}
	if r := CalculateArmorRating(diamondChest); r != 8 {
		t.Errorf("expected diamond chestplate rating 8, got %d", r)
	}

	// Test fallback when slot numbers are 0 (carried items)
	carried := []CachedItem{
		{Slot: 0, ItemID: "minecraft:diamond_chestplate", Count: 1}, // 8
		{Slot: 0, ItemID: "minecraft:iron_helmet", Count: 1},        // 2
	}
	if r := CalculateArmorRating(carried); r != 10 {
		t.Errorf("expected carried armor fallback rating 10, got %d", r)
	}

	// Empty inventory -> 0
	if r := CalculateArmorRating(nil); r != 0 {
		t.Errorf("expected 0 for nil inventory, got %d", r)
	}
}
