package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// releaseFrame is a daytime frame at x with a floor block (so the sensor evidently works) and,
// optionally, beds at the given x positions.
func releaseFrame(tick int64, x float64, beds ...int32) *civquestv1.TelemetryFrame {
	blocks := []*civquestv1.VisibleBlock{{Pos: &civquestv1.BlockPosProto{X: int32(x), Y: 63, Z: 0}, BlockId: "minecraft:grass_block"}}
	for _, bx := range beds {
		blocks = append(blocks, &civquestv1.VisibleBlock{Pos: &civquestv1.BlockPosProto{X: bx, Y: 64, Z: 0}, BlockId: "minecraft:red_bed"})
	}
	f := makeArbiterTestFrame(tick, 1000, 20, nil, blocks)
	f.Status.Position = &civquestv1.Vec3{X: x, Y: 64, Z: 0.5}
	return f
}

func bedOwnerAgent(t *testing.T) (*AgentState, *fakeBedRegistry) {
	t.Helper()
	reg := newFakeBedRegistry()
	st := NewAgentState()
	st.SetBedRegistry(reg)
	if b := st.ensureOwnBed("able", releaseFrame(0, 0.5, 4)); b == nil {
		t.Fatal("setup: able must own the bed")
	}
	return st, reg
}

func TestBedRelease_GoneOnlyAfterSustainedObservation(t *testing.T) {
	st, reg := bedOwnerAgent(t)
	// A flicker: missing for a while, seen again, missing again. The window restarts.
	for tick := int64(20); tick <= 180; tick += 20 {
		st.ensureOwnBed("able", releaseFrame(tick, 0.5))
	}
	st.ensureOwnBed("able", releaseFrame(200, 0.5, 4))
	for tick := int64(220); tick < 220+bedGoneTicks; tick += 20 {
		st.ensureOwnBed("able", releaseFrame(tick, 0.5))
	}
	if o, _ := reg.BedOwner(4, 64, 0); o != "able" {
		t.Fatalf("released before the window elapsed: owner %q", o)
	}
	st.ensureOwnBed("able", releaseFrame(220+bedGoneTicks, 0.5))
	if o, _ := reg.BedOwner(4, 64, 0); o != "" || st.GetAssignedBed() != nil {
		t.Fatalf("broken bed must be released: owner %q assigned %v", o, st.GetAssignedBed())
	}
	if st.mayRecallBed("able", &civquestv1.BlockPosProto{X: 4, Y: 64, Z: 0}) {
		t.Fatal("POI memory must not reclaim a bed observed gone")
	}
	if b := st.ensureOwnBed("able", releaseFrame(600, 0.5, 4)); b == nil || !st.mayRecallBed("able", b) {
		t.Fatalf("a replaced bed is claimable again: %v", b)
	}
}

func TestBedRelease_InconclusiveFramesKeepClaim(t *testing.T) {
	full := releaseFrame(0, 0.5)
	for len(full.InterestingBlocks) < bedFrameBlockCap {
		full.InterestingBlocks = append(full.InterestingBlocks, full.InterestingBlocks[0])
	}
	empty := releaseFrame(0, 0.5)
	empty.InterestingBlocks = nil
	for name, f := range map[string]*civquestv1.TelemetryFrame{"far": releaseFrame(0, 40.5), "truncated": full, "empty": empty} {
		st, reg := bedOwnerAgent(t)
		for tick := int64(20); tick < 1000; tick += 20 {
			f.WorldTick = tick
			st.ensureOwnBed("able", f)
		}
		if o, _ := reg.BedOwner(4, 64, 0); o != "able" {
			t.Errorf("%s: claim lost (owner %q)", name, o)
		}
	}
}

// An unreachable bed is abandoned at once but released only after the hold, so it is not passed
// straight round the clan; meanwhile the owner moves to another bed and ends with one claim.
func TestBedRelease_UnreachableReleasedAfterHold(t *testing.T) {
	st, reg := bedOwnerAgent(t)
	st.MarkTargetUnreachable(4.5, 64, 0.5, 20, DefaultUnreachableTargetTTL)
	b := st.ensureOwnBed("able", releaseFrame(20, 0.5, 4, 9))
	if b == nil || b.X != 9 {
		t.Fatalf("able should move to the reachable bed, got %v", b)
	}
	st.ensureOwnBed("able", releaseFrame(20+bedAbandonHoldTicks-1, 0.5, 4, 9))
	if o, _ := reg.BedOwner(4, 64, 0); o != "able" {
		t.Fatalf("released before the hold elapsed: %q", o)
	}
	st.ensureOwnBed("able", releaseFrame(20+bedAbandonHoldTicks, 0.5, 4, 9))
	if owned, _ := reg.BedOwnedBy("able"); len(owned) != 1 || owned[0] != [3]int32{9, 64, 0} {
		t.Fatalf("exactly the new claim expected: %v", owned)
	}
}

func TestBedRelease_AbandonedAccessTargetIsNotRetaken(t *testing.T) {
	st, reg := bedOwnerAgent(t)
	st.abandonBed("able", 20) // only the access target was blacklisted
	if st.mayRecallBed("able", &civquestv1.BlockPosProto{X: 4, Y: 64, Z: 0}) {
		t.Fatal("POI memory must not retake an abandoned bed")
	}
	for _, tick := range []int64{40, 20 + bedAbandonHoldTicks - 1} {
		if b := st.ensureOwnBed("able", releaseFrame(tick, 0.5, 4)); b != nil {
			t.Fatalf("abandoned bed retaken at %d: %v", tick, b)
		}
	}
	st.releaseAbandonedBeds("able", 20+bedAbandonHoldTicks)
	if o, _ := reg.BedOwner(4, 64, 0); o != "" {
		t.Fatalf("abandoned bed still claimed by %q", o)
	}
}

func TestBedRelease_RestartResumesOwnClaim(t *testing.T) {
	_, reg := bedOwnerAgent(t)
	for name, f := range map[string]*civquestv1.TelemetryFrame{
		"visible, with a nearer free bed": releaseFrame(20, 10.5, 4, 11),
		"out of sight":                    releaseFrame(20, 60.5, 61),
	} {
		st := NewAgentState()
		st.SetBedRegistry(reg)
		if b := st.ensureOwnBed("able", f); b == nil || b.X != 4 {
			t.Errorf("%s: resumed %v, want x=4", name, b)
		}
		if owned, _ := reg.BedOwnedBy("able"); len(owned) != 1 {
			t.Errorf("%s: claims %v", name, owned)
		}
	}
}

// A claim with no bed behind it (a placement that never landed, or broken while the agent was
// down) blocks a second claim only until it has been observed gone.
func TestBedRelease_UnbackedClaimExpires(t *testing.T) {
	reg := newFakeBedRegistry()
	_, _ = reg.ClaimBed(2, 64, 0, "able", 1)
	st := NewAgentState()
	st.SetBedRegistry(reg)
	var got *civquestv1.BlockPosProto
	for tick := int64(20); tick <= 20+bedGoneTicks; tick += 20 {
		if got = st.ensureOwnBed("able", releaseFrame(tick, 0.5, 9)); got != nil && tick < 20+bedGoneTicks {
			t.Fatalf("second claim while the first is unproven: %v at %d", got, tick)
		}
	}
	if got == nil || got.X != 9 {
		t.Fatalf("after expiry able claims the visible bed, got %v", got)
	}
	if o, _ := reg.BedOwner(2, 64, 0); o != "" {
		t.Fatalf("stale claim survives: %q", o)
	}
}
