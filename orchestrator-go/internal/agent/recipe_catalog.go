package agent

import (
	"context"
	"log"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/bridge"
	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// syncRecipeGraph fetches the runtime crafting catalog from the NeoForge server via ExportRecipeGraph
// and installs the resulting dynamic RecipeGraph into r.system1State (ADR-029 §4).
func (r *AgentRuntime) syncRecipeGraph(ctx context.Context, client *bridge.Client) error {
	if r == nil || client == nil {
		return nil
	}

	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	resp, err := client.ExportRecipeGraph(callCtx, &civquestv1.ExportRecipeRequest{})
	if err != nil {
		log.Printf("[Agent %s] ExportRecipeGraph sync failed (fail-closed crafting until synced): %v", r.agentID, err)
		return err
	}
	if resp == nil {
		return nil
	}

	graph := crafting.NewRecipeGraph(resp.Recipes)
	r.system1State.SetRecipeGraph(graph)
	if r.debugLogger != nil {
		r.debugLogger.Emit(0, "RECIPE_CATALOG_SYNCED", "Synced runtime crafting recipe graph from server", map[string]any{
			"recipe_count": len(resp.Recipes),
		})
	}
	return nil
}
