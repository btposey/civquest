package system1

import (
	"fmt"
	"math"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
)

// SurvivalKitEvaluator evaluates survival bootstrap inventory items, basic tools,
// materials, and bootstrap cooldowns into the FactGraph.
type SurvivalKitEvaluator struct{}

// NewSurvivalKitEvaluator creates a new SurvivalKitEvaluator instance.
func NewSurvivalKitEvaluator() *SurvivalKitEvaluator {
	return &SurvivalKitEvaluator{}
}

func (e *SurvivalKitEvaluator) Name() string {
	return "survival_kit"
}

func (e *SurvivalKitEvaluator) Category() string {
	return "bootstrap"
}

func (e *SurvivalKitEvaluator) Evaluate(ctx *EvaluatorContext, graph *FactGraph) error {
	if graph == nil {
		return nil
	}

	var inv bootstrapInventorySummary
	if ctx != nil && ctx.Frame != nil && ctx.Frame.Status != nil {
		inv = inspectBootstrapInventory(ctx.Frame.Status.Inventory)
	}

	hasKit := inv.hasAxe && (inv.hasSword || inv.hasWeapon) && inv.hasLight
	hasAxe := inv.hasAxe
	hasSword := inv.hasSword
	hasWeapon := inv.hasWeapon
	hasLight := inv.hasLight
	logCount := int(inv.logCount)
	plankCount := int(inv.planksCount)
	cobblestoneCount := int(inv.cobbleCount)
	hasCraftingTable := inv.hasCraftingTable

	needsStoneUpgrade := !inv.hasStoneGear && inv.cobbleCount >= 3 && (inv.logCount >= 1 || inv.planksCount >= 1 || inv.stickCount >= 1)
	needsWoodenTools := (!inv.hasAxe || !inv.hasSword)
	needsTools := needsWoodenTools || needsStoneUpgrade

	onCooldown := false
	if ctx != nil && ctx.State != nil && ctx.Frame != nil {
		onCooldown = ctx.State.IsBootstrapOnCooldown(ctx.Frame.WorldTick)
	}

	graph.SetBool("bootstrap.has_kit", "bootstrap", hasKit, fmt.Sprintf("Survival kit complete: %t", hasKit))
	graph.SetBool("bootstrap.has_axe", "bootstrap", hasAxe, fmt.Sprintf("Has axe: %t", hasAxe))
	graph.SetBool("bootstrap.has_sword", "bootstrap", hasSword, fmt.Sprintf("Has sword: %t", hasSword))
	graph.SetBool("bootstrap.has_weapon", "bootstrap", hasWeapon, fmt.Sprintf("Has weapon: %t", hasWeapon))
	graph.SetBool("bootstrap.has_light", "bootstrap", hasLight, fmt.Sprintf("Has light source: %t", hasLight))
	graph.SetInt("bootstrap.log_count", "bootstrap", logCount, fmt.Sprintf("Log count: %d", logCount))
	graph.SetInt("bootstrap.plank_count", "bootstrap", plankCount, fmt.Sprintf("Plank count: %d", plankCount))
	graph.SetInt("bootstrap.cobblestone_count", "bootstrap", cobblestoneCount, fmt.Sprintf("Cobblestone count: %d", cobblestoneCount))
	graph.SetBool("bootstrap.has_crafting_table", "bootstrap", hasCraftingTable, fmt.Sprintf("Has crafting table: %t", hasCraftingTable))
	graph.SetBool("bootstrap.needs_tools", "bootstrap", needsTools, fmt.Sprintf("Needs tools: %t", needsTools))
	graph.SetBool("bootstrap.on_cooldown", "bootstrap", onCooldown, fmt.Sprintf("Bootstrap on cooldown: %t", onCooldown))

	return nil
}

// ShelterEvaluator evaluates forward outpost shelter construction progress,
// block placement, bed assignment, campfire/light proximity, and cooldowns into the FactGraph.
type ShelterEvaluator struct{}

// NewShelterEvaluator creates a new ShelterEvaluator instance.
func NewShelterEvaluator() *ShelterEvaluator {
	return &ShelterEvaluator{}
}

func (e *ShelterEvaluator) Name() string {
	return "shelter"
}

func (e *ShelterEvaluator) Category() string {
	return "shelter"
}

func (e *ShelterEvaluator) Evaluate(ctx *EvaluatorContext, graph *FactGraph) error {
	if graph == nil {
		return nil
	}

	blocksPlaced := 0
	buildIndex := 0
	onCooldown := false
	if ctx != nil && ctx.State != nil {
		blocksPlaced = ctx.State.GetShelterBlocksPlaced()
		buildIndex = ctx.State.GetShelterBuildIndex()
		if ctx.Frame != nil {
			onCooldown = ctx.State.IsShelterOnCooldown(ctx.Frame.WorldTick)
		}
	}

	totalBlocks := blueprints.CabinTotalBlocks
	complete := (blocksPlaced >= totalBlocks || buildIndex >= totalBlocks)

	hasCampfire := false
	if ctx != nil && ctx.Frame != nil {
		if ctx.Frame.Status != nil && ctx.Frame.Status.Position != nil {
			pos := ctx.Frame.Status.Position
			for _, b := range ctx.Frame.InterestingBlocks {
				if b != nil && b.Pos != nil {
					id := strings.ToLower(b.BlockId)
					if strings.Contains(id, "campfire") || strings.Contains(id, "torch") || strings.Contains(id, "lantern") {
						if math.Hypot(pos.X-float64(b.Pos.X), pos.Z-float64(b.Pos.Z)) <= 8.0 {
							hasCampfire = true
							break
						}
					}
				}
			}
		} else {
			for _, b := range ctx.Frame.InterestingBlocks {
				if b != nil {
					id := strings.ToLower(b.BlockId)
					if strings.Contains(id, "campfire") || strings.Contains(id, "torch") || strings.Contains(id, "lantern") {
						hasCampfire = true
						break
					}
				}
			}
		}

		if !hasCampfire && ctx.Frame.Status != nil {
			for _, it := range ctx.Frame.Status.Inventory {
				if it != nil && it.Count > 0 {
					id := strings.ToLower(it.ItemId)
					if strings.Contains(id, "campfire") || strings.Contains(id, "torch") || strings.Contains(id, "lantern") {
						hasCampfire = true
						break
					}
				}
			}
		}
	}

	hasBed := false
	if ctx != nil {
		if ctx.State != nil && ctx.State.GetAssignedBed() != nil {
			hasBed = true
		}
		if !hasBed && ctx.Frame != nil {
			if ctx.Frame.Status != nil && ctx.Frame.Status.Position != nil {
				pos := ctx.Frame.Status.Position
				for _, b := range ctx.Frame.InterestingBlocks {
					if b != nil && b.Pos != nil && strings.Contains(strings.ToLower(b.BlockId), "bed") {
						if math.Hypot(pos.X-float64(b.Pos.X), pos.Z-float64(b.Pos.Z)) <= 16.0 {
							hasBed = true
							break
						}
					}
				}
			}
			if !hasBed && ctx.Frame.Status != nil {
				for _, it := range ctx.Frame.Status.Inventory {
					if it != nil && it.Count > 0 && strings.Contains(strings.ToLower(it.ItemId), "bed") {
						hasBed = true
						break
					}
				}
			}
		}
	}

	graph.SetBool("shelter.complete", "shelter", complete, fmt.Sprintf("Shelter complete: %t", complete))
	graph.SetInt("shelter.blocks_placed", "shelter", blocksPlaced, fmt.Sprintf("Blocks placed: %d", blocksPlaced))
	graph.SetInt("shelter.total_blocks", "shelter", totalBlocks, fmt.Sprintf("Total blocks required: %d", totalBlocks))
	graph.SetBool("shelter.on_cooldown", "shelter", onCooldown, fmt.Sprintf("Shelter on cooldown: %t", onCooldown))
	graph.SetBool("shelter.has_campfire", "shelter", hasCampfire, fmt.Sprintf("Has campfire/light: %t", hasCampfire))
	graph.SetBool("shelter.has_bed", "shelter", hasBed, fmt.Sprintf("Has assigned/available bed: %t", hasBed))

	return nil
}
