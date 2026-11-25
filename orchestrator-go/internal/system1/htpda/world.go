package htpda

import civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"

// Stage is the derived settlement stage. It only selects a PriorityTable row;
// it is never a dispatch switch. The system1 adapter converts system1.Stage.
type Stage string

// Routine is the derived circadian routine.
type Routine string

// FactReader is the read-only subset of system1.FactGraph that activities may
// use. *system1.FactGraph satisfies it structurally (no import needed).
type FactReader interface {
	GetBool(key string) bool
	GetInt(key string) (int, bool)
	GetFloat(key string) (float64, bool)
	GetString(key string) string
}

// World is the read-only view of one tick. It has no setters, so neither
// Propose nor Step can mutate agent state. Store and Memory readers are added
// by the migration beads that introduce those homes (see ADR-036 Deviations).
type World interface {
	Tick() int64
	Frame() *civquestv1.TelemetryFrame
	Facts() FactReader
	Stage() Stage
	Routine() Routine
}
