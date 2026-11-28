package system1

// The watchdog reports and does nothing else (cq-3sa.1, ADR-041 section 4).

import (
	"bytes"
	"log"
	"math"
	"os"
	"strings"
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/htpda"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/watchdog"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/watchdog/watchdogtest"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

type firingReport struct {
	tick    int64
	details map[string]any
}

// reports collects the watchdog events of fsm.
func reports(fsm *AgentFSM) *[]firingReport {
	got := &[]firingReport{}
	fsm.SetEventSink(func(tick int64, category, _ string, d map[string]any) {
		if category == EventWatchdog {
			*got = append(*got, firingReport{tick, d})
		}
	})
	return got
}

// watchRig is an agent with work on its stack whose frames the test feeds to the watchdog
// alone: nothing is decided, so the test says what is sent and what the game reports.
type watchRig struct {
	fsm   *AgentFSM
	state *AgentState
	got   *[]firingReport
	tick  int64
	batch int64
	// the next frames
	tod      int64
	pos      civquestv1.Vec3
	inv      []*civquestv1.ItemStackProto
	sleeping bool
	line     bool
	stage    State
}

const rigFrame = uint64(7)

func newWatchRig(t *testing.T) *watchRig {
	t.Helper()
	r := &watchRig{fsm: NewAgentFSMWithRegistry(StageSettlement, nil, NewDefaultEngineRegistry()), state: NewAgentState(),
		tick: 100, tod: 2000, pos: civquestv1.Vec3{X: 0.5, Y: 64, Z: 0.5}, inv: fullSurvivalKitInv()}
	cfg := xdg.DefaultAgentIdentityConfig("watched")
	cfg.Profession = "EXPLORER"
	r.fsm.Tick(&FSMContext{AgentID: "watched", State: r.state, Config: cfg,
		Frame: makeArbiterTestFrame(r.tick, r.tod, 20, r.inv, nil)})
	if r.fsm.kernel.Depth() < 2 {
		t.Fatalf("setup: no work on the stack: %v", r.fsm.ActivityFrames())
	}
	r.got, r.stage = reports(r.fsm), r.fsm.currentStage
	return r
}

func (r *watchRig) world() *fsmWorld {
	f := makeArbiterTestFrame(r.tick, r.tod, 20, r.inv, nil)
	f.Status.Position = &civquestv1.Vec3{X: r.pos.X, Y: r.pos.Y, Z: r.pos.Z}
	f.Status.IsSleeping = r.sleeping
	if r.line {
		f.Status.Fishing = &civquestv1.FishingState{Cast: true, InWater: true}
	}
	return &fsmWorld{fsm: r.fsm, tick: r.tick, ctx: &FSMContext{AgentID: "watched", State: r.state, Frame: f}}
}

// step is one frame, 4 ticks on: the game's report arrives, then intents are sent.
func (r *watchRig) step(rc *civquestv1.IntentReceipt, intents ...*civquestv1.AgentIntent) {
	r.tick += 4
	w := r.world()
	r.fsm.currentStage = r.stage
	r.fsm.observeProgress(w)
	if rc != nil {
		r.fsm.observeBreaking(w, rc)
		if r.fsm.admitReceiptLocked(rc) {
			r.fsm.observeReceipt(w, rc)
		}
	}
	for _, in := range intents {
		r.fsm.sent(w, htpda.FrameID(rigFrame), activityKindExploration, in)
	}
	r.fsm.watchStandstill(w)
}

// run steps for ticks; each says what happens at the n-th frame.
func (r *watchRig) run(ticks int64, each func(n int) (*civquestv1.IntentReceipt, []*civquestv1.AgentIntent)) {
	for n := 0; int64(n)*4 < ticks; n++ {
		var rc *civquestv1.IntentReceipt
		var intents []*civquestv1.AgentIntent
		if each != nil {
			rc, intents = each(n)
		}
		r.step(rc, intents...)
	}
}

func (r *watchRig) answer(status civquestv1.IntentReceipt_ReceiptStatus, progress float64) *civquestv1.IntentReceipt {
	rc := &civquestv1.IntentReceipt{BatchId: r.batch, FrameId: rigFrame, Status: status}
	if progress > 0 {
		rc.Facts = &civquestv1.MotorFacts{Progress: progress}
	}
	return rc
}

func breakIntent(x int32) *civquestv1.AgentIntent {
	return &civquestv1.AgentIntent{Action: &civquestv1.AgentIntent_InteractBlock{InteractBlock: &civquestv1.InteractBlockIntent{
		Pos: &civquestv1.BlockPosProto{X: x, Y: 64, Z: 0}, InteractionType: civquestv1.InteractBlockIntent_HARVEST_BREAK}}}
}

func (r *watchRig) expectStandstill(t *testing.T) {
	t.Helper()
	watchdogtest.Expect(t, r.fsm, watchdog.NoProgress, activityKindExploration, "stuck on purpose")
}

// everyFrames sends intent every k frames and answers it two frames later with status.
func (r *watchRig) everyFrames(k int, intent func(n int) *civquestv1.AgentIntent, status civquestv1.IntentReceipt_ReceiptStatus) func(int) (*civquestv1.IntentReceipt, []*civquestv1.AgentIntent) {
	return func(n int) (*civquestv1.IntentReceipt, []*civquestv1.AgentIntent) {
		switch n % k {
		case 0:
			r.batch++
			return nil, []*civquestv1.AgentIntent{intent(n / k)}
		case 2:
			return r.answer(status, 0), nil
		}
		return nil, nil
	}
}

func TestWatchdog_StandstillIsReportedAndNothingIsDone(t *testing.T) {
	r := newWatchRig(t)
	r.expectStandstill(t)
	before := r.fsm.ActivityFrames()
	var logged bytes.Buffer
	log.SetOutput(&logged)
	defer log.SetOutput(os.Stderr)
	r.fsm.attempting(activityKindExploration, "walk to (9.0, 64.0, 9.0)", r.tick)
	r.run(standstillTicks-8, nil)
	if len(*r.got) != 0 {
		t.Fatalf("reported before its time: %v", *r.got)
	}
	r.run(16, nil)
	if len(*r.got) != 1 {
		t.Fatalf("reports %v, want one", *r.got)
	}
	// It lasts: reported again, and only every watchdogRepeatTicks.
	r.run(3*watchdogRepeatTicks, nil)
	if got := len(*r.got); got != 4 {
		t.Fatalf("reports of one lasting standstill = %d, want 4", got)
	}
	for i := 1; i < len(*r.got); i++ {
		if gap := (*r.got)[i].tick - (*r.got)[i-1].tick; gap < watchdogRepeatTicks || gap > watchdogRepeatTicks+4 {
			t.Errorf("reports %d ticks apart", gap)
		}
	}
	d := (*r.got)[0].details
	if d["condition"] != string(watchdog.NoProgress) || d["activity"] != activityKindExploration || d["attempting"] != "walk to (9.0, 64.0, 9.0)" {
		t.Errorf("details %v", d)
	}
	for _, want := range []string{"[ERROR]", "watched", "no_progress", activityKindExploration, "walk to (9.0, 64.0, 9.0)"} {
		if !strings.Contains(logged.String(), want) {
			t.Errorf("error log lacks %q: %s", want, logged.String())
		}
	}
	// Frozen stays frozen.
	if after := r.fsm.ActivityFrames(); len(after) != len(before) || after[len(after)-1].ID != before[len(before)-1].ID {
		t.Errorf("the stack was changed: %v then %v", before, after)
	}
	if r.state.rescanWhy.Load() != nil || len(r.fsm.workBackoffUntil) != 0 {
		t.Error("the watchdog asked for a fresh look or backed work off")
	}
}

// Sending, a refusal and a failure are not progress (cq-6nz: 3900 breaks that failed).
func TestWatchdog_FailedAndUnansweredActionsAreNotProgress(t *testing.T) {
	for name, status := range map[string]civquestv1.IntentReceipt_ReceiptStatus{
		"failed":     civquestv1.IntentReceipt_RECEIPT_STATUS_FAILED,
		"timed out":  civquestv1.IntentReceipt_RECEIPT_STATUS_TIMEOUT,
		"aborted":    civquestv1.IntentReceipt_RECEIPT_STATUS_ABORTED,
		"unanswered": civquestv1.IntentReceipt_RECEIPT_STATUS_QUEUED,
	} {
		t.Run(name, func(t *testing.T) {
			r := newWatchRig(t)
			r.expectStandstill(t)
			r.run(standstillTicks+40, r.everyFrames(5, func(int) *civquestv1.AgentIntent { return breakIntent(2) }, status))
			if len(*r.got) != 1 {
				t.Errorf("reports %v, want one", *r.got)
			}
		})
	}
}

// A walk or a survey the game completes is no progress: only the agent having moved is.
func TestWatchdog_CompletedWalksWithoutMovementAreNotProgress(t *testing.T) {
	r := newWatchRig(t)
	r.expectStandstill(t)
	r.run(standstillTicks+40, r.everyFrames(5, func(n int) *civquestv1.AgentIntent {
		return &civquestv1.AgentIntent{Action: &civquestv1.AgentIntent_Navigate{Navigate: &civquestv1.NavigateIntent{
			Target: &civquestv1.Vec3{X: float64(n)}}}}
	}, civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED))
	if len(*r.got) != 1 {
		t.Errorf("reports %v, want one", *r.got)
	}
}

// The mod completes a break of a block that is gone: the same thing confirmed again and again,
// with the agent where it was and nothing in its pack, counts once.
func TestWatchdog_OneActionConfirmedForEverCountsOnce(t *testing.T) {
	r := newWatchRig(t)
	r.expectStandstill(t)
	r.run(standstillTicks+80, r.everyFrames(5, func(n int) *civquestv1.AgentIntent { return breakIntent(int32(n % 2)) },
		civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED))
	if len(*r.got) != 1 {
		t.Errorf("reports %v, want one", *r.got)
	}
}

func TestWatchdog_ProgressIsNotAStandstill(t *testing.T) {
	for name, tc := range map[string]func(r *watchRig){
		// Different actions the game completes.
		"completed actions": func(r *watchRig) {
			r.run(5*standstillTicks, r.everyFrames(100, func(n int) *civquestv1.AgentIntent { return breakIntent(int32(n)) },
				civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED))
		},
		"movement": func(r *watchRig) {
			for i := 0; i < 10; i++ {
				r.pos.X += standstillMove
				r.run(standstillTicks-100, nil)
			}
		},
		"a changing pack": func(r *watchRig) {
			for i := 0; i < 10; i++ {
				r.inv = append(fullSurvivalKitInv(), &civquestv1.ItemStackProto{ItemId: "minecraft:cobblestone", Count: int32(i + 1)})
				r.run(standstillTicks-100, nil)
			}
		},
		"a tool wearing": func(r *watchRig) {
			for i := 0; i < 10; i++ {
				r.inv = fullSurvivalKitInv()
				r.inv[0].Durability = int32(i + 1)
				r.run(standstillTicks-100, nil)
			}
		},
		// 3750 ticks is stone broken by hand while afloat: far beyond the watch, with no
		// completion until the end.
		"slow mining": func(r *watchRig) {
			r.batch++
			r.step(nil, breakIntent(2))
			r.run(3750, func(n int) (*civquestv1.IntentReceipt, []*civquestv1.AgentIntent) {
				return r.answer(civquestv1.IntentReceipt_RECEIPT_STATUS_EXECUTING, float64(n+1)*4/3750), nil
			})
		},
		"a line cast": func(r *watchRig) { r.line = true; r.run(4*standstillTicks, nil) },
		"asleep":      func(r *watchRig) { r.sleeping = true; r.run(4*standstillTicks, nil) },
		"night":       func(r *watchRig) { r.tod = 14000; r.run(4*standstillTicks, nil) },
		"idle pause": func(r *watchRig) {
			r.state.enterIdle()
			r.run(4*standstillTicks, nil)
		},
		"frames not seen": func(r *watchRig) {
			for i := 0; i < 5; i++ {
				r.tick += standstillTicks
				r.run(40, nil)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := newWatchRig(t)
			tc(r)
			if len(*r.got) != 0 {
				t.Errorf("reported %v", *r.got)
			}
		})
	}
}

// A break under way that gets no further, or that starts again and again from nothing, is not
// progress.
func TestWatchdog_BreakGettingNoFurtherIsAStandstill(t *testing.T) {
	for name, progress := range map[string]func(n int) float64{
		"stalled":          func(int) float64 { return 0.3 },
		"starting afresh":  func(n int) float64 { return float64(n%50) / 100 },
		"nothing reported": func(int) float64 { return 0 },
		// A walk's distance to go is not a break getting on.
		"a distance": func(n int) float64 { return 2 + float64(n) },
	} {
		t.Run(name, func(t *testing.T) {
			r := newWatchRig(t)
			r.expectStandstill(t)
			r.batch++
			r.step(nil, breakIntent(2))
			r.run(standstillTicks+260, func(n int) (*civquestv1.IntentReceipt, []*civquestv1.AgentIntent) {
				return r.answer(civquestv1.IntentReceipt_RECEIPT_STATUS_EXECUTING, progress(n)), nil
			})
			if len(*r.got) != 1 {
				t.Errorf("reports %v, want one", *r.got)
			}
		})
	}
}

// An emergency does not wind back the standstill watch: a threat flickering every few seconds
// kept it from ever firing (play test 10), and what is done in a fight is not the work getting on.
func TestWatchdog_FlickeringEmergencyDoesNotHideAStandstill(t *testing.T) {
	r := newWatchRig(t)
	r.expectStandstill(t)
	settled := r.stage
	r.run(standstillTicks+100, func(n int) (*civquestv1.IntentReceipt, []*civquestv1.AgentIntent) {
		r.stage = settled
		if n%15 < 2 {
			r.stage = StageEmergency
			r.inv = fullSurvivalKitInv()
			r.inv[1].Durability = int32(n)
		}
		return nil, nil
	})
	if len(*r.got) == 0 {
		t.Error("the emergencies hid the standstill")
	}
}

// Whoever asks: an activity that keeps asking for withheld walks is reported, and goes on
// holding its place.
func TestWatchdog_RefusalsAreReportedAndNothingIsDone(t *testing.T) {
	fsm := NewAgentFSMWithRegistry(StageSettlement, nil, NewDefaultEngineRegistry())
	seen := watchdogtest.Expect(t, fsm, watchdog.Refused, activityKindShelter, "refused on purpose")
	watchdogtest.Expect(t, fsm, watchdog.Refused, activityKindTaskSubroutine, "refused on purpose: a task is a fault too")
	got := reports(fsm)
	w := &fsmWorld{fsm: fsm}
	for w.tick = 100; w.tick < 100+refusalPatience; w.tick += 4 {
		fsm.refused(w, activityKindShelter)
	}
	if len(*got) != 0 {
		t.Fatalf("reported before its patience ran out: %v", *got)
	}
	for ; w.tick <= 100+refusalPatience+2*watchdogRepeatTicks; w.tick += 4 {
		fsm.refused(w, activityKindShelter)
	}
	if seen() != 3 {
		t.Errorf("reports of one lasting refusal = %d, want 3", seen())
	}
	if len(fsm.workBackoffUntil) != 0 || w.skipped != nil {
		t.Error("the refused activity was backed off")
	}
	// The agent getting somewhere in between starts the patience again.
	for w.tick = 5000; w.tick < 5000+3*refusalPatience; w.tick += 100 {
		fsm.refused(w, activityKindForaging)
		fsm.progressed(w)
	}
	for w.tick = 9000; w.tick <= 9000+refusalPatience; w.tick += 100 {
		fsm.refused(w, activityKindTaskSubroutine)
	}
}

// Asking for something else is not getting anywhere: only progress ends a run of refusals.
func TestWatchdog_SendingSomethingElseDoesNotEndARefusalRun(t *testing.T) {
	fsm := NewAgentFSMWithRegistry(StageSettlement, nil, NewDefaultEngineRegistry())
	watchdogtest.Expect(t, fsm, watchdog.Refused, activityKindForaging, "refused on purpose")
	w := &fsmWorld{fsm: fsm}
	for w.tick = 100; w.tick <= 100+refusalPatience; w.tick += 100 {
		fsm.refused(w, activityKindForaging)
		fsm.sent(w, 1, activityKindForaging, breakIntent(1))
	}
}

// interruptions runs kind pushed over other work n times, each over after life ticks.
func interruptions(fsm *AgentFSM, kind htpda.Kind, n int, life int64, halted bool) *fsmWorld {
	w := &fsmWorld{fsm: fsm, tick: 100, skipped: map[string]bool{}}
	for i := 0; i < n; i++ {
		w.interrupted(kind, []htpda.Frame{})
		w.tick += life
		w.interruptionOver(kind, halted)
		w.tick += 4
	}
	return w
}

func TestWatchdog_ThrashingIsReportedAndNotBackedOff(t *testing.T) {
	for name, tc := range map[string]struct {
		kind   htpda.Kind
		n      int
		life   int64
		halted bool
		want   bool
	}{
		"fell straight through":         {activityKindBed, 1, 0, false, true},
		"ends at once, again and again": {activityKindWool, thrashLimit, 8, true, true},
		"ends at once, twice":           {activityKindWool, thrashLimit - 1, 8, true, false},
		"lasts long enough":             {activityKindWool, 10, thrashLife + 4, true, false},
		"halted in the tick it began":   {activityKindWool, 1, 0, true, false},
		"needs are left alone":          {activityKindEmergency, 10, 0, false, false},
		"travel is left alone":          {activityKindSurvey, 10, 0, false, false},
	} {
		t.Run(name, func(t *testing.T) {
			fsm := NewAgentFSMWithRegistry(StageSettlement, nil, NewDefaultEngineRegistry())
			if tc.want {
				watchdogtest.Expect(t, fsm, watchdog.Thrash, string(tc.kind), "thrashing on purpose")
			}
			got := reports(fsm)
			w := interruptions(fsm, tc.kind, tc.n, tc.life, tc.halted)
			if (len(*got) == 1) != tc.want {
				t.Errorf("reports %v, want one: %v", *got, tc.want)
			}
			if len(fsm.workBackoffUntil) != 0 || w.skipped[string(tc.kind)] {
				t.Error("thrashing work was backed off")
			}
		})
	}
}

// clearSurvey is open country all round.
func clearSurvey(tick int64) *civquestv1.HorizonSurveyReport {
	r := &civquestv1.HorizonSurveyReport{OriginX: 0.5, OriginY: 64, OriginZ: 0.5, CompletedTick: tick, MaxRange: 64}
	for i := int32(0); i < 36; i++ {
		rad := float64(i*10) * math.Pi / 180
		r.Sectors = append(r.Sectors, &civquestv1.HorizonSector{SectorIndex: i, BearingDeg: float32(i * 10), HitDistance: 64,
			FeatureType: "OPEN_PLAINS", HitBlockId: "minecraft:air", HitX: -math.Sin(rad) * 64, HitY: 64, HitZ: math.Cos(rad) * 64, OpenHorizon: true})
	}
	return r
}

// travelRun drives an explorer that never gets anywhere, with a coal seam in view whenever
// seam says so, and returns the ticks at which gathering was pushed.
func travelRun(fsm *AgentFSM, ticks int64, seam func(fsm *AgentFSM) bool) (pushes []int64) {
	st := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("fred")
	cfg.Profession = "EXPLORER"
	fsm.SetEventSink(func(tick int64, category, summary string, _ map[string]any) {
		if category == EventFrame && summary == "push gather" {
			pushes = append(pushes, tick)
		}
	})
	var survey *civquestv1.HorizonSurveyReport
	for tick := int64(100); tick < 100+ticks; tick += 4 {
		var blocks []*civquestv1.VisibleBlock
		if seam(fsm) {
			blocks = []*civquestv1.VisibleBlock{blk(10, 64, 0, "minecraft:coal_ore")}
		}
		inv := append(fullSurvivalKitInv(), &civquestv1.ItemStackProto{ItemId: "minecraft:stone_pickaxe", Count: 1})
		f := makeArbiterTestFrame(tick, 2000, 20, inv, blocks)
		f.Status.NavigationState = civquestv1.NavigationState_NAVIGATION_STATE_PATHING
		if survey != nil {
			st.IngestHorizonSurveyReport(survey, tick)
			f.HorizonSurvey, survey = survey, nil
		}
		for _, in := range fsm.Tick(&FSMContext{AgentID: "fred", State: st, Config: cfg, Frame: f}).Intents {
			if in.Intent.GetSurveyHorizon() != nil {
				survey = clearSurvey(tick)
			}
		}
	}
	return pushes
}

// Play test 10: a find that is gone as soon as gathering looks ends the frame at once, and
// nothing keeps it from being offered again. It is reported, again and again at intervals, and
// left to go on (cq-3sa.6).
func TestWatchdog_FindThatVanishesIsReportedAndNotSuppressed(t *testing.T) {
	fsm := NewAgentFSMWithRegistry(StageSettlement, nil, NewDefaultEngineRegistry())
	seen := watchdogtest.Expect(t, fsm, watchdog.Thrash, activityKindGather,
		"cq-3sa.6: gathering that ends as ineligible once pushed is offered again at once")
	pushes := travelRun(fsm, 800, func(fsm *AgentFSM) bool { return fsm.ActiveActivityKind() != activityKindGather })
	if len(pushes) == 0 {
		t.Fatal("gathering was never offered: the scenario no longer reaches the case")
	}
	if n := within(pushes, 700, 900); n < thrashLimit {
		t.Errorf("gather pushed %d times in the last 200 ticks: something suppresses it", n)
	}
	if got := seen(); got != 2 {
		t.Errorf("reports in 800 ticks of thrashing = %d, want 2 (one every %d ticks)", got, watchdogRepeatTicks)
	}
}

func within(ticks []int64, from, to int64) (n int) {
	for _, t := range ticks {
		if t >= from && t < to {
			n++
		}
	}
	return n
}

// An objective that holds the stack through the day without moving or doing anything is
// reported and, being frozen, stays.
func TestWatchdog_DaytimeStandstillStaysFrozen(t *testing.T) {
	fsm := NewAgentFSMWithRegistry(StageSettlement, nil, NewDefaultEngineRegistry())
	st := NewAgentState()
	cfg := xdg.DefaultAgentIdentityConfig("stander")
	cfg.Profession = "EXPLORER"
	watchdogtest.Expect(t, fsm, watchdog.NoProgress, activityKindExploration, "stuck on purpose: it says it walks and never moves")
	got := reports(fsm)
	var base htpda.FrameID
	for tick := int64(100); tick < 100+3*standstillTicks; tick += 4 {
		f := makeArbiterTestFrame(tick, 2000, 20, fullSurvivalKitInv(), nil)
		f.Status.NavigationState = civquestv1.NavigationState_NAVIGATION_STATE_PATHING
		fsm.Tick(&FSMContext{AgentID: "stander", State: st, Config: cfg, Frame: f})
		if frames := fsm.ActivityFrames(); len(frames) > 0 {
			if base == 0 {
				base = htpda.FrameID(frames[0].ID)
			} else if htpda.FrameID(frames[0].ID) != base {
				t.Fatalf("t=%d the work under way was replaced: %v", tick, frames)
			}
		}
	}
	if len(*got) < 2 {
		t.Errorf("reports %v, want the standstill reported and reported again", *got)
	}
}
