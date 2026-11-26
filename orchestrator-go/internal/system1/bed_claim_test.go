package system1

import (
	"sync"
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// fakeBedRegistry is an in-memory BedRegistry shared by several AgentStates in one test.
type fakeBedRegistry struct {
	mu     sync.Mutex
	owners map[[3]int32]string
}

func newFakeBedRegistry() *fakeBedRegistry { return &fakeBedRegistry{owners: map[[3]int32]string{}} }

func (r *fakeBedRegistry) ClaimBed(x, y, z int32, agent string, _ int64) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := [3]int32{x, y, z}
	if o, ok := r.owners[k]; ok {
		return o, nil
	}
	r.owners[k] = agent
	return agent, nil
}
func (r *fakeBedRegistry) BedOwner(x, y, z int32) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.owners[[3]int32{x, y, z}], nil
}
func (r *fakeBedRegistry) ReleaseBed(x, y, z int32, agent string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := [3]int32{x, y, z}
	if r.owners[k] == agent {
		delete(r.owners, k)
	}
	return nil
}

func nightFrameWithPioneerBed(id string) *civquestv1.TelemetryFrame {
	inv := []*civquestv1.ItemStackProto{
		{Slot: 0, ItemId: "minecraft:oak_planks", Count: 16},
		{Slot: 1, ItemId: "minecraft:torch", Count: 4},
	}
	f := makeArbiterTestFrame(100, 14000, 20, inv, []*civquestv1.VisibleBlock{
		{Pos: &civquestv1.BlockPosProto{X: 4, Y: 64, Z: 0}, BlockId: "minecraft:red_bed"},
	})
	f.Status.AgentId = id
	return f
}

func tickNight(t *testing.T, id string, reg BedRegistry) (*AgentFSM, *AgentState) {
	t.Helper()
	fsm := NewAgentFSMWithRegistry(StageBootstrap, nil, NewDefaultEngineRegistry())
	st := NewAgentState()
	if reg != nil {
		st.SetBedRegistry(reg)
	}
	cfg := xdg.DefaultAgentIdentityConfig(id)
	fsm.Tick(&FSMContext{AgentID: id, State: st, Config: cfg, Frame: nightFrameWithPioneerBed(id)})
	return fsm, st
}

// cq-390.1 root cause: a visible bed satisfied shelter.has_bed for EVERY agent, so with one
// pioneer bed in sight the Shelter activity never bid (the playtest-2 regression).
func TestBedClaim_ForeignClaimedBedDoesNotSuppressShelter(t *testing.T) {
	reg := newFakeBedRegistry()
	if o, _ := reg.ClaimBed(4, 64, 0, "able", 1); o != "able" {
		t.Fatal("setup claim failed")
	}
	fsm, st := tickNight(t, "bert", reg)
	if fsm.ActiveActivityKind() != activityKindShelter {
		t.Fatalf("bert (bed owned by able) should build shelter, active=%q", fsm.ActiveActivityKind())
	}
	if st.GetAssignedBed() != nil {
		t.Fatalf("bert must not be assigned able's bed: %+v", st.GetAssignedBed())
	}
}

// The first agent to see the unclaimed bed owns it and uses it instead of building.
func TestBedClaim_FirstAgentOwnsBedAndSkipsShelter(t *testing.T) {
	reg := newFakeBedRegistry()
	fsm, st := tickNight(t, "able", reg)
	if fsm.ActiveActivityKind() == activityKindShelter {
		t.Fatal("able has a bed; shelter must not bid")
	}
	if b := st.GetAssignedBed(); b == nil || b.X != 4 {
		t.Fatalf("able should be assigned the pioneer bed, got %+v", b)
	}
	if o, _ := reg.BedOwner(4, 64, 0); o != "able" {
		t.Fatalf("registry owner = %q", o)
	}
}

// Control documenting the pre-fix behaviour: with no clan registry a visible bed still
// satisfies the gate and suppresses shelter (single-agent legacy semantics).
func TestBedClaim_LegacyNoRegistryBedSuppressesShelter(t *testing.T) {
	fsm, _ := tickNight(t, "solo", nil)
	if fsm.ActiveActivityKind() == activityKindShelter {
		t.Fatal("legacy single-agent: a visible bed must keep suppressing shelter")
	}
}

// cq-390.2: contention yields. An agent holding a stale assignment to a bed another agent owns
// drops it and does not retry; the bed activity (not SLEEP_IN_BED) takes over.
func TestBedClaim_ContendedAssignmentYields(t *testing.T) {
	reg := newFakeBedRegistry()
	_, _ = reg.ClaimBed(4, 64, 0, "able", 1)
	st := NewAgentState()
	st.SetBedRegistry(reg)
	st.SetAssignedBed(&civquestv1.BlockPosProto{X: 4, Y: 64, Z: 0})
	if got := st.ensureOwnBed("bert", nightFrameWithPioneerBed("bert")); got != nil || st.GetAssignedBed() != nil {
		t.Fatalf("bert must drop able's bed, got=%v assigned=%v", got, st.GetAssignedBed())
	}
	if in := EvaluateSleep("bert", nightFrameWithPioneerBed("bert"), st); in != nil {
		t.Fatalf("bert must not target able's bed, intent=%v", in)
	}
}

// A Java bed is two blocks (foot + head = foot+facing) reported as two visible blocks. It is ONE
// bed: the second agent must not "own" the other half.
func TestBedClaim_TwoBlockBedHasOneOwner(t *testing.T) {
	reg := newFakeBedRegistry()
	frame := func(id string, agentX float64) *civquestv1.TelemetryFrame {
		f := nightFrameWithPioneerBed(id)
		f.Status.Position = &civquestv1.Vec3{X: agentX, Y: 64, Z: 0.5}
		f.InterestingBlocks = []*civquestv1.VisibleBlock{
			{Pos: &civquestv1.BlockPosProto{X: 5, Y: 64, Z: 0}, BlockId: "minecraft:white_bed", BlockStateString: "facing=east,occupied=false,part=head"},
			{Pos: &civquestv1.BlockPosProto{X: 4, Y: 64, Z: 0}, BlockId: "minecraft:white_bed", BlockStateString: "facing=east,occupied=false,part=foot"},
		}
		return f
	}
	able, bert := NewAgentState(), NewAgentState()
	able.SetBedRegistry(reg)
	bert.SetBedRegistry(reg)
	// able stands nearer the head half, bert too: whichever half is nearest, the foot is the key.
	if b := able.ensureOwnBed("able", frame("able", 7.5)); b == nil || b.X != 4 {
		t.Fatalf("able should own the bed by its foot block, got %v", b)
	}
	if b := bert.ensureOwnBed("bert", frame("bert", 7.5)); b != nil {
		t.Fatalf("bert must not claim the other half of able's bed, got %v", b)
	}
	if _, available := bert.BedAvailableFor("bert", frame("bert", 7.5)); available {
		t.Fatal("able's bed must not look available to bert")
	}
	for _, x := range []int32{4, 5} {
		if o, _ := reg.BedOwner(x, 64, 0); o != "able" {
			t.Fatalf("half x=%d owner = %q, want able", x, o)
		}
	}
}
