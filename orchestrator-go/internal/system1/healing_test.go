package system1

import (
	"testing"
)

// cq-gey.5: an agent sat at 1-4 health with food 16, below the 18 at which wounds close.
func TestHealing_WoundedAgentsEatOrLookForFood(t *testing.T) {
	bread := withInv(item(0, "minecraft:bread", 2))
	st := NewAgentState()
	if got := EvaluateHunger("carl", rf(100, 3000, withHP(16), withFood(16), bread), st, false); got.GetUseItem() == nil {
		t.Errorf("a wounded agent carrying food should eat to heal, got %v", got)
	}
	if got := EvaluateHunger("carl", rf(100, 3000, withHP(16), withFood(18), bread), NewAgentState(), false); got != nil {
		t.Errorf("fed enough to heal, nothing to eat: %v", got)
	}
	if got := EvaluateHunger("carl", rf(100, 3000, withHP(20), withFood(16), bread), NewAgentState(), false); got != nil {
		t.Errorf("unhurt and fed: %v", got)
	}
	for _, c := range []struct {
		hp   float32
		food int32
		want bool
	}{{4, 16, true}, {4, 18, false}, {18, 16, false}, {20, 12, true}} {
		if got := NeedsForaging(rf(100, 3000, withHP(c.hp), withFood(c.food))); got != c.want {
			t.Errorf("hp %.0f food %d without food: forage=%t want %t", c.hp, c.food, got, c.want)
		}
	}
}
