package crafting

import (
	"sort"
	"strings"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

const (
	maxPlanDepth = 10
	maxBatchLoop = 64
)

// IngredientSlot represents one grouped ingredient requirement for a recipe,
// accepting any of AcceptedItemIDs for a total of Count items.
type IngredientSlot struct {
	AcceptedItemIDs []string
	Count           int
}

// Recipe represents a normalized crafting recipe indexed in RecipeGraph.
type Recipe struct {
	RecipeID         string
	RecipeType       string
	WorkstationBlock string
	Requires3x3      bool
	Slots            []IngredientSlot
	ResultItemID     string
	ResultCount      int
}

// Step describes the next concrete crafting action (or table prerequisite) toward a target item.
type Step struct {
	RecipeID      string
	ResultItemID  string
	ResultCount   int
	Requires3x3   bool
	CanExecuteNow bool
	NeedsTable    bool
	MissingRaw    bool
}

// RecipeGraph indexes runtime crafting recipes exported from the Minecraft server
// and resolves cycle-safe, multi-step prerequisite crafting chains (ADR-029).
type RecipeGraph struct {
	byResult map[string][]Recipe
}

// NewRecipeGraph constructs a RecipeGraph from protobuf RecipeNode entries.
// Supports both IngredientSlots (preferred, tag-aware) and legacy Ingredients fallback.
func NewRecipeGraph(nodes []*civquestv1.RecipeNode) *RecipeGraph {
	byResult := make(map[string][]Recipe)
	for _, node := range nodes {
		if node == nil || node.Result == nil {
			continue
		}
		resultID := normalizeItemID(node.Result.ItemId)
		if resultID == "" {
			continue
		}
		workstation := normalizeItemID(node.WorkstationBlock)
		if workstation != "" && workstation != "minecraft:crafting_table" {
			// Skip non-crafting-table workstation recipes (e.g. furnace, cooking pot) from crafting planner.
			continue
		}

		slots := parseNodeSlots(node)
		if len(slots) == 0 {
			continue
		}

		resultCount := int(node.Result.Count)
		if resultCount <= 0 {
			resultCount = 1
		}
		requires3x3 := node.GetRequires_3X3() || workstation == "minecraft:crafting_table"
		if requires3x3 && workstation == "" {
			workstation = "minecraft:crafting_table"
		}

		recipeID := normalizeItemID(node.RecipeId)
		if recipeID == "" {
			recipeID = resultID
		}

		byResult[resultID] = append(byResult[resultID], Recipe{
			RecipeID:         recipeID,
			RecipeType:       normalizeItemID(node.RecipeType),
			WorkstationBlock: workstation,
			Requires3x3:      requires3x3,
			Slots:            slots,
			ResultItemID:     resultID,
			ResultCount:      resultCount,
		})
	}

	// Deterministic ordering: prefer 2x2 recipes over 3x3, then sort by RecipeID.
	for itemID, list := range byResult {
		sort.Slice(list, func(i, j int) bool {
			if list[i].Requires3x3 != list[j].Requires3x3 {
				return !list[i].Requires3x3
			}
			return list[i].RecipeID < list[j].RecipeID
		})
		byResult[itemID] = list
	}

	return &RecipeGraph{byResult: byResult}
}

// IsEmpty returns true if the RecipeGraph is nil or has no indexed recipes.
func (g *RecipeGraph) IsEmpty() bool {
	return g == nil || len(g.byResult) == 0
}

// RecipesFor returns all indexed crafting recipes that produce targetItemID.
func (g *RecipeGraph) RecipesFor(targetItemID string) []Recipe {
	if g == nil {
		return nil
	}
	list := g.byResult[normalizeItemID(targetItemID)]
	if len(list) == 0 {
		return nil
	}
	out := make([]Recipe, len(list))
	copy(out, list)
	return out
}

// InventoryMapFromProtos converts a slice of ItemStackProto into a normalized itemID -> total count map.
func InventoryMapFromProtos(items []*civquestv1.ItemStackProto) map[string]int {
	inv := make(map[string]int, len(items))
	for _, it := range items {
		if it == nil || it.Count <= 0 {
			continue
		}
		id := normalizeItemID(it.ItemId)
		if id == "" {
			continue
		}
		inv[id] += int(it.Count)
	}
	return inv
}

// CanEventuallyCraft returns true if inv contains enough raw/intermediate materials
// to complete a valid crafting chain for targetItemID.
func (g *RecipeGraph) CanEventuallyCraft(targetItemID string, inv map[string]int) bool {
	if g.IsEmpty() {
		return false
	}
	target := normalizeItemID(targetItemID)
	if target == "" {
		return false
	}
	workInv := cloneInv(inv)
	visiting := make(map[string]bool)
	_, ok := g.simulateCraftOne(target, workInv, visiting, 0)
	return ok
}

// PlanNextStep resolves the next crafting step required to produce targetItemID from inv.
// Returns (Step, true) when inv has sufficient raw materials to complete the full chain.
// Returns (Step{ResultItemID: target, MissingRaw: true}, false) when the target is unknown or raw materials are missing.
func (g *RecipeGraph) PlanNextStep(targetItemID string, inv map[string]int, hasTable bool) (Step, bool) {
	target := normalizeItemID(targetItemID)
	if g.IsEmpty() || target == "" {
		return Step{ResultItemID: target, MissingRaw: true}, false
	}
	recipes := g.byResult[target]
	if len(recipes) == 0 {
		return Step{ResultItemID: target, MissingRaw: true}, false
	}

	// If any recipe for target is already directly executable with current inv + workstation, pick it immediately.
	for _, r := range recipes {
		if canCraftRecipeDirectly(r, inv) {
			if !r.Requires3x3 || hasTable {
				return Step{
					RecipeID:      r.RecipeID,
					ResultItemID:  r.ResultItemID,
					ResultCount:   r.ResultCount,
					Requires3x3:   r.Requires3x3,
					CanExecuteNow: true,
					NeedsTable:    false,
					MissingRaw:    false,
				}, true
			}
		}
	}

	// Otherwise, simulate full prerequisite plans across available recipes for target.
	// Collect successful plans and pick the best next step.
	var bestPlan []Recipe
	for _, r := range recipes {
		workInv := cloneInv(inv)
		visiting := map[string]bool{target: true}
		plan, ok := g.simulateCraftRecipe(r, workInv, visiting, 1)
		if !ok || len(plan) == 0 {
			continue
		}
		if bestPlan == nil || len(plan) < len(bestPlan) {
			bestPlan = plan
		}
	}

	if len(bestPlan) == 0 {
		return Step{ResultItemID: target, MissingRaw: true}, false
	}

	chosen := g.selectBestNextStep(target, bestPlan, inv, hasTable)
	canExec := canCraftRecipeDirectly(chosen, inv) && (!chosen.Requires3x3 || hasTable)
	needsTable := chosen.Requires3x3 && !hasTable && canCraftRecipeDirectly(chosen, inv)

	return Step{
		RecipeID:      chosen.RecipeID,
		ResultItemID:  chosen.ResultItemID,
		ResultCount:   chosen.ResultCount,
		Requires3x3:   chosen.Requires3x3,
		CanExecuteNow: canExec,
		NeedsTable:    needsTable,
		MissingRaw:    false,
	}, true
}

// PlanNextStepForSuffix finds the first craftable target recipe whose ResultItemID ends with suffix
// (e.g. "_planks") and plans its next step. Prefers steps executable now.
func (g *RecipeGraph) PlanNextStepForSuffix(suffix string, inv map[string]int, hasTable bool) (Step, bool) {
	if g.IsEmpty() {
		return Step{MissingRaw: true}, false
	}
	normSuffix := normalizeItemID(suffix)
	if normSuffix == "" {
		return Step{MissingRaw: true}, false
	}

	var candidates []string
	for resultID := range g.byResult {
		if strings.HasSuffix(resultID, normSuffix) {
			candidates = append(candidates, resultID)
		}
	}
	sort.Strings(candidates)

	var fallback Step
	hasFallback := false
	for _, itemID := range candidates {
		step, ok := g.PlanNextStep(itemID, inv, hasTable)
		if !ok {
			continue
		}
		if step.CanExecuteNow {
			return step, true
		}
		if !hasFallback {
			fallback = step
			hasFallback = true
		}
	}
	if hasFallback {
		return fallback, true
	}
	return Step{MissingRaw: true}, false
}

func (g *RecipeGraph) selectBestNextStep(target string, plan []Recipe, inv map[string]int, hasTable bool) Recipe {
	targetRecipe := plan[len(plan)-1]

	// Determine which direct ingredient slots of targetRecipe are not yet satisfied in inv.
	deficientDirectItems := computeDeficientItemsForRecipe(targetRecipe, inv)

	// 1. Prefer an immediately executable 2x2 step (or any executable step if hasTable) whose output
	//    satisfies a direct missing slot of targetRecipe and preserves target feasibility.
	for _, s := range plan {
		if !canCraftRecipeDirectly(s, inv) {
			continue
		}
		if s.Requires3x3 && !hasTable {
			continue
		}
		if deficientDirectItems[s.ResultItemID] && g.stepPreservesTarget(target, s, inv) {
			return s
		}
	}

	// 2. Otherwise pick the first step in the plan that is immediately executable right now
	//    (prioritizing 2x2 steps when !hasTable so pocket crafting completes before walking to a table).
	if !hasTable {
		for _, s := range plan {
			if !s.Requires3x3 && canCraftRecipeDirectly(s, inv) && g.stepPreservesTarget(target, s, inv) {
				return s
			}
		}
	}

	for _, s := range plan {
		if canCraftRecipeDirectly(s, inv) && (!s.Requires3x3 || hasTable) {
			return s
		}
	}

	// 3. No step is executable without a crafting table; return the first step requiring a table
	//    whose ingredients are ready in inv.
	for _, s := range plan {
		if s.Requires3x3 && canCraftRecipeDirectly(s, inv) {
			return s
		}
	}

	return plan[0]
}

func (g *RecipeGraph) stepPreservesTarget(target string, step Recipe, inv map[string]int) bool {
	nextInv := cloneInv(inv)
	if !applyRecipeOnce(step, nextInv) {
		return false
	}
	// If step itself produced the target, feasibility is satisfied.
	if step.ResultItemID == target {
		return true
	}
	return g.CanEventuallyCraft(target, nextInv)
}

func computeDeficientItemsForRecipe(r Recipe, inv map[string]int) map[string]bool {
	deficient := make(map[string]bool)
	remInv := cloneInv(inv)
	for _, slot := range r.Slots {
		needed := slot.Count
		for _, id := range slot.AcceptedItemIDs {
			if have := remInv[id]; have > 0 {
				take := have
				if take > needed {
					take = needed
				}
				remInv[id] -= take
				needed -= take
				if needed == 0 {
					break
				}
			}
		}
		if needed > 0 {
			for _, id := range slot.AcceptedItemIDs {
				deficient[id] = true
			}
		}
	}
	return deficient
}

func (g *RecipeGraph) simulateCraftOne(itemID string, workInv map[string]int, visiting map[string]bool, depth int) ([]Recipe, bool) {
	if depth > maxPlanDepth || visiting[itemID] {
		return nil, false
	}
	recipes := g.byResult[itemID]
	if len(recipes) == 0 {
		return nil, false
	}

	visiting[itemID] = true
	defer delete(visiting, itemID)

	for _, r := range recipes {
		snap := cloneInv(workInv)
		steps, ok := g.simulateCraftRecipe(r, snap, visiting, depth+1)
		if ok {
			replaceInv(workInv, snap)
			return steps, true
		}
	}
	return nil, false
}

func (g *RecipeGraph) simulateCraftRecipe(r Recipe, workInv map[string]int, visiting map[string]bool, depth int) ([]Recipe, bool) {
	if depth > maxPlanDepth {
		return nil, false
	}
	var steps []Recipe

	for _, slot := range r.Slots {
		missing := slot.Count
		missing = consumeMatchingFromInv(slot.AcceptedItemIDs, missing, workInv)

		batches := 0
		for missing > 0 {
			if batches >= maxBatchLoop {
				return nil, false
			}
			batches++
			crafted := false
			for _, candID := range slot.AcceptedItemIDs {
				subSteps, ok := g.simulateCraftOne(candID, workInv, visiting, depth+1)
				if ok {
					steps = append(steps, subSteps...)
					missing = consumeMatchingFromInv(slot.AcceptedItemIDs, missing, workInv)
					crafted = true
					break
				}
			}
			if !crafted {
				return nil, false
			}
		}
	}

	workInv[r.ResultItemID] += r.ResultCount
	steps = append(steps, r)
	return steps, true
}

func canCraftRecipeDirectly(r Recipe, inv map[string]int) bool {
	rem := cloneInv(inv)
	for _, slot := range r.Slots {
		missing := consumeMatchingFromInv(slot.AcceptedItemIDs, slot.Count, rem)
		if missing > 0 {
			return false
		}
	}
	return true
}

func applyRecipeOnce(r Recipe, inv map[string]int) bool {
	snap := cloneInv(inv)
	for _, slot := range r.Slots {
		missing := consumeMatchingFromInv(slot.AcceptedItemIDs, slot.Count, snap)
		if missing > 0 {
			return false
		}
	}
	snap[r.ResultItemID] += r.ResultCount
	replaceInv(inv, snap)
	return true
}

func consumeMatchingFromInv(acceptedIDs []string, needed int, inv map[string]int) int {
	if needed <= 0 {
		return 0
	}
	for _, id := range acceptedIDs {
		have := inv[id]
		if have <= 0 {
			continue
		}
		take := have
		if take > needed {
			take = needed
		}
		inv[id] = have - take
		needed -= take
		if needed == 0 {
			return 0
		}
	}
	return needed
}

func parseNodeSlots(node *civquestv1.RecipeNode) []IngredientSlot {
	if len(node.IngredientSlots) > 0 {
		slots := make([]IngredientSlot, 0, len(node.IngredientSlots))
		for _, s := range node.IngredientSlots {
			if s == nil || s.Count <= 0 {
				continue
			}
			seen := make(map[string]struct{}, len(s.AcceptedItemIds))
			accepted := make([]string, 0, len(s.AcceptedItemIds))
			for _, rawID := range s.AcceptedItemIds {
				id := normalizeItemID(rawID)
				if id == "" {
					continue
				}
				if _, dup := seen[id]; dup {
					continue
				}
				seen[id] = struct{}{}
				accepted = append(accepted, id)
			}
			if len(accepted) == 0 {
				return nil
			}
			sort.Strings(accepted)
			slots = append(slots, IngredientSlot{
				AcceptedItemIDs: accepted,
				Count:           int(s.Count),
			})
		}
		return slots
	}

	if len(node.Ingredients) > 0 {
		slots := make([]IngredientSlot, 0, len(node.Ingredients))
		for _, ing := range node.Ingredients {
			if ing == nil || ing.Count <= 0 {
				continue
			}
			id := normalizeItemID(ing.ItemId)
			if id == "" {
				return nil
			}
			slots = append(slots, IngredientSlot{
				AcceptedItemIDs: []string{id},
				Count:           int(ing.Count),
			})
		}
		return slots
	}

	return nil
}

func normalizeItemID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

func cloneInv(inv map[string]int) map[string]int {
	out := make(map[string]int, len(inv))
	for k, v := range inv {
		if v > 0 {
			out[normalizeItemID(k)] += v
		}
	}
	return out
}

func replaceInv(dst, src map[string]int) {
	for k := range dst {
		delete(dst, k)
	}
	for k, v := range src {
		if v > 0 {
			dst[k] = v
		}
	}
}
