package system1

import "testing"

type capturedEvent struct {
	Tick     int64
	Category string
	Summary  string
	Details  map[string]any
}

func captureSink(out *[]capturedEvent) EventSink {
	return func(tick int64, cat, sum string, d map[string]any) {
		*out = append(*out, capturedEvent{tick, cat, sum, d})
	}
}

func countCat(evs []capturedEvent, cat string) int {
	n := 0
	for _, e := range evs {
		if e.Category == cat {
			n++
		}
	}
	return n
}

// A stuck receipt must surface MOTOR_STUCK, the Recover frame push/expire must surface as
// RECOVER_DECISION, task frame push as FSM_FRAME, and VITALS must be throttled (~20 ticks).
func TestPlaytestEventsStuckRecoverAndVitals(t *testing.T) {
	var evs []capturedEvent
	sc := replayScenario{
		Name: "events", Stage: StageBootstrap, Profession: "EXPLORER", Sink: captureSink(&evs),
		Steps: []replayStep{
			{Frame: rf(100, 3000, withBlocks(treeColumn...))},
			{Frame: rf(110, 3000, withBlocks(treeColumn...)), Receipt: terrainStuck(2)},
			{Frame: rf(121, 3000, withBlocks(treeColumn...))},
			{Frame: rf(215, 3000, withBlocks(treeColumn...))},
		},
	}
	runReplay(t, sc)

	if countCat(evs, EventMotorStuck) != 1 {
		t.Fatalf("want 1 MOTOR_STUCK, got %d: %+v", countCat(evs, EventMotorStuck), evs)
	}
	var push, expire bool
	for _, e := range evs {
		if e.Category == EventRecover && e.Details["op"] == "push" && e.Details["kind"] == activityKindRecover {
			push = true
		}
		if e.Category == EventRecover && e.Details["op"] == "expire" {
			expire = true
			if e.Details["result"] != "blocked:terrain" {
				t.Fatalf("expire result = %v", e.Details["result"])
			}
		}
	}
	if !push || !expire {
		t.Fatalf("missing recover push/expire: push=%v expire=%v\n%+v", push, expire, evs)
	}
	if countCat(evs, EventFrame) == 0 {
		t.Fatalf("expected FSM_FRAME events for non-recover frames")
	}
	// ticks 100,110,121(>=20 after 100? 100->121 = second emission at 121 since 110-100<20),215.
	var vt []int64
	for _, e := range evs {
		if e.Category == EventVitals {
			vt = append(vt, e.Tick)
			for _, k := range []string{"health", "food", "air", "in_water", "on_fire", "position"} {
				if _, ok := e.Details[k]; !ok {
					t.Fatalf("vitals missing %q: %+v", k, e.Details)
				}
			}
		}
	}
	want := []int64{100, 121, 215}
	if len(vt) != len(want) {
		t.Fatalf("vitals ticks = %v want %v", vt, want)
	}
	for i := range want {
		if vt[i] != want[i] {
			t.Fatalf("vitals ticks = %v want %v", vt, want)
		}
	}
}

func TestPlaytestEventsNilSinkIsSilent(t *testing.T) {
	runReplay(t, replayScenario{Name: "nil", Stage: StageBootstrap, Profession: "EXPLORER",
		Steps: []replayStep{{Frame: rf(100, 3000, withBlocks(treeColumn...))}}})
}

func TestNavReasonNilSafe(t *testing.T) {
	var fsm *AgentFSM
	if fsm.NavReason() != nil {
		t.Fatal("nil FSM must give nil reason")
	}
}
