package system1

// Fight-vs-flee is decided ONCE per engagement and then held (bead cq-gpa.3).
//
// Before this, EvaluateReflexes recomputed panic every frame from a confidence that moves with
// HP, allies and visible threat, so one zombie produced AttackIntent(flee=true) and
// AttackIntent(flee=false) on alternating frames. The decision now lives in the emergency
// frame's Local (emergencyLocal.Eng); the reflex evaluator asks it for the verdict.
//
// Rules (all tick budgets are design budgets, not measurements):
//   - an engagement starts with the first hostile/attacker target seen while none is live; its
//     ID is "<target entity>@<start tick>". It stays live until no target has been seen for
//     engagementIdleTicks; target switches inside a live engagement keep the verdict.
//   - the default verdict is the reflex's own panic assessment at the start tick (confidence,
//     HP and gear), and it is then held regardless of how confidence moves.
//   - hysteresis: a held "fight" escalates to "flee" at most once, when HP falls to
//     engagementFleeFloorRatio of max, or a creeper starts hissing. A held "flee" never reverts to fight inside the engagement.
//   - a System 2 directive (ENGAGE_THREAT / EVADE_THREAT) issued while the engagement is live
//     overrides the default once; the survival escalations above still win. The reflex never
//     waits for the midbrain: with no directive the default stands.

import "fmt"

const (
	// engagementIdleTicks: ticks without a target before the engagement is over (design budget, ~2s).
	engagementIdleTicks = int64(40)
	// engagementFleeFloorRatio: single fight->flee escalation floor (design budget), the same
	// share of health at which a fight is not started: a live run showed an agent fighting
	// down to 2.5 with a wooden sword under the old floor of 0.20.
	engagementFleeFloorRatio = LowHPThresholdRatio

	// Directive actions the midbrain may issue to steer a live engagement.
	DirectiveActionEngageThreat = "ENGAGE_THREAT"
	DirectiveActionEvadeThreat  = "EVADE_THREAT"
)

// engagement is the held fight-vs-flee verdict (a value type held in emergencyLocal).
type engagement struct {
	Live       bool
	ID         string // "<target>@<start tick>"
	Target     int32
	Start      int64
	LastSeen   int64
	Flee       bool
	Escalated  bool  // fight -> flee escalation already spent
	OverrideBy int64 // IssuedTick of the directive already applied (0 none)
}

// engagementInput is what one reflex evaluation feeds the held verdict.
type engagementInput struct {
	Tick         int64
	Target       int32 // priority hostile / attacker entity (0 when none known)
	HasThreat    bool  // a hostile or attacker is present this frame
	DefaultFlee  bool  // the reflex's own panic assessment this frame
	HP, MaxHP    float64
	CreeperHiss  bool
	DirectiveAct string // active directive action ("" none)
	DirectiveAt  int64  // its issued tick
}

// resolve updates the engagement and returns whether to flee. Pure on its receiver copy.
func (e *engagement) resolve(in engagementInput) bool {
	if e.Live && !in.HasThreat && in.Tick-e.LastSeen > engagementIdleTicks {
		*e = engagement{}
	}
	if !e.Live {
		if !in.HasThreat {
			return in.DefaultFlee
		}
		*e = engagement{Live: true, Target: in.Target, Start: in.Tick, LastSeen: in.Tick, Flee: in.DefaultFlee}
		e.ID = fmt.Sprintf("%d@%d", in.Target, in.Tick)
	}
	if in.HasThreat {
		e.LastSeen = in.Tick
	}
	// Midbrain override: only a directive that arrived while this engagement is live, once.
	if in.DirectiveAt >= e.Start && in.DirectiveAt != e.OverrideBy {
		switch in.DirectiveAct {
		case DirectiveActionEvadeThreat:
			e.Flee, e.OverrideBy = true, in.DirectiveAt
		case DirectiveActionEngageThreat:
			e.Flee, e.OverrideBy = false, in.DirectiveAt
		}
	}
	// Survival escalation: fight -> flee at most once.
	if !e.Flee && !e.Escalated {
		maxHP := in.MaxHP
		if maxHP <= 0 {
			maxHP = 20
		}
		if in.CreeperHiss || in.HP <= engagementFleeFloorRatio*maxHP {
			e.Flee, e.Escalated = true, true
		}
	}
	return e.Flee
}
