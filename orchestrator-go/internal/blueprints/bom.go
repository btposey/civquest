package blueprints

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
)

// NormalizeItemID strips block state properties and normalizes the namespace.
// For example: "minecraft:oak_stairs[facing=north,half=bottom]" -> "minecraft:oak_stairs"
// "stone" -> "minecraft:stone"
func NormalizeItemID(raw string) string {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return "minecraft:air"
	}
	bracketIdx := strings.IndexByte(clean, '[')
	if bracketIdx != -1 {
		clean = strings.TrimSpace(clean[:bracketIdx])
	}
	if !strings.Contains(clean, ":") {
		clean = "minecraft:" + clean
	}
	return clean
}

// BOMItemStatus details the demand, stock, shortage, and surplus for a single material item.
type BOMItemStatus struct {
	ItemID    string `json:"item_id"`
	Required  int    `json:"required"`
	Available int    `json:"available"`
	Missing   int    `json:"missing"`
	Surplus   int    `json:"surplus"`
}

// BOMReport summarizes the full material breakdown of a construction blueprint against inventory/granary stock.
type BOMReport struct {
	Required           map[string]int           `json:"required"`
	Available          map[string]int           `json:"available"`
	Missing            map[string]int           `json:"missing"`
	Surplus            map[string]int           `json:"surplus"`
	Items              map[string]BOMItemStatus `json:"items"`
	IsSatisfied        bool                     `json:"is_satisfied"`
	TotalRequired      int                      `json:"total_required"`
	TotalAvailable     int                      `json:"total_available"`
	TotalMissing       int                      `json:"total_missing"`
	FulfillmentPercent float64                  `json:"fulfillment_percent"`
}

// ComputeBOMReport compares required materials against available inventory stock.
// Detects missing shortages and surplus supplies. Both required and inventory keys can be
// exact block state strings or normalized item IDs.
func ComputeBOMReport(required map[string]int, inventoryStock map[string]int) *BOMReport {
	// Normalize required map: item_id -> total count
	normalizedRequired := make(map[string]int)
	for k, count := range required {
		if count <= 0 {
			continue
		}
		item := NormalizeItemID(k)
		if isAir(item) {
			continue
		}
		normalizedRequired[item] += count
	}

	// Normalize available inventory map: item_id -> total count
	normalizedAvailable := make(map[string]int)
	for k, count := range inventoryStock {
		if count <= 0 {
			continue
		}
		item := NormalizeItemID(k)
		normalizedAvailable[item] += count
	}

	report := &BOMReport{
		Required:  make(map[string]int),
		Available: make(map[string]int),
		Missing:   make(map[string]int),
		Surplus:   make(map[string]int),
		Items:     make(map[string]BOMItemStatus),
	}

	totalReq := 0
	totalAvail := 0
	totalMissing := 0
	fulfilledReq := 0

	// Check all required items
	for item, reqQty := range normalizedRequired {
		availQty := normalizedAvailable[item]
		missing := 0
		surplus := 0

		if availQty >= reqQty {
			surplus = availQty - reqQty
			fulfilledReq += reqQty
		} else {
			missing = reqQty - availQty
			fulfilledReq += availQty
		}

		report.Required[item] = reqQty
		report.Available[item] = availQty
		if missing > 0 {
			report.Missing[item] = missing
		}
		if surplus > 0 {
			report.Surplus[item] = surplus
		}

		report.Items[item] = BOMItemStatus{
			ItemID:    item,
			Required:  reqQty,
			Available: availQty,
			Missing:   missing,
			Surplus:   surplus,
		}

		totalReq += reqQty
		totalAvail += availQty
		totalMissing += missing
	}

	// Also detect any inventory items that have 0 required (pure surplus)
	for item, availQty := range normalizedAvailable {
		if _, needed := normalizedRequired[item]; !needed && availQty > 0 {
			report.Available[item] = availQty
			report.Surplus[item] = availQty
			report.Items[item] = BOMItemStatus{
				ItemID:    item,
				Required:  0,
				Available: availQty,
				Missing:   0,
				Surplus:   availQty,
			}
			totalAvail += availQty
		}
	}

	report.TotalRequired = totalReq
	report.TotalAvailable = totalAvail
	report.TotalMissing = totalMissing
	report.IsSatisfied = (totalMissing == 0)

	if totalReq == 0 {
		report.FulfillmentPercent = 100.0
	} else {
		pct := (float64(fulfilledReq) / float64(totalReq)) * 100.0
		report.FulfillmentPercent = math.Round(pct*100) / 100
	}

	return report
}

// ComputeBOMAgainstGranary computes the BOMReport for required materials against the live settlement granary stock.
func ComputeBOMAgainstGranary(ctx context.Context, required map[string]int, granary *db.CivicRegistryDB) (*BOMReport, error) {
	if granary == nil {
		return ComputeBOMReport(required, nil), nil
	}

	inventory, err := granary.GetGranaryInventoryContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading granary inventory: %w", err)
	}

	stockMap := make(map[string]int, len(inventory))
	for _, stock := range inventory {
		stockMap[stock.ItemID] = stock.Available()
	}

	return ComputeBOMReport(required, stockMap), nil
}

// ComputeBlueprintBOMAgainstGranary calculates the BOM of a blueprint and compares it to live granary stock.
func ComputeBlueprintBOMAgainstGranary(ctx context.Context, bp *Blueprint, granary *db.CivicRegistryDB) (*BOMReport, error) {
	if bp == nil {
		return nil, ErrNilBlueprint
	}
	return ComputeBOMAgainstGranary(ctx, bp.CalculateBOM(), granary)
}

// GatheringQuota assigns a resource collection target for a settler agent.
type GatheringQuota struct {
	QuotaID          string `json:"quota_id"`
	ProjectID        string `json:"project_id"`
	ItemID           string `json:"item_id"`
	TargetQuantity   int    `json:"target_quantity"`
	GatheredQuantity int    `json:"gathered_quantity"`
	AssignedAgentID  string `json:"assigned_agent_id,omitempty"`
	Priority         int    `json:"priority"` // 1 (lowest) - 10 (highest)
}

// GenerateGatheringQuotas produces actionable resource collection tasks for missing materials.
// If activeSettlers are provided, quotas are assigned in round-robin fashion.
func GenerateGatheringQuotas(projectID string, missing map[string]int, activeSettlers []string) []GatheringQuota {
	if len(missing) == 0 {
		return nil
	}

	// Sort item IDs for deterministic output
	sortedItems := make([]string, 0, len(missing))
	for item := range missing {
		if missing[item] > 0 {
			sortedItems = append(sortedItems, item)
		}
	}
	sort.Strings(sortedItems)

	quotas := make([]GatheringQuota, 0, len(sortedItems))
	for idx, item := range sortedItems {
		qty := missing[item]
		quotaID := fmt.Sprintf("quota-%s-%s", projectID, strings.ReplaceAll(NormalizeItemID(item), ":", "-"))

		assignedAgent := ""
		if len(activeSettlers) > 0 {
			assignedAgent = activeSettlers[idx%len(activeSettlers)]
		}

		// Calculate priority (1-10) based on volume and tier
		priority := calculateQuotaPriority(item, qty)

		quotas = append(quotas, GatheringQuota{
			QuotaID:          quotaID,
			ProjectID:        projectID,
			ItemID:           NormalizeItemID(item),
			TargetQuantity:   qty,
			GatheredQuantity: 0,
			AssignedAgentID:  assignedAgent,
			Priority:         priority,
		})
	}

	return quotas
}

// PrestigeBounty represents a settlement bounty offered to players for donating scarce construction materials.
type PrestigeBounty struct {
	BountyID         string  `json:"bounty_id"`
	ProjectID        string  `json:"project_id"`
	ItemID           string  `json:"item_id"`
	RequiredQuantity int     `json:"required_quantity"`
	PrestigeReward   float64 `json:"prestige_reward"`
	Description      string  `json:"description"`
}

// GeneratePrestigeBounties creates player bounties with calculated prestige rewards for all missing construction materials.
func GeneratePrestigeBounties(projectID string, missing map[string]int) []PrestigeBounty {
	if len(missing) == 0 {
		return nil
	}

	sortedItems := make([]string, 0, len(missing))
	for item := range missing {
		if missing[item] > 0 {
			sortedItems = append(sortedItems, item)
		}
	}
	sort.Strings(sortedItems)

	bounties := make([]PrestigeBounty, 0, len(sortedItems))
	for _, item := range sortedItems {
		qty := missing[item]
		normItem := NormalizeItemID(item)
		bountyID := fmt.Sprintf("bounty-%s-%s", projectID, strings.ReplaceAll(normItem, ":", "-"))

		multiplier := itemPrestigeMultiplier(normItem)
		reward := math.Round(float64(qty)*multiplier*100) / 100

		desc := fmt.Sprintf("Deliver %d x %s to settlement granary for project %s", qty, normItem, projectID)

		bounties = append(bounties, PrestigeBounty{
			BountyID:         bountyID,
			ProjectID:        projectID,
			ItemID:           normItem,
			RequiredQuantity: qty,
			PrestigeReward:   reward,
			Description:      desc,
		})
	}

	return bounties
}

func calculateQuotaPriority(item string, qty int) int {
	norm := strings.ToLower(item)
	basePriority := 5

	// Foundation/structural materials have higher base priority
	if strings.Contains(norm, "log") || strings.Contains(norm, "cobblestone") || strings.Contains(norm, "stone") {
		basePriority = 8
	} else if strings.Contains(norm, "plank") {
		basePriority = 7
	} else if strings.Contains(norm, "glass") || strings.Contains(norm, "torch") {
		basePriority = 6
	}

	if qty > 64 {
		basePriority += 1
	}
	if basePriority > 10 {
		basePriority = 10
	}
	return basePriority
}

func itemPrestigeMultiplier(item string) float64 {
	norm := strings.ToLower(item)
	switch {
	case strings.Contains(norm, "netherite"):
		return 50.0
	case strings.Contains(norm, "diamond"):
		return 15.0
	case strings.Contains(norm, "emerald"):
		return 8.0
	case strings.Contains(norm, "gold"):
		return 3.0
	case strings.Contains(norm, "iron"):
		return 1.5
	case strings.Contains(norm, "copper"):
		return 0.75
	case strings.Contains(norm, "log"):
		return 0.5
	case strings.Contains(norm, "plank"):
		return 0.25
	case strings.Contains(norm, "cobblestone") || strings.Contains(norm, "stone"):
		return 0.2
	default:
		return 0.25
	}
}
