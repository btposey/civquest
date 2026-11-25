package system1

import (
	"testing"
)

func TestColonizeEvaluator_Evaluate(t *testing.T) {
	eval := NewColonizeEvaluator()
	if eval.Name() != "colonize" {
		t.Fatalf("expected evaluator name 'colonize', got %q", eval.Name())
	}
	if eval.Category() != "colonize" {
		t.Fatalf("expected evaluator category 'colonize', got %q", eval.Category())
	}

	// 1. Nil graph returns without error
	if err := eval.Evaluate(nil, nil); err != nil {
		t.Fatalf("unexpected error on nil graph: %v", err)
	}

	// 2. Default state: not establishing
	state := NewAgentState()
	evalCtx := &EvaluatorContext{State: state}
	graph := NewFactGraph()

	if err := eval.Evaluate(evalCtx, graph); err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if graph.GetBool("colonize.is_establishing") {
		t.Errorf("expected colonize.is_establishing false initially")
	}

	// 3. State set to establishing
	state.SetEstablishingNewSettlement(true)
	if err := eval.Evaluate(evalCtx, graph); err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if !graph.GetBool("colonize.is_establishing") {
		t.Errorf("expected colonize.is_establishing true after SetEstablishingNewSettlement(true)")
	}

	// 4. State set back to false
	state.SetEstablishingNewSettlement(false)
	if err := eval.Evaluate(evalCtx, graph); err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if graph.GetBool("colonize.is_establishing") {
		t.Errorf("expected colonize.is_establishing false after SetEstablishingNewSettlement(false)")
	}
}

func TestColonizeEvaluator_RegisteredInDefaultRegistry(t *testing.T) {
	reg := NewDefaultEngineRegistry()
	eval, ok := reg.GetEvaluator("colonize")
	if !ok || eval == nil {
		t.Fatalf("expected 'colonize' evaluator to be registered in NewDefaultEngineRegistry()")
	}
	if eval.Category() != "colonize" {
		t.Errorf("expected category 'colonize', got %q", eval.Category())
	}
}
