// Package plan holds the pure planning model (cq-dcx.3): a committed Plan with
// a bill of materials, a site and a lease, plus deterministic planners that
// derive ordered steps from a read-only Facts snapshot and the recipe graph.
//
// Everything here is a value computation: no I/O, no clocks, no randomness, no
// wiring into activities. A later PlanActivity executes the steps.
package plan

import (
	"sort"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
)

// StepKind enumerates the step vocabulary.
type StepKind string

const (
	StepGather StepKind = "gather" // collect Qty of Item from a SourceClass
	StepCraft  StepKind = "craft"  // run RecipeID Times times
	StepPlace  StepKind = "place"  // place Item (e.g. crafting table) near the site
	StepBuild  StepKind = "build"  // build Blueprint (not emitted by ToolProgression)
	StepGoto   StepKind = "goto"   // walk to Pos
)

// SourceClass says where a Gather step finds its item.
type SourceClass string

const (
	SourceTree  SourceClass = "tree"
	SourceStone SourceClass = "stone"
	SourceOre   SourceClass = "ore"
	SourceWorld SourceClass = "world"
)

// Pos is an integer block position.
type Pos struct{ X, Y, Z int }

// Step is one executable unit of a plan.
type Step struct {
	Kind         StepKind
	Item         string      // gather/place: the item; craft: the result item
	Qty          int         // gather: amount to collect
	Source       SourceClass // gather only
	RecipeID     string      // craft only
	Times        int         // craft only: number of craft operations
	Requires3x3  bool        // craft only: needs a crafting table in reach
	RequiresTool string      // gather only: tool item (any tier >=) needed to harvest
	Pos          *Pos        // goto target
	Blueprint    string      // build only
}

// BOMLine is one raw-material requirement of a plan.
type BOMLine struct {
	Item   string
	Qty    int
	Source SourceClass
}

// GoalKind identifies a planner.
type GoalKind string

// GoalToolProgression is the wooden->stone tool and torch progression.
const GoalToolProgression GoalKind = "tool_progression"

// Goal names what a plan is for.
type Goal struct {
	Kind GoalKind
}

// Lease is the minimal ownership/expiry record a plan carries. ExpiresTick 0
// means no expiry.
type Lease struct {
	Owner       string
	ExpiresTick int64
}

// Site is where the plan works (the crafting-table location when known).
type Site struct {
	Pos    Pos
	Radius int
	Valid  bool
}

// Plan is a committed multi-step plan. Steps and BOM are the snapshot computed
// at CreatedTick; Next always re-derives the remaining steps from live facts
// so progress needs no cursor.
type Plan struct {
	ID          string
	Goal        Goal
	Steps       []Step
	BOM         []BOMLine
	Site        Site
	Lease       Lease
	CreatedTick int64
	// Unplannable lists goal items the planner could not schedule (missing
	// recipe or missing prerequisite tool), sorted.
	Unplannable []string
}

// TableFacts describes the crafting table situation as seen by the caller.
type TableFacts struct {
	Near  bool    // a table is within crafting reach right now
	Known bool    // a table position is known (Pos valid)
	Pos   Pos     // known table position
	Dist  float64 // walking distance to Pos in blocks
}

// Facts is the read-only snapshot a planner consumes. Inventory keys are
// normalized item ids ("minecraft:oak_log").
type Facts struct {
	Tick      int64
	Inventory map[string]int
	Table     TableFacts
	Recipes   *crafting.RecipeGraph
	// ToolGate maps a harvested raw item to the minimum tool item required
	// ("" for none). Nil uses DefaultToolGate. system1 can adapt its
	// block->tool model (RequirementForBlock/CraftTargetForRequirement) here.
	ToolGate func(item string) string
}

// MaxTableWalk is the farthest known table (blocks) worth walking to instead
// of crafting a new one.
const MaxTableWalk = 48.0

// TorchQty is the torch stack the progression aims for (one craft yields 4).
const TorchQty = 4

// NextStatus is the outcome of Next.
type NextStatus int

const (
	NextReady    NextStatus = iota // a step is returned
	NextComplete                   // goal satisfied, nothing left
	NextBlocked                    // nothing schedulable (unplannable goal items remain)
)

// Invalidation is why a plan should be replanned ("" = still valid).
type Invalidation string

const (
	InvalidNone          Invalidation = ""
	InvalidLeaseExpired  Invalidation = "lease_expired"
	InvalidRecipeMissing Invalidation = "recipe_missing"
	InvalidBOMDrift      Invalidation = "bom_drift"
	InvalidSiteLost      Invalidation = "site_lost"
)

// Goal planners.

// New builds a plan for goal from facts. Returns ok=false for an unknown goal.
func New(id string, goal Goal, lease Lease, f Facts) (Plan, bool) {
	steps, bom, unplannable, ok := derive(goal, f)
	if !ok {
		return Plan{}, false
	}
	p := Plan{
		ID: id, Goal: goal, Steps: steps, BOM: bom, Lease: lease,
		CreatedTick: f.Tick, Unplannable: unplannable,
	}
	p.Site = siteFor(steps, f)
	return p, true
}

// NewToolProgression is New for GoalToolProgression.
func NewToolProgression(id string, lease Lease, f Facts) Plan {
	p, _ := New(id, Goal{Kind: GoalToolProgression}, lease, f)
	return p
}

// Next returns the next executable step of p given live facts. It re-derives
// the remaining steps from the facts, so completed work is skipped
// automatically and the result is a pure function of (p.Goal, f).
func Next(p Plan, f Facts) (Step, NextStatus) {
	steps, _, unplannable, ok := derive(p.Goal, f)
	if !ok {
		return Step{}, NextBlocked
	}
	if len(steps) > 0 {
		return steps[0], NextReady
	}
	if len(unplannable) > 0 {
		return Step{}, NextBlocked
	}
	return Step{}, NextComplete
}

// Check applies the invalidation rules and returns the first that fires.
func Check(p Plan, f Facts) Invalidation {
	if p.Lease.ExpiresTick > 0 && f.Tick >= p.Lease.ExpiresTick {
		return InvalidLeaseExpired
	}
	for _, s := range p.Steps {
		if s.Kind == StepCraft && !hasRecipe(f.Recipes, s.RecipeID, s.Item) {
			return InvalidRecipeMissing
		}
	}
	// A committed Goto to a table that is no longer known cannot be followed.
	for _, s := range p.Steps {
		if s.Kind == StepGoto && !f.Table.Near && !f.Table.Known {
			return InvalidSiteLost
		}
	}
	_, bom, _, ok := derive(p.Goal, f)
	if !ok {
		return InvalidRecipeMissing
	}
	planned := map[string]int{}
	for _, b := range p.BOM {
		planned[b.Item] = b.Qty
	}
	for _, b := range bom {
		if b.Qty > planned[b.Item] {
			return InvalidBOMDrift
		}
	}
	return InvalidNone
}

// Replan recomputes the plan from facts, keeping ID, goal and lease owner.
// CreatedTick moves to the current tick.
func Replan(p Plan, f Facts) Plan {
	np, ok := New(p.ID, p.Goal, p.Lease, f)
	if !ok {
		return p
	}
	return np
}

func hasRecipe(g *crafting.RecipeGraph, recipeID, result string) bool {
	for _, r := range g.RecipesFor(result) {
		if r.RecipeID == recipeID {
			return true
		}
	}
	return false
}

func siteFor(steps []Step, f Facts) Site {
	if f.Table.Known {
		return Site{Pos: f.Table.Pos, Radius: 4, Valid: true}
	}
	for _, s := range steps {
		if s.Kind == StepGoto && s.Pos != nil {
			return Site{Pos: *s.Pos, Radius: 4, Valid: true}
		}
	}
	return Site{}
}

func derive(goal Goal, f Facts) ([]Step, []BOMLine, []string, bool) {
	switch goal.Kind {
	case GoalToolProgression:
		steps, bom, un := planToolProgression(f)
		return steps, bom, un, true
	}
	return nil, nil, nil, false
}

// DefaultToolGate is the built-in raw item -> minimum tool table.
func DefaultToolGate(item string) string {
	switch sourceOf(item) {
	case SourceStone, SourceOre:
		return "minecraft:wooden_pickaxe"
	}
	return ""
}

func sourceOf(item string) SourceClass {
	id := strings.TrimPrefix(item, "minecraft:")
	switch {
	case strings.HasSuffix(id, "_log"), strings.HasSuffix(id, "_stem"), strings.HasSuffix(id, "_wood"):
		return SourceTree
	case id == "cobblestone", id == "cobbled_deepslate", id == "stone", id == "blackstone":
		return SourceStone
	case id == "coal", id == "charcoal", strings.HasSuffix(id, "_ore"), strings.HasPrefix(id, "raw_"):
		return SourceOre
	}
	return SourceWorld
}

// toolRank parses a tool/weapon item into (class, tier). ok=false otherwise.
func toolRank(item string) (class string, tier int, ok bool) {
	id := strings.TrimPrefix(item, "minecraft:")
	i := strings.IndexByte(id, '_')
	if i < 0 {
		return "", 0, false
	}
	switch id[:i] {
	case "wooden", "golden":
		tier = 1
	case "stone":
		tier = 2
	case "iron":
		tier = 3
	case "diamond", "netherite":
		tier = 4
	default:
		return "", 0, false
	}
	class = id[i+1:]
	switch class {
	case "pickaxe", "axe", "sword", "shovel", "hoe":
		return class, tier, true
	}
	return "", 0, false
}

// owned reports whether inv satisfies item: a tool is satisfied by any held
// tool of the same class and >= tier, other items by count >= qty.
func owned(item string, qty int, inv map[string]int) bool {
	if class, tier, ok := toolRank(item); ok {
		for id, n := range inv {
			if n <= 0 {
				continue
			}
			if c, t, ok := toolRank(id); ok && c == class && t >= tier {
				return true
			}
		}
		return false
	}
	return inv[item] >= qty
}

func sortedBOM(m map[string]int) []BOMLine {
	out := make([]BOMLine, 0, len(m))
	for item, q := range m {
		out = append(out, BOMLine{Item: item, Qty: q, Source: sourceOf(item)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Item < out[j].Item })
	return out
}
