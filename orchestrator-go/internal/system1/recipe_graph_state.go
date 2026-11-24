package system1

import "github.com/btposey/civquest/orchestrator-go/internal/crafting"

// SetRecipeGraph attaches a dynamic runtime RecipeGraph to AgentState.
func (s *AgentState) SetRecipeGraph(graph *crafting.RecipeGraph) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recipeGraph = graph
}

// RecipeGraph returns the attached dynamic RecipeGraph, or nil if not yet synced.
func (s *AgentState) RecipeGraph() *crafting.RecipeGraph {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.recipeGraph
}

// RecipeGraphLocked returns the attached dynamic RecipeGraph while s.mu is held.
func (s *AgentState) RecipeGraphLocked() *crafting.RecipeGraph {
	if s == nil {
		return nil
	}
	return s.recipeGraph
}
