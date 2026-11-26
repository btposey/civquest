package system1

// Event-log replay harness for the System 1 HTPDA (bead cq-2sr.2).
//
// A scenario is a script of events (TelemetryFrame, IntentReceipt, ReflexNotice,
// System 2 directive) fed one step at a time through AgentFSM.Tick. After each tick
// the harness records an implementation-agnostic trace line:
//
//	t=<tick> stage=<S> routine=<R> stack=[<top activity kind>/<depth>] intents=[Kind(key=val ...); ...]
//
// The stack observation only uses public accessors (ActiveActivityKind(),
// ActivityDepth() over the single htpda activity stack), isolated in observeStack.
//
// Receipts: a scripted receipt names only a batch ID. The harness plays the mod's side of
// the frame-ID seam (ADR-036 §5): the first time a batch ID appears it is bound to the
// frame that emitted the most recent intents, and every later receipt for that batch
// carries that frame ID (a batch belongs to exactly one frame forever). Traces are compared against
// golden files in testdata/replay/. Goldens are CHARACTERIZATION records of current
// behaviour; regenerate with:
//
//	go test ./internal/system1 -run Replay -update-replay
//
// Review the diff before accepting a regenerated golden: a change is a behaviour change.

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

var updateReplay = flag.Bool("update-replay", false, "rewrite replay golden files")

// ---- script model ----

// replayDirective is a System 2 strategic directive delivered before a step's tick.
type replayDirective struct {
	Action, Target, Goal  string
	IssuedTick, ExpiresAt int64
}

// replayStep is one event batch: optional directive / receipt / reflex notices plus the telemetry frame.
type replayStep struct {
	Frame     *civquestv1.TelemetryFrame
	Receipt   *civquestv1.IntentReceipt
	Reflexes  []*civquestv1.ReflexNotice
	Directive *replayDirective
	StartTask *TaskSubroutine      // a committed task started from outside any frame (applied before the tick)
	Setup     func(st *AgentState) // world-knowledge seeding (e.g. a committed task); applied before the tick
	// Unframed leaves the receipt's frame_id 0: it answers an intent no frame owns (the
	// pre-kernel reflex skills such as biological needs), which Recover handles as an orphan.
	Unframed   bool
	Probe      func(st *AgentState) string
	ProbeTask  func(tk *TaskSubroutine) string // probes the topmost task frame's task (nil when none)
	Annotation string
}

type replayScenario struct {
	Name       string
	Stage      State
	Profession string
	Clan       int
	Steps      []replayStep
	// Check holds scenario-specific semantic assertions over the recorded trace.
	Check func(t *testing.T, tr []replayRecord)
	// Sink, when set, receives the FSM's playtest events (cq-gpa.5).
	Sink EventSink
	// Registry overrides the default engine registry (e.g. a hermetic blueprint library).
	Registry *EngineRegistry
}

type replayRecord struct {
	Tick    int64
	Stage   Stage
	Routine Routine
	Top     string
	Depth   int
	Intents []string
	Probe   string
	Note    string
}

func (r replayRecord) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "t=%d stage=%s routine=%s stack=[%s/%d] intents=[%s]", r.Tick, r.Stage, r.Routine, r.Top, r.Depth, strings.Join(r.Intents, "; "))
	if r.Probe != "" {
		fmt.Fprintf(&sb, " probe={%s}", r.Probe)
	}
	if r.Note != "" {
		fmt.Fprintf(&sb, "  // %s", r.Note)
	}
	return sb.String()
}

// observeStack is the ONLY place that reads the activity stack.
func observeStack(fsm *AgentFSM) (string, int) {
	top := "-"
	if k := fsm.ActiveActivityKind(); k != "" {
		top = k
	}
	return top, fsm.ActivityDepth()
}

func summarizeIntent(in *civquestv1.AgentIntent) string {
	if in == nil {
		return "nil"
	}
	switch {
	case in.GetNavigate() != nil:
		n := in.GetNavigate()
		tg := n.GetTarget()
		return fmt.Sprintf("Navigate(x=%.1f z=%.1f)", tg.GetX(), tg.GetZ())
	case in.GetAttack() != nil:
		a := in.GetAttack()
		return fmt.Sprintf("Attack(target=%d shield=%t kite=%t flee=%t)", a.TargetEntityId, a.RaiseShield, a.KeepDistanceKite, a.FleeImmediately)
	case in.GetInteractBlock() != nil:
		b := in.GetInteractBlock()
		return fmt.Sprintf("Interact(%s %d,%d,%d %s)", b.InteractionType, b.Pos.GetX(), b.Pos.GetY(), b.Pos.GetZ(), b.BlockOrItemId)
	case in.GetUseItem() != nil:
		u := in.GetUseItem()
		return fmt.Sprintf("UseItem(slot=%d eat=%t equip=%t)", u.InventorySlot, u.IsEatingOrDrinking, u.EquipArmorOrOffhand)
	case in.GetCraftItem() != nil:
		return fmt.Sprintf("Craft(%s x%d)", in.GetCraftItem().RecipeId, in.GetCraftItem().BatchCount)
	case in.GetBuildBlock() != nil:
		b := in.GetBuildBlock()
		return fmt.Sprintf("Build(%d,%d,%d %s)", b.TargetVoxel.GetX(), b.TargetVoxel.GetY(), b.TargetVoxel.GetZ(), b.BlockStateString)
	case in.GetSurveyHorizon() != nil:
		return fmt.Sprintf("Survey(sectors=%d)", in.GetSurveyHorizon().NumSectors)
	case in.GetChat() != nil:
		return "Chat"
	case in.GetPlaceSignpost() != nil:
		return "Signpost"
	case in.GetTransferContainer() != nil:
		return "Transfer"
	default:
		return fmt.Sprintf("Other(%T)", in.GetAction())
	}
}

// runReplay executes the scenario and returns the recorded trace.
func runReplay(t *testing.T, sc replayScenario) []replayRecord {
	t.Helper()
	fsm := NewAgentFSMWithRegistry(sc.Stage, nil, sc.Registry)
	fsm.SetEventSink(sc.Sink)
	st := NewAgentState()
	if sc.Clan > 0 {
		st.SetClanSize(sc.Clan)
	}
	cfg := xdg.DefaultAgentIdentityConfig("replay")
	if sc.Profession != "" {
		cfg.Profession = sc.Profession
	}
	var out []replayRecord
	batchFrame := map[int64]uint64{}
	var lastEmitFrame uint64
	for i, step := range sc.Steps {
		f := step.Frame
		if f == nil {
			t.Fatalf("%s step %d: nil frame", sc.Name, i)
		}
		if step.Receipt != nil {
			rc := step.Receipt
			if rc.FrameId == 0 && !step.Unframed {
				fid, ok := batchFrame[rc.BatchId]
				if !ok {
					fid = lastEmitFrame
					batchFrame[rc.BatchId] = fid
				}
				rc.FrameId = fid
			}
			f.Receipt = rc
		}
		if len(step.Reflexes) > 0 {
			f.ReflexNotices = step.Reflexes
		}
		if step.Setup != nil {
			step.Setup(st)
		}
		if step.StartTask != nil {
			fsm.StartTask(st, step.StartTask)
		}
		// Mirrors AgentRuntime.processHorizonSurveyAndRendezvous: completed surveys are ingested
		// into AgentState by the runtime before the FSM ticks (Tick itself ingests receipts).
		if sv := f.GetHorizonSurvey(); sv != nil {
			st.IngestHorizonSurveyReport(sv, f.WorldTick)
		}
		if d := step.Directive; d != nil {
			st.SetStrategicDirective(d.Action, d.Target, d.Goal, d.IssuedTick, d.ExpiresAt)
		}
		res := fsm.Tick(&FSMContext{AgentID: "replay", State: st, Config: cfg, Frame: f})
		top, depth := observeStack(fsm)
		rec := replayRecord{Tick: f.WorldTick, Stage: res.Stage, Routine: res.Routine, Top: top, Depth: depth, Note: step.Annotation}
		for _, fi := range res.Intents {
			rec.Intents = append(rec.Intents, summarizeIntent(fi.Intent))
			if fi.Frame != 0 {
				lastEmitFrame = fi.Frame
			}
		}
		if step.ProbeTask != nil {
			rec.Probe = step.ProbeTask(fsmTask(fsm))
		}
		if step.Probe != nil {
			rec.Probe = step.Probe(st)
		}
		out = append(out, rec)
	}
	return out
}

func renderTrace(tr []replayRecord) string {
	var sb strings.Builder
	for _, r := range tr {
		sb.WriteString(r.String())
		sb.WriteByte('\n')
	}
	return sb.String()
}

func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "replay", name+".golden")
	if *updateReplay {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden %s (run with -update-replay): %v", path, err)
	}
	if string(want) != got {
		t.Fatalf("replay trace diverged from golden %s\n--- want\n%s--- got\n%s", path, want, got)
	}
}

// ---- frame builders ----

type fo func(f *civquestv1.TelemetryFrame)

func rf(tick, tod int64, opts ...fo) *civquestv1.TelemetryFrame {
	f := &civquestv1.TelemetryFrame{
		WorldTick: tick,
		TimeOfDay: tod,
		Status: &civquestv1.AgentStatus{
			AgentId:         "replay",
			Health:          20,
			MaxHealth:       20,
			FoodLevel:       20,
			Position:        &civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5},
			NavigationState: civquestv1.NavigationState_NAVIGATION_STATE_IDLE,
		},
	}
	for _, o := range opts {
		o(f)
	}
	return f
}

func withPos(x, y, z float64) fo {
	return func(f *civquestv1.TelemetryFrame) { f.Status.Position = &civquestv1.Vec3{X: x, Y: y, Z: z} }
}
func withNav(n civquestv1.NavigationState) fo {
	return func(f *civquestv1.TelemetryFrame) { f.Status.NavigationState = n }
}
func withHP(h float32) fo { return func(f *civquestv1.TelemetryFrame) { f.Status.Health = h } }
func withFood(v int32) fo { return func(f *civquestv1.TelemetryFrame) { f.Status.FoodLevel = v } }
func withInv(items ...*civquestv1.ItemStackProto) fo {
	return func(f *civquestv1.TelemetryFrame) { f.Status.Inventory = items }
}
func withBlocks(b ...*civquestv1.VisibleBlock) fo {
	return func(f *civquestv1.TelemetryFrame) { f.InterestingBlocks = b }
}
func withEntities(e ...*civquestv1.VisibleEntity) fo {
	return func(f *civquestv1.TelemetryFrame) { f.VisibleEntities = e }
}
func withSurvey(r *civquestv1.HorizonSurveyReport) fo {
	return func(f *civquestv1.TelemetryFrame) { f.HorizonSurvey = r }
}

func item(slot int32, id string, n int32) *civquestv1.ItemStackProto {
	return &civquestv1.ItemStackProto{Slot: slot, ItemId: id, Count: n}
}
func blk(x, y, z int32, id string) *civquestv1.VisibleBlock {
	return &civquestv1.VisibleBlock{Pos: &civquestv1.BlockPosProto{X: x, Y: y, Z: z}, BlockId: id}
}
func receipt(batch int64, st civquestv1.IntentReceipt_ReceiptStatus, reason string) *civquestv1.IntentReceipt {
	return &civquestv1.IntentReceipt{BatchId: batch, Status: st, FailureReason: reason}
}

// Motor-fact receipts (ADR-037): Java reports stuck as MotorFacts on a FAILED receipt.
func stuckReceipt(batch int64, facts *civquestv1.MotorFacts) *civquestv1.IntentReceipt {
	facts.Stuck = true
	return &civquestv1.IntentReceipt{BatchId: batch, Status: rcFailed, FailureReason: "Navigation stuck", Facts: facts}
}
func terrainStuck(batch int64) *civquestv1.IntentReceipt {
	return stuckReceipt(batch, &civquestv1.MotorFacts{StuckTicks: 16, BlockedBearingDeg: 0, Progress: 8})
}
func peerStuck(batch int64) *civquestv1.IntentReceipt {
	return stuckReceipt(batch, &civquestv1.MotorFacts{StuckTicks: 16, BlockedByEntity: true, Progress: 8})
}
func enclosedStuck(batch int64) *civquestv1.IntentReceipt {
	return stuckReceipt(batch, &civquestv1.MotorFacts{StuckTicks: 16, Enclosed: true, Progress: 8})
}

func creeperAt(id int32, x, z float64, dist float32) *civquestv1.VisibleEntity {
	return &civquestv1.VisibleEntity{EntityId: id, EntityType: "minecraft:creeper", IsHostile: true, Distance: dist, Position: &civquestv1.Vec3{X: x, Y: 64, Z: z}}
}

func survivalKit() []*civquestv1.ItemStackProto {
	return []*civquestv1.ItemStackProto{item(0, "minecraft:wooden_axe", 1), item(1, "minecraft:wooden_sword", 1), item(2, "minecraft:torch", 8)}
}

func uniformSurvey(ox, oz float64, tick int64) *civquestv1.HorizonSurveyReport {
	return makeUniformSurveyReport(ox, 64, oz, tick, "OPEN_PLAINS", "minecraft:air", 64.0, false, true)
}

func probeUnreachable(x, y, z float64, tick func() int64) func(*AgentState) string {
	return func(st *AgentState) string {
		return fmt.Sprintf("unreachable(%.1f,%.0f,%.1f)=%t", x, y, z, st.IsTargetUnreachable(x, y, z, tick()))
	}
}

func hasIntentPrefix(r replayRecord, prefix string) bool {
	for _, s := range r.Intents {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

func TestReplay_Scenarios(t *testing.T) {
	scs := replayScenarios()
	if len(scs) < 10 {
		t.Fatalf("acceptance requires >=10 golden scenarios, have %d", len(scs))
	}
	names := make([]string, 0, len(scs))
	seen := map[string]bool{}
	for _, sc := range scs {
		if seen[sc.Name] {
			t.Fatalf("duplicate scenario %s", sc.Name)
		}
		seen[sc.Name] = true
		names = append(names, sc.Name)
	}
	sort.Strings(names)
	for _, sc := range scs {
		sc := sc
		t.Run(sc.Name, func(t *testing.T) {
			tr := runReplay(t, sc)
			checkGolden(t, sc.Name, renderTrace(tr))
			if sc.Check != nil {
				sc.Check(t, tr)
			}
		})
	}
}
