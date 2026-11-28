package system1

import (
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// Armory target categories.
const (
	CategoryWeapons = "WEAPONS"
	CategoryArmor   = "ARMOR"
	CategoryArrows  = "ARROWS"
	CategoryTNT     = "TNT"
)

// Default baseline armory quotas.
const (
	DefaultQuotaWeapons = 2
	DefaultQuotaArmor   = 4
	DefaultQuotaArrows  = 10
	DefaultQuotaTNT     = 4
)

// ArmoryQuotas encapsulates settlement militia outfit targets scaled by population N.
// Per specification:
// - 1 complete set of armor for each individual (Helmets: N, Chestplates: N, Leggings: N, Boots: N)
// - 1 sword and 1 bow for each individual (Swords: N, Bows: N)
// - 10 arrows per bow (Arrows: 10 * N)
// - Dynamite/TNT reserve stockpile: max(4, N)
type ArmoryQuotas struct {
	Population  int
	Swords      int
	Bows        int
	Helmets     int
	Chestplates int
	Leggings    int
	Boots       int
	Arrows      int
	TNT         int
}

// ComputeArmoryQuotas computes the armory requirements for a given settlement population N.
// If population <= 0, defaults to 1.
func ComputeArmoryQuotas(population int) ArmoryQuotas {
	if population <= 0 {
		population = 1
	}
	tntQuota := 4
	if population > tntQuota {
		tntQuota = population
	}
	return ArmoryQuotas{
		Population:  population,
		Swords:      population,
		Bows:        population,
		Helmets:     population,
		Chestplates: population,
		Leggings:    population,
		Boots:       population,
		Arrows:      10 * population,
		TNT:         tntQuota,
	}
}

// DetermineSettlementPopulation extracts the active settler population from the civic registry.
// Defaults to at least 1 if the registry is nil or has <= 0 registered settlers.
func DetermineSettlementPopulation(civicDB *db.CivicRegistryDB) int {
	if civicDB == nil {
		return 1
	}
	count, err := civicDB.GetSettlementPopulation()
	if err != nil || count <= 0 {
		return 1
	}
	return count
}

// ArmoryStockSummary aggregates carried and granary armory assets.
type ArmoryStockSummary struct {
	Swords      int
	Bows        int
	Shields     int
	Helmets     int
	Chestplates int
	Leggings    int
	Boots       int
	Arrows      int
	TNT         int
}

// TotalWeapons returns the total weapons available (swords, bows, and shields).
func (s ArmoryStockSummary) TotalWeapons() int {
	return s.Swords + s.Bows + s.Shields
}

// TotalArmor returns the total armor pieces available across all slots.
func (s ArmoryStockSummary) TotalArmor() int {
	return s.Helmets + s.Chestplates + s.Leggings + s.Boots
}

// ScanArmoryStock aggregates weapons, armor, arrows, and TNT across the agent's carried inventory
// and any civic registry settlement granary stock.
func ScanArmoryStock(inventory []*civquestv1.ItemStackProto, civicDB *db.CivicRegistryDB) ArmoryStockSummary {
	var summary ArmoryStockSummary

	// 1. Carried inventory
	for _, it := range inventory {
		if it == nil || it.Count <= 0 || it.ItemId == "" {
			continue
		}
		classifyArmoryItem(it.ItemId, int(it.Count), &summary)
	}

	// 2. Settlement granary stock from civic registry
	if civicDB != nil {
		if stocks, err := civicDB.GetGranaryInventory(); err == nil {
			for _, st := range stocks {
				if st == nil || st.Quantity <= 0 || st.ItemID == "" {
					continue
				}
				classifyArmoryItem(st.ItemID, st.Quantity, &summary)
			}
		}
	}

	return summary
}

func classifyArmoryItem(itemID string, count int, s *ArmoryStockSummary) {
	id := strings.ToLower(strings.TrimSpace(itemID))
	switch {
	case strings.Contains(id, "arrow"):
		s.Arrows += count
	case strings.Contains(id, "tnt"):
		s.TNT += count
	case strings.HasSuffix(id, "_sword") || id == "minecraft:sword":
		s.Swords += count
	case strings.HasSuffix(id, "crossbow") || strings.HasSuffix(id, "bow") || id == "minecraft:bow":
		s.Bows += count
	case strings.HasSuffix(id, "shield") || id == "minecraft:shield":
		s.Shields += count
	case strings.HasSuffix(id, "_helmet") || strings.HasSuffix(id, "_cap"):
		s.Helmets += count
	case strings.HasSuffix(id, "_chestplate") || strings.HasSuffix(id, "_tunic"):
		s.Chestplates += count
	case strings.HasSuffix(id, "_leggings") || strings.HasSuffix(id, "_pants"):
		s.Leggings += count
	case strings.HasSuffix(id, "_boots"):
		s.Boots += count
	}
}

// CheckArmorySufficiency inspects carried inventory and civic registry granary stocks against
// settlement armory quotas. Returns isSufficient=true if all quotas are satisfied.
// If insufficient, returns isSufficient=false and the primary neededCategory.
func CheckArmorySufficiency(inventory []*civquestv1.ItemStackProto, civicDB *db.CivicRegistryDB) (bool, string) {
	pop := DetermineSettlementPopulation(civicDB)
	quotas := ComputeArmoryQuotas(pop)
	return CheckArmorySufficiencyWithQuotas(inventory, civicDB, quotas)
}

// CheckArmorySufficiencyWithQuotas inspects stock against explicit armory quotas.
func CheckArmorySufficiencyWithQuotas(inventory []*civquestv1.ItemStackProto, civicDB *db.CivicRegistryDB, quotas ArmoryQuotas) (bool, string) {
	stock := ScanArmoryStock(inventory, civicDB)

	// 1. Weapons: swords >= N, bows >= N, or total weapons >= 2*N
	if stock.TotalWeapons() < (quotas.Swords+quotas.Bows) || (stock.Swords < quotas.Swords && stock.Bows < quotas.Bows) {
		return false, CategoryWeapons
	}

	// 2. Armor: complete sets (helmets >= N, chestplates >= N, leggings >= N, boots >= N)
	// or sufficient total armor pieces (>= 4*N)
	hasCompleteSets := stock.Helmets >= quotas.Helmets && stock.Chestplates >= quotas.Chestplates &&
		stock.Leggings >= quotas.Leggings && stock.Boots >= quotas.Boots
	hasSufficientArmorPieces := stock.TotalArmor() >= 4*quotas.Population
	if !hasCompleteSets && !hasSufficientArmorPieces {
		return false, CategoryArmor
	}

	// 3. Arrows: 10 * N
	if stock.Arrows < quotas.Arrows {
		return false, CategoryArrows
	}

	// 4. Dynamite/TNT: reserve stockpile max(4, N)
	if stock.TNT < quotas.TNT {
		return false, CategoryTNT
	}

	return true, ""
}

type armoryMaterialsSummary struct {
	ironCount          int32
	cobbleCount        int32
	stickCount         int32
	planksCount        int32
	featherCount       int32
	flintCount         int32
	gunpowderCount     int32
	sandCount          int32
	leatherCount       int32
	stringCount        int32
	craftingTableCount int32
	logCount           int32
}

func inspectArmoryMaterials(inventory []*civquestv1.ItemStackProto) armoryMaterialsSummary {
	var m armoryMaterialsSummary
	for _, it := range inventory {
		if it == nil || it.Count <= 0 || it.ItemId == "" {
			continue
		}
		id := strings.ToLower(strings.TrimSpace(it.ItemId))
		switch {
		case strings.Contains(id, "iron_ingot") || id == "iron" || strings.HasSuffix(id, ":iron_ingot"):
			m.ironCount += it.Count
		case strings.Contains(id, "cobblestone") || strings.Contains(id, "cobbled_deepslate") || strings.Contains(id, "blackstone"):
			m.cobbleCount += it.Count
		case id == "minecraft:stick" || strings.HasSuffix(id, ":stick") || id == "stick":
			m.stickCount += it.Count
		case strings.HasSuffix(id, "_planks") || strings.Contains(id, "planks"):
			m.planksCount += it.Count
		case strings.Contains(id, "feather"):
			m.featherCount += it.Count
		case strings.Contains(id, "flint"):
			m.flintCount += it.Count
		case strings.Contains(id, "gunpowder") || strings.Contains(id, "sulfur"):
			m.gunpowderCount += it.Count
		case strings.Contains(id, "sand"):
			m.sandCount += it.Count
		case strings.Contains(id, "leather"):
			m.leatherCount += it.Count
		case strings.Contains(id, "string"):
			m.stringCount += it.Count
		case strings.Contains(id, "crafting_table"):
			m.craftingTableCount += it.Count
		case strings.HasSuffix(id, "_log") || strings.HasSuffix(id, "_wood") || strings.HasSuffix(id, "_stem"):
			m.logCount += it.Count
		}
	}
	return m
}

// EvaluateArmoryMaintenance evaluates off-watch armory quotas, crafting materials, and crafting workstations
// to emit crafting, navigation, or table placement intents.
// Returns nil if all armory quotas are satisfied, or if no materials exist for any unmet quota.
func EvaluateArmoryMaintenance(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	cfg *xdg.AgentIdentityConfig,
	state *AgentState,
	civicDB *db.CivicRegistryDB,
	walk *walkLocal,
) *civquestv1.AgentIntent {
	if frame == nil || frame.Status == nil || frame.Status.Position == nil || state == nil {
		return nil
	}
	if frame.Status.IsSleeping {
		return nil
	}

	// 1. Quota sufficiency check across carried and granary inventory
	isSufficient, _ := CheckArmorySufficiency(frame.Status.Inventory, civicDB)
	if isSufficient {
		state.SetArmoryMaintenanceNeeded(false)
		return nil
	}

	pop := DetermineSettlementPopulation(civicDB)
	quotas := ComputeArmoryQuotas(pop)
	stock := ScanArmoryStock(frame.Status.Inventory, civicDB)

	needsWeapons := stock.TotalWeapons() < (quotas.Swords+quotas.Bows) || (stock.Swords < quotas.Swords && stock.Bows < quotas.Bows)
	hasCompleteArmor := stock.Helmets >= quotas.Helmets && stock.Chestplates >= quotas.Chestplates &&
		stock.Leggings >= quotas.Leggings && stock.Boots >= quotas.Boots
	needsArmor := !hasCompleteArmor && stock.TotalArmor() < 4*quotas.Population
	needsArrows := stock.Arrows < quotas.Arrows
	needsTNT := stock.TNT < quotas.TNT

	mats := inspectArmoryMaterials(frame.Status.Inventory)

	// Material check per category
	canCraftArrows := mats.featherCount >= 1 && mats.stickCount >= 1 && mats.flintCount >= 1
	canCraftShield := mats.planksCount >= 1 && mats.ironCount >= 1
	canCraftIronSword := mats.ironCount >= 1 && mats.stickCount >= 1
	canCraftStoneSword := mats.cobbleCount >= 1 && mats.stickCount >= 1
	canCraftBow := mats.stringCount >= 3 && mats.stickCount >= 3
	canCraftWeapons := canCraftShield || canCraftIronSword || canCraftStoneSword || canCraftBow
	canCraftArmor := mats.ironCount >= 1 || mats.leatherCount >= 1
	canCraftTNT := mats.gunpowderCount >= 1 && mats.sandCount >= 1

	// Select recipe for first unmet category with available materials
	var recipeToCraft string
	if needsArrows && canCraftArrows {
		recipeToCraft = "minecraft:arrow"
	} else if needsWeapons && canCraftWeapons {
		if canCraftShield {
			recipeToCraft = "minecraft:shield"
		} else if canCraftIronSword {
			recipeToCraft = "minecraft:iron_sword"
		} else if canCraftStoneSword {
			recipeToCraft = "minecraft:stone_sword"
		} else if canCraftBow {
			recipeToCraft = "minecraft:bow"
		}
	} else if needsArmor && canCraftArmor {
		if mats.ironCount >= 1 {
			switch {
			case mats.ironCount >= 8:
				recipeToCraft = "minecraft:iron_chestplate"
			case mats.ironCount >= 7:
				recipeToCraft = "minecraft:iron_leggings"
			case mats.ironCount >= 5:
				recipeToCraft = "minecraft:iron_helmet"
			default:
				recipeToCraft = "minecraft:iron_boots"
			}
		} else if mats.leatherCount >= 1 {
			switch {
			case mats.leatherCount >= 8:
				recipeToCraft = "minecraft:leather_chestplate"
			case mats.leatherCount >= 7:
				recipeToCraft = "minecraft:leather_leggings"
			case mats.leatherCount >= 5:
				recipeToCraft = "minecraft:leather_helmet"
			default:
				recipeToCraft = "minecraft:leather_boots"
			}
		}
	} else if needsTNT && canCraftTNT {
		recipeToCraft = "minecraft:tnt"
	}

	// If no materials exist for any unmet quota, allow fallback to secondary hobby
	if recipeToCraft == "" {
		state.SetArmoryMaintenanceNeeded(false)
		return nil
	}

	// 2. Cooldown check: max 1 crafting intent every 20 ticks
	lastCraft := state.GetLastCraftTick()
	canCraft := lastCraft == 0 || frame.WorldTick < lastCraft || frame.WorldTick-lastCraft >= 20
	if !canCraft {
		return nil
	}

	pos := frame.Status.Position

	// 3. Find the nearest usable crafting table, in view or recalled (ADR-022)
	state.mu.Lock()
	nearestCraftingTable, recalledTablePOI, bestTableDistSq := findNearestCraftingTableLocked(frame, pos, state)
	state.mu.Unlock()

	if nearestCraftingTable != nil {
		if isCraftingTableWithinReach(nearestCraftingTable, bestTableDistSq, pos) {
			// Craft at reachable table (<= 4.5 blocks reach, same level)
			state.SetLastCraftTick(frame.WorldTick)
			return &civquestv1.AgentIntent{
				AgentId: agentID,
				Action: &civquestv1.AgentIntent_CraftItem{
					CraftItem: &civquestv1.CraftItemIntent{
						RecipeId:       recipeToCraft,
						WorkstationPos: nearestCraftingTable.Pos,
						BatchCount:     1,
					},
				},
			}
		}

		// Navigate to distant table if reachable and not already pathing
		navState := frame.Status.NavigationState
		if navState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK {
			state.MarkWorkstationUnreachable(nearestCraftingTable.Pos.X, nearestCraftingTable.Pos.Y, nearestCraftingTable.Pos.Z, frame.WorldTick)
			if recalledTablePOI != nil {
				if si := state.SpatialIndex(); si != nil {
					si.DecayPOIReliability(recalledTablePOI.POIID, 0.3)
				}
			}
			return nil
		}
		if navState != civquestv1.NavigationState_NAVIGATION_STATE_PATHING {
			target := &civquestv1.Vec3{
				X: float64(nearestCraftingTable.Pos.X) + 0.5,
				Y: float64(nearestCraftingTable.Pos.Y),
				Z: float64(nearestCraftingTable.Pos.Z) + 0.5,
			}
			// From the spot the table was seen from, walk to the table itself.
			if recalledTablePOI != nil && !poiWalkOver(frame.Status.Position, recalledTablePOI) {
				target = poiAccessTarget(recalledTablePOI)
			}
			return &civquestv1.AgentIntent{
				AgentId: agentID,
				Action: &civquestv1.AgentIntent_Navigate{
					Navigate: &civquestv1.NavigateIntent{
						Target:           target,
						SpeedMultiplier:  1.0,
						ArrivalTolerance: 2,
					},
				},
			}
		}
		return nil
	}

	// 4. Place crafting table from inventory if available
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.stationRefusedLocked(craftingTableItem, frame) {
		return nil
	}
	if mats.craftingTableCount > 0 {
		in, _ := state.placeStationLocked(agentID, craftingTableItem, frame, walk)
		return in
	}

	// Direct crafting fallback (portable 2x2, settlement workshop, or testing environment)
	state.mem.lastCraftTick = frame.WorldTick
	return &civquestv1.AgentIntent{
		AgentId: agentID,
		Action: &civquestv1.AgentIntent_CraftItem{
			CraftItem: &civquestv1.CraftItemIntent{
				RecipeId:   recipeToCraft,
				BatchCount: 1,
			},
		},
	}
}
