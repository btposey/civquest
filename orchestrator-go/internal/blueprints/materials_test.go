package blueprints

import "testing"

func TestWoodSubstitute(t *testing.T) {
	for _, c := range []struct {
		state string
		held  []string
		want  string
		ok    bool
	}{
		{BlockOakSlabBtm, []string{"minecraft:acacia_slab"}, "minecraft:acacia_slab[type=bottom]", true},
		{BlockOakSlabBtm, []string{"minecraft:acacia_planks"}, "minecraft:acacia_planks", true},
		{BlockOakSlabBtm, []string{"minecraft:oak_slab"}, BlockOakSlabBtm, true},
		{BlockOakSlabBtm, []string{"minecraft:cobblestone"}, "", false},
		{BlockOakPlanks, []string{"minecraft:birch_planks"}, "minecraft:birch_planks", true},
		{BlockOakLogAxis, []string{"minecraft:acacia_log"}, "minecraft:acacia_log[axis=y]", true},
		{BlockOakLogAxis, []string{"minecraft:acacia_planks"}, "minecraft:acacia_planks", true},
		{BlockOakDoorLow, []string{"minecraft:acacia_planks"}, "", false},
		{BlockWallTorchS, nil, BlockWallTorchS, true},
	} {
		if got, ok := WoodSubstitute(c.state, c.held); got != c.want || ok != c.ok {
			t.Errorf("WoodSubstitute(%s, %v) = %q, %v; want %q, %v", c.state, c.held, got, ok, c.want, c.ok)
		}
	}
}
