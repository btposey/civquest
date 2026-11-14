package agrarian

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Standard processing stations for agrarian crafting, cooking, and fermentation.
type ProcessingStation string

const (
	StationNone               ProcessingStation = "none"
	StationCraftingTable      ProcessingStation = "minecraft:crafting_table"
	StationFurnace            ProcessingStation = "minecraft:furnace"
	StationSmoker             ProcessingStation = "minecraft:smoker"
	StationCampfire           ProcessingStation = "minecraft:campfire"
	StationCookingPot         ProcessingStation = "farmersdelight:cooking_pot"
	StationCuttingBoard       ProcessingStation = "farmersdelight:cutting_board"
	StationSkillet            ProcessingStation = "farmersdelight:skillet"
	StationFermentationBarrel ProcessingStation = "vinery:fermentation_barrel"
	StationWinePress          ProcessingStation = "vinery:wine_press"
	StationKeg                ProcessingStation = "brewinandchewin:keg"
)

// Standard mod identifiers.
const (
	ModVanilla         = "minecraft"
	ModFarmersDelight  = "farmersdelight"
	ModVinery          = "vinery"
	ModBrewinAndChewin = "brewinandchewin"
)

// Sentinel errors.
var (
	ErrRecipeNotFound      = errors.New("recipe not found")
	ErrRecipeCycleDetected = errors.New("cycle detected in recipe dependency graph")
	ErrInsufficientItems   = errors.New("insufficient input items to satisfy recipe")
	ErrEmptyRecipeID       = errors.New("recipe_id cannot be empty")
)

// IngredientRequirement defines a required input item and amount for a recipe.
type IngredientRequirement struct {
	ItemID string `json:"item_id"`
	Count  int    `json:"count"`
}

// RecipeOutput defines an item produced by completing a recipe.
type RecipeOutput struct {
	ItemID string `json:"item_id"`
	Count  int    `json:"count"`
}

// Recipe models a single crafting, cooking, or fermentation transformation.
type Recipe struct {
	RecipeID      string                  `json:"recipe_id"`
	DisplayName   string                  `json:"display_name"`
	ModID         string                  `json:"mod_id"`
	Station       ProcessingStation       `json:"station"`
	CookTimeTicks int                     `json:"cook_time_ticks"`
	Inputs        []IngredientRequirement `json:"inputs"`
	Outputs       []RecipeOutput          `json:"outputs"`
}

// RecipeGraphNode represents a node in the serialized DAG export (either an item or recipe).
type RecipeGraphNode struct {
	ID      string            `json:"id"`
	Type    string            `json:"type"` // "ITEM" or "RECIPE"
	Label   string            `json:"label"`
	ModID   string            `json:"mod_id,omitempty"`
	Station ProcessingStation `json:"station,omitempty"`
}

// RecipeGraphEdge represents a directed dependency edge in the serialized DAG export.
type RecipeGraphEdge struct {
	From  string `json:"from"`  // Source item ID or recipe ID
	To    string `json:"to"`    // Recipe ID or produced item ID
	Count int    `json:"count"` // Quantity consumed or produced
	Type  string `json:"type"`  // "INPUT" or "OUTPUT"
}

// RecipeGraphExport is the structured JSON envelope produced by ExportRecipeGraph().
type RecipeGraphExport struct {
	Version     int                `json:"version"`
	ModList     []string           `json:"mod_list"`
	RecipeCount int                `json:"recipe_count"`
	Recipes     []*Recipe          `json:"recipes"`
	Nodes       []*RecipeGraphNode `json:"nodes"`
	Edges       []*RecipeGraphEdge `json:"edges"`
}

// CraftingPlanStep represents one resolved execution step in a multi-tier crafting plan.
type CraftingPlanStep struct {
	StepIndex int     `json:"step_index"`
	Recipe    *Recipe `json:"recipe"`
	Runs      int     `json:"runs"`
}

// CraftingPlan contains the ordered recipe execution steps to produce a desired item.
type CraftingPlan struct {
	TargetItemID string              `json:"target_item_id"`
	TargetCount  int                 `json:"target_count"`
	Steps        []*CraftingPlanStep `json:"steps"`
}

// RecipeDAG is an in-memory Directed Acyclic Graph tracking crafting, cooking,
// and fermentation recipe transformations across vanilla and modded ecosystems.
type RecipeDAG struct {
	mu sync.RWMutex

	recipes   map[string]*Recipe
	produces  map[string][]*Recipe // output ItemID -> recipes
	consumes  map[string][]*Recipe // input ItemID -> recipes
	itemEdges map[string]map[string]bool
}

// NewRecipeDAG constructs an empty recipe graph.
func NewRecipeDAG() *RecipeDAG {
	return &RecipeDAG{
		recipes:   make(map[string]*Recipe),
		produces:  make(map[string][]*Recipe),
		consumes:  make(map[string][]*Recipe),
		itemEdges: make(map[string]map[string]bool),
	}
}

// AddRecipe inserts a recipe into the DAG, validating that it does not introduce cycles.
func (d *RecipeDAG) AddRecipe(recipe *Recipe) error {
	if recipe == nil {
		return errors.New("recipe cannot be nil")
	}
	if strings.TrimSpace(recipe.RecipeID) == "" {
		return ErrEmptyRecipeID
	}
	if len(recipe.Inputs) == 0 {
		return errors.New("recipe must have at least one input ingredient")
	}
	if len(recipe.Outputs) == 0 {
		return errors.New("recipe must have at least one output item")
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	// Clone edge map to tentatively check cycle without mutating state
	candidateEdges := make(map[string]map[string]bool, len(d.itemEdges))
	for src, dsts := range d.itemEdges {
		candidateEdges[src] = make(map[string]bool, len(dsts))
		for dItem := range dsts {
			candidateEdges[src][dItem] = true
		}
	}

	// Add tentative item dependency edges: input item -> output item
	for _, in := range recipe.Inputs {
		inID := strings.TrimSpace(in.ItemID)
		if inID == "" {
			continue
		}
		if candidateEdges[inID] == nil {
			candidateEdges[inID] = make(map[string]bool)
		}
		for _, out := range recipe.Outputs {
			outID := strings.TrimSpace(out.ItemID)
			if outID == "" {
				continue
			}
			// Direct self-dependency
			if inID == outID {
				return ErrRecipeCycleDetected
			}
			candidateEdges[inID][outID] = true
		}
	}

	// Cycle detection using DFS
	if hasCycle(candidateEdges) {
		return ErrRecipeCycleDetected
	}

	// Commit state
	recipeCopy := *recipe
	// Defensive copy slices
	recipeCopy.Inputs = make([]IngredientRequirement, len(recipe.Inputs))
	copy(recipeCopy.Inputs, recipe.Inputs)
	recipeCopy.Outputs = make([]RecipeOutput, len(recipe.Outputs))
	copy(recipeCopy.Outputs, recipe.Outputs)

	d.recipes[recipe.RecipeID] = &recipeCopy
	d.itemEdges = candidateEdges

	for _, out := range recipeCopy.Outputs {
		outID := strings.TrimSpace(out.ItemID)
		d.produces[outID] = append(d.produces[outID], &recipeCopy)
	}
	for _, in := range recipeCopy.Inputs {
		inID := strings.TrimSpace(in.ItemID)
		d.consumes[inID] = append(d.consumes[inID], &recipeCopy)
	}

	return nil
}

// hasCycle detects directed cycles in the item adjacency graph via DFS.
func hasCycle(graph map[string]map[string]bool) bool {
	visited := make(map[string]int) // 0: unvisited, 1: visiting, 2: visited

	var dfs func(node string) bool
	dfs = func(node string) bool {
		visited[node] = 1 // visiting
		for neighbor := range graph[node] {
			state := visited[neighbor]
			if state == 1 {
				return true // cycle detected
			}
			if state == 0 {
				if dfs(neighbor) {
					return true
				}
			}
		}
		visited[node] = 2 // done
		return false
	}

	for node := range graph {
		if visited[node] == 0 {
			if dfs(node) {
				return true
			}
		}
	}
	return false
}

// GetRecipe retrieves a recipe by ID.
func (d *RecipeDAG) GetRecipe(recipeID string) (*Recipe, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	r, ok := d.recipes[recipeID]
	return r, ok
}

// RecipesProducing returns all recipes that output the specified item.
func (d *RecipeDAG) RecipesProducing(itemID string) []*Recipe {
	d.mu.RLock()
	defer d.mu.RUnlock()
	list := d.produces[strings.TrimSpace(itemID)]
	result := make([]*Recipe, len(list))
	copy(result, list)
	return result
}

// RecipesConsuming returns all recipes that require the specified input item.
func (d *RecipeDAG) RecipesConsuming(itemID string) []*Recipe {
	d.mu.RLock()
	defer d.mu.RUnlock()
	list := d.consumes[strings.TrimSpace(itemID)]
	result := make([]*Recipe, len(list))
	copy(result, list)
	return result
}

// AllRecipes returns all registered recipes.
func (d *RecipeDAG) AllRecipes() []*Recipe {
	d.mu.RLock()
	defer d.mu.RUnlock()
	result := make([]*Recipe, 0, len(d.recipes))
	for _, r := range d.recipes {
		result = append(result, r)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].RecipeID < result[j].RecipeID
	})
	return result
}

// CanCraft determines whether the provided inventory item counts satisfy all input requirements of recipeID.
func (d *RecipeDAG) CanCraft(recipeID string, inventory map[string]int) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()

	recipe, ok := d.recipes[recipeID]
	if !ok {
		return false
	}
	for _, input := range recipe.Inputs {
		if inventory[input.ItemID] < input.Count {
			return false
		}
	}
	return true
}

// PlanCraftingPath calculates the dependency order of recipe steps needed to synthesize
// a target item from existing available inventory.
func (d *RecipeDAG) PlanCraftingPath(targetItemID string, availableItems map[string]int) (*CraftingPlan, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	targetItemID = strings.TrimSpace(targetItemID)
	if targetItemID == "" {
		return nil, errors.New("target item id cannot be empty")
	}

	// Working copy of inventory
	inv := make(map[string]int, len(availableItems))
	for k, v := range availableItems {
		inv[k] = v
	}

	if inv[targetItemID] > 0 {
		return &CraftingPlan{
			TargetItemID: targetItemID,
			TargetCount:  inv[targetItemID],
			Steps:        nil,
		}, nil
	}

	var steps []*CraftingPlanStep
	stepIdx := 0

	var resolve func(itemID string, needed int) error
	resolve = func(itemID string, needed int) error {
		if inv[itemID] >= needed {
			return nil
		}

		producers := d.produces[itemID]
		if len(producers) == 0 {
			return fmt.Errorf("no recipe produces item %s (needed %d, have %d)", itemID, needed, inv[itemID])
		}

		// Pick the first viable producer
		var chosenRecipe *Recipe
		var outputYield int
		for _, r := range producers {
			for _, out := range r.Outputs {
				if out.ItemID == itemID && out.Count > 0 {
					chosenRecipe = r
					outputYield = out.Count
					break
				}
			}
			if chosenRecipe != nil {
				break
			}
		}

		if chosenRecipe == nil || outputYield <= 0 {
			return fmt.Errorf("unable to determine yield for %s", itemID)
		}

		stillNeeded := needed - inv[itemID]
		runs := (stillNeeded + outputYield - 1) / outputYield

		// Recursively resolve all inputs required for this recipe
		for _, in := range chosenRecipe.Inputs {
			subReq := in.Count * runs
			if err := resolve(in.ItemID, subReq); err != nil {
				return err
			}
			inv[in.ItemID] -= subReq
		}

		// Add produced items to inventory
		for _, out := range chosenRecipe.Outputs {
			inv[out.ItemID] += out.Count * runs
		}

		stepIdx++
		steps = append(steps, &CraftingPlanStep{
			StepIndex: stepIdx,
			Recipe:    chosenRecipe,
			Runs:      runs,
		})
		return nil
	}

	if err := resolve(targetItemID, 1); err != nil {
		return nil, err
	}

	return &CraftingPlan{
		TargetItemID: targetItemID,
		TargetCount:  1,
		Steps:        steps,
	}, nil
}

// ExportRecipeGraph exports the entire in-memory recipe DAG as a structured JSON byte array.
func (d *RecipeDAG) ExportRecipeGraph() ([]byte, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	itemNodes := make(map[string]*RecipeGraphNode)
	recipeNodes := make([]*RecipeGraphNode, 0, len(d.recipes))
	edges := make([]*RecipeGraphEdge, 0)
	modSet := make(map[string]bool)

	allRecipes := make([]*Recipe, 0, len(d.recipes))
	for _, r := range d.recipes {
		allRecipes = append(allRecipes, r)
		if r.ModID != "" {
			modSet[r.ModID] = true
		}

		// Recipe node
		recipeNodeID := "recipe:" + r.RecipeID
		recipeNodes = append(recipeNodes, &RecipeGraphNode{
			ID:      recipeNodeID,
			Type:    "RECIPE",
			Label:   r.DisplayName,
			ModID:   r.ModID,
			Station: r.Station,
		})

		// Input edges: Item -> Recipe
		for _, in := range r.Inputs {
			itemNodeID := "item:" + in.ItemID
			if _, exists := itemNodes[itemNodeID]; !exists {
				modPrefix := ModVanilla
				if parts := strings.Split(in.ItemID, ":"); len(parts) == 2 {
					modPrefix = parts[0]
				}
				itemNodes[itemNodeID] = &RecipeGraphNode{
					ID:    itemNodeID,
					Type:  "ITEM",
					Label: in.ItemID,
					ModID: modPrefix,
				}
			}
			edges = append(edges, &RecipeGraphEdge{
				From:  itemNodeID,
				To:    recipeNodeID,
				Count: in.Count,
				Type:  "INPUT",
			})
		}

		// Output edges: Recipe -> Item
		for _, out := range r.Outputs {
			itemNodeID := "item:" + out.ItemID
			if _, exists := itemNodes[itemNodeID]; !exists {
				modPrefix := ModVanilla
				if parts := strings.Split(out.ItemID, ":"); len(parts) == 2 {
					modPrefix = parts[0]
				}
				itemNodes[itemNodeID] = &RecipeGraphNode{
					ID:    itemNodeID,
					Type:  "ITEM",
					Label: out.ItemID,
					ModID: modPrefix,
				}
			}
			edges = append(edges, &RecipeGraphEdge{
				From:  recipeNodeID,
				To:    itemNodeID,
				Count: out.Count,
				Type:  "OUTPUT",
			})
		}
	}

	nodes := make([]*RecipeGraphNode, 0, len(itemNodes)+len(recipeNodes))
	for _, node := range itemNodes {
		nodes = append(nodes, node)
	}
	nodes = append(nodes, recipeNodes...)

	// Stable sorting for deterministic export
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].ID < nodes[j].ID
	})
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From == edges[j].From {
			return edges[i].To < edges[j].To
		}
		return edges[i].From < edges[j].From
	})
	sort.Slice(allRecipes, func(i, j int) bool {
		return allRecipes[i].RecipeID < allRecipes[j].RecipeID
	})

	mods := make([]string, 0, len(modSet))
	for m := range modSet {
		mods = append(mods, m)
	}
	sort.Strings(mods)

	export := RecipeGraphExport{
		Version:     1,
		ModList:     mods,
		RecipeCount: len(allRecipes),
		Recipes:     allRecipes,
		Nodes:       nodes,
		Edges:       edges,
	}

	return json.MarshalIndent(export, "", "  ")
}

// ExportRecipeGraphJSON returns the exported graph as a formatted JSON string.
func (d *RecipeDAG) ExportRecipeGraphJSON() (string, error) {
	bytes, err := d.ExportRecipeGraph()
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

// DefaultAgrarianRecipeDAG initializes and populates a RecipeDAG covering
// Vanilla, Farmer's Delight, Vinery, and Brewin' and Chewin' staples.
func DefaultAgrarianRecipeDAG() (*RecipeDAG, error) {
	dag := NewRecipeDAG()

	catalog := []*Recipe{
		// ==================== VANILLA STAPLES ====================
		{
			RecipeID:      "minecraft:bread",
			DisplayName:   "Bread",
			ModID:         ModVanilla,
			Station:       StationCraftingTable,
			CookTimeTicks: 0,
			Inputs: []IngredientRequirement{
				{ItemID: "minecraft:wheat", Count: 3},
			},
			Outputs: []RecipeOutput{
				{ItemID: "minecraft:bread", Count: 1},
			},
		},
		{
			RecipeID:      "minecraft:baked_potato",
			DisplayName:   "Baked Potato",
			ModID:         ModVanilla,
			Station:       StationSmoker,
			CookTimeTicks: 100,
			Inputs: []IngredientRequirement{
				{ItemID: "minecraft:potato", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "minecraft:baked_potato", Count: 1},
			},
		},
		{
			RecipeID:      "minecraft:cooked_beef",
			DisplayName:   "Steak",
			ModID:         ModVanilla,
			Station:       StationSmoker,
			CookTimeTicks: 100,
			Inputs: []IngredientRequirement{
				{ItemID: "minecraft:beef", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "minecraft:cooked_beef", Count: 1},
			},
		},
		{
			RecipeID:      "minecraft:cooked_porkchop",
			DisplayName:   "Cooked Porkchop",
			ModID:         ModVanilla,
			Station:       StationSmoker,
			CookTimeTicks: 100,
			Inputs: []IngredientRequirement{
				{ItemID: "minecraft:porkchop", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "minecraft:cooked_porkchop", Count: 1},
			},
		},
		{
			RecipeID:      "minecraft:cooked_mutton",
			DisplayName:   "Cooked Mutton",
			ModID:         ModVanilla,
			Station:       StationSmoker,
			CookTimeTicks: 100,
			Inputs: []IngredientRequirement{
				{ItemID: "minecraft:mutton", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "minecraft:cooked_mutton", Count: 1},
			},
		},
		{
			RecipeID:      "minecraft:cooked_chicken",
			DisplayName:   "Cooked Chicken",
			ModID:         ModVanilla,
			Station:       StationSmoker,
			CookTimeTicks: 100,
			Inputs: []IngredientRequirement{
				{ItemID: "minecraft:chicken", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "minecraft:cooked_chicken", Count: 1},
			},
		},
		{
			RecipeID:      "minecraft:mushroom_stew",
			DisplayName:   "Mushroom Stew",
			ModID:         ModVanilla,
			Station:       StationCraftingTable,
			CookTimeTicks: 0,
			Inputs: []IngredientRequirement{
				{ItemID: "minecraft:red_mushroom", Count: 1},
				{ItemID: "minecraft:brown_mushroom", Count: 1},
				{ItemID: "minecraft:bowl", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "minecraft:mushroom_stew", Count: 1},
			},
		},
		{
			RecipeID:      "minecraft:rabbit_stew",
			DisplayName:   "Rabbit Stew",
			ModID:         ModVanilla,
			Station:       StationCraftingTable,
			CookTimeTicks: 0,
			Inputs: []IngredientRequirement{
				{ItemID: "minecraft:cooked_rabbit", Count: 1},
				{ItemID: "minecraft:carrot", Count: 1},
				{ItemID: "minecraft:baked_potato", Count: 1},
				{ItemID: "minecraft:brown_mushroom", Count: 1},
				{ItemID: "minecraft:bowl", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "minecraft:rabbit_stew", Count: 1},
			},
		},
		{
			RecipeID:      "minecraft:pumpkin_pie",
			DisplayName:   "Pumpkin Pie",
			ModID:         ModVanilla,
			Station:       StationCraftingTable,
			CookTimeTicks: 0,
			Inputs: []IngredientRequirement{
				{ItemID: "minecraft:pumpkin", Count: 1},
				{ItemID: "minecraft:sugar", Count: 1},
				{ItemID: "minecraft:egg", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "minecraft:pumpkin_pie", Count: 1},
			},
		},
		{
			RecipeID:      "minecraft:cake",
			DisplayName:   "Cake",
			ModID:         ModVanilla,
			Station:       StationCraftingTable,
			CookTimeTicks: 0,
			Inputs: []IngredientRequirement{
				{ItemID: "minecraft:milk_bucket", Count: 3},
				{ItemID: "minecraft:sugar", Count: 2},
				{ItemID: "minecraft:egg", Count: 1},
				{ItemID: "minecraft:wheat", Count: 3},
			},
			Outputs: []RecipeOutput{
				{ItemID: "minecraft:cake", Count: 1},
			},
		},
		{
			RecipeID:      "minecraft:golden_carrot",
			DisplayName:   "Golden Carrot",
			ModID:         ModVanilla,
			Station:       StationCraftingTable,
			CookTimeTicks: 0,
			Inputs: []IngredientRequirement{
				{ItemID: "minecraft:gold_nugget", Count: 8},
				{ItemID: "minecraft:carrot", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "minecraft:golden_carrot", Count: 1},
			},
		},
		{
			RecipeID:      "minecraft:golden_apple",
			DisplayName:   "Golden Apple",
			ModID:         ModVanilla,
			Station:       StationCraftingTable,
			CookTimeTicks: 0,
			Inputs: []IngredientRequirement{
				{ItemID: "minecraft:gold_ingot", Count: 8},
				{ItemID: "minecraft:apple", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "minecraft:golden_apple", Count: 1},
			},
		},
		{
			RecipeID:      "minecraft:cookie",
			DisplayName:   "Cookie",
			ModID:         ModVanilla,
			Station:       StationCraftingTable,
			CookTimeTicks: 0,
			Inputs: []IngredientRequirement{
				{ItemID: "minecraft:wheat", Count: 2},
				{ItemID: "minecraft:cocoa_beans", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "minecraft:cookie", Count: 8},
			},
		},

		// ==================== FARMER'S DELIGHT ====================
		{
			RecipeID:      "farmersdelight:wheat_dough",
			DisplayName:   "Wheat Dough",
			ModID:         ModFarmersDelight,
			Station:       StationCraftingTable,
			CookTimeTicks: 0,
			Inputs: []IngredientRequirement{
				{ItemID: "minecraft:wheat", Count: 3},
				{ItemID: "minecraft:water_bucket", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "farmersdelight:wheat_dough", Count: 3},
			},
		},
		{
			RecipeID:      "farmersdelight:raw_pasta",
			DisplayName:   "Raw Pasta",
			ModID:         ModFarmersDelight,
			Station:       StationCuttingBoard,
			CookTimeTicks: 0,
			Inputs: []IngredientRequirement{
				{ItemID: "farmersdelight:wheat_dough", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "farmersdelight:raw_pasta", Count: 2},
			},
		},
		{
			RecipeID:      "farmersdelight:tomato_sauce",
			DisplayName:   "Tomato Sauce",
			ModID:         ModFarmersDelight,
			Station:       StationCookingPot,
			CookTimeTicks: 200,
			Inputs: []IngredientRequirement{
				{ItemID: "farmersdelight:tomato", Count: 2},
				{ItemID: "minecraft:bowl", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "farmersdelight:tomato_sauce", Count: 1},
			},
		},
		{
			RecipeID:      "farmersdelight:vegetable_noodles",
			DisplayName:   "Vegetable Noodles",
			ModID:         ModFarmersDelight,
			Station:       StationCookingPot,
			CookTimeTicks: 200,
			Inputs: []IngredientRequirement{
				{ItemID: "farmersdelight:raw_pasta", Count: 1},
				{ItemID: "farmersdelight:tomato", Count: 1},
				{ItemID: "farmersdelight:onion", Count: 1},
				{ItemID: "minecraft:carrot", Count: 1},
				{ItemID: "minecraft:bowl", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "farmersdelight:vegetable_noodles", Count: 1},
			},
		},
		{
			RecipeID:      "farmersdelight:beef_stew",
			DisplayName:   "Beef Stew",
			ModID:         ModFarmersDelight,
			Station:       StationCookingPot,
			CookTimeTicks: 200,
			Inputs: []IngredientRequirement{
				{ItemID: "minecraft:beef", Count: 1},
				{ItemID: "minecraft:carrot", Count: 1},
				{ItemID: "minecraft:potato", Count: 1},
				{ItemID: "minecraft:bowl", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "farmersdelight:beef_stew", Count: 1},
			},
		},
		{
			RecipeID:      "farmersdelight:shepherds_pie",
			DisplayName:   "Shepherd's Pie",
			ModID:         ModFarmersDelight,
			Station:       StationCookingPot,
			CookTimeTicks: 200,
			Inputs: []IngredientRequirement{
				{ItemID: "minecraft:cooked_mutton", Count: 1},
				{ItemID: "minecraft:baked_potato", Count: 1},
				{ItemID: "farmersdelight:onion", Count: 1},
				{ItemID: "minecraft:bowl", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "farmersdelight:shepherds_pie", Count: 1},
			},
		},
		{
			RecipeID:      "farmersdelight:fried_rice",
			DisplayName:   "Fried Rice",
			ModID:         ModFarmersDelight,
			Station:       StationCookingPot,
			CookTimeTicks: 200,
			Inputs: []IngredientRequirement{
				{ItemID: "farmersdelight:rice", Count: 1},
				{ItemID: "minecraft:egg", Count: 1},
				{ItemID: "minecraft:carrot", Count: 1},
				{ItemID: "farmersdelight:onion", Count: 1},
				{ItemID: "minecraft:bowl", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "farmersdelight:fried_rice", Count: 1},
			},
		},
		{
			RecipeID:      "farmersdelight:dumplings",
			DisplayName:   "Dumplings",
			ModID:         ModFarmersDelight,
			Station:       StationCookingPot,
			CookTimeTicks: 200,
			Inputs: []IngredientRequirement{
				{ItemID: "farmersdelight:wheat_dough", Count: 2},
				{ItemID: "farmersdelight:cabbage", Count: 1},
				{ItemID: "farmersdelight:onion", Count: 1},
				{ItemID: "minecraft:chicken", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "farmersdelight:dumplings", Count: 2},
			},
		},
		{
			RecipeID:      "farmersdelight:stuffed_pumpkin",
			DisplayName:   "Stuffed Pumpkin",
			ModID:         ModFarmersDelight,
			Station:       StationCookingPot,
			CookTimeTicks: 300,
			Inputs: []IngredientRequirement{
				{ItemID: "minecraft:pumpkin", Count: 1},
				{ItemID: "farmersdelight:rice", Count: 1},
				{ItemID: "minecraft:beef", Count: 1},
				{ItemID: "minecraft:carrot", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "farmersdelight:stuffed_pumpkin", Count: 1},
			},
		},
		{
			RecipeID:      "farmersdelight:roast_chicken_block",
			DisplayName:   "Roast Chicken",
			ModID:         ModFarmersDelight,
			Station:       StationCookingPot,
			CookTimeTicks: 300,
			Inputs: []IngredientRequirement{
				{ItemID: "minecraft:chicken", Count: 1},
				{ItemID: "minecraft:carrot", Count: 1},
				{ItemID: "minecraft:potato", Count: 1},
				{ItemID: "farmersdelight:onion", Count: 1},
				{ItemID: "minecraft:bowl", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "farmersdelight:roast_chicken_block", Count: 1},
			},
		},
		{
			RecipeID:      "farmersdelight:cabbage_rolls",
			DisplayName:   "Cabbage Rolls",
			ModID:         ModFarmersDelight,
			Station:       StationCraftingTable,
			CookTimeTicks: 0,
			Inputs: []IngredientRequirement{
				{ItemID: "farmersdelight:cabbage", Count: 1},
				{ItemID: "minecraft:beef", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "farmersdelight:cabbage_rolls", Count: 1},
			},
		},

		// ==================== VINERY ====================
		{
			RecipeID:      "vinery:red_grape_seeds",
			DisplayName:   "Red Grape Seeds",
			ModID:         ModVinery,
			Station:       StationCraftingTable,
			CookTimeTicks: 0,
			Inputs: []IngredientRequirement{
				{ItemID: "vinery:red_grape", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "vinery:red_grape_seeds", Count: 1},
			},
		},
		{
			RecipeID:      "vinery:savanna_grape_seeds",
			DisplayName:   "Savanna Grape Seeds",
			ModID:         ModVinery,
			Station:       StationCraftingTable,
			CookTimeTicks: 0,
			Inputs: []IngredientRequirement{
				{ItemID: "vinery:white_grape", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "vinery:savanna_grape_seeds", Count: 1},
			},
		},
		{
			RecipeID:      "vinery:grape_juice",
			DisplayName:   "Red Grape Juice",
			ModID:         ModVinery,
			Station:       StationWinePress,
			CookTimeTicks: 150,
			Inputs: []IngredientRequirement{
				{ItemID: "vinery:red_grape", Count: 2},
				{ItemID: "minecraft:glass_bottle", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "vinery:grape_juice", Count: 1},
			},
		},
		{
			RecipeID:      "vinery:white_grape_juice",
			DisplayName:   "White Grape Juice",
			ModID:         ModVinery,
			Station:       StationWinePress,
			CookTimeTicks: 150,
			Inputs: []IngredientRequirement{
				{ItemID: "vinery:white_grape", Count: 2},
				{ItemID: "minecraft:glass_bottle", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "vinery:white_grape_juice", Count: 1},
			},
		},
		{
			RecipeID:      "vinery:claret_wine",
			DisplayName:   "Claret Wine",
			ModID:         ModVinery,
			Station:       StationFermentationBarrel,
			CookTimeTicks: 400,
			Inputs: []IngredientRequirement{
				{ItemID: "vinery:grape_juice", Count: 1},
				{ItemID: "minecraft:sweet_berries", Count: 1},
				{ItemID: "vinery:wine_bottle", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "vinery:claret_wine", Count: 1},
			},
		},
		{
			RecipeID:      "vinery:noir_wine",
			DisplayName:   "Noir Wine",
			ModID:         ModVinery,
			Station:       StationFermentationBarrel,
			CookTimeTicks: 400,
			Inputs: []IngredientRequirement{
				{ItemID: "vinery:grape_juice", Count: 2},
				{ItemID: "vinery:wine_bottle", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "vinery:noir_wine", Count: 1},
			},
		},
		{
			RecipeID:      "vinery:apple_wine",
			DisplayName:   "Apple Wine",
			ModID:         ModVinery,
			Station:       StationFermentationBarrel,
			CookTimeTicks: 400,
			Inputs: []IngredientRequirement{
				{ItemID: "minecraft:apple", Count: 2},
				{ItemID: "vinery:wine_bottle", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "vinery:apple_wine", Count: 1},
			},
		},
		{
			RecipeID:      "vinery:solaris_wine",
			DisplayName:   "Solaris Wine",
			ModID:         ModVinery,
			Station:       StationFermentationBarrel,
			CookTimeTicks: 400,
			Inputs: []IngredientRequirement{
				{ItemID: "vinery:white_grape_juice", Count: 2},
				{ItemID: "vinery:wine_bottle", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "vinery:solaris_wine", Count: 1},
			},
		},

		// ==================== BREWIN' AND CHEWIN' ====================
		{
			RecipeID:      "brewinandchewin:beer",
			DisplayName:   "Beer",
			ModID:         ModBrewinAndChewin,
			Station:       StationKeg,
			CookTimeTicks: 500,
			Inputs: []IngredientRequirement{
				{ItemID: "minecraft:wheat", Count: 2},
				{ItemID: "minecraft:water_bucket", Count: 1},
				{ItemID: "minecraft:glass_bottle", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "brewinandchewin:beer", Count: 1},
			},
		},
		{
			RecipeID:      "brewinandchewin:mead",
			DisplayName:   "Mead",
			ModID:         ModBrewinAndChewin,
			Station:       StationKeg,
			CookTimeTicks: 500,
			Inputs: []IngredientRequirement{
				{ItemID: "minecraft:honey_bottle", Count: 2},
				{ItemID: "minecraft:water_bucket", Count: 1},
				{ItemID: "minecraft:glass_bottle", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "brewinandchewin:mead", Count: 1},
			},
		},
		{
			RecipeID:      "brewinandchewin:cheese_wheel",
			DisplayName:   "Cheese Wheel",
			ModID:         ModBrewinAndChewin,
			Station:       StationKeg,
			CookTimeTicks: 600,
			Inputs: []IngredientRequirement{
				{ItemID: "minecraft:milk_bucket", Count: 3},
				{ItemID: "minecraft:brown_mushroom", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "brewinandchewin:cheese_wheel", Count: 1},
			},
		},
		{
			RecipeID:      "brewinandchewin:fermented_salad",
			DisplayName:   "Fermented Salad",
			ModID:         ModBrewinAndChewin,
			Station:       StationKeg,
			CookTimeTicks: 400,
			Inputs: []IngredientRequirement{
				{ItemID: "farmersdelight:cabbage", Count: 2},
				{ItemID: "farmersdelight:onion", Count: 1},
				{ItemID: "minecraft:bowl", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "brewinandchewin:fermented_salad", Count: 1},
			},
		},
		{
			RecipeID:      "brewinandchewin:beef_jerky",
			DisplayName:   "Beef Jerky",
			ModID:         ModBrewinAndChewin,
			Station:       StationSmoker,
			CookTimeTicks: 150,
			Inputs: []IngredientRequirement{
				{ItemID: "minecraft:beef", Count: 1},
			},
			Outputs: []RecipeOutput{
				{ItemID: "brewinandchewin:beef_jerky", Count: 2},
			},
		},
	}

	for _, recipe := range catalog {
		if err := dag.AddRecipe(recipe); err != nil {
			return nil, fmt.Errorf("loading recipe %s: %w", recipe.RecipeID, err)
		}
	}

	return dag, nil
}
