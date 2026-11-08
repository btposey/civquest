package eval

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/agent"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// System1Scenario defines a zero-token System 1 impulse/reflex evaluation scenario
// executed through AgentRuntime.HandleTelemetryFrame and graded by the System 1 probability evaluator.
type System1Scenario struct {
	Name        string
	Description string
	Trials      int
	Threshold   system1.ProbabilityThreshold
	RunTrial    func(trialIdx int, rng *rand.Rand, rt *agent.AgentRuntime) system1.TrialSample
}

// RunSystem1Scenario executes a System1Scenario across N stochastic trials using an isolated AgentRuntime
// with zero System 2 services attached (enforcing Invariant 3: Zero Token Survival Reflexes in <5ms).
func RunSystem1Scenario(sc System1Scenario, seed int64) system1.EvaluationReport {
	trials := sc.Trials
	if trials <= 0 {
		trials = 50
	}
	rng := rand.New(rand.NewSource(seed))

	baseTmp, err := os.MkdirTemp("", "civquest-sys1-eval-"+sc.Name+"-*")
	if err != nil {
		return system1.EvaluateScenario(sc.Name, nil, sc.Threshold)
	}
	defer os.RemoveAll(baseTmp)

	samples := make([]system1.TrialSample, 0, trials)
	for i := 0; i < trials; i++ {
		trialDir := filepath.Join(baseTmp, fmt.Sprintf("t-%d", i))
		r := xdg.NewResolver(xdg.WithCustomRoot(trialDir))
		_ = r.InitAgentDirectories("reflex_agent")

		cfg := xdg.DefaultAgentIdentityConfig("reflex_agent")
		cfg.ClanID = "pioneers"
		cfg.Profession = "GUARD"

		rt, err := agent.NewRuntime(agent.Config{
			AgentID:         "reflex_agent",
			ClanID:          "pioneers",
			IdentityConfig:  cfg,
			Resolver:        r,
			IntentQueueSize: 64,
		})
		if err != nil {
			samples = append(samples, system1.ScoreChecks(i, nil, 0, sc.Threshold.MinTrialScore, err))
			continue
		}

		sample := sc.RunTrial(i, rng, rt)
		samples = append(samples, sample)
	}

	return system1.EvaluateScenario(sc.Name, samples, sc.Threshold)
}

// RunSystem1Suite runs all provided System1Scenarios and returns the EvaluationReports and overall pass boolean.
func RunSystem1Suite(scenarios []System1Scenario, seed int64) ([]system1.EvaluationReport, bool) {
	reports := make([]system1.EvaluationReport, 0, len(scenarios))
	allPassed := true
	for idx, sc := range scenarios {
		rep := RunSystem1Scenario(sc, seed+int64(idx*1009))
		if !rep.ThresholdMet {
			allPassed = false
		}
		reports = append(reports, rep)
	}
	return reports, allPassed
}

// DefaultSystem1Scenarios returns the canonical 6-scenario System 1 impulse/reflex benchmark suite.
func DefaultSystem1Scenarios() []System1Scenario {
	return []System1Scenario{
		NewSystem1CombatPanicFleeScenario(),
		NewSystem1CombatConfidentStandScenario(),
		NewSystem1RangedShieldReflexScenario(),
		NewSystem1BiologicalHungerPriorityScenario(),
		NewSystem1DesirePathPavingScenario(),
		NewSystem1StochasticImpulseDistributionScenario(),
	}
}

func drainRuntimeIntents(rt *agent.AgentRuntime) []*civquestv1.AgentIntent {
	var out []*civquestv1.AgentIntent
	ch := rt.IntentQueue()
	for {
		select {
		case intent := <-ch:
			if intent != nil {
				out = append(out, intent)
			}
		default:
			return out
		}
	}
}

// NewSystem1CombatPanicFleeScenario verifies that overwhelmed or critically injured agents
// immediately raise shields, eat food, and sprint-flee in <5ms with zero LLM tokens.
func NewSystem1CombatPanicFleeScenario() System1Scenario {
	threshold := system1.ProbabilityThreshold{
		MinPassProbability: 0.95,
		MinMeanScore:       0.95,
		MinTrialScore:      0.90,
		MaxP95Latency:      5 * time.Millisecond,
	}
	return System1Scenario{
		Name:        "system1-combat-panic-flee",
		Description: "Evaluates zero-token panic flee impulse (shield + eat + sprint navigate) under lethal threat in <5ms",
		Trials:      50,
		Threshold:   threshold,
		RunTrial: func(trialIdx int, rng *rand.Rand, rt *agent.AgentRuntime) system1.TrialSample {
			rt.IdentityConfig().Traits.Bravery = 0.10 + rng.Float64()*0.30
			hp := float32(2.0 + rng.Float64()*5.0) // Critical HP <= 7.0

			frame := &civquestv1.TelemetryFrame{
				WorldTick: int64(100 + trialIdx*10),
				TimeOfDay: 6000,
				Status: &civquestv1.AgentStatus{
					Health:    hp,
					MaxHealth: 20.0,
					FoodLevel: 12,
					Position:  &civquestv1.Vec3{X: 100, Y: 64, Z: 100},
					Inventory: []*civquestv1.ItemStackProto{
						{ItemId: "minecraft:cooked_beef", Count: 8, Slot: 2},
						{ItemId: "minecraft:shield", Count: 1, Slot: 40},
					},
				},
				VisibleEntities: []*civquestv1.VisibleEntity{
					{
						EntityId:   101,
						EntityType: "minecraft:creeper",
						Distance:   float32(2.5 + rng.Float64()*2.0),
						IsHostile:  true,
						FuseTicks:  15,
						Position:   &civquestv1.Vec3{X: 103, Y: 64, Z: 100},
					},
					{
						EntityId:   102,
						EntityType: "minecraft:skeleton",
						Distance:   8.0,
						IsHostile:  true,
						HeldItemId: "minecraft:bow",
						Position:   &civquestv1.Vec3{X: 108, Y: 64, Z: 100},
					},
				},
			}

			start := time.Now()
			rt.HandleTelemetryFrame(frame)
			latency := time.Since(start)

			intents := drainRuntimeIntents(rt)
			var hasPanicAttack, hasSprintFlee, hasEat bool
			for _, in := range intents {
				if att := in.GetAttack(); att != nil && att.FleeImmediately && att.RaiseShield {
					hasPanicAttack = true
				}
				if nav := in.GetNavigate(); nav != nil && nav.Sprint {
					hasSprintFlee = true
				}
				if use := in.GetUseItem(); use != nil && use.IsEatingOrDrinking {
					hasEat = true
				}
			}

			sub5ms := latency < 5*time.Millisecond
			checks := []system1.CheckOutcome{
				{Name: "panic_shield_and_flee_flag", Passed: hasPanicAttack, Weight: 2.0, Score: boolToScore(hasPanicAttack)},
				{Name: "sprint_escape_navigation", Passed: hasSprintFlee, Weight: 2.0, Score: boolToScore(hasSprintFlee)},
				{Name: "emergency_food_consumption", Passed: hasEat, Weight: 1.5, Score: boolToScore(hasEat)},
				{Name: "sub_5ms_reflex_budget", Passed: sub5ms, Weight: 2.0, Score: boolToScore(sub5ms), Reason: latency.String()},
			}
			return system1.ScoreChecks(trialIdx, checks, latency, threshold.MinTrialScore, nil)
		},
	}
}

// NewSystem1CombatConfidentStandScenario verifies that a well-equipped, healthy agent
// stands its ground and counter-attacks a manageable melee threat in <5ms.
func NewSystem1CombatConfidentStandScenario() System1Scenario {
	threshold := system1.ProbabilityThreshold{
		MinPassProbability: 0.95,
		MinMeanScore:       0.95,
		MinTrialScore:      0.90,
		MaxP95Latency:      5 * time.Millisecond,
	}
	return System1Scenario{
		Name:        "system1-combat-confident-stand",
		Description: "Evaluates confident melee stand and counter-attack impulse when confidence >= 1.0 in <5ms",
		Trials:      50,
		Threshold:   threshold,
		RunTrial: func(trialIdx int, rng *rand.Rand, rt *agent.AgentRuntime) system1.TrialSample {
			rt.IdentityConfig().Traits.Bravery = 0.70 + rng.Float64()*0.30
			hp := float32(16.0 + rng.Float64()*4.0)

			frame := &civquestv1.TelemetryFrame{
				WorldTick: int64(200 + trialIdx*10),
				TimeOfDay: 4000,
				Status: &civquestv1.AgentStatus{
					Health:    hp,
					MaxHealth: 20.0,
					FoodLevel: 20,
					Position:  &civquestv1.Vec3{X: 50, Y: 64, Z: 50},
					Inventory: []*civquestv1.ItemStackProto{
						{ItemId: "minecraft:iron_sword", Count: 1, Slot: 0},
						{ItemId: "minecraft:iron_chestplate", Count: 1, Slot: 102},
						{ItemId: "minecraft:shield", Count: 1, Slot: 40},
					},
				},
				VisibleEntities: []*civquestv1.VisibleEntity{
					{
						EntityId:   201,
						EntityType: "minecraft:zombie",
						Distance:   float32(2.0 + rng.Float64()*3.0),
						IsHostile:  true,
						Position:   &civquestv1.Vec3{X: 52, Y: 64, Z: 50},
					},
				},
			}

			start := time.Now()
			rt.HandleTelemetryFrame(frame)
			latency := time.Since(start)

			intents := drainRuntimeIntents(rt)
			var hasConfidentAttack bool
			for _, in := range intents {
				if att := in.GetAttack(); att != nil && att.TargetEntityId == 201 && !att.FleeImmediately {
					hasConfidentAttack = true
				}
			}

			sub5ms := latency < 5*time.Millisecond
			checks := []system1.CheckOutcome{
				{Name: "confident_melee_attack_intent", Passed: hasConfidentAttack, Weight: 3.0, Score: boolToScore(hasConfidentAttack)},
				{Name: "sub_5ms_reflex_budget", Passed: sub5ms, Weight: 2.0, Score: boolToScore(sub5ms), Reason: latency.String()},
			}
			return system1.ScoreChecks(trialIdx, checks, latency, threshold.MinTrialScore, nil)
		},
	}
}

// NewSystem1RangedShieldReflexScenario verifies that ranged hostile threats (Skeletons/Pillagers)
// immediately trigger shield-raising reflexes in <5ms.
func NewSystem1RangedShieldReflexScenario() System1Scenario {
	threshold := system1.ProbabilityThreshold{
		MinPassProbability: 0.95,
		MinMeanScore:       0.95,
		MinTrialScore:      0.90,
		MaxP95Latency:      5 * time.Millisecond,
	}
	return System1Scenario{
		Name:        "system1-ranged-shield-reflex",
		Description: "Evaluates zero-token shield-raising reflex against ranged Skeleton/Pillager attackers in <5ms",
		Trials:      50,
		Threshold:   threshold,
		RunTrial: func(trialIdx int, rng *rand.Rand, rt *agent.AgentRuntime) system1.TrialSample {
			rt.IdentityConfig().Traits.Bravery = 0.80
			dist := float32(6.0 + rng.Float64()*10.0)

			frame := &civquestv1.TelemetryFrame{
				WorldTick: int64(300 + trialIdx*10),
				TimeOfDay: 5000,
				Status: &civquestv1.AgentStatus{
					Health:    20.0,
					MaxHealth: 20.0,
					FoodLevel: 20,
					Position:  &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
					Inventory: []*civquestv1.ItemStackProto{
						{ItemId: "minecraft:iron_sword", Count: 1, Slot: 0},
						{ItemId: "minecraft:shield", Count: 1, Slot: 40},
					},
				},
				VisibleEntities: []*civquestv1.VisibleEntity{
					{
						EntityId:   301,
						EntityType: "minecraft:skeleton",
						Distance:   dist,
						IsHostile:  true,
						HeldItemId: "minecraft:bow",
						Position:   &civquestv1.Vec3{X: float64(dist), Y: 64, Z: 0},
					},
				},
			}

			start := time.Now()
			rt.HandleTelemetryFrame(frame)
			latency := time.Since(start)

			intents := drainRuntimeIntents(rt)
			var shieldRaised bool
			for _, in := range intents {
				if att := in.GetAttack(); att != nil && att.RaiseShield {
					shieldRaised = true
				}
			}

			sub5ms := latency < 5*time.Millisecond
			checks := []system1.CheckOutcome{
				{Name: "ranged_shield_raised", Passed: shieldRaised, Weight: 3.0, Score: boolToScore(shieldRaised)},
				{Name: "sub_5ms_reflex_budget", Passed: sub5ms, Weight: 2.0, Score: boolToScore(sub5ms), Reason: latency.String()},
			}
			return system1.ScoreChecks(trialIdx, checks, latency, threshold.MinTrialScore, nil)
		},
	}
}

// NewSystem1BiologicalHungerPriorityScenario verifies that critical hunger (FoodLevel <= 6)
// triggers immediate food consumption and preempts nighttime sleeping in <5ms.
func NewSystem1BiologicalHungerPriorityScenario() System1Scenario {
	threshold := system1.ProbabilityThreshold{
		MinPassProbability: 0.95,
		MinMeanScore:       0.95,
		MinTrialScore:      0.90,
		MaxP95Latency:      5 * time.Millisecond,
	}
	return System1Scenario{
		Name:        "system1-biological-hunger-priority",
		Description: "Evaluates biological hunger reflex preempting nocturnal sleep when FoodLevel <= 6 in <5ms",
		Trials:      50,
		Threshold:   threshold,
		RunTrial: func(trialIdx int, rng *rand.Rand, rt *agent.AgentRuntime) system1.TrialSample {
			foodLevel := int32(1 + rng.Intn(6)) // [1..6] critical hunger

			frame := &civquestv1.TelemetryFrame{
				WorldTick: int64(14000 + trialIdx*40),
				TimeOfDay: 14500, // Nighttime
				Status: &civquestv1.AgentStatus{
					Health:    18.0,
					MaxHealth: 20.0,
					FoodLevel: foodLevel,
					Position:  &civquestv1.Vec3{X: 10, Y: 64, Z: 10},
					Inventory: []*civquestv1.ItemStackProto{
						{ItemId: "minecraft:bread", Count: 4, Slot: 1},
					},
				},
				InterestingBlocks: []*civquestv1.VisibleBlock{
					{
						BlockId: "minecraft:red_bed",
						Pos:     &civquestv1.BlockPosProto{X: 11, Y: 64, Z: 10},
					},
				},
			}

			start := time.Now()
			rt.HandleTelemetryFrame(frame)
			latency := time.Since(start)

			intents := drainRuntimeIntents(rt)
			var ateFood, sleptInBed bool
			for _, in := range intents {
				if use := in.GetUseItem(); use != nil && use.IsEatingOrDrinking {
					ateFood = true
				}
				if inter := in.GetInteractBlock(); inter != nil {
					sleptInBed = true
				}
			}

			preempted := ateFood && !sleptInBed
			sub5ms := latency < 5*time.Millisecond
			checks := []system1.CheckOutcome{
				{Name: "hunger_preempts_sleep", Passed: preempted, Weight: 3.0, Score: boolToScore(preempted)},
				{Name: "sub_5ms_reflex_budget", Passed: sub5ms, Weight: 2.0, Score: boolToScore(sub5ms), Reason: latency.String()},
			}
			return system1.ScoreChecks(trialIdx, checks, latency, threshold.MinTrialScore, nil)
		},
	}
}

// NewSystem1DesirePathPavingScenario verifies that repeated agent foot traffic (>= 3 traversals)
// over unpaved grass/dirt triggers emergent desire-path paving intents in <5ms.
func NewSystem1DesirePathPavingScenario() System1Scenario {
	threshold := system1.ProbabilityThreshold{
		MinPassProbability: 0.95,
		MinMeanScore:       0.95,
		MinTrialScore:      0.90,
		MaxP95Latency:      5 * time.Millisecond,
	}
	return System1Scenario{
		Name:        "system1-desire-path-paving",
		Description: "Evaluates emergent desire-path road paving impulse after repeated block traversals in <5ms",
		Trials:      50,
		Threshold:   threshold,
		RunTrial: func(trialIdx int, rng *rand.Rand, rt *agent.AgentRuntime) system1.TrialSample {
			bx := int32(rng.Intn(200) - 100)
			bz := int32(rng.Intn(200) - 100)

			makeFrame := func(tick int64, x, z int32) *civquestv1.TelemetryFrame {
				return &civquestv1.TelemetryFrame{
					WorldTick: tick,
					TimeOfDay: 3000,
					Status: &civquestv1.AgentStatus{
						Health:    20.0,
						MaxHealth: 20.0,
						FoodLevel: 20,
						Position:  &civquestv1.Vec3{X: float64(x) + 0.5, Y: 64, Z: float64(z) + 0.5},
						Inventory: []*civquestv1.ItemStackProto{
							{ItemId: "minecraft:iron_shovel", Count: 1, Slot: 0},
							{ItemId: "minecraft:cobblestone", Count: 32, Slot: 1},
						},
					},
					InterestingBlocks: []*civquestv1.VisibleBlock{
						{
							BlockId: "minecraft:grass_block",
							Pos:     &civquestv1.BlockPosProto{X: x, Y: 63, Z: z},
						},
					},
				}
			}

			// Step onto (bx, bz) 3 times separated by stepping onto an adjacent block
			start := time.Now()
			rt.HandleTelemetryFrame(makeFrame(10, bx, bz))
			rt.HandleTelemetryFrame(makeFrame(20, bx+1, bz))
			rt.HandleTelemetryFrame(makeFrame(30, bx, bz))
			rt.HandleTelemetryFrame(makeFrame(40, bx+1, bz))
			rt.HandleTelemetryFrame(makeFrame(50, bx, bz))
			latency := time.Since(start) / 5

			intents := drainRuntimeIntents(rt)
			var paved bool
			for _, in := range intents {
				if ib := in.GetInteractBlock(); ib != nil && ib.InteractionType == civquestv1.InteractBlockIntent_PAVE_ROAD_PATH {
					paved = true
				}
			}

			sub5ms := latency < 5*time.Millisecond
			checks := []system1.CheckOutcome{
				{Name: "desire_path_pave_road_emitted", Passed: paved, Weight: 3.0, Score: boolToScore(paved)},
				{Name: "sub_5ms_reflex_budget", Passed: sub5ms, Weight: 2.0, Score: boolToScore(sub5ms), Reason: latency.String()},
			}
			return system1.ScoreChecks(trialIdx, checks, latency, threshold.MinTrialScore, nil)
		},
	}
}

// NewSystem1StochasticImpulseDistributionScenario evaluates the probabilistic fight-vs-flee
// distribution when Bravery is sampled stochastically in [0.0, 1.0] against a Pillager (threat=25):
// Confidence = (15.0 / 25.0) * 1.15 * (0.5 + Bravery) = 0.69 * (0.5 + Bravery).
// Confidence >= 1.0 iff Bravery >= (1.0/0.69 - 0.5) = 0.949, or against a Skeleton (threat=20):
// Confidence = (15.0 / 20.0) * 1.15 * (0.5 + Bravery) = 0.8625 * (0.5 + Bravery) >= 1.0 iff Bravery >= 0.6594 (~34% stand, ~66% flee).
// With HP=18 against Skeleton (threat=20) and M_gear=1.15:
// Confidence = (18/20)*1.15*(0.5+Bravery) = 1.035*(0.5+Bravery) >= 1.0 iff Bravery >= 0.4662 (theoretical P(stand) ≈ 0.534).
func NewSystem1StochasticImpulseDistributionScenario() System1Scenario {
	threshold := system1.ProbabilityThreshold{
		MinPassProbability: 0.42, // Theoretical P(stand) ≈ 0.534; over N=100 seeded trials, pass rate must exceed 0.42
		MinMeanScore:       0.42,
		MinTrialScore:      0.90,
		MaxP95Latency:      5 * time.Millisecond,
	}
	return System1Scenario{
		Name:        "system1-stochastic-impulse-distribution",
		Description: "Evaluates stochastic trait-weighted fight-vs-flee impulse probability distribution against theoretical System 1 curve",
		Trials:      100,
		Threshold:   threshold,
		RunTrial: func(trialIdx int, rng *rand.Rand, rt *agent.AgentRuntime) system1.TrialSample {
			bravery := rng.Float64() // Uniform [0.0, 1.0]
			rt.IdentityConfig().Traits.Bravery = bravery

			frame := &civquestv1.TelemetryFrame{
				WorldTick: int64(500 + trialIdx*10),
				TimeOfDay: 6000,
				Status: &civquestv1.AgentStatus{
					Health:    18.0,
					MaxHealth: 20.0,
					FoodLevel: 20,
					Position:  &civquestv1.Vec3{X: 0, Y: 64, Z: 0},
					Inventory: []*civquestv1.ItemStackProto{
						{ItemId: "minecraft:iron_sword", Count: 1, Slot: 0},
					},
				},
				VisibleEntities: []*civquestv1.VisibleEntity{
					{
						EntityId:   601,
						EntityType: "minecraft:skeleton",
						Distance:   4.0,
						IsHostile:  true,
						Position:   &civquestv1.Vec3{X: 4, Y: 64, Z: 0},
					},
				},
			}

			start := time.Now()
			rt.HandleTelemetryFrame(frame)
			latency := time.Since(start)

			intents := drainRuntimeIntents(rt)
			var stoodGround bool
			for _, in := range intents {
				if att := in.GetAttack(); att != nil && !att.FleeImmediately && att.TargetEntityId == 601 {
					stoodGround = true
				}
			}

			sub5ms := latency < 5*time.Millisecond
			checks := []system1.CheckOutcome{
				{Name: "stood_ground_impulse", Passed: stoodGround, Weight: 3.0, Score: boolToScore(stoodGround), Reason: fmt.Sprintf("bravery=%.2f", bravery)},
				{Name: "sub_5ms_reflex_budget", Passed: sub5ms, Weight: 1.0, Score: boolToScore(sub5ms), Reason: latency.String()},
			}
			return system1.ScoreChecks(trialIdx, checks, latency, threshold.MinTrialScore, nil)
		},
	}
}

func boolToScore(b bool) float64 {
	if b {
		return 1.0
	}
	return 0.0
}
