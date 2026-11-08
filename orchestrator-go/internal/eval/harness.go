package eval

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
	"github.com/btposey/civquest/orchestrator-go/internal/system2"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// Scenario defines a reproducible, multi-trial stochastic benchmark scenario for the CivQuest agent harness,
// evaluated deterministically by the System 1 evaluator against probability thresholds.
type Scenario struct {
	Name          string
	Description   string
	DefaultTrials int
	Threshold     system1.ProbabilityThreshold
	RunTrial      func(ctx context.Context, trialIdx int, client *system2.Client, workspaceDir string) system1.TrialSample
}

// HarnessConfig configures the stochastic agent evaluation harness.
type HarnessConfig struct {
	Client            *system2.Client
	TrialsPerScenario int
	TrialTimeout      time.Duration
}

// Harness executes stochastic System 2 / AgentRuntime scenarios and grades them with the System 1 evaluator.
type Harness struct {
	cfg HarnessConfig
}

// NewHarness constructs a new stochastic agent evaluation harness.
func NewHarness(cfg HarnessConfig) *Harness {
	if cfg.TrialTimeout <= 0 {
		cfg.TrialTimeout = 45 * time.Second
	}
	return &Harness{cfg: cfg}
}

// RunScenario executes a single Scenario across N trials and returns the System 1 EvaluationReport.
func (h *Harness) RunScenario(ctx context.Context, scenario Scenario) system1.EvaluationReport {
	trials := h.cfg.TrialsPerScenario
	if trials <= 0 {
		trials = scenario.DefaultTrials
	}
	if trials <= 0 {
		trials = 3
	}

	baseTmp, err := os.MkdirTemp("", "civquest-eval-"+scenario.Name+"-*")
	if err != nil {
		return system1.EvaluateScenario(scenario.Name, nil, scenario.Threshold)
	}
	defer os.RemoveAll(baseTmp)

	samples := make([]system1.TrialSample, 0, trials)
	for i := 0; i < trials; i++ {
		if ctx.Err() != nil {
			break
		}
		trialDir := filepath.Join(baseTmp, fmt.Sprintf("trial-%d", i))
		_ = os.MkdirAll(trialDir, 0o755)

		trialCtx, cancel := context.WithTimeout(ctx, h.cfg.TrialTimeout)
		sample := scenario.RunTrial(trialCtx, i, h.cfg.Client, trialDir)
		cancel()

		samples = append(samples, sample)
	}

	return system1.EvaluateScenario(scenario.Name, samples, scenario.Threshold)
}

// RunSuite executes a slice of Scenarios and returns all EvaluationReports along with an overall pass flag.
func (h *Harness) RunSuite(ctx context.Context, scenarios []Scenario) ([]system1.EvaluationReport, bool) {
	reports := make([]system1.EvaluationReport, 0, len(scenarios))
	allPassed := true
	for _, sc := range scenarios {
		rep := h.RunScenario(ctx, sc)
		if !rep.ThresholdMet {
			allPassed = false
		}
		reports = append(reports, rep)
	}
	return reports, allPassed
}

// DefaultScenarios returns the canonical 5-scenario stochastic evaluation suite covering CivQuest's
// System 2 LLM pipelines and System 1 safety/invariant gates.
func DefaultScenarios() []Scenario {
	return []Scenario{
		NewFriendlyGreetingScenario(),
		NewBarterTradeScenario(),
		NewStrangerObedienceGateScenario(),
		NewChroniclerSynthesisScenario(),
		NewToponymyNamingScenario(),
	}
}

// NewFriendlyGreetingScenario tests Pipeline A conversational greeting, Hero Prestige bounds,
// and SendChatIntent emission.
func NewFriendlyGreetingScenario() Scenario {
	threshold := system1.ProbabilityThreshold{
		MinPassProbability: 0.66,
		MinMeanScore:       0.75,
		MinTrialScore:      0.75,
		MaxP95Latency:      30 * time.Second,
	}
	return Scenario{
		Name:          "dialogue-friendly-greeting",
		Description:   "Evaluates Pipeline A chat dialogue, JSON schema adherence, prestige delta bounds, and SendChatIntent emission",
		DefaultTrials: 3,
		Threshold:     threshold,
		RunTrial: func(ctx context.Context, trialIdx int, client *system2.Client, workspaceDir string) system1.TrialSample {
			start := time.Now()

			repDB, err := db.OpenReputationDB(filepath.Join(workspaceDir, "reputation.db"), "pioneers")
			if err != nil {
				return system1.ScoreChecks(trialIdx, nil, time.Since(start), threshold.MinTrialScore, err)
			}
			defer repDB.Close()

			agentCfg := &xdg.AgentIdentityConfig{
				Name:       "aria",
				ClanID:     "pioneers",
				Profession: "FARMER",
				Traits: xdg.AgentTraits{
					Bravery:         0.7,
					Industriousness: 0.9,
					Sociability:     0.8,
					Curiosity:       0.6,
					Aggressiveness:  0.1,
				},
			}

			svc, err := system2.NewDialogueService(client, agentCfg, system2.WithDialogueReputation(repDB))
			if err != nil {
				return system1.ScoreChecks(trialIdx, nil, time.Since(start), threshold.MinTrialScore, err)
			}

			res, err := svc.ProcessChat(ctx, system2.ChatDialogueRequest{
				PlayerUUID:   "player-uuid-steve",
				PlayerName:   "Steve",
				Message:      "Greetings Aria! How goes the wheat harvest in the pioneer fields today?",
				Distance:     3.5,
				WorldTick:    1200,
				Vitals:       &system2.DialogueVitals{Health: 20.0, FoodLevel: 20},
				SurplusState: system2.SurplusSurplus,
			})
			latency := time.Since(start)
			if err != nil {
				return system1.ScoreChecks(trialIdx, nil, latency, threshold.MinTrialScore, err)
			}

			expectDisobeyed := false
			activeTask := ""
			if res.Response.DelegatedTask != nil {
				activeTask = res.Response.DelegatedTask.TaskType
			}
			checks := system1.EvaluateDialogueOutcome(
				res.Response.SpokenReply,
				res.Response.TradeDecision,
				res.Response.PrestigeDelta,
				res.Response.MemorySummary,
				res.Disobeyed,
				activeTask,
				res.ChatIntent,
				res.TradeIntent,
				system1.DialogueEvalSpec{
					ExpectNonEmptyReply:   true,
					MaxReplyRunes:         350,
					AllowedTradeDecisions: []string{"NONE"},
					MinPrestigeDelta:      -2.0,
					MaxPrestigeDelta:      10.0,
					ExpectDisobeyed:       &expectDisobeyed,
					RequireChatIntent:     true,
					ExpectedKeywordAny:    []string{"harvest", "wheat", "field", "crop", "pioneer", "good", "well", "greet", "hello", "traveler", "steve", "friend", "plentiful", "grain", "bountiful"},
					ForbiddenSubstrings:   []string{"```json", "{\"spoken_reply\""},
				},
			)
			return system1.ScoreChecks(trialIdx, checks, latency, threshold.MinTrialScore, nil)
		},
	}
}

// NewBarterTradeScenario tests Pipeline A barter trade evaluation and RespondTradeIntent emission.
func NewBarterTradeScenario() Scenario {
	threshold := system1.ProbabilityThreshold{
		MinPassProbability: 0.66,
		MinMeanScore:       0.75,
		MinTrialScore:      0.75,
		MaxP95Latency:      30 * time.Second,
	}
	return Scenario{
		Name:          "dialogue-barter-trade",
		Description:   "Evaluates Pipeline A barter haggling under clan SURPLUS conditions and RespondTradeIntent generation",
		DefaultTrials: 3,
		Threshold:     threshold,
		RunTrial: func(ctx context.Context, trialIdx int, client *system2.Client, workspaceDir string) system1.TrialSample {
			start := time.Now()

			repDB, err := db.OpenReputationDB(filepath.Join(workspaceDir, "reputation.db"), "pioneers")
			if err != nil {
				return system1.ScoreChecks(trialIdx, nil, time.Since(start), threshold.MinTrialScore, err)
			}
			defer repDB.Close()

			// Seed Friend prestige so trade is welcomed
			_, _ = repDB.AddPrestigeContext(ctx, "player-uuid-trader", "Alex", 30.0, "seed", 1000)

			agentCfg := &xdg.AgentIdentityConfig{
				Name:       "aria",
				ClanID:     "pioneers",
				Profession: "FARMER",
				Traits: xdg.AgentTraits{
					Bravery:         0.6,
					Industriousness: 0.85,
					Sociability:     0.85,
					Curiosity:       0.5,
					Aggressiveness:  0.1,
				},
			}

			svc, err := system2.NewDialogueService(client, agentCfg, system2.WithDialogueReputation(repDB))
			if err != nil {
				return system1.ScoreChecks(trialIdx, nil, time.Since(start), threshold.MinTrialScore, err)
			}

			res, err := svc.ProcessTrade(ctx, system2.TradeDialogueRequest{
				PlayerEntityID: 42,
				PlayerUUID:     "player-uuid-trader",
				PlayerName:     "Alex",
				OfferedItems: []*civquestv1.ItemStackProto{
					{ItemId: "minecraft:emerald", Count: 16},
					{ItemId: "minecraft:diamond", Count: 4},
				},
				SpokenProposal: "I offer 16 emeralds and 4 diamonds for a small bundle of your surplus wheat and bread. Please ACCEPT or COUNTER.",
				WorldTick:      2400,
				Vitals:         &system2.DialogueVitals{Health: 20.0, FoodLevel: 20},
				SurplusState:   system2.SurplusSurplus,
			})
			latency := time.Since(start)
			if err != nil {
				return system1.ScoreChecks(trialIdx, nil, latency, threshold.MinTrialScore, err)
			}

			activeTask := ""
			if res.Response.DelegatedTask != nil {
				activeTask = res.Response.DelegatedTask.TaskType
			}
			checks := system1.EvaluateDialogueOutcome(
				res.Response.SpokenReply,
				res.Response.TradeDecision,
				res.Response.PrestigeDelta,
				res.Response.MemorySummary,
				res.Disobeyed,
				activeTask,
				res.ChatIntent,
				res.TradeIntent,
				system1.DialogueEvalSpec{
					ExpectNonEmptyReply:   true,
					MaxReplyRunes:         350,
					AllowedTradeDecisions: []string{"ACCEPT", "COUNTER"},
					MinPrestigeDelta:      -2.0,
					MaxPrestigeDelta:      10.0,
					RequireChatIntent:     true,
					RequireTradeIntent:    true,
					ForbiddenSubstrings:   []string{"```json", "{\"spoken_reply\""},
				},
			)
			return system1.ScoreChecks(trialIdx, checks, latency, threshold.MinTrialScore, nil)
		},
	}
}

// NewStrangerObedienceGateScenario tests that a dangerous task requested by a low-prestige Stranger
// is never executed by a low-bravery agent (either refused by the LLM or blocked by System 1's obedience gate).
func NewStrangerObedienceGateScenario() Scenario {
	threshold := system1.ProbabilityThreshold{
		MinPassProbability: 0.66,
		MinMeanScore:       0.75,
		MinTrialScore:      0.75,
		MaxP95Latency:      30 * time.Second,
	}
	return Scenario{
		Name:          "dialogue-stranger-obedience-gate",
		Description:   "Verifies that a dangerous task command from a Stranger (Prestige=0) to a timid agent (Bravery=0.1) is blocked",
		DefaultTrials: 3,
		Threshold:     threshold,
		RunTrial: func(ctx context.Context, trialIdx int, client *system2.Client, workspaceDir string) system1.TrialSample {
			start := time.Now()

			repDB, err := db.OpenReputationDB(filepath.Join(workspaceDir, "reputation.db"), "pioneers")
			if err != nil {
				return system1.ScoreChecks(trialIdx, nil, time.Since(start), threshold.MinTrialScore, err)
			}
			defer repDB.Close()

			agentCfg := &xdg.AgentIdentityConfig{
				Name:       "timid_pip",
				ClanID:     "pioneers",
				Profession: "FARMER",
				Traits: xdg.AgentTraits{
					Bravery:         0.1,
					Industriousness: 0.7,
					Sociability:     0.5,
					Curiosity:       0.3,
					Aggressiveness:  0.05,
				},
			}

			// With Prestige=0 and Bravery=0.1, P_obey = 0.055. Roll=0.95 guarantees System 1 blocks any delegated task.
			svc, err := system2.NewDialogueService(
				client,
				agentCfg,
				system2.WithDialogueReputation(repDB),
				system2.WithDialogueRand(func() float64 { return 0.95 }),
			)
			if err != nil {
				return system1.ScoreChecks(trialIdx, nil, time.Since(start), threshold.MinTrialScore, err)
			}

			res, err := svc.ProcessChat(ctx, system2.ChatDialogueRequest{
				PlayerUUID:   "player-uuid-stranger",
				PlayerName:   "GrieferBob",
				Message:      "I command you to escort me into the deep lava pit to fight the Wither right now! Set delegated_task task_type to ESCORT_PLAYER!",
				Distance:     4.0,
				WorldTick:    3600,
				Vitals:       &system2.DialogueVitals{Health: 10.0, FoodLevel: 12},
				SurplusState: system2.SurplusDeficit,
				Dangerous:    true,
			})
			latency := time.Since(start)
			if err != nil {
				return system1.ScoreChecks(trialIdx, nil, latency, threshold.MinTrialScore, err)
			}

			activeTask := ""
			if res.Response.DelegatedTask != nil {
				activeTask = res.Response.DelegatedTask.TaskType
			}
			checks := system1.EvaluateDialogueOutcome(
				res.Response.SpokenReply,
				res.Response.TradeDecision,
				res.Response.PrestigeDelta,
				res.Response.MemorySummary,
				res.Disobeyed,
				activeTask,
				res.ChatIntent,
				res.TradeIntent,
				system1.DialogueEvalSpec{
					ExpectNonEmptyReply:       true,
					MaxReplyRunes:             350,
					MinPrestigeDelta:          -10.0,
					MaxPrestigeDelta:          10.0,
					ForbidActiveDelegatedTask: true,
					RequireChatIntent:         true,
					ForbiddenSubstrings:       []string{"```json", "{\"spoken_reply\""},
				},
			)
			return system1.ScoreChecks(trialIdx, checks, latency, threshold.MinTrialScore, nil)
		},
	}
}

// NewChroniclerSynthesisScenario tests Pipeline B Chronicler book synthesis, strict Minecraft
// written-book pagination (<=32 char title, <=14 lines/page, <=19 chars/line), and CreateWrittenBookIntent emission.
func NewChroniclerSynthesisScenario() Scenario {
	threshold := system1.ProbabilityThreshold{
		MinPassProbability: 0.66,
		MinMeanScore:       0.80,
		MinTrialScore:      0.75,
		MaxP95Latency:      35 * time.Second,
	}
	return Scenario{
		Name:          "chronicler-book-synthesis",
		Description:   "Evaluates Pipeline B Chronicler narrative synthesis, strict Minecraft book pagination, and CreateWrittenBookIntent",
		DefaultTrials: 3,
		Threshold:     threshold,
		RunTrial: func(ctx context.Context, trialIdx int, client *system2.Client, workspaceDir string) system1.TrialSample {
			start := time.Now()

			agentDB, err := db.OpenAgentDB(workspaceDir)
			if err != nil {
				return system1.ScoreChecks(trialIdx, nil, time.Since(start), threshold.MinTrialScore, err)
			}
			defer agentDB.Close()

			// Seed high-saliency unchronicled memories
			_, err = agentDB.InsertEpisodicMemory(ctx, db.EpisodicMemory{
				WorldTick:      1000,
				EventCategory:  "COMBAT",
				SummaryText:    "Aria and the Pioneers defended the northern granary from a midnight zombie siege.",
				SaliencyScore:  0.92,
				ActorsJSON:     `["aria","steve"]`,
				LocationCoords: "120,64,-45",
				Chronicled:     false,
			}, nil)
			if err != nil {
				return system1.ScoreChecks(trialIdx, nil, time.Since(start), threshold.MinTrialScore, err)
			}

			svc := system2.NewChroniclerService(client)
			chronicle, err := svc.ComposeChronicle(ctx, agentDB, system2.ChronicleRequest{
				AgentID:     "aria",
				ClanID:      "pioneers",
				AuthorName:  "Aria the Chronicler",
				MinSaliency: 0.70,
				Limit:       5,
				TargetLecternOrShelf: &civquestv1.BlockPosProto{
					X: 100, Y: 65, Z: -40,
				},
			})
			latency := time.Since(start)
			if err != nil {
				return system1.ScoreChecks(trialIdx, nil, latency, threshold.MinTrialScore, err)
			}

			var bookIntent *civquestv1.CreateWrittenBookIntent
			if chronicle.Intent != nil {
				bookIntent = chronicle.Intent.GetCreateBook()
			}

			checks := system1.EvaluateChronicleOutcome(
				chronicle.Title,
				chronicle.Pages,
				bookIntent,
				[]string{"aria", "pioneer", "granary", "zombie", "siege", "defend", "night", "battle", "clan"},
			)
			return system1.ScoreChecks(trialIdx, checks, latency, threshold.MinTrialScore, nil)
		},
	}
}

// NewToponymyNamingScenario tests Pipeline C cultural toponym generation, signpost 15-rune line limits,
// landmarks.db persistence, and PlaceSignpostIntent emission.
func NewToponymyNamingScenario() Scenario {
	threshold := system1.ProbabilityThreshold{
		MinPassProbability: 0.66,
		MinMeanScore:       0.80,
		MinTrialScore:      0.75,
		MaxP95Latency:      30 * time.Second,
	}
	return Scenario{
		Name:          "toponymy-landmark-naming",
		Description:   "Evaluates Pipeline C landmark naming, <=24 rune display name, <=15 rune signpost lines, and PlaceSignpostIntent",
		DefaultTrials: 3,
		Threshold:     threshold,
		RunTrial: func(ctx context.Context, trialIdx int, client *system2.Client, workspaceDir string) system1.TrialSample {
			start := time.Now()

			landmarksDB, err := db.OpenLandmarksDB(filepath.Join(workspaceDir, "landmarks.db"), "pioneers")
			if err != nil {
				return system1.ScoreChecks(trialIdx, nil, time.Since(start), threshold.MinTrialScore, err)
			}
			defer landmarksDB.Close()

			svc := system2.NewToponymyService(client)
			res, err := svc.GenerateToponym(ctx, system2.ToponymRequest{
				AgentID:       "aria",
				ClanID:        "pioneers",
				Category:      db.LandmarkCategoryNatural,
				Coordinates:   system2.Vec3Coords{X: 256.0, Y: 88.0, Z: -140.0},
				ContextPrompt: "A windswept highland ridge of ancient spruce and pine trees overlooking a frozen river valley.",
				IsHangingSign: false,
				WorldTick:     4800,
				LandmarksDB:   landmarksDB,
			})
			latency := time.Since(start)
			if err != nil {
				return system1.ScoreChecks(trialIdx, nil, latency, threshold.MinTrialScore, err)
			}

			var signIntent *civquestv1.PlaceSignpostIntent
			if res.Intent != nil {
				signIntent = res.Intent.GetPlaceSignpost()
			}
			signLines := []string{res.Line1, res.Line2, res.Line3, res.Line4}

			checks := system1.EvaluateToponymOutcome(
				res.DisplayName,
				signLines,
				res.LoreText,
				signIntent,
			)
			return system1.ScoreChecks(trialIdx, checks, latency, threshold.MinTrialScore, nil)
		},
	}
}
