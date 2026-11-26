package system1

// Activity arbitration priorities (ADR-036, bead cq-2sr.10). The ONE place the numeric
// weights live: each activity's Propose only decides eligibility; the kernel scores a bid as
// weight(kind) * utility. Tunable data, not logic (centralised further by cq-aik.13).

import "github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"

// Priority weights per activity kind, highest first.
const (
	priorityEmergency = 1.00
	priorityDead      = 1.00
	// priorityRecover is the weight of the orphan-stuck Recover root bid (ADR-037); framed
	// intents are recovered by their frame pushing Recover, not by arbitration.
	priorityRecover = 0.97
	// priorityExpedition is the kernel weight of an Expedition bid; its Utility carries the
	// bidder's own priority (e.g. priorityDirective for a SCOUT_FRONTIER directive).
	priorityExpedition = 1.00
	priorityForaging   = 0.95
	priorityShelter    = 0.85
	priorityTask       = 0.75
	// priorityDirective is the weight of the strategic_directive activity and the Utility of
	// the Expedition bid a SCOUT_FRONTIER directive makes.
	priorityDirective    = 0.70
	priorityConstruction = 0.65
	priorityBootstrap    = 0.60
	priorityExploration  = 0.40
)

// defaultPriorities is the stage-independent priority row (read-only).
var defaultPriorities = map[htpda.Kind]float64{
	activityKindEmergency:          priorityEmergency,
	activityKindDead:               priorityDead,
	activityKindRecover:            priorityRecover,
	activityKindExpedition:         priorityExpedition,
	activityKindForaging:           priorityForaging,
	activityKindShelter:            priorityShelter,
	activityKindTaskSubroutine:     priorityTask,
	activityKindStrategicDirective: priorityDirective,
	activityKindConstruction:       priorityConstruction,
	activityKindBootstrap:          priorityBootstrap,
	activityKindExploration:        priorityExploration,
}

// activityPriorityTable is the kernel PriorityTable built from defaultPriorities.
func activityPriorityTable() htpda.PriorityTable {
	return htpda.PriorityTable{htpda.AnyStage: defaultPriorities}
}

// activityPriority returns the configured weight of kind (0 when unknown).
func activityPriority(kind string) float64 { return defaultPriorities[htpda.Kind(kind)] }
