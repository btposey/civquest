package system1

import (
	"fmt"
	"strings"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// NavigationEvaluator evaluates patrol routes, frontier scouting azimuth and radial legs,
// and road paving readiness into the FactGraph.
type NavigationEvaluator struct{}

// NewNavigationEvaluator creates a new NavigationEvaluator instance.
func NewNavigationEvaluator() *NavigationEvaluator {
	return &NavigationEvaluator{}
}

func (e *NavigationEvaluator) Name() string {
	return "navigation"
}

func (e *NavigationEvaluator) Category() string {
	return "navigation"
}

func (e *NavigationEvaluator) Evaluate(ctx *EvaluatorContext, graph *FactGraph) error {
	if graph == nil {
		return nil
	}

	evaluatePatrolFacts(ctx, graph)
	evaluateScoutingFacts(ctx, graph)
	evaluatePavingFacts(ctx, graph)

	return nil
}

// InfrastructureEvaluator evaluates road paving readiness, corridor gating, and origin locks into the FactGraph.
type InfrastructureEvaluator struct{}

// NewInfrastructureEvaluator creates a new InfrastructureEvaluator instance.
func NewInfrastructureEvaluator() *InfrastructureEvaluator {
	return &InfrastructureEvaluator{}
}

func (e *InfrastructureEvaluator) Name() string {
	return "infrastructure"
}

func (e *InfrastructureEvaluator) Category() string {
	return "infrastructure"
}

func (e *InfrastructureEvaluator) Evaluate(ctx *EvaluatorContext, graph *FactGraph) error {
	if graph == nil {
		return nil
	}

	evaluatePavingFacts(ctx, graph)

	return nil
}

func evaluatePatrolFacts(ctx *EvaluatorContext, graph *FactGraph) {
	patrolActive := false
	if ctx != nil && ctx.State != nil && ctx.State.IsCivicPatrolActive() {
		patrolActive = true
	}
	if ctx != nil && ctx.Config != nil && isGuardProfession(ctx.Config.Profession) {
		patrolActive = true
	}

	waypointCount := 0
	waypointIndex := 0
	if ctx != nil {
		if ctx.State != nil {
			if pts := ctx.State.GetPatrolWaypoints(); len(pts) > 0 {
				waypointCount = len(pts)
			} else if len(ctx.SafeNodes) > 0 {
				waypointCount = len(ctx.SafeNodes)
			} else if outposts := ctx.State.GetOutposts(); len(outposts) > 0 {
				waypointCount = len(outposts)
			}
			waypointIndex = ctx.State.GetPatrolWaypointIndex()
		} else if len(ctx.SafeNodes) > 0 {
			waypointCount = len(ctx.SafeNodes)
		}
	}

	graph.SetBool("patrol.active", "navigation", patrolActive, fmt.Sprintf("Patrol active: %t", patrolActive))
	graph.SetInt("patrol.waypoint_count", "navigation", waypointCount, fmt.Sprintf("Patrol waypoint count: %d", waypointCount))
	graph.SetInt("patrol.waypoint_index", "navigation", waypointIndex, fmt.Sprintf("Patrol waypoint index: %d", waypointIndex))
}

func evaluateScoutingFacts(ctx *EvaluatorContext, graph *FactGraph) {
	prof := ""
	if ctx != nil && ctx.Config != nil {
		prof = ctx.Config.Profession
	}
	isExplorer := isExplorerProfession(prof)
	isColonize := ctx != nil && ctx.State != nil && ctx.State.Stage() == StageColonize

	routine := ""
	if graph != nil {
		routine = graph.GetString("circadian.routine")
	}
	if routine == "" && ctx != nil && ctx.State != nil {
		routine = string(ctx.State.Routine())
	}
	isPrimaryDuty := routine == string(RoutinePrimaryDuty) || routine == "PRIMARY_DUTY"

	scoutingActive := isExplorer || isColonize || isPrimaryDuty

	graph.SetBool("scouting.active", "navigation", scoutingActive, fmt.Sprintf("Frontier scouting active: %t", scoutingActive))
}

func evaluatePavingFacts(ctx *EvaluatorContext, graph *FactGraph) {
	var tracker *RoadPavingTracker
	if ctx != nil {
		if ctx.PavingTracker != nil {
			tracker = ctx.PavingTracker
		} else if ctx.State != nil {
			tracker = ctx.State.GetPavingTracker()
		}
	}

	isSleeping := false
	isStuck := false
	isEnclosed := false
	if ctx != nil && ctx.Frame != nil && ctx.Frame.Status != nil {
		isSleeping = ctx.Frame.Status.IsSleeping
		isStuck = ctx.Frame.Status.NavigationState == civquestv1.NavigationState_NAVIGATION_STATE_STUCK
		isEnclosed = ctx.Frame.Status.NavigationEnclosed
	}
	if graph != nil && graph.GetBool("vitals.is_sleeping") {
		isSleeping = true
	}

	needsPave := (tracker != nil) && !isSleeping && !isStuck && !isEnclosed

	homeOriginSet := false
	if tracker != nil && tracker.GetHomeOrigin() != nil {
		homeOriginSet = true
	}
	if !homeOriginSet && ctx != nil && ctx.State != nil && ctx.State.GetHomeOrigin() != nil {
		homeOriginSet = true
	}

	graph.SetBool("paving.needs_pave", "infrastructure", needsPave, fmt.Sprintf("Road paving needs pave: %t", needsPave))
	graph.SetBool("paving.home_origin_set", "infrastructure", homeOriginSet, fmt.Sprintf("Road paving home origin set: %t", homeOriginSet))
}

func isGuardProfession(prof string) bool {
	p := strings.ToUpper(strings.TrimSpace(prof))
	return p == "GUARD" || p == "WATCHMAN" || p == "PATROL" || p == "SENTRY" || p == "WARRIOR" || p == "SOLDIER" || p == "MILITIA"
}

func isExplorerProfession(prof string) bool {
	p := strings.ToUpper(strings.TrimSpace(prof))
	return p == "EXPLORER" || p == "SCOUT" || p == "TRAILBLAZER"
}
