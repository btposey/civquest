package eval

import (
	"github.com/btposey/civquest/orchestrator-go/internal/crafting"
	"github.com/btposey/civquest/orchestrator-go/internal/crafting/craftingtest"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// VanillaRecipeNodes returns the simulation catalogue. It has the live server's shape,
// including tag-wide slots and recipe cycles, so a planner that cannot cope with them fails
// here instead of in play (cq-yn1.3). In production the graph arrives from the mod; scenarios
// install this one on AgentState via SetRecipeGraph.
func VanillaRecipeNodes() []*civquestv1.RecipeNode { return craftingtest.Nodes() }

// VanillaRecipeGraph builds the planner graph over VanillaRecipeNodes.
func VanillaRecipeGraph() *crafting.RecipeGraph { return crafting.NewRecipeGraph(VanillaRecipeNodes()) }
