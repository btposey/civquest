package integration

import (
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/eval"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
)

// TestSystem1ImpulseAgentHarness_ProbabilisticEvaluator exercises the zero-token System 1
// impulse/intuitive agent responses through AgentRuntime.HandleTelemetryFrame (without System 2)
// and evaluates empirical pass probabilities, Wilson 95% confidence intervals, and <5ms latency budgets.
func TestSystem1ImpulseAgentHarness_ProbabilisticEvaluator(t *testing.T) {
	scenarios := eval.DefaultSystem1Scenarios()
	reports, allPassed := eval.RunSystem1Suite(scenarios, 20261108)

	t.Logf("\n%s", system1.FormatSuiteReportMarkdown(reports))

	for _, r := range reports {
		if !r.ThresholdMet {
			t.Errorf("System 1 scenario %q failed probability threshold: %v (PassProb=%.2f, MeanScore=%.2f, Wilson95=[%.2f, %.2f], P95=%s)",
				r.ScenarioName, r.FailureReasons, r.PassProbability, r.MeanScore, r.WilsonLowerBound95, r.WilsonUpperBound95, r.P95Latency)
		}
	}

	if !allPassed {
		t.Fatalf("One or more System 1 impulse scenarios failed their probabilistic threshold")
	}
}
