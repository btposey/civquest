package system1

import (
	"fmt"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// CircadianEvaluator evaluates the world time of day, weather, shift, and routine
// into the FactGraph.
type CircadianEvaluator struct{}

// NewCircadianEvaluator creates a CircadianEvaluator instance.
func NewCircadianEvaluator() *CircadianEvaluator {
	return &CircadianEvaluator{}
}

func (e *CircadianEvaluator) Name() string {
	return "circadian"
}

func (e *CircadianEvaluator) Category() string {
	return "Circadian"
}

func (e *CircadianEvaluator) Evaluate(ctx *EvaluatorContext, graph *FactGraph) error {
	if graph == nil {
		return nil
	}

	tod := int64(0)
	var weather civquestv1.Weather
	if ctx != nil && ctx.Frame != nil {
		tod = ctx.Frame.TimeOfDay
		weather = ctx.Frame.Weather
	}

	normTod := ((tod % 24000) + 24000) % 24000
	var shift string = xdg.ShiftFirst
	if ctx != nil && ctx.Config != nil {
		shift = ctx.Config.EffectiveShift()
	}

	var state StateView
	if ctx != nil {
		state = ctx.State
	}

	routine := RoutineDawnPrep
	if ctx != nil && ctx.Config != nil {
		routine = ComputeCircadianRoutine(tod, weather, ctx.Config, state)
	}
	if state != nil {
		routine = rhythm(state.BaseStrategy(), routine)
		routine = gateBootstrapCircadianRoutine(state.Stage(), routine, state, isStateShelterOrBedSatisfied(state))
	}

	isTavern := normTod >= 11000 && normTod < 13000
	isDuskOrNight := (weather == civquestv1.Weather_WEATHER_THUNDER) || (normTod >= 11000 && normTod < 23500)

	graph.SetInt("circadian.time_of_day", "Circadian", int(normTod), fmt.Sprintf("Time of day is %d ticks", normTod))
	graph.SetString("circadian.shift", "Circadian", string(shift), fmt.Sprintf("Active shift: %s", shift))
	graph.SetString("circadian.routine", "Circadian", string(routine), fmt.Sprintf("Scheduled routine: %s", routine))
	graph.SetBool("circadian.is_tavern_social", "Circadian", isTavern, fmt.Sprintf("Tavern social hour active: %t", isTavern))
	graph.SetBool("circadian.is_dusk_or_night", "Circadian", isDuskOrNight, fmt.Sprintf("Dusk or night active: %t", isDuskOrNight))

	return nil
}
