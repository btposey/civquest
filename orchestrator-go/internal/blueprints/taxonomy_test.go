package blueprints

import (
	"testing"
)

func TestTaxonomy_IsTaxonomyTagAndNormalize(t *testing.T) {
	if !IsTaxonomyTag("#minecraft:planks") {
		t.Errorf("expected #minecraft:planks to be taxonomy tag")
	}
	if !IsTaxonomyTag("#planks") {
		t.Errorf("expected #planks to be taxonomy tag")
	}
	if IsTaxonomyTag("minecraft:oak_planks") {
		t.Errorf("minecraft:oak_planks should not be taxonomy tag")
	}

	norm := NormalizeTaxonomyTag("#planks")
	if norm != "#minecraft:planks" {
		t.Errorf("expected #minecraft:planks, got %q", norm)
	}

	normC := NormalizeTaxonomyTag("#c:stones")
	if normC != "#c:stones" {
		t.Errorf("expected #c:stones, got %q", normC)
	}
}

func TestTaxonomy_MatchesTaxonomy(t *testing.T) {
	tests := []struct {
		tag      string
		concrete string
		matches  bool
	}{
		{TagPlanks, "minecraft:oak_planks", true},
		{TagPlanks, "minecraft:spruce_planks", true},
		{TagPlanks, "minecraft:cherry_planks", true},
		{TagPlanks, "minecraft:oak_log", false},
		{TagLogs, "minecraft:oak_log", true},
		{TagLogs, "minecraft:spruce_log", true},
		{TagLogs, "minecraft:crimson_stem", true},
		{TagLogs, "minecraft:oak_planks", false},
		{TagStairs, "minecraft:oak_stairs[facing=north,half=bottom]", true},
		{TagStairs, "minecraft:spruce_stairs", true},
		{TagStairs, "minecraft:cobblestone_stairs", true},
		{TagStairs, "minecraft:stone_stairs", true},
		{TagStairs, "minecraft:oak_slab", false},
		{TagStones, "minecraft:stone", true},
		{TagStones, "minecraft:cobblestone", true},
		{TagStones, "minecraft:deepslate", true},
		{TagStones, "minecraft:granite", true},
		{TagStones, "minecraft:oak_planks", false},
		{TagDoors, "minecraft:oak_door[facing=south,half=lower]", true},
		{TagDoors, "minecraft:iron_door", true},
	}

	for _, tt := range tests {
		got := MatchesTaxonomy(tt.tag, tt.concrete)
		if got != tt.matches {
			t.Errorf("MatchesTaxonomy(%q, %q) = %v, want %v", tt.tag, tt.concrete, got, tt.matches)
		}
	}
}

func TestTaxonomy_ResolveInSituBlock_PreservesDirection(t *testing.T) {
	// Settlement has 64 spruce planks and 128 spruce stairs in stock (spruce taiga biome)
	inv := map[string]int{
		"minecraft:spruce_planks": 64,
		"minecraft:spruce_stairs": 128,
		"minecraft:spruce_log":    32,
	}

	// 1. Generic stairs with directional properties -> resolves to spruce_stairs preserving direction
	targetStairs := "#minecraft:stairs[facing=south,half=top,shape=straight]"
	resolved := ResolveInSituBlock(targetStairs, inv, "minecraft:cobblestone_stairs")
	expected := "minecraft:spruce_stairs[facing=south,half=top,shape=straight]"
	if resolved != expected {
		t.Errorf("ResolveInSituBlock(%q) = %q, want %q", targetStairs, resolved, expected)
	}

	// 2. Generic log with axis -> resolves to spruce_log preserving axis
	targetLog := "#minecraft:logs[axis=x]"
	resolvedLog := ResolveInSituBlock(targetLog, inv, "minecraft:oak_log")
	expectedLog := "minecraft:spruce_log[axis=x]"
	if resolvedLog != expectedLog {
		t.Errorf("ResolveInSituBlock(%q) = %q, want %q", targetLog, resolvedLog, expectedLog)
	}

	// 3. Selection of highest stock material
	invMulti := map[string]int{
		"minecraft:birch_planks":  10,
		"minecraft:spruce_planks": 50,
	}
	resolvedPlanks := ResolveInSituBlock("#minecraft:planks", invMulti, "minecraft:oak_planks")
	if resolvedPlanks != "minecraft:spruce_planks" {
		t.Errorf("expected highest-stock spruce_planks, got %q", resolvedPlanks)
	}
}

func TestTaxonomy_EvaluateTaxonomyBOM(t *testing.T) {
	// Blueprint requires 100 generic planks, 40 generic stairs, and 2 concrete crafting tables
	bom := map[string]int{
		"#minecraft:planks":        100,
		"#minecraft:stairs":        40,
		"minecraft:crafting_table": 2,
	}

	// Case 1: In-situ stock across multiple wood species satisfies the generic plank requirement
	invSufficient := map[string]int{
		"minecraft:spruce_planks":  60,
		"minecraft:birch_planks":   50, // 60 + 50 = 110 >= 100
		"minecraft:spruce_stairs":  45, // 45 >= 40
		"minecraft:crafting_table": 2,  // 2 >= 2
	}

	satisfied, surplus, missing := EvaluateTaxonomyBOM(bom, invSufficient)
	if !satisfied {
		t.Errorf("expected BOM to be satisfied with mixed in-situ resources, missing: %+v", missing)
	}
	if surplus["#minecraft:planks"] != 10 {
		t.Errorf("expected plank surplus of 10, got %d", surplus["#minecraft:planks"])
	}

	// Case 2: Insufficient stock emits missing count
	invDeficient := map[string]int{
		"minecraft:spruce_planks":  30,
		"minecraft:crafting_table": 1,
	}
	sat2, _, miss2 := EvaluateTaxonomyBOM(bom, invDeficient)
	if sat2 {
		t.Errorf("expected BOM to fail with deficient stock")
	}
	if miss2["#minecraft:planks"] != 70 {
		t.Errorf("expected missing 70 planks, got %d", miss2["#minecraft:planks"])
	}
	if miss2["#minecraft:stairs"] != 40 {
		t.Errorf("expected missing 40 stairs, got %d", miss2["#minecraft:stairs"])
	}
	if miss2["minecraft:crafting_table"] != 1 {
		t.Errorf("expected missing 1 crafting table, got %d", miss2["minecraft:crafting_table"])
	}
}
