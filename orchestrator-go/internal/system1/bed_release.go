package system1

// Bed claim release and resumption (cq-390.5). A claim lasts as long as the bed does something
// for its owner. It is released when the bed block is observed gone over a sustained window (one
// odd frame never costs an agent its bed). A bed navigation has blacklisted as unreachable is
// abandoned at once (the owner may claim another) and released after it has stayed abandoned for
// bedAbandonHoldTicks, so a bad bed is not passed straight round the clan. Permadeath releases
// every claim in agent.ExecutePermadeath. A restarted agent takes its surviving claim back from
// the registry.

import (
	"math"
	"strings"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

const (
	// bedObserveRadius: within it a bed missing from the frame is evidence. It sits well inside
	// the sensor's block scan radius (16).
	bedObserveRadius = 12.0
	// bedFrameBlockCap is the sensor's interesting-block limit (SensoryFrustumCollector
	// MAX_INTERESTING_BLOCKS): a full frame may have truncated the bed away.
	bedFrameBlockCap = 128
	// bedGoneTicks is how long a bed must stay observed gone before its claim is released. It
	// exceeds bedPlaceVerifyTTL so a placement in flight is judged by its own verifier first.
	bedGoneTicks = 200
	// bedGoneMaxGapTicks: a longer silence between observations restarts the window.
	bedGoneMaxGapTicks = 60
	// bedAbandonHoldTicks: one unreachable-blacklist window.
	bedAbandonHoldTicks = DefaultUnreachableTargetTTL
)

// bedWatch is the per-agent evidence behind releasing a claim. Guarded by AgentState.mu.
type bedWatch struct {
	gonePos   [3]int32
	goneSince int64
	goneLast  int64
	watching  bool
	// lost are released beds that were observed gone: remembered bed POIs there are stale
	// until a bed is seen (and claimed) at the spot again.
	lost map[[3]int32]bool
	// abandoned maps a claim given up as unreachable to the tick it is released.
	abandoned map[[3]int32]int64
}

func bedKey(p *civquestv1.BlockPosProto) [3]int32 { return [3]int32{p.X, p.Y, p.Z} }

func isBedBlockID(id string) bool {
	id = strings.ToLower(id)
	return id == "bed" || strings.HasSuffix(id, ":bed") || strings.HasSuffix(id, "_bed")
}

func bedDistance(frame *civquestv1.TelemetryFrame, p *civquestv1.BlockPosProto) float64 {
	if frame.Status == nil || frame.Status.Position == nil {
		return math.Inf(1)
	}
	at := frame.Status.Position
	dx, dy, dz := float64(p.X)+0.5-at.X, float64(p.Y)+0.5-at.Y, float64(p.Z)+0.5-at.Z
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

// bedObservedGone reports whether this frame is positive evidence that no bed is at p: another
// block is listed there, or the agent is close enough that the sensor would have listed the bed
// (an empty or full block list proves nothing).
func bedObservedGone(frame *civquestv1.TelemetryFrame, p *civquestv1.BlockPosProto) bool {
	if b := FindVisibleBlockAt(frame, p.X, p.Y, p.Z); b != nil {
		return !isBedBlockID(b.BlockId)
	}
	n := len(frame.InterestingBlocks)
	return n > 0 && n < bedFrameBlockCap && bedDistance(frame, p) <= bedObserveRadius
}

// bedConfirmedGone folds this frame into the watch on p and reports whether p has now been
// observed gone continuously for bedGoneTicks.
func (s *AgentState) bedConfirmedGone(frame *civquestv1.TelemetryFrame, p *civquestv1.BlockPosProto) bool {
	gone := bedObservedGone(frame, p)
	tick := frame.WorldTick
	s.mu.Lock()
	defer s.mu.Unlock()
	w := &s.bedWatch
	if !gone {
		w.watching = false
		return false
	}
	if !w.watching || w.gonePos != bedKey(p) || tick < w.goneLast || tick-w.goneLast > bedGoneMaxGapTicks {
		w.watching, w.gonePos, w.goneSince = true, bedKey(p), tick
	}
	w.goneLast = tick
	return tick-w.goneSince >= bedGoneTicks
}

func (s *AgentState) bedUnreachable(p *civquestv1.BlockPosProto, tick int64) bool {
	return s.IsTargetUnreachable(float64(p.X)+0.5, float64(p.Y), float64(p.Z)+0.5, tick)
}

// heldBeds returns the claims the agent still counts as its bed (abandoned ones excluded).
func (s *AgentState) heldBeds(reg BedRegistry, agentID string) ([][3]int32, error) {
	owned, err := reg.BedOwnedBy(agentID)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	held := owned[:0]
	for _, p := range owned {
		if _, gone := s.bedWatch.abandoned[p]; !gone {
			held = append(held, p)
		}
	}
	return held, nil
}

// releaseOwnBeds drops the claims the agent holds and returns the blocks released. A failed
// release leaves the row; resumeOwnBed meets it again and retries through the same evidence.
func (s *AgentState) releaseOwnBeds(agentID string) [][3]int32 {
	reg := s.bedReg()
	if reg == nil {
		return nil
	}
	held, err := s.heldBeds(reg, agentID)
	if err != nil {
		return nil
	}
	var released [][3]int32
	for _, p := range held {
		if reg.ReleaseBed(p[0], p[1], p[2], agentID) == nil {
			released = append(released, p)
		}
	}
	return released
}

// abandonClaim schedules the release of one claim.
func (s *AgentState) abandonClaim(p [3]int32, tick int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bedWatch.abandoned == nil {
		s.bedWatch.abandoned = map[[3]int32]int64{}
	}
	if _, ok := s.bedWatch.abandoned[p]; !ok {
		s.bedWatch.abandoned[p] = tick + bedAbandonHoldTicks
	}
}

// releaseAbandonedBeds releases the abandoned claims whose hold has run out.
func (s *AgentState) releaseAbandonedBeds(agentID string, tick int64) {
	reg := s.bedReg()
	if reg == nil {
		return
	}
	var due [][3]int32
	s.mu.RLock()
	for p, at := range s.bedWatch.abandoned {
		if tick >= at {
			due = append(due, p)
		}
	}
	s.mu.RUnlock()
	for _, p := range due {
		if reg.ReleaseBed(p[0], p[1], p[2], agentID) != nil {
			continue // retried next frame
		}
		s.mu.Lock()
		delete(s.bedWatch.abandoned, p)
		s.mu.Unlock()
	}
}

// abandonBed gives up the assigned bed as unreachable: the agent stops counting its claim,
// which is released after bedAbandonHoldTicks, and the bed block itself is blacklisted so the
// agent does not take it straight back when only its access target had been.
func (s *AgentState) abandonBed(agentID string, tick int64) {
	cur := s.GetAssignedBed()
	s.ClearAssignedBed()
	reg := s.bedReg()
	if cur == nil || reg == nil {
		return
	}
	if !s.bedUnreachable(cur, tick) {
		s.MarkTargetUnreachable(float64(cur.X)+0.5, float64(cur.Y), float64(cur.Z)+0.5, tick, DefaultUnreachableTargetTTL)
	}
	held, err := s.heldBeds(reg, agentID)
	if err != nil {
		return // resumeOwnBed abandons the blacklisted claim once the registry answers
	}
	for _, p := range held {
		s.abandonClaim(p, tick)
	}
}

// loseBed releases a bed observed gone and remembers the spot as stale.
func (s *AgentState) loseBed(agentID string, pos *civquestv1.BlockPosProto) {
	released := append(s.releaseOwnBeds(agentID), bedKey(pos))
	s.ClearAssignedBed()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bedWatch.watching = false
	if s.bedWatch.lost == nil {
		s.bedWatch.lost = map[[3]int32]bool{}
	}
	for _, p := range released {
		s.bedWatch.lost[p] = true
	}
}

// wantBedAgain clears release evidence for a bed the agent has just (re)claimed.
func (s *AgentState) wantBedAgain(halves []*civquestv1.BlockPosProto) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range halves {
		delete(s.bedWatch.lost, bedKey(p))
		delete(s.bedWatch.abandoned, bedKey(p))
	}
}

// mayRecallBed gates claiming a bed from POI memory: never one observed gone, and never a
// second bed while the agent holds a claim.
func (s *AgentState) mayRecallBed(agentID string, p *civquestv1.BlockPosProto) bool {
	reg := s.bedReg()
	if reg == nil || p == nil {
		return true
	}
	s.mu.RLock()
	lost := s.bedWatch.lost[bedKey(p)]
	s.mu.RUnlock()
	if lost {
		return false
	}
	s.mu.RLock()
	_, abandoned := s.bedWatch.abandoned[bedKey(p)]
	s.mu.RUnlock()
	owned, err := s.heldBeds(reg, agentID)
	if err != nil || abandoned {
		return false
	}
	for _, o := range owned {
		if o == bedKey(p) {
			return true
		}
	}
	return len(owned) == 0
}

// resumeOwnBed settles claims held without an assigned bed (an agent restart, or a placement
// still in flight). holds=true means the agent has a claim and must not take another; bed is the
// assignment it resumed, nil while the claim is still unproven. A visible bed is resumed first;
// else a claim too far to check is trusted; else the claimed spot is watched until it either
// shows a bed or has been gone long enough to release.
func (s *AgentState) resumeOwnBed(agentID string, frame *civquestv1.TelemetryFrame) (holds bool, bed *civquestv1.BlockPosProto) {
	reg := s.bedReg()
	if reg == nil {
		return false, nil
	}
	owned, err := s.heldBeds(reg, agentID)
	if err != nil {
		return true, nil // never double-book on uncertainty
	}
	var far, near *civquestv1.BlockPosProto
	for _, o := range owned {
		p := &civquestv1.BlockPosProto{X: o[0], Y: o[1], Z: o[2]}
		if s.bedUnreachable(p, frame.WorldTick) {
			s.abandonClaim(o, frame.WorldTick)
			continue
		}
		if b := FindVisibleBlockAt(frame, p.X, p.Y, p.Z); b != nil && isBedBlockID(b.BlockId) {
			if s.claimWholeBed(agentID, frame, p) {
				return true, s.GetAssignedBed()
			}
			if !s.wholeBedTakenByOther(agentID, frame, p) {
				return true, nil
			}
			s.releaseOwnBeds(agentID) // the other half belongs to someone else
			return false, nil
		}
		switch {
		case bedDistance(frame, p) > bedObserveRadius:
			if far == nil {
				far = p
			}
		case near == nil:
			near = p
		}
	}
	if far != nil {
		s.SetAssignedBed(far)
		return true, far
	}
	if near == nil {
		return false, nil
	}
	if s.bedConfirmedGone(frame, near) {
		s.loseBed(agentID, near)
		return false, nil
	}
	return true, nil
}
