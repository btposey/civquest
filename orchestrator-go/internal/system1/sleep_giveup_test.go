package system1

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// cq-6l3.4: agents sent about 250 sleep intents in one night to a bed that never worked.
func TestSleep_GivesUpABedThatNeverWorks(t *testing.T) {
	st, _ := bedOwnerAgent(t)
	tries := 0
	for tick := int64(1000); tick < 1000+DefaultUnreachableTargetTTL; tick += 4 {
		f := releaseFrame(tick, 3.5, 4)
		f.TimeOfDay = 14000
		if in := EvaluateSleep("able", f, st); in.GetInteractBlock().GetInteractionType() == civquestv1.InteractBlockIntent_SLEEP_IN_BED {
			tries++
		}
	}
	if tries != maxSleepAttempts {
		t.Fatalf("sent %d sleep intents, want %d", tries, maxSleepAttempts)
	}
	if b := st.GetAssignedBed(); b != nil {
		t.Fatalf("the failing bed must be given up, still assigned %v", b)
	}
}

// Nights apart do not add up.
func TestSleep_AttemptsStartOverEachNight(t *testing.T) {
	st := NewAgentState()
	for night := int64(0); night < 20; night++ {
		if n := st.countSleepAttempt(1000 + night*24000); n != 1 {
			t.Fatalf("night %d counted %d", night, n)
		}
	}
}
