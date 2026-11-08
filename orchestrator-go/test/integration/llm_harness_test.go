package integration

import (
	"context"
	"net"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/eval"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
	"github.com/btposey/civquest/orchestrator-go/internal/system2"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

func resolveLiveLLMConfig() (string, string) {
	baseURL := os.Getenv("CIVQUEST_LLM_BASE_URL")
	model := os.Getenv("CIVQUEST_LLM_MODEL")

	if r := xdg.NewResolver(); r != nil {
		if cfg, err := r.LoadGlobalConfig(); err == nil && cfg != nil {
			if baseURL == "" && cfg.LLMGateway.BaseURL != "" {
				baseURL = cfg.LLMGateway.BaseURL
			}
			if model == "" && cfg.LLMGateway.Model != "" {
				model = cfg.LLMGateway.Model
			}
		}
	}

	if baseURL == "" {
		baseURL = "http://127.0.0.1:8080/v1"
	}
	if model == "" {
		model = "Spark-X2.5-4B-Q4_K_M"
	}
	return baseURL, model
}

func isEndpointReachable(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return false
	}
	conn, err := net.DialTimeout("tcp", u.Host, 1500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// TestLiveLLMAgentHarness_System1StochasticEvaluator runs the multi-trial CivQuest agent harness
// against the live OpenAI-compatible endpoint (e.g., local llama.cpp at http://127.0.0.1:8080/v1)
// and verifies that empirical pass probabilities and rubric scores satisfy System 1 probability thresholds.
func TestLiveLLMAgentHarness_System1StochasticEvaluator(t *testing.T) {
	baseURL, model := resolveLiveLLMConfig()
	if !isEndpointReachable(baseURL) {
		t.Skipf("skipping live LLM agent harness evaluation: endpoint %s is not reachable", baseURL)
	}

	t.Logf("Running stochastic agent harness against live OpenAI endpoint: %s (model: %s)", baseURL, model)

	client, err := system2.NewClientWithConfig(
		xdg.LLMGatewayConfig{
			BaseURL:               baseURL,
			Model:                 model,
			StructuredTemperature: 0.20,
			CreativeTemperature:   0.85,
		},
		xdg.InferenceGovernorConfig{
			MaxConcurrentLLMRequests: 2,
			RequestTimeoutSeconds:    45,
		},
	)
	if err != nil {
		t.Fatalf("failed to initialize System 2 client: %v", err)
	}
	defer client.Close()

	harness := eval.NewHarness(eval.HarnessConfig{
		Client:            client,
		TrialsPerScenario: 3,
		TrialTimeout:      45 * time.Second,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	scenarios := eval.DefaultScenarios()
	reports, allPassed := harness.RunSuite(ctx, scenarios)

	t.Logf("\n%s", system1.FormatSuiteReportMarkdown(reports))

	for _, r := range reports {
		for _, s := range r.Samples {
			if !s.Passed {
				t.Logf("  [Scenario %s | Trial %d] FAILED (score=%.2f, latency=%s, err=%q): checks=%+v",
					r.ScenarioName, s.TrialIndex, s.Score, s.Latency.Round(time.Millisecond), s.Error, s.Checks)
			}
		}
		if !r.ThresholdMet {
			t.Errorf("Scenario %q failed System 1 probability threshold: %v (PassProb=%.2f, MeanScore=%.2f, Wilson95=[%.2f, %.2f])",
				r.ScenarioName, r.FailureReasons, r.PassProbability, r.MeanScore, r.WilsonLowerBound95, r.WilsonUpperBound95)
		}
	}

	if !allPassed {
		t.Fatalf("One or more stochastic agent harness scenarios failed their System 1 probability thresholds")
	}
}
