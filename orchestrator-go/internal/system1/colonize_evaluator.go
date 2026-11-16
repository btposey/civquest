package system1

import "fmt"

// ColonizeEvaluator evaluates whether the agent is actively establishing a new settlement.
type ColonizeEvaluator struct{}

// NewColonizeEvaluator creates a new ColonizeEvaluator instance.
func NewColonizeEvaluator() *ColonizeEvaluator {
	return &ColonizeEvaluator{}
}

func (e *ColonizeEvaluator) Name() string {
	return "colonize"
}

func (e *ColonizeEvaluator) Category() string {
	return "colonize"
}

func (e *ColonizeEvaluator) Evaluate(ctx *EvaluatorContext, graph *FactGraph) error {
	if graph == nil {
		return nil
	}

	isEstablishing := false
	if ctx != nil && ctx.State != nil {
		isEstablishing = ctx.State.IsEstablishingNewSettlement()
	}

	graph.SetBool("colonize.is_establishing", "colonize", isEstablishing, fmt.Sprintf("Establishing new settlement: %t", isEstablishing))
	return nil
}
