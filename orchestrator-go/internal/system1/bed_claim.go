package system1

// Bed ownership (cq-390.1 / cq-390.2). A bed is a per-agent resource: the first agent to claim
// a bed block owns it, every other agent treats it as taken. The claim lives in the clan-shared
// registry (a port; CivicRegistryDB in production), and AssignedBed stays the agent's own durable
// pointer to the bed it owns. Without a registry (single agent / unit scope) any visible bed is
// claimable, which is the legacy behaviour.

import (
	"math"
	"strings"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// BedRegistry is the clan-shared bed ownership port.
type BedRegistry interface {
	// ClaimBed claims the bed for agentID unless owned; returns the owner after the call.
	ClaimBed(x, y, z int32, agentID string, worldTick int64) (owner string, err error)
	// BedOwner returns the owner, or "" when unclaimed.
	BedOwner(x, y, z int32) (string, error)
	// ReleaseBed drops agentID's claim.
	ReleaseBed(x, y, z int32, agentID string) error
}

// bedClaimRadius is how far from the agent a visible bed may be claimed as "mine".
const bedClaimRadius = 16.0

// SetBedRegistry installs the clan-shared bed registry.
func (s *AgentState) SetBedRegistry(r BedRegistry) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.bedRegistry = r
	s.mu.Unlock()
}

func (s *AgentState) bedReg() BedRegistry {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.bedRegistry
}

// claimBed tries to make the bed at pos this agent's. True when the agent owns it afterwards.
// A registry error is treated as "not mine" (never double-book on uncertainty).
func (s *AgentState) claimBed(agentID string, pos *civquestv1.BlockPosProto, tick int64) bool {
	reg := s.bedReg()
	if reg == nil {
		return true
	}
	if pos == nil || agentID == "" {
		return false
	}
	// Read first: this runs every tick and an owned bed must not take the registry write lock.
	if owner, err := reg.BedOwner(pos.X, pos.Y, pos.Z); err != nil || owner != "" {
		return err == nil && owner == agentID
	}
	owner, err := reg.ClaimBed(pos.X, pos.Y, pos.Z, agentID, tick)
	return err == nil && owner == agentID
}

// bedTakenByOther reports whether pos is owned by a different agent.
func (s *AgentState) bedTakenByOther(agentID string, pos *civquestv1.BlockPosProto) bool {
	reg := s.bedReg()
	if reg == nil || pos == nil {
		return false
	}
	owner, err := reg.BedOwner(pos.X, pos.Y, pos.Z)
	return err == nil && owner != "" && owner != agentID
}

// ensureOwnBed validates the agent's assigned bed against the registry and, when it has none,
// claims the nearest claimable visible bed. It returns the bed the agent owns (nil: none).
// Contention yields: an assigned bed another agent owns is dropped, not retried.
func (s *AgentState) ensureOwnBed(agentID string, frame *civquestv1.TelemetryFrame) *civquestv1.BlockPosProto {
	if s == nil || frame == nil {
		return nil
	}
	if cur := s.GetAssignedBed(); cur != nil {
		if s.bedTakenByOther(agentID, cur) {
			s.ClearAssignedBed()
		} else if s.claimBed(agentID, cur, frame.WorldTick) {
			return cur
		} else {
			s.ClearAssignedBed()
		}
	}
	if frame.Status == nil || frame.Status.Position == nil {
		return nil
	}
	pos := frame.Status.Position
	var best *civquestv1.BlockPosProto
	bestD := math.MaxFloat64
	for _, b := range frame.InterestingBlocks {
		if b == nil || b.Pos == nil || !strings.Contains(strings.ToLower(b.BlockId), "bed") {
			continue
		}
		if s.IsTargetUnreachable(float64(b.Pos.X)+0.5, float64(b.Pos.Y), float64(b.Pos.Z)+0.5, frame.WorldTick) {
			continue
		}
		d := math.Hypot(pos.X-float64(b.Pos.X), pos.Z-float64(b.Pos.Z))
		if d > bedClaimRadius || d >= bestD || s.bedTakenByOther(agentID, b.Pos) {
			continue
		}
		best, bestD = b.Pos, d
	}
	if best != nil && s.claimBed(agentID, best, frame.WorldTick) {
		s.SetAssignedBed(best)
		return s.GetAssignedBed()
	}
	return nil
}

// BedAvailableFor is the read-only gate behind shelter.has_bed. registryActive=false means no
// clan registry is wired (legacy rules apply); otherwise available says the agent owns its
// assigned bed or can claim a visible unclaimed one.
func (s *AgentState) BedAvailableFor(agentID string, frame *civquestv1.TelemetryFrame) (registryActive, available bool) {
	if s.bedReg() == nil {
		return false, false
	}
	if cur := s.GetAssignedBed(); cur != nil && !s.bedTakenByOther(agentID, cur) {
		return true, true
	}
	if frame == nil || frame.Status == nil || frame.Status.Position == nil {
		return true, false
	}
	pos := frame.Status.Position
	for _, b := range frame.InterestingBlocks {
		if b == nil || b.Pos == nil || !strings.Contains(strings.ToLower(b.BlockId), "bed") {
			continue
		}
		if s.IsTargetUnreachable(float64(b.Pos.X)+0.5, float64(b.Pos.Y), float64(b.Pos.Z)+0.5, frame.WorldTick) ||
			math.Hypot(pos.X-float64(b.Pos.X), pos.Z-float64(b.Pos.Z)) > bedClaimRadius || s.bedTakenByOther(agentID, b.Pos) {
			continue
		}
		return true, true
	}
	return true, false
}

// claimBedNoLock is claimBed for callers already holding s.mu (reads the registry field directly).
func (s *AgentState) claimBedNoLock(agentID string, pos *civquestv1.BlockPosProto, tick int64) bool {
	reg := s.bedRegistry
	if reg == nil {
		return true
	}
	if pos == nil || agentID == "" {
		return false
	}
	owner, err := reg.ClaimBed(pos.X, pos.Y, pos.Z, agentID, tick)
	return err == nil && owner == agentID
}
