package system1

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
)

func init() {
	RegisterEvaluator(NewBlueprintEvaluator())
}

type bpCacheEntry struct {
	invKey               string
	isUnhoused           bool
	hasTavern            bool
	hasStorehouse        bool
	militiaUnderEquipped bool
	facts                []Fact
}

// BlueprintEvaluator evaluates available blueprints against settlement storehouse reserves
// using generic taxonomy BOM resolution and recommends blueprints matching civic deficits.
type BlueprintEvaluator struct {
	CachedBlueprints []*blueprints.Blueprint
	mu               sync.RWMutex
	cachedLib        []*blueprints.Blueprint
	libLoaded        bool
	agentCache       map[string]*bpCacheEntry
}

// NewBlueprintEvaluator creates a new BlueprintEvaluator instance with optional preloaded blueprints.
func NewBlueprintEvaluator(cached ...*blueprints.Blueprint) *BlueprintEvaluator {
	e := &BlueprintEvaluator{
		CachedBlueprints: cached,
		agentCache:       make(map[string]*bpCacheEntry),
	}
	if len(cached) > 0 {
		for _, bp := range cached {
			if bp != nil && len(bp.BOM) == 0 {
				bp.BOM = bp.CalculateBOM()
			}
		}
		sort.Slice(e.CachedBlueprints, func(i, j int) bool {
			return e.CachedBlueprints[i].ID < e.CachedBlueprints[j].ID
		})
	}
	return e
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
	if e.libLoaded {
		lib := e.cachedLib
		e.mu.RUnlock()
		return lib
	}
	e.mu.RUnlock()

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.libLoaded {
		return e.cachedLib
	}

	bps, err := blueprints.ListLibraryBlueprints()
	if err != nil || len(bps) == 0 {
		_ = blueprints.EnsureStarterBlueprints()
		bps, _ = blueprints.ListLibraryBlueprints()
	}
	for _, bp := range bps {
		if bp != nil && len(bp.BOM) == 0 {
			bp.BOM = bp.CalculateBOM()
		}
	}
	sort.Slice(bps, func(i, j int) bool {
		return bps[i].ID < bps[j].ID
	})
	e.cachedLib = bps
	e.libLoaded = true
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

	agentID := ""
	if ctx != nil {
		agentID = ctx.AgentID
	}

	var invKey string
	if len(inventory) > 0 {
		var b strings.Builder
		keys := make([]string, 0, len(inventory))
		for k := range inventory {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString(k)
			b.WriteByte(':')
			b.WriteString(strconv.Itoa(inventory[k]))
			b.WriteByte(';')
		}
		invKey = b.String()
	}

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

	// Check agent-specific cache to avoid redundant evaluation across 5 Hz ticks
	e.mu.RLock()
	if e.agentCache != nil {
		if entry, ok := e.agentCache[agentID]; ok && entry != nil {
			if entry.invKey == invKey && entry.isUnhoused == isUnhoused && entry.hasTavern == hasTavern && entry.hasStorehouse == hasStorehouse && entry.militiaUnderEquipped == militiaUnderEquipped && len(entry.facts) > 0 {
				for _, f := range entry.facts {
					graph.Set(f)
				}
				e.mu.RUnlock()
				return nil
			}
		}
	}
	e.mu.RUnlock()

	// 2. Discover candidate blueprints (in-memory cached)
	bps := e.getLibraryBlueprints()

	var factsToCache []Fact

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

	for _, bp := range bps {
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
		affStr := "false"
		if satisfied {
			affStr = "true"
		}
		affFact := Fact{
			Key:        "blueprint.affordable:" + bp.ID,
			Category:   "Blueprint",
			Value:      FactValue{Bool: satisfied, String: affStr},
			Confidence: 1.0,
			Evidence:   affStr,
		}
		graph.Set(affFact)
		factsToCache = append(factsToCache, affFact)

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
		missingFact := Fact{
			Key:        "blueprint.missing:" + bp.ID,
			Category:   "Blueprint",
			Value:      FactValue{String: missingSummary},
			Confidence: 1.0,
			Evidence:   missingSummary,
		}
		graph.Set(missingFact)
		factsToCache = append(factsToCache, missingFact)

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
		feasibleFact := Fact{
			Key:        "blueprint.feasible:" + purpose,
			Category:   "Blueprint",
			Value:      FactValue{String: joined},
			Confidence: 1.0,
			Evidence:   evidence,
		}
		graph.Set(feasibleFact)
		factsToCache = append(factsToCache, feasibleFact)
	}

	// 3. Evaluate settlement civic deficits and determine blueprint.recommended
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
	recFact := Fact{
		Key:        "blueprint.recommended",
		Category:   "Blueprint",
		Value:      FactValue{String: recommended},
		Confidence: 1.0,
		Evidence:   recEvidence,
	}
	graph.Set(recFact)
	factsToCache = append(factsToCache, recFact)

	// Save to agent cache
	e.mu.Lock()
	if e.agentCache == nil {
		e.agentCache = make(map[string]*bpCacheEntry)
	}
	e.agentCache[agentID] = &bpCacheEntry{
		invKey:               invKey,
		isUnhoused:           isUnhoused,
		hasTavern:            hasTavern,
		hasStorehouse:        hasStorehouse,
		militiaUnderEquipped: militiaUnderEquipped,
		facts:                factsToCache,
	}
	e.mu.Unlock()

	return nil
}
