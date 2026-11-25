package system1

import (
	"math"
	"time"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"google.golang.org/protobuf/proto"
)

// CachedItem represents an individual inventory item slot snapshot.
type CachedItem struct {
	Slot          int32  `json:"slot"`
	ItemID        string `json:"item_id"`
	Count         int32  `json:"count"`
	Durability    int32  `json:"durability,omitempty"`
	MaxDurability int32  `json:"max_durability,omitempty"`
	CustomName    string `json:"custom_name,omitempty"`
}

// NavPosition represents 3D coordinates for navigation destinations.
type NavPosition struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

// RuntimeTimers tracks cooldowns, rate limits, and scheduling timestamps for an agent.
type RuntimeTimers struct {
	LastEatingTick        int64
	LastMotionLogTick     int64
	LastMapPersistTick    int64
	LastTradeReassessTick int64
	LastChronicleAttempt  time.Time
	LastChronicleTick     int64
	LastToponymyAttempt   time.Time
	LastToponymyTick      int64
	LastToponymyPos       *civquestv1.Vec3
	LastTradeTick         int64
	LastTradePlayerUUID   string
	LastTradeAttemptAt    time.Time
	ClanSizeResolved      bool
	LastClanSizeTick      int64
}

// RuntimeNavTracking tracks active navigation destinations, POI targets, and follow throttling.
type RuntimeNavTracking struct {
	Destination         *NavPosition
	TargetPOIID         string
	LastHarvestPos      *civquestv1.BlockPosProto
	LastFollowNavTarget *civquestv1.Vec3
	LastFollowNavTick   int64
}

// RuntimeCombatTracking tracks flee deduplication and combat encounter summaries.
type RuntimeCombatTracking struct {
	LastFleeTarget       *civquestv1.Vec3
	LastFleeTick         int64
	LastCombatMemoryTick int64
	LastCombatSummary    string
}

// RuntimeIdleTracking tracks idle dwell timestamps and daily contemplation markers.
type RuntimeIdleTracking struct {
	LastContemplatedDay       int64
	LastIdleContemplationTick int64
	IdleStartTick             int64
	HasIdleStartTick          bool
	LastActivePos             [3]float64
	HasLastActivePos          bool
}

// RuntimeViewerState tracks transition logging markers and System 2 viewer reflection strings.
type RuntimeViewerState struct {
	LastLoggedRoutine RoutineState
	LastLoggedGoal    string
	LatestThought     string
	LatestDialogue    string
	LatestChronicle   string
}

// RuntimeCaches stores telemetry frame snapshots, inventory caches, safe nodes, and trade valuations.
type RuntimeCaches struct {
	SafeNodes          []db.MapNode
	TradeValuations    map[string]db.TradeValuation
	HasCachedInventory bool
	LastCachedItems    []CachedItem
	LatestFrame        *civquestv1.TelemetryFrame
	LastProcessedSeqID int64
}

// RuntimeTracking consolidates mutable per-agent runtime state under AgentState ownership.
type RuntimeTracking struct {
	Timers RuntimeTimers
	Nav    RuntimeNavTracking
	Combat RuntimeCombatTracking
	Idle   RuntimeIdleTracking
	Viewer RuntimeViewerState
	Caches RuntimeCaches
}

func cloneVec3(v *civquestv1.Vec3) *civquestv1.Vec3 {
	if v == nil {
		return nil
	}
	return proto.Clone(v).(*civquestv1.Vec3)
}

func cloneBlockPosProto(p *civquestv1.BlockPosProto) *civquestv1.BlockPosProto {
	if p == nil {
		return nil
	}
	return proto.Clone(p).(*civquestv1.BlockPosProto)
}

func cloneNavPosition(p *NavPosition) *NavPosition {
	if p == nil {
		return nil
	}
	cp := *p
	return &cp
}

// LastEatingTick safely returns the world tick of the last consumption event.
func (s *AgentState) LastEatingTick() int64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runtimeState.Timers.LastEatingTick
}

// GetLastEatingTick safely returns the world tick of the last consumption event.
func (s *AgentState) GetLastEatingTick() int64 {
	return s.LastEatingTick()
}

// SetLastEatingTick safely records the world tick of an item consumption event.
func (s *AgentState) SetLastEatingTick(tick int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimeState.Timers.LastEatingTick = tick
}

// LastMotionLogTick returns the world tick of the last sampled motion telemetry log.
func (s *AgentState) LastMotionLogTick() int64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runtimeState.Timers.LastMotionLogTick
}

// SetLastMotionLogTick sets the world tick of the last sampled motion telemetry log.
func (s *AgentState) SetLastMotionLogTick(tick int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimeState.Timers.LastMotionLogTick = tick
}

// TryAdvanceMotionLogTick returns true and updates the motion log tick if interval ticks have elapsed.
func (s *AgentState) TryAdvanceMotionLogTick(worldTick, interval int64) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	last := s.runtimeState.Timers.LastMotionLogTick
	if last == 0 || worldTick < last || worldTick-last >= interval {
		s.runtimeState.Timers.LastMotionLogTick = worldTick
		return true
	}
	return false
}

// LastMapPersistTick returns the last world tick at which map graph persistence ran.
func (s *AgentState) LastMapPersistTick() int64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runtimeState.Timers.LastMapPersistTick
}

// SetLastMapPersistTick sets the last world tick at which map graph persistence ran.
func (s *AgentState) SetLastMapPersistTick(tick int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimeState.Timers.LastMapPersistTick = tick
}

// LastTradeReassessTick returns the world tick of the last trade valuation reassessment.
func (s *AgentState) LastTradeReassessTick() int64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runtimeState.Timers.LastTradeReassessTick
}

// SetLastTradeReassessTick sets the world tick of the last trade valuation reassessment.
func (s *AgentState) SetLastTradeReassessTick(tick int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimeState.Timers.LastTradeReassessTick = tick
}

// TryAdvanceTradeReassessTick returns true and updates LastTradeReassessTick if interval ticks have elapsed.
func (s *AgentState) TryAdvanceTradeReassessTick(worldTick, interval int64) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	last := s.runtimeState.Timers.LastTradeReassessTick
	if last == 0 || worldTick-last >= interval {
		s.runtimeState.Timers.LastTradeReassessTick = worldTick
		return true
	}
	return false
}

// ChronicleTiming returns the timestamp and world tick of the last chronicle synthesis attempt.
func (s *AgentState) ChronicleTiming() (time.Time, int64) {
	if s == nil {
		return time.Time{}, 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runtimeState.Timers.LastChronicleAttempt, s.runtimeState.Timers.LastChronicleTick
}

// SetChronicleTiming updates the timestamp and world tick of the last chronicle synthesis attempt.
func (s *AgentState) SetChronicleTiming(attempt time.Time, tick int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimeState.Timers.LastChronicleAttempt = attempt
	s.runtimeState.Timers.LastChronicleTick = tick
}

// CanAttemptChronicle reports whether chronicle rate limits have elapsed.
func (s *AgentState) CanAttemptChronicle(now time.Time, worldTick int64, minDuration time.Duration, minTicks int64) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	t := &s.runtimeState.Timers
	if now.Sub(t.LastChronicleAttempt) < minDuration || (t.LastChronicleTick > 0 && worldTick-t.LastChronicleTick < minTicks) {
		return false
	}
	return true
}

// ToponymyTiming returns the timestamp, world tick, and cloned coordinates of the last toponymy attempt.
func (s *AgentState) ToponymyTiming() (time.Time, int64, *civquestv1.Vec3) {
	if s == nil {
		return time.Time{}, 0, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runtimeState.Timers.LastToponymyAttempt, s.runtimeState.Timers.LastToponymyTick, cloneVec3(s.runtimeState.Timers.LastToponymyPos)
}

// SetToponymyTiming updates the timestamp, world tick, and coordinates of the last toponymy attempt.
func (s *AgentState) SetToponymyTiming(attempt time.Time, tick int64, pos *civquestv1.Vec3) {
	if s == nil {
		return
	}
	cloned := cloneVec3(pos)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimeState.Timers.LastToponymyAttempt = attempt
	s.runtimeState.Timers.LastToponymyTick = tick
	s.runtimeState.Timers.LastToponymyPos = cloned
}

// CanAttemptToponymy reports whether toponymy rate limits and minimum distance from the last site are satisfied.
func (s *AgentState) CanAttemptToponymy(now time.Time, worldTick int64, pos *civquestv1.Vec3, minDuration time.Duration, minTicks int64, minDist float64) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	t := &s.runtimeState.Timers
	if now.Sub(t.LastToponymyAttempt) < minDuration || (t.LastToponymyTick > 0 && worldTick-t.LastToponymyTick < minTicks) {
		return false
	}
	if t.LastToponymyPos != nil && pos != nil {
		dx := pos.X - t.LastToponymyPos.X
		dz := pos.Z - t.LastToponymyPos.Z
		if math.Sqrt(dx*dx+dz*dz) < minDist {
			return false
		}
	}
	return true
}

// LastTradeAttempt returns the player UUID, world tick, and wall-clock time of the last trade attempt.
func (s *AgentState) LastTradeAttempt() (string, int64, time.Time) {
	if s == nil {
		return "", 0, time.Time{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	t := &s.runtimeState.Timers
	return t.LastTradePlayerUUID, t.LastTradeTick, t.LastTradeAttemptAt
}

// SetLastTradeAttempt records the player UUID, world tick, and wall-clock time of a trade attempt.
func (s *AgentState) SetLastTradeAttempt(playerUUID string, tick int64, at time.Time) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimeState.Timers.LastTradePlayerUUID = playerUUID
	s.runtimeState.Timers.LastTradeTick = tick
	s.runtimeState.Timers.LastTradeAttemptAt = at
}

// TryClaimTradeAttempt checks trade debouncing for playerUUID and atomically records the attempt if allowed.
// Returns true when the trade attempt is permitted (not debounced).
func (s *AgentState) TryClaimTradeAttempt(playerUUID string, worldTick int64, now time.Time, cooldownTicks int64, cooldownDur time.Duration) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t := &s.runtimeState.Timers
	debounced := playerUUID == t.LastTradePlayerUUID &&
		((worldTick >= t.LastTradeTick && worldTick-t.LastTradeTick < cooldownTicks) || now.Sub(t.LastTradeAttemptAt) < cooldownDur)
	if debounced {
		return false
	}
	t.LastTradePlayerUUID = playerUUID
	t.LastTradeTick = worldTick
	t.LastTradeAttemptAt = now
	return true
}

// ClanSizeTiming returns whether clan size has been resolved and the world tick of the last resolution.
func (s *AgentState) ClanSizeTiming() (bool, int64) {
	if s == nil {
		return false, 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runtimeState.Timers.ClanSizeResolved, s.runtimeState.Timers.LastClanSizeTick
}

// SetClanSizeTiming updates clan size resolution state and tick.
func (s *AgentState) SetClanSizeTiming(resolved bool, tick int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimeState.Timers.ClanSizeResolved = resolved
	s.runtimeState.Timers.LastClanSizeTick = tick
}

// TryClaimClanSizeRefresh returns true and updates clan size refresh state if resolution or periodic refresh is due.
func (s *AgentState) TryClaimClanSizeRefresh(worldTick, refreshIntervalTicks int64) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t := &s.runtimeState.Timers
	shouldResolve := !t.ClanSizeResolved || (worldTick >= t.LastClanSizeTick && worldTick-t.LastClanSizeTick >= refreshIntervalTicks)
	if shouldResolve {
		t.ClanSizeResolved = true
		t.LastClanSizeTick = worldTick
	}
	return shouldResolve
}

// NavDestination returns a defensive copy of the current navigation destination target.
func (s *AgentState) NavDestination() *NavPosition {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneNavPosition(s.runtimeState.Nav.Destination)
}

// SetNavDestination updates the current navigation destination target.
func (s *AgentState) SetNavDestination(dest *NavPosition) {
	if s == nil {
		return
	}
	cloned := cloneNavPosition(dest)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimeState.Nav.Destination = cloned
}

// NavTargetPOIID returns the currently tracked POI target ID for navigation.
func (s *AgentState) NavTargetPOIID() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runtimeState.Nav.TargetPOIID
}

// SetNavTargetPOIID sets the tracked POI target ID for navigation.
func (s *AgentState) SetNavTargetPOIID(poiID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimeState.Nav.TargetPOIID = poiID
}

// ClearNavTarget clears both the tracked navigation destination and POI target ID.
func (s *AgentState) ClearNavTarget() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimeState.Nav.Destination = nil
	s.runtimeState.Nav.TargetPOIID = ""
}

// TakeNavTargetForStuck atomically reads and clears the tracked POI target ID and destination.
func (s *AgentState) TakeNavTargetForStuck() (string, *NavPosition) {
	if s == nil {
		return "", nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	poiID := s.runtimeState.Nav.TargetPOIID
	dest := cloneNavPosition(s.runtimeState.Nav.Destination)
	s.runtimeState.Nav.TargetPOIID = ""
	s.runtimeState.Nav.Destination = nil
	return poiID, dest
}

// LastHarvestPos returns a defensive copy of the last queued harvest block coordinate.
func (s *AgentState) LastHarvestPos() *civquestv1.BlockPosProto {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneBlockPosProto(s.runtimeState.Nav.LastHarvestPos)
}

// SetLastHarvestPos stores a defensive copy of the last queued harvest block coordinate.
func (s *AgentState) SetLastHarvestPos(pos *civquestv1.BlockPosProto) {
	if s == nil {
		return
	}
	cloned := cloneBlockPosProto(pos)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimeState.Nav.LastHarvestPos = cloned
}

// TakeLastHarvestPos atomically returns a defensive copy of LastHarvestPos and clears it.
func (s *AgentState) TakeLastHarvestPos() *civquestv1.BlockPosProto {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cloned := cloneBlockPosProto(s.runtimeState.Nav.LastHarvestPos)
	s.runtimeState.Nav.LastHarvestPos = nil
	return cloned
}

// LastFollowNavTarget returns a defensive copy of the last companion follow navigation target and tick.
func (s *AgentState) LastFollowNavTarget() (*civquestv1.Vec3, int64) {
	if s == nil {
		return nil, 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneVec3(s.runtimeState.Nav.LastFollowNavTarget), s.runtimeState.Nav.LastFollowNavTick
}

// SetLastFollowNavTarget stores a defensive copy of the companion follow navigation target and tick.
func (s *AgentState) SetLastFollowNavTarget(target *civquestv1.Vec3, tick int64) {
	if s == nil {
		return
	}
	cloned := cloneVec3(target)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimeState.Nav.LastFollowNavTarget = cloned
	s.runtimeState.Nav.LastFollowNavTick = tick
}

// TryUpdateFollowNavTarget checks whether follow navigation to target should proceed (not deduplicated).
// If not deduplicated, it records the cloned target and worldTick and returns true.
func (s *AgentState) TryUpdateFollowNavTarget(target *civquestv1.Vec3, worldTick int64, minDist float64, cooldownTicks int64) bool {
	if s == nil || target == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.runtimeState.Nav.LastFollowNavTarget
	if prev != nil {
		dx := target.X - prev.X
		dy := target.Y - prev.Y
		dz := target.Z - prev.Z
		distTarget := math.Sqrt(dx*dx + dy*dy + dz*dz)
		ticksElapsed := worldTick - s.runtimeState.Nav.LastFollowNavTick
		if distTarget < minDist && ticksElapsed <= cooldownTicks && ticksElapsed >= 0 {
			return false
		}
	}
	s.runtimeState.Nav.LastFollowNavTarget = cloneVec3(target)
	s.runtimeState.Nav.LastFollowNavTick = worldTick
	return true
}

// LastFleeTarget returns a defensive copy of the last flee navigation target.
func (s *AgentState) LastFleeTarget() *civquestv1.Vec3 {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneVec3(s.runtimeState.Combat.LastFleeTarget)
}

// LastFleeTick returns the world tick of the last flee navigation intent.
func (s *AgentState) LastFleeTick() int64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runtimeState.Combat.LastFleeTick
}

// ClearFleeTarget clears the flee target deduplication state.
func (s *AgentState) ClearFleeTarget() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimeState.Combat.LastFleeTarget = nil
	s.runtimeState.Combat.LastFleeTick = 0
}

// ShouldDeduplicateFleeNav checks whether a flee navigate intent duplicates a recent one within 2m and 10 ticks.
// If not deduplicated, it stores the cloned target and worldTick and returns false.
func (s *AgentState) ShouldDeduplicateFleeNav(worldTick int64, target *civquestv1.Vec3) bool {
	if s == nil || target == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.runtimeState.Combat.LastFleeTarget
	if prev != nil {
		dx := target.X - prev.X
		dy := target.Y - prev.Y
		dz := target.Z - prev.Z
		dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
		ticksElapsed := worldTick - s.runtimeState.Combat.LastFleeTick
		if dist < 2.0 && ticksElapsed <= 10 && ticksElapsed >= 0 {
			return true
		}
	}
	s.runtimeState.Combat.LastFleeTarget = cloneVec3(target)
	s.runtimeState.Combat.LastFleeTick = worldTick
	return false
}

// LastCombatSummary returns the latest recorded combat encounter summary string.
func (s *AgentState) LastCombatSummary() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runtimeState.Combat.LastCombatSummary
}

// LastCombatMemoryTick returns the world tick of the last combat memory ingestion.
func (s *AgentState) LastCombatMemoryTick() int64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runtimeState.Combat.LastCombatMemoryTick
}

// RecordCombatSummary stores summary and reports whether episodic memory ingestion should occur based on cooldownTicks.
func (s *AgentState) RecordCombatSummary(summary string, worldTick, cooldownTicks int64) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimeState.Combat.LastCombatSummary = summary
	last := s.runtimeState.Combat.LastCombatMemoryTick
	shouldIngest := last == 0 || worldTick < last || worldTick-last >= cooldownTicks
	if shouldIngest {
		s.runtimeState.Combat.LastCombatMemoryTick = worldTick
	}
	return shouldIngest
}

// LastContemplatedDay returns the last in-game day index on which contemplation triggered.
func (s *AgentState) LastContemplatedDay() int64 {
	if s == nil {
		return -1
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runtimeState.Idle.LastContemplatedDay
}

// SetLastContemplatedDay updates the last in-game day index on which contemplation triggered.
func (s *AgentState) SetLastContemplatedDay(day int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimeState.Idle.LastContemplatedDay = day
}

// LastIdleContemplationTick returns the world tick of the last daytime idle contemplation.
func (s *AgentState) LastIdleContemplationTick() int64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runtimeState.Idle.LastIdleContemplationTick
}

// SetLastIdleContemplationTick updates the world tick of the last daytime idle contemplation.
func (s *AgentState) SetLastIdleContemplationTick(tick int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimeState.Idle.LastIdleContemplationTick = tick
}

// IdleState returns a consistent snapshot of the idle tracker fields.
func (s *AgentState) IdleState() (idleStartTick int64, hasIdleStartTick bool, lastIdleContemplationTick int64, lastActivePos [3]float64, hasLastActivePos bool) {
	if s == nil {
		return 0, false, 0, [3]float64{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	idle := &s.runtimeState.Idle
	return idle.IdleStartTick, idle.HasIdleStartTick, idle.LastIdleContemplationTick, idle.LastActivePos, idle.HasLastActivePos
}

// MarkContemplationActiveAt resets the idle baseline to worldTick and pos when a contemplation directive activates.
func (s *AgentState) MarkContemplationActiveAt(worldTick int64, pos *civquestv1.Vec3) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimeState.Idle.IdleStartTick = worldTick
	s.runtimeState.Idle.HasIdleStartTick = true
	if pos != nil {
		s.runtimeState.Idle.LastActivePos = [3]float64{pos.X, pos.Y, pos.Z}
		s.runtimeState.Idle.HasLastActivePos = true
	}
}

// UpdateIdleTracker updates idle dwell tracking based on productivity, position displacement, or reset conditions.
func (s *AgentState) UpdateIdleTracker(worldTick int64, pos *civquestv1.Vec3, reset bool, emittedProductive bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	idle := &s.runtimeState.Idle

	if reset {
		idle.IdleStartTick = 0
		idle.HasIdleStartTick = false
		idle.HasLastActivePos = false
		return
	}

	if emittedProductive {
		idle.IdleStartTick = worldTick
		idle.HasIdleStartTick = true
		if pos != nil {
			idle.LastActivePos = [3]float64{pos.X, pos.Y, pos.Z}
			idle.HasLastActivePos = true
		}
		return
	}

	if !idle.HasIdleStartTick || idle.IdleStartTick <= 0 || worldTick < idle.IdleStartTick {
		idle.IdleStartTick = worldTick
		idle.HasIdleStartTick = true
		if pos != nil {
			idle.LastActivePos = [3]float64{pos.X, pos.Y, pos.Z}
			idle.HasLastActivePos = true
		}
		return
	}

	if pos != nil {
		if !idle.HasLastActivePos {
			idle.LastActivePos = [3]float64{pos.X, pos.Y, pos.Z}
			idle.HasLastActivePos = true
			return
		}
		dx := pos.X - idle.LastActivePos[0]
		dy := pos.Y - idle.LastActivePos[1]
		dz := pos.Z - idle.LastActivePos[2]
		if dx*dx+dy*dy+dz*dz >= 1.5*1.5 {
			idle.IdleStartTick = worldTick
			idle.LastActivePos = [3]float64{pos.X, pos.Y, pos.Z}
		}
	}
}

// CompareAndSwapLoggedGoal updates LastLoggedGoal if goal differs, returning the previous goal and whether it changed.
func (s *AgentState) CompareAndSwapLoggedGoal(goal string) (string, bool) {
	if s == nil {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.runtimeState.Viewer.LastLoggedGoal
	if goal == old {
		return old, false
	}
	s.runtimeState.Viewer.LastLoggedGoal = goal
	return old, true
}

// LastLoggedGoal returns the most recently logged immediate goal string.
func (s *AgentState) LastLoggedGoal() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runtimeState.Viewer.LastLoggedGoal
}

// CompareAndSwapLoggedRoutine updates LastLoggedRoutine if routine differs, returning the previous routine and whether it changed.
func (s *AgentState) CompareAndSwapLoggedRoutine(routine RoutineState) (RoutineState, bool) {
	if s == nil {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.runtimeState.Viewer.LastLoggedRoutine
	if routine == old {
		return old, false
	}
	s.runtimeState.Viewer.LastLoggedRoutine = routine
	return old, true
}

// LastLoggedRoutine returns the most recently logged routine state.
func (s *AgentState) LastLoggedRoutine() RoutineState {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runtimeState.Viewer.LastLoggedRoutine
}

// SetLatestThought stores the latest System 2 contemplation thought string.
func (s *AgentState) SetLatestThought(thought string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimeState.Viewer.LatestThought = thought
}

// SetLatestDialogue stores the latest speech utterance or conversation snippet.
func (s *AgentState) SetLatestDialogue(dialogue string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimeState.Viewer.LatestDialogue = dialogue
}

// SetLatestChronicle stores the latest chronicler diary page or historical event summary.
func (s *AgentState) SetLatestChronicle(chronicle string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimeState.Viewer.LatestChronicle = chronicle
}

// ViewerReflections returns a consistent snapshot of the latest thought, dialogue, and chronicle strings.
func (s *AgentState) ViewerReflections() (thought, dialogue, chronicle string) {
	if s == nil {
		return "", "", ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	v := &s.runtimeState.Viewer
	return v.LatestThought, v.LatestDialogue, v.LatestChronicle
}

// SafeNodes returns a defensive copy of cached safe map nodes.
func (s *AgentState) SafeNodes() []db.MapNode {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	nodes := s.runtimeState.Caches.SafeNodes
	if nodes == nil {
		return nil
	}
	cp := make([]db.MapNode, len(nodes))
	copy(cp, nodes)
	return cp
}

// SetSafeNodes stores a defensive copy of safe map nodes.
func (s *AgentState) SetSafeNodes(nodes []db.MapNode) {
	if s == nil {
		return
	}
	var cp []db.MapNode
	if nodes != nil {
		cp = make([]db.MapNode, len(nodes))
		copy(cp, nodes)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimeState.Caches.SafeNodes = cp
}

// UpsertSafeNode updates an existing safe node by ID or appends a new safe node.
func (s *AgentState) UpsertSafeNode(node db.MapNode) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	nodes := s.runtimeState.Caches.SafeNodes
	for i := range nodes {
		if nodes[i].ID == node.ID {
			nodes[i] = node
			return
		}
	}
	s.runtimeState.Caches.SafeNodes = append(nodes, node)
}

// TradeValuations returns a defensive copy of cached trade valuations.
func (s *AgentState) TradeValuations() map[string]db.TradeValuation {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	src := s.runtimeState.Caches.TradeValuations
	cp := make(map[string]db.TradeValuation, len(src))
	for k, v := range src {
		cp[k] = v
	}
	return cp
}

// SetTradeValuations stores a defensive copy of trade valuations.
func (s *AgentState) SetTradeValuations(vals map[string]db.TradeValuation) {
	if s == nil {
		return
	}
	cp := make(map[string]db.TradeValuation, len(vals))
	for k, v := range vals {
		cp[k] = v
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimeState.Caches.TradeValuations = cp
}

// UpdateTradeValuations merges updated trade valuations into the cached map.
func (s *AgentState) UpdateTradeValuations(vals []db.TradeValuation) {
	if s == nil || len(vals) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runtimeState.Caches.TradeValuations == nil {
		s.runtimeState.Caches.TradeValuations = make(map[string]db.TradeValuation, len(vals))
	}
	for _, v := range vals {
		s.runtimeState.Caches.TradeValuations[v.ItemID] = v
	}
}

// CachedInventory returns whether inventory has been cached and a defensive copy of cached items.
func (s *AgentState) CachedInventory() (bool, []CachedItem) {
	if s == nil {
		return false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := &s.runtimeState.Caches
	if len(c.LastCachedItems) == 0 {
		return c.HasCachedInventory, nil
	}
	cp := make([]CachedItem, len(c.LastCachedItems))
	copy(cp, c.LastCachedItems)
	return c.HasCachedInventory, cp
}

// SetCachedInventory updates the cached inventory snapshot using a defensive copy of items.
func (s *AgentState) SetCachedInventory(items []CachedItem) {
	if s == nil {
		return
	}
	var cp []CachedItem
	if len(items) > 0 {
		cp = make([]CachedItem, len(items))
		copy(cp, items)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimeState.Caches.HasCachedInventory = true
	s.runtimeState.Caches.LastCachedItems = cp
}

// LatestFrame returns a defensive clone of the most recently received TelemetryFrame.
func (s *AgentState) LatestFrame() *civquestv1.TelemetryFrame {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.runtimeState.Caches.LatestFrame == nil {
		return nil
	}
	return proto.Clone(s.runtimeState.Caches.LatestFrame).(*civquestv1.TelemetryFrame)
}

// SetLatestFrame stores a defensive clone of the most recently received TelemetryFrame.
func (s *AgentState) SetLatestFrame(frame *civquestv1.TelemetryFrame) {
	if s == nil {
		return
	}
	var cloned *civquestv1.TelemetryFrame
	if frame != nil {
		cloned = proto.Clone(frame).(*civquestv1.TelemetryFrame)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimeState.Caches.LatestFrame = cloned
}

// LastProcessedSeqID returns the latest sequence ID acknowledged by the game server.
func (s *AgentState) LastProcessedSeqID() int64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runtimeState.Caches.LastProcessedSeqID
}

// SetLastProcessedSeqID records the latest sequence ID acknowledged by the game server.
func (s *AgentState) SetLastProcessedSeqID(seqID int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimeState.Caches.LastProcessedSeqID = seqID
}

// LatestWorldTick returns the WorldTick from the latest cached TelemetryFrame.
func (s *AgentState) LatestWorldTick() int64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.runtimeState.Caches.LatestFrame != nil {
		return s.runtimeState.Caches.LatestFrame.WorldTick
	}
	return 0
}

// LatestHealth returns the agent health from the latest cached TelemetryFrame.
func (s *AgentState) LatestHealth() float32 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	f := s.runtimeState.Caches.LatestFrame
	if f != nil && f.Status != nil {
		return f.Status.Health
	}
	return 0
}

// LatestFoodLevel returns the agent food level from the latest cached TelemetryFrame.
func (s *AgentState) LatestFoodLevel() int32 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	f := s.runtimeState.Caches.LatestFrame
	if f != nil && f.Status != nil {
		return f.Status.FoodLevel
	}
	return 0
}

// LatestPosition returns a defensive copy of the agent position from the latest cached TelemetryFrame.
func (s *AgentState) LatestPosition() *civquestv1.Vec3 {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	f := s.runtimeState.Caches.LatestFrame
	if f != nil && f.Status != nil && f.Status.Position != nil {
		return cloneVec3(f.Status.Position)
	}
	return nil
}

// LatestNavigationState returns the navigation state enum from the latest cached TelemetryFrame.
func (s *AgentState) LatestNavigationState() civquestv1.NavigationState {
	if s == nil {
		return civquestv1.NavigationState_NAVIGATION_STATE_UNSPECIFIED
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	f := s.runtimeState.Caches.LatestFrame
	if f != nil && f.Status != nil {
		return f.Status.NavigationState
	}
	return civquestv1.NavigationState_NAVIGATION_STATE_UNSPECIFIED
}

// ConstructionProgress returns the current blueprint quadrant block index for this agent.
func (s *AgentState) ConstructionProgress() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.constructionProgress
}

// SetConstructionProgress sets the blueprint quadrant block index for this agent.
func (s *AgentState) SetConstructionProgress(idx int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.constructionProgress = idx
}

// AdvanceConstructionProgress increments and returns the new blueprint quadrant block index.
func (s *AgentState) AdvanceConstructionProgress() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.constructionProgress++
	return s.constructionProgress
}
