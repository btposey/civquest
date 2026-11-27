package system1

import civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"

// StateView is the read-only projection of AgentState exposed to evaluators.
// It deliberately contains no mutating methods: evaluators derive facts from
// state and frame, they never change them.
type StateView interface {
	ActiveTaskInfo() (active bool, kind string, phase TaskPhase, routine Routine)
	GetAssignedBed() *civquestv1.BlockPosProto
	// BedAvailableFor is the read-only bed gate: the agent owns/may claim a bed (cq-390.1).
	BedAvailableFor(agentID string, frame *civquestv1.TelemetryFrame) (registryActive, available bool)
	GetHomeOrigin() *civquestv1.Vec3
	GetOutposts() []*civquestv1.Vec3
	GetPatrolWaypointIndex() int
	GetPatrolWaypoints() []*civquestv1.Vec3
	GetPavingTracker() *RoadPavingTracker
	GetCabinBuilt() int
	IsBootstrapOnCooldown(currentTick int64) bool
	IsCivicPatrolActive() bool
	IsEstablishingNewSettlement() bool
	IsShelterOnCooldown(currentTick int64) bool
	IsTargetUnreachable(x, y, z float64, worldTick int64) bool
	Routine() Routine
	BaseStrategy() Strategy
	SettlementEstablished() bool
	SpatialIndexLocked() *SpatialIndex
	Stage() Stage
	ActiveStrategicDirective(worldTick int64) (StrategicDirectiveState, bool)
	IsCompanionActive() bool
	IsArmoryMaintenanceNeeded() bool
	IsPrimaryDutyBlocked() bool
}

var _ StateView = (*AgentState)(nil)
