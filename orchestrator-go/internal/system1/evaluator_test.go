package system1

import (
	"errors"
	"math"
	"testing"
	"time"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

func TestEvaluateScenario_ProbabilityThreshold(t *testing.T) {
	samples := []TrialSample{
		ScoreChecks(0, []CheckOutcome{{Name: "a", Passed: true, Weight: 1, Score: 1}}, 120*time.Millisecond, 0.75, nil),
		ScoreChecks(1, []CheckOutcome{{Name: "a", Passed: true, Weight: 1, Score: 0.9}}, 180*time.Millisecond, 0.75, nil),
		ScoreChecks(2, []CheckOutcome{{Name: "a", Passed: true, Weight: 1, Score: 0.85}}, 210*time.Millisecond, 0.75, nil),
		ScoreChecks(3, []CheckOutcome{{Name: "a", Passed: false, Weight: 1, Score: 0.2}}, 300*time.Millisecond, 0.75, nil),
	}

	report := EvaluateScenario("test_dialogue", samples, ProbabilityThreshold{
		MinPassProbability: 0.75,
		MinMeanScore:       0.70,
		MinTrialScore:      0.75,
		MaxP95Latency:      500 * time.Millisecond,
	})

	if !report.ThresholdMet {
		t.Fatalf("expected threshold to be met, failures: %v", report.FailureReasons)
	}
	if math.Abs(report.PassProbability-0.75) > 1e-6 {
		t.Errorf("expected PassProbability 0.75, got %.2f", report.PassProbability)
	}
	if report.WilsonLowerBound95 <= 0.0 || report.WilsonUpperBound95 > 1.0 || report.WilsonLowerBound95 >= report.WilsonUpperBound95 {
		t.Errorf("invalid Wilson 95%% interval: [%.3f, %.3f]", report.WilsonLowerBound95, report.WilsonUpperBound95)
	}

	// Stricter threshold should fail deterministically
	strict := EvaluateScenario("strict_dialogue", samples, ProbabilityThreshold{
		MinPassProbability: 0.90,
		MinMeanScore:       0.90,
	})
	if strict.ThresholdMet || len(strict.FailureReasons) == 0 {
		t.Errorf("expected strict threshold to fail, got report: %+v", strict)
	}

	// Hard error trial scores 0
	errSample := ScoreChecks(4, nil, 50*time.Millisecond, 0.75, errors.New("timeout"))
	if errSample.Passed || errSample.Score != 0 {
		t.Errorf("expected error trial to fail with score 0, got %+v", errSample)
	}
}

func TestEvaluateDialogueChronicleToponymOutcomes(t *testing.T) {
	expectDisobeyed := false
	dChecks := EvaluateDialogueOutcome(
		"Greetings traveler! Our wheat harvest in the pioneer fields is thriving.",
		"NONE",
		1.5,
		"Greeted Steve and shared harvest news.",
		false,
		"NONE",
		&civquestv1.SendChatIntent{Message: "Greetings traveler!"},
		nil,
		DialogueEvalSpec{
			ExpectNonEmptyReply:   true,
			MaxReplyRunes:         300,
			AllowedTradeDecisions: []string{"NONE"},
			MinPrestigeDelta:      -5.0,
			MaxPrestigeDelta:      10.0,
			ExpectDisobeyed:       &expectDisobeyed,
			RequireChatIntent:     true,
			ExpectedKeywordAny:    []string{"harvest", "wheat", "pioneer", "greet"},
			ForbiddenSubstrings:   []string{"```json", "{\"spoken_reply\""},
		},
	)
	sample := ScoreChecks(0, dChecks, 100*time.Millisecond, 0.85, nil)
	if !sample.Passed || sample.Score < 0.99 {
		t.Errorf("expected perfect dialogue score, got %.2f (checks: %+v)", sample.Score, dChecks)
	}

	cChecks := EvaluateChronicleOutcome(
		"Chronicle of Pioneers",
		[]string{"In the first dawn,\nAria planted wheat\nnear the river."},
		&civquestv1.CreateWrittenBookIntent{
			Title: "Chronicle of Pioneers",
			Pages: []string{"In the first dawn,\nAria planted wheat\nnear the river."},
		},
		[]string{"aria", "wheat"},
	)
	cSample := ScoreChecks(0, cChecks, 100*time.Millisecond, 0.85, nil)
	if !cSample.Passed || cSample.Score < 0.99 {
		t.Errorf("expected perfect chronicle score, got %.2f (checks: %+v)", cSample.Score, cChecks)
	}

	tChecks := EvaluateToponymOutcome(
		"Whispering Pines",
		[]string{"Whispering", "Pines"},
		"Named by Aria upon discovering the ancient taiga ridge.",
		&civquestv1.PlaceSignpostIntent{
			Pos:   &civquestv1.BlockPosProto{X: 10, Y: 64, Z: 10},
			Line1: "Whispering",
			Line2: "Pines",
		},
	)
	tSample := ScoreChecks(0, tChecks, 100*time.Millisecond, 0.85, nil)
	if !tSample.Passed || tSample.Score < 0.99 {
		t.Errorf("expected perfect toponym score, got %.2f (checks: %+v)", tSample.Score, tChecks)
	}
}
