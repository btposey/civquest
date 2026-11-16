package system1

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
)

func init() {
	RegisterEvaluator(NewBlueprintEvaluator())
}

// BlueprintEvaluator evaluates available blueprints against settlement storehouse reserves
// using generic taxonomy BOM resolution and recommends blueprints matching civic deficits.
type BlueprintEvaluator struct {
	CachedBlueprints []*blueprints.Blueprint
	mu               sync.RWMutex
	cachedLib        []*blueprints.Blueprint
}

// NewBlueprintEvaluator creates a new BlueprintEvaluator instance with optional preloaded blueprints.
func NewBlueprintEvaluator(cached ...*blueprints.Blueprint) *BlueprintEvaluator {
	return &BlueprintEvaluator{CachedBlueprints: cached}
}

// Name returns the unique evaluator registration name.
func (e *BlueprintEvaluator) Name() string {
	return "blueprint"
}

// Category returns the evaluation category for FactGraph grouping.
func (e *BlueprintEvaluator) Category() string {
	return "Blueprint"
}

// getLibraryBlueprints returns available blueprints, caching library disk scans in memory
// to preserve the <5ms System 1 execution budget.
func (e *BlueprintEvaluator) getLibraryBlueprints() []*blueprints.Blueprint {
	if len(e.CachedBlueprints) > 0 {
		return e.CachedBlueprints
	}

	e.mu.RLock()
	if len(e.cachedLib) > 0 {
		lib := e.cachedLib
		e.mu.RUnlock()
		return lib
	}
	e.mu.RUnlock()

	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.cachedLib) > 0 {
		return e.cachedLib
	}

	bps, err := blueprints.ListLibraryBlueprints()
	if err != nil || len(bps) == 0 {
		_ = blueprints.EnsureStarterBlueprints()
		bps, _ = blueprints.ListLibraryBlueprints()
	}
	e.cachedLib = bps
	return bps
}

// Evaluate matches available blueprints against storehouse reserves and emits facts into FactGraph.
func (e *BlueprintEvaluator) Evaluate(ctx *EvaluatorContext, graph *FactGraph) error {
	if graph == nil {
		return nil
	}

	// 1. Gather available storehouse (granary) and carried inventory
	inventory := make(map[string]int)

	if ctx != nil && ctx.CivicDB != nil {
		if stocks, err := ctx.CivicDB.GetGranaryInventoryContext(context.Background()); err == nil {
			for _, st := range stocks {
				if st == nil || st.ItemID == "" {
					continue
				}
				avail := st.Quantity - st.ReservedQuantity
				if avail > 0 {
					norm := blueprints.NormalizeItemID(st.ItemID)
					inventory[norm] += avail
				}
			}
		}
	}

	if ctx != nil && ctx.Frame != nil && ctx.Frame.Status != nil {
		for _, item := range ctx.Frame.Status.Inventory {
			if item == nil || item.Count <= 0 || item.ItemId == "" {
				continue
			}
			norm := blueprints.NormalizeItemID(item.ItemId)
			inventory[norm] += int(item.Count)
		}
	}

	// 2. Discover candidate blueprints (in-memory cached)
	bps := e.getLibraryBlueprints()

	// Track feasible blueprints by civic purpose
	feasibleByPurpose := make(map[string][]string)
	allPurposes := make(map[string]bool)

	// Seed with standard architectural purposes
	standardPurposes := []string{
		blueprints.PurposeShelter,
		blueprints.PurposeHome,
		blueprints.PurposeTavern,
		blueprints.PurposeArmory,
		blueprints.PurposeStorehouse,
		blueprints.PurposeFarm,
		blueprints.PurposeWorkshop,
		blueprints.PurposeBarracks,
		blueprints.PurposeLibrary,
		blueprints.PurposeTownhall,
	}
	for _, p := range standardPurposes {
		allPurposes[p] = true
		feasibleByPurpose[p] = []string{}
	}

	var affordableBPs []*blueprints.Blueprint

	// Sort candidate blueprints by ID for deterministic evaluation
	sortedBPs := make([]*blueprints.Blueprint, len(bps))
	copy(sortedBPs, bps)
	sort.Slice(sortedBPs, func(i, j int) bool {
		return sortedBPs[i].ID < sortedBPs[j].ID
	})

	for _, bp := range sortedBPs {
		if bp == nil {
			continue
		}
		if bp.ID == "" {
			bp.ID = blueprints.Slugify(bp.Name)
		}

		normPurpose := blueprints.NormalizePurpose(bp.Purpose)
		allPurposes[normPurpose] = true

		bom := bp.BOM
		if len(bom) == 0 {
			bom = bp.CalculateBOM()
		}

		satisfied, _, missing := blueprints.EvaluateTaxonomyBOM(bom, inventory)

		// Emit blueprint.affordable:<id> = "true" | "false"
		affStr := fmt.Sprintf("%t", satisfied)
		graph.Set(Fact{
			Key:        "blueprint.affordable:" + bp.ID,
			Category:   "Blueprint",
			Value:      FactValue{Bool: satisfied, String: affStr},
			Confidence: 1.0,
			Evidence:   affStr,
		})

		// Emit blueprint.missing:<id>
		var missingSummary string
		if !satisfied && len(missing) > 0 {
			var missingList []string
			for item, needed := range missing {
				missingList = append(missingList, fmt.Sprintf("%s:%d", item, needed))
			}
			sort.Strings(missingList)
			missingSummary = strings.Join(missingList, ", ")
		}
		graph.SetString("blueprint.missing:"+bp.ID, "Blueprint", missingSummary, missingSummary)

		if satisfied {
			feasibleByPurpose[normPurpose] = append(feasibleByPurpose[normPurpose], bp.ID)
			affordableBPs = append(affordableBPs, bp)
		}
	}

	// Emit blueprint.feasible:<purpose>
	for purpose := range allPurposes {
		ids := feasibleByPurpose[purpose]
		sort.Strings(ids)
		joined := strings.Join(ids, ", ")
		evidence := fmt.Sprintf("Feasible %s blueprints: %s", purpose, joined)
		if joined == "" {
			evidence = fmt.Sprintf("No feasible %s blueprints available", purpose)
		}
		graph.SetString("blueprint.feasible:"+purpose, "Blueprint", joined, evidence)
	}

	// 3. Evaluate settlement civic deficits and determine blueprint.recommended
	// Deficit priorities:
	// Priority 1: Unhoused (SHELTER / HOME)
	// Priority 2: Tavern deficit (TAVERN if no tavern exists and affordable)
	// Priority 3: Militia under-equipped (ARMORY / BARRACKS)
	// Priority 4: Storehouse deficit (STOREHOUSE / GRANARY)
	// Fallback: First affordable blueprint

	isUnhoused := false
	if !graph.GetBool("shelter.complete") || !graph.GetBool("shelter.has_bed") {
		isUnhoused = true
	}
	if ctx != nil && ctx.State != nil && ctx.State.GetFSMState() == StateShelter {
		isUnhoused = true
	}

	hasTavern := false
	hasStorehouse := false
	if ctx != nil && ctx.CivicDB != nil {
		if zones, err := ctx.CivicDB.ListZones(); err == nil {
			for _, z := range zones {
				if z == nil {
					continue
				}
				if strings.EqualFold(z.ZoneType, db.ZoneTypeTavern) {
					hasTavern = true
				}
				if strings.EqualFold(z.ZoneType, db.ZoneTypeGranary) || strings.EqualFold(z.ZoneType, "STOREHOUSE") {
					hasStorehouse = true
				}
			}
		}
	}

	militiaUnderEquipped := false
	if fact, exists := graph.Get("armory.sufficient"); exists && !fact.Value.Bool {
		militiaUnderEquipped = true
	}

	findAffordableForPurposes := func(purposes ...string) string {
		for _, p := range purposes {
			normP := blueprints.NormalizePurpose(p)
			for _, bp := range affordableBPs {
				if blueprints.NormalizePurpose(bp.Purpose) == normP {
					return bp.ID
				}
			}
		}
		return ""
	}

	var recommended string
	switch {
	case isUnhoused && findAffordableForPurposes(blueprints.PurposeShelter, blueprints.PurposeHome) != "":
		recommended = findAffordableForPurposes(blueprints.PurposeShelter, blueprints.PurposeHome)
	case !hasTavern && findAffordableForPurposes(blueprints.PurposeTavern) != "":
		recommended = findAffordableForPurposes(blueprints.PurposeTavern)
	case militiaUnderEquipped && findAffordableForPurposes(blueprints.PurposeArmory, blueprints.PurposeBarracks) != "":
		recommended = findAffordableForPurposes(blueprints.PurposeArmory, blueprints.PurposeBarracks)
	case !hasStorehouse && findAffordableForPurposes(blueprints.PurposeStorehouse, blueprints.PurposeGranary) != "":
		recommended = findAffordableForPurposes(blueprints.PurposeStorehouse, blueprints.PurposeGranary)
	case len(affordableBPs) > 0:
		recommended = affordableBPs[0].ID
	}

	var recEvidence string
	if recommended != "" {
		recEvidence = fmt.Sprintf("Recommended blueprint: %s", recommended)
	} else {
		recEvidence = "No affordable blueprints currently recommended"
	}
	graph.SetString("blueprint.recommended", "Blueprint", recommended, recEvidence)

	return nil
}
