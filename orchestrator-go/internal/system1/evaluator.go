package system1

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// CheckOutcome records a single deterministic System 1 invariant or rubric check within a trial.
type CheckOutcome struct {
	Name   string  `json:"name"`
	Passed bool    `json:"passed"`
	Weight float64 `json:"weight"`
	Score  float64 `json:"score"` // [0.0, 1.0]
	Reason string  `json:"reason,omitempty"`
}

// TrialSample represents the evaluated result of a single stochastic LLM harness execution.
type TrialSample struct {
	TrialIndex int            `json:"trial_index"`
	Passed     bool           `json:"passed"`
	Score      float64        `json:"score"` // Weighted score in [0.0, 1.0]
	Latency    time.Duration  `json:"latency"`
	Checks     []CheckOutcome `json:"checks"`
	Error      string         `json:"error,omitempty"`
}

// ProbabilityThreshold defines the minimum acceptable probabilistic performance for a stochastic scenario.
type ProbabilityThreshold struct {
	// MinPassProbability is the minimum empirical pass rate (k/N) required in [0.0, 1.0].
	MinPassProbability float64 `json:"min_pass_probability"`
	// MinMeanScore is the minimum mean rubric score across all trials in [0.0, 1.0].
	MinMeanScore float64 `json:"min_mean_score"`
	// MinTrialScore is the minimum weighted rubric score required for an individual trial to count as Passed.
	MinTrialScore float64 `json:"min_trial_score"`
	// MaxP95Latency is the optional maximum 95th-percentile latency per trial (0 disables check).
	MaxP95Latency time.Duration `json:"max_p95_latency"`
}

// EvaluationReport summarizes the statistical performance of the agent harness across N stochastic trials.
type EvaluationReport struct {
	ScenarioName       string               `json:"scenario_name"`
	TotalTrials        int                  `json:"total_trials"`
	PassedTrials       int                  `json:"passed_trials"`
	PassProbability    float64              `json:"pass_probability"`
	WilsonLowerBound95 float64              `json:"wilson_lower_bound_95"`
	WilsonUpperBound95 float64              `json:"wilson_upper_bound_95"`
	MeanScore          float64              `json:"mean_score"`
	StdDevScore        float64              `json:"std_dev_score"`
	MinScore           float64              `json:"min_score"`
	MaxScore           float64              `json:"max_score"`
	P50Latency         time.Duration        `json:"p50_latency"`
	P95Latency         time.Duration        `json:"p95_latency"`
	Threshold          ProbabilityThreshold `json:"threshold"`
	ThresholdMet       bool                 `json:"threshold_met"`
	FailureReasons     []string             `json:"failure_reasons,omitempty"`
	Samples            []TrialSample        `json:"samples"`
}

// ScoreChecks computes the weighted score in [0.0, 1.0] and pass status from a slice of deterministic checks.
// Any check with Weight > 0 and Score < 1.0 reduces the weighted score; if all checks pass or the weighted
// score is >= minTrialScore (and no hard error occurred), the trial is marked as Passed.
func ScoreChecks(trialIdx int, checks []CheckOutcome, latency time.Duration, minTrialScore float64, err error) TrialSample {
	if minTrialScore <= 0 {
		minTrialScore = 0.75
	}
	sample := TrialSample{
		TrialIndex: trialIdx,
		Latency:    latency,
		Checks:     checks,
	}
	if err != nil {
		sample.Error = err.Error()
		sample.Passed = false
		sample.Score = 0.0
		return sample
	}

	var totalWeight, weightedSum float64
	for _, c := range checks {
		w := c.Weight
		if w <= 0 {
			w = 1.0
		}
		sc := math.Max(0.0, math.Min(1.0, c.Score))
		if c.Passed && sc == 0.0 {
			sc = 1.0
		}
		totalWeight += w
		weightedSum += w * sc
	}

	if totalWeight > 0 {
		sample.Score = weightedSum / totalWeight
	} else {
		sample.Score = 1.0
	}

	sample.Passed = sample.Score >= minTrialScore-1e-9
	return sample
}

// EvaluateScenario aggregates N TrialSamples into an EvaluationReport and deterministically evaluates
// whether the empirical pass probability and mean utility score satisfy the ProbabilityThreshold.
func EvaluateScenario(name string, samples []TrialSample, threshold ProbabilityThreshold) EvaluationReport {
	if threshold.MinTrialScore <= 0 {
		threshold.MinTrialScore = 0.75
	}
	report := EvaluationReport{
		ScenarioName: name,
		TotalTrials:  len(samples),
		Threshold:    threshold,
		Samples:      samples,
	}
	if len(samples) == 0 {
		report.ThresholdMet = false
		report.FailureReasons = []string{"no trials executed"}
		return report
	}

	var sumScore float64
	report.MinScore = 1.0
	report.MaxScore = 0.0
	latencies := make([]time.Duration, 0, len(samples))

	for _, s := range samples {
		if s.Passed {
			report.PassedTrials++
		}
		sumScore += s.Score
		if s.Score < report.MinScore {
			report.MinScore = s.Score
		}
		if s.Score > report.MaxScore {
			report.MaxScore = s.Score
		}
		latencies = append(latencies, s.Latency)
	}

	n := float64(len(samples))
	report.PassProbability = float64(report.PassedTrials) / n
	report.MeanScore = sumScore / n

	var varianceSum float64
	for _, s := range samples {
		diff := s.Score - report.MeanScore
		varianceSum += diff * diff
	}
	if len(samples) > 1 {
		report.StdDevScore = math.Sqrt(varianceSum / float64(len(samples)-1))
	}

	report.WilsonLowerBound95, report.WilsonUpperBound95 = WilsonScoreInterval95(report.PassedTrials, len(samples))

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	report.P50Latency = percentileDuration(latencies, 0.50)
	report.P95Latency = percentileDuration(latencies, 0.95)

	report.ThresholdMet = true
	if report.PassProbability+1e-9 < threshold.MinPassProbability {
		report.ThresholdMet = false
		report.FailureReasons = append(report.FailureReasons,
			fmt.Sprintf("pass probability %.2f (%d/%d) below threshold %.2f",
				report.PassProbability, report.PassedTrials, report.TotalTrials, threshold.MinPassProbability))
	}
	if report.MeanScore+1e-9 < threshold.MinMeanScore {
		report.ThresholdMet = false
		report.FailureReasons = append(report.FailureReasons,
			fmt.Sprintf("mean rubric score %.2f below threshold %.2f",
				report.MeanScore, threshold.MinMeanScore))
	}
	if threshold.MaxP95Latency > 0 && report.P95Latency > threshold.MaxP95Latency {
		report.ThresholdMet = false
		report.FailureReasons = append(report.FailureReasons,
			fmt.Sprintf("P95 latency %v exceeds max %v", report.P95Latency, threshold.MaxP95Latency))
	}

	return report
}

// WilsonScoreInterval95 computes the 95% Wilson score confidence interval [lower, upper]
// for a binomial proportion with k successes in n trials (z = 1.95996).
func WilsonScoreInterval95(k, n int) (float64, float64) {
	if n <= 0 {
		return 0.0, 0.0
	}
	const z = 1.959963984540054
	z2 := z * z
	nf := float64(n)
	phat := float64(k) / nf

	denom := 1.0 + z2/nf
	center := (phat + z2/(2.0*nf)) / denom
	halfWidth := (z * math.Sqrt((phat*(1.0-phat)+z2/(4.0*nf))/nf)) / denom

	lower := math.Max(0.0, center-halfWidth)
	upper := math.Min(1.0, center+halfWidth)
	return lower, upper
}

func percentileDuration(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// DialogueEvalSpec defines deterministic System 1 expectations for evaluating a Dialogue/Trade turn.
type DialogueEvalSpec struct {
	ExpectNonEmptyReply       bool
	MaxReplyRunes             int
	AllowedTradeDecisions     []string
	MinPrestigeDelta          float64
	MaxPrestigeDelta          float64
	ExpectDisobeyed           *bool
	ForbidActiveDelegatedTask bool
	RequireChatIntent         bool
	RequireTradeIntent        bool
	ExpectedKeywordAny        []string // Optional case-insensitive keywords where at least one helps score
	ForbiddenSubstrings       []string // Substrings that must not appear in SpokenReply (e.g. raw JSON braces)
}

// EvaluateDialogueOutcome runs zero-token System 1 checks against a completed dialogue or trade turn.
func EvaluateDialogueOutcome(
	spokenReply string,
	tradeDecision string,
	prestigeDelta float64,
	memorySummary string,
	disobeyed bool,
	activeTaskType string,
	chatIntent *civquestv1.SendChatIntent,
	tradeIntent *civquestv1.RespondTradeIntent,
	spec DialogueEvalSpec,
) []CheckOutcome {
	var checks []CheckOutcome

	// 1. Spoken reply presence and length
	trimmedReply := strings.TrimSpace(spokenReply)
	if spec.ExpectNonEmptyReply {
		passed := len(trimmedReply) > 0
		checks = append(checks, CheckOutcome{
			Name:   "non_empty_spoken_reply",
			Passed: passed,
			Weight: 2.0,
			Score:  boolScore(passed),
			Reason: fmt.Sprintf("reply length=%d runes", utf8.RuneCountInString(trimmedReply)),
		})
	}

	maxRunes := spec.MaxReplyRunes
	if maxRunes <= 0 {
		maxRunes = 512
	}
	runeCount := utf8.RuneCountInString(trimmedReply)
	withinLen := runeCount <= maxRunes
	checks = append(checks, CheckOutcome{
		Name:   "reply_length_bounds",
		Passed: withinLen,
		Weight: 1.0,
		Score:  boolScore(withinLen),
		Reason: fmt.Sprintf("runes=%d (max=%d)", runeCount, maxRunes),
	})

	// 2. No leaked JSON formatting or markdown code fences in spoken_reply
	for _, forbidden := range spec.ForbiddenSubstrings {
		if forbidden == "" {
			continue
		}
		clean := !strings.Contains(strings.ToLower(trimmedReply), strings.ToLower(forbidden))
		checks = append(checks, CheckOutcome{
			Name:   "no_forbidden_substring_" + forbidden,
			Passed: clean,
			Weight: 1.5,
			Score:  boolScore(clean),
		})
	}

	// 3. Trade decision enum check
	if len(spec.AllowedTradeDecisions) > 0 {
		matched := false
		for _, allowed := range spec.AllowedTradeDecisions {
			if strings.EqualFold(tradeDecision, allowed) {
				matched = true
				break
			}
		}
		checks = append(checks, CheckOutcome{
			Name:   "trade_decision_valid",
			Passed: matched,
			Weight: 2.0,
			Score:  boolScore(matched),
			Reason: fmt.Sprintf("got=%q, allowed=%v", tradeDecision, spec.AllowedTradeDecisions),
		})
	}

	// 4. Prestige delta bounds
	if spec.MinPrestigeDelta != 0 || spec.MaxPrestigeDelta != 0 {
		inRange := !math.IsNaN(prestigeDelta) &&
			prestigeDelta >= spec.MinPrestigeDelta-1e-6 &&
			prestigeDelta <= spec.MaxPrestigeDelta+1e-6
		checks = append(checks, CheckOutcome{
			Name:   "prestige_delta_bounds",
			Passed: inRange,
			Weight: 1.5,
			Score:  boolScore(inRange),
			Reason: fmt.Sprintf("delta=%.2f, expected=[%.2f, %.2f]", prestigeDelta, spec.MinPrestigeDelta, spec.MaxPrestigeDelta),
		})
	}

	// 5. Obedience gate check
	if spec.ExpectDisobeyed != nil {
		ok := disobeyed == *spec.ExpectDisobeyed
		checks = append(checks, CheckOutcome{
			Name:   "obedience_gate_coherence",
			Passed: ok,
			Weight: 2.5,
			Score:  boolScore(ok),
			Reason: fmt.Sprintf("disobeyed=%v, expected=%v", disobeyed, *spec.ExpectDisobeyed),
		})
	}
	if spec.ForbidActiveDelegatedTask {
		normTask := strings.ToUpper(strings.TrimSpace(activeTaskType))
		safe := normTask == "" || normTask == "NONE"
		checks = append(checks, CheckOutcome{
			Name:   "dangerous_delegation_blocked",
			Passed: safe,
			Weight: 3.0,
			Score:  boolScore(safe),
			Reason: fmt.Sprintf("active_task_type=%q, disobeyed=%v", normTask, disobeyed),
		})
	}

	// 6. Protobuf Intent emission checks
	if spec.RequireChatIntent {
		hasChat := chatIntent != nil && strings.TrimSpace(chatIntent.GetMessage()) != ""
		checks = append(checks, CheckOutcome{
			Name:   "chat_intent_emitted",
			Passed: hasChat,
			Weight: 2.0,
			Score:  boolScore(hasChat),
		})
	}
	if spec.RequireTradeIntent {
		hasTrade := tradeIntent != nil
		checks = append(checks, CheckOutcome{
			Name:   "trade_intent_emitted",
			Passed: hasTrade,
			Weight: 2.0,
			Score:  boolScore(hasTrade),
		})
	}

	// 7. Memory summary non-empty
	hasMem := strings.TrimSpace(memorySummary) != ""
	checks = append(checks, CheckOutcome{
		Name:   "memory_summary_populated",
		Passed: hasMem,
		Weight: 1.0,
		Score:  boolScore(hasMem),
	})

	// 8. Optional contextual keyword relevance
	if len(spec.ExpectedKeywordAny) > 0 {
		lowerReply := strings.ToLower(trimmedReply + " " + memorySummary)
		found := false
		for _, kw := range spec.ExpectedKeywordAny {
			if strings.Contains(lowerReply, strings.ToLower(kw)) {
				found = true
				break
			}
		}
		checks = append(checks, CheckOutcome{
			Name:   "contextual_keyword_relevance",
			Passed: found,
			Weight: 1.0,
			Score:  boolScore(found),
			Reason: fmt.Sprintf("checked keywords=%v", spec.ExpectedKeywordAny),
		})
	}

	return checks
}

// EvaluateChronicleOutcome runs deterministic System 1 checks on a Chronicler book output.
func EvaluateChronicleOutcome(title string, pages []string, intent *civquestv1.CreateWrittenBookIntent, expectedKeywords []string) []CheckOutcome {
	var checks []CheckOutcome

	titleRunes := utf8.RuneCountInString(strings.TrimSpace(title))
	titleOK := titleRunes > 0 && titleRunes <= 32
	checks = append(checks, CheckOutcome{
		Name:   "book_title_bounds",
		Passed: titleOK,
		Weight: 2.0,
		Score:  boolScore(titleOK),
		Reason: fmt.Sprintf("title=%q (%d runes)", title, titleRunes),
	})

	hasPages := len(pages) > 0 && len(pages) <= 100
	checks = append(checks, CheckOutcome{
		Name:   "book_page_count_valid",
		Passed: hasPages,
		Weight: 2.0,
		Score:  boolScore(hasPages),
		Reason: fmt.Sprintf("pages=%d", len(pages)),
	})

	allPagesFit := true
	for i, p := range pages {
		lines := strings.Split(p, "\n")
		maxLineRunes := 0
		for _, l := range lines {
			if rc := utf8.RuneCountInString(l); rc > maxLineRunes {
				maxLineRunes = rc
			}
		}
		if len(lines) > 14 || maxLineRunes > 19 || utf8.RuneCountInString(p) > 250 || strings.TrimSpace(p) == "" {
			allPagesFit = false
			checks = append(checks, CheckOutcome{
				Name:   fmt.Sprintf("page_%d_pagination", i+1),
				Passed: false,
				Weight: 1.5,
				Score:  0.0,
				Reason: fmt.Sprintf("lines=%d (max 14), maxLineRunes=%d (max 19), totalRunes=%d (max 250)", len(lines), maxLineRunes, utf8.RuneCountInString(p)),
			})
			break
		}
	}
	if allPagesFit && hasPages {
		checks = append(checks, CheckOutcome{
			Name:   "strict_minecraft_book_pagination",
			Passed: true,
			Weight: 2.0,
			Score:  1.0,
		})
	}

	intentOK := intent != nil && intent.GetTitle() != "" && len(intent.GetPages()) == len(pages)
	checks = append(checks, CheckOutcome{
		Name:   "create_written_book_intent_valid",
		Passed: intentOK,
		Weight: 1.5,
		Score:  boolScore(intentOK),
	})

	if len(expectedKeywords) > 0 {
		joined := strings.ToLower(title + " " + strings.Join(pages, " "))
		matched := 0
		for _, kw := range expectedKeywords {
			if strings.Contains(joined, strings.ToLower(kw)) {
				matched++
			}
		}
		ratio := float64(matched) / float64(len(expectedKeywords))
		checks = append(checks, CheckOutcome{
			Name:   "chronicle_lore_continuity",
			Passed: matched > 0,
			Weight: 1.5,
			Score:  math.Min(1.0, ratio+0.25),
			Reason: fmt.Sprintf("matched %d/%d lore themes", matched, len(expectedKeywords)),
		})
	}

	return checks
}

// EvaluateToponymOutcome runs deterministic System 1 checks on a Toponymy landmark naming output.
func EvaluateToponymOutcome(landmarkName string, signLines []string, lore string, intent *civquestv1.PlaceSignpostIntent) []CheckOutcome {
	var checks []CheckOutcome

	nameRunes := utf8.RuneCountInString(strings.TrimSpace(landmarkName))
	nameOK := nameRunes > 0 && nameRunes <= 24
	checks = append(checks, CheckOutcome{
		Name:   "landmark_name_bounds",
		Passed: nameOK,
		Weight: 2.0,
		Score:  boolScore(nameOK),
		Reason: fmt.Sprintf("name=%q (%d runes, max 24)", landmarkName, nameRunes),
	})

	nonEmptyLines := 0
	signOK := len(signLines) > 0 && len(signLines) <= 4
	for _, line := range signLines {
		if strings.TrimSpace(line) != "" {
			nonEmptyLines++
		}
		if utf8.RuneCountInString(line) > 15 {
			signOK = false
			break
		}
	}
	if nonEmptyLines == 0 {
		signOK = false
	}
	checks = append(checks, CheckOutcome{
		Name:   "signpost_line_width_bounds",
		Passed: signOK,
		Weight: 2.0,
		Score:  boolScore(signOK),
		Reason: fmt.Sprintf("lines=%v", signLines),
	})

	loreOK := utf8.RuneCountInString(strings.TrimSpace(lore)) >= 10
	checks = append(checks, CheckOutcome{
		Name:   "historical_lore_present",
		Passed: loreOK,
		Weight: 1.0,
		Score:  boolScore(loreOK),
	})

	intentOK := intent != nil && intent.GetPos() != nil && (intent.GetLine1() != "" || intent.GetLine2() != "")
	checks = append(checks, CheckOutcome{
		Name:   "place_signpost_intent_emitted",
		Passed: intentOK,
		Weight: 1.5,
		Score:  boolScore(intentOK),
	})

	return checks
}

// FormatSuiteReportMarkdown renders a slice of EvaluationReports into a Markdown summary table
// suitable for CLI output and evaluation logs (matching boggycreek/fleet eval reporting conventions).
func FormatSuiteReportMarkdown(reports []EvaluationReport) string {
	var sb strings.Builder
	sb.WriteString("# CivQuest System 1 Stochastic Agent Harness Evaluation Report\n\n")
	sb.WriteString("| Scenario | Status | Pass Prob (k/N) | Min Prob | Wilson 95% CI | Mean Score | Min Score | P50 Latency | P95 Latency |\n")
	sb.WriteString("| :--- | :---: | :---: | :---: | :---: | :---: | :---: | :---: | :---: |\n")

	for _, r := range reports {
		status := "PASS"
		if !r.ThresholdMet {
			status = "FAIL"
		}
		sb.WriteString(fmt.Sprintf("| %s | %s | %.2f (%d/%d) | %.2f | [%.2f, %.2f] | %.2f (±%.2f) | %.2f | %s | %s |\n",
			r.ScenarioName,
			status,
			r.PassProbability, r.PassedTrials, r.TotalTrials,
			r.Threshold.MinPassProbability,
			r.WilsonLowerBound95, r.WilsonUpperBound95,
			r.MeanScore, r.StdDevScore,
			r.Threshold.MinMeanScore,
			formatLatency(r.P50Latency),
			formatLatency(r.P95Latency),
		))
	}
	return sb.String()
}

func formatLatency(d time.Duration) string {
	if d < time.Millisecond {
		return d.Round(time.Microsecond).String()
	}
	return d.Round(time.Millisecond).String()
}

func boolScore(b bool) float64 {
	if b {
		return 1.0
	}
	return 0.0
}
