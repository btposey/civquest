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

// cq-0x4: one item places both halves, so no builder is asked for the second one.
func TestNoTwoBlockItemIsAskedForTwice(t *testing.T) {
	doors := 0
	for _, in := range FrontierCabinBlueprint() {
		if SetByOtherHalf(in.BlockStateString) {
			t.Errorf("cabin asks for %s", in.BlockStateString)
		}
		if NormalizeItemID(in.BlockStateString) == "minecraft:oak_door" {
			doors++
		}
	}
	if doors != 1 {
		t.Errorf("cabin door placements = %d, want 1", doors)
	}
	vm := NewVoxelMatrix(1, 2, 1)
	for y, half := range []string{"lower", "upper"} {
		if err := vm.SetStateString(0, y, 0, "minecraft:oak_door[facing=south,half="+half+"]"); err != nil {
			t.Fatal(err)
		}
	}
	n := 0
	for _, work := range PartitionBlueprint(vm) {
		n += len(work)
	}
	if n != 1 {
		t.Errorf("a two-high door makes %d work items, want 1", n)
	}
}

// cq-390.8: a pane if there is one, else an upright trapdoor, else a fence, of any wood.
func TestFittingFill(t *testing.T) {
	for _, tc := range []struct {
		held []string
		want string
	}{
		{[]string{"minecraft:spruce_fence", "minecraft:birch_trapdoor", "minecraft:glass_pane"}, "minecraft:glass_pane"},
		{[]string{"minecraft:spruce_fence", "minecraft:birch_trapdoor"}, "minecraft:birch_trapdoor[facing=east,half=bottom,open=true]"},
		{[]string{"minecraft:iron_trapdoor", "minecraft:spruce_fence"}, "minecraft:spruce_fence"},
		{[]string{"minecraft:oak_fence_gate", "minecraft:oak_planks"}, ""},
	} {
		if got, ok := FittingFill(BlockWindowWest, tc.held); got != tc.want || ok != (tc.want != "") {
			t.Errorf("held %v: window = %q, %v; want %q", tc.held, got, ok, tc.want)
		}
	}
	if got, _ := FittingFill(BlockOakDoorLow, []string{"minecraft:oak_trapdoor", "minecraft:cherry_door"}); got != "minecraft:cherry_door[facing=south,half=lower]" {
		t.Errorf("door = %q", got)
	}
}
