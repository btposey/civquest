package system2

import (
	"testing"
	"time"
	"unicode/utf8"
)

func TestComputePacedSchedule_ConciseSingleUtterance(t *testing.T) {
	reply := "Morning Steve! The wheat's coming in gold and heavy today."
	sched := ComputePacedSchedule(reply, DefaultChatTokensPerSec, DefaultMaxChatUtteranceRunes)
	if len(sched) != 1 {
		t.Fatalf("expected 1 segment for concise reply, got %d: %+v", len(sched), sched)
	}
	if sched[0].Delay != 0 {
		t.Errorf("expected first segment delay 0, got %v", sched[0].Delay)
	}
	if sched[0].Text != reply {
		t.Errorf("got %q, want %q", sched[0].Text, reply)
	}
}

func TestComputePacedSchedule_HistorianMultiSentencePacing(t *testing.T) {
	story := "Gather round the hearth and hear of the First Winter Siege. " +
		"When the undead horde descended from the northern ridge, Aria held the granary gate alone with a chipped iron shield. " +
		"By dawn our banners still stood unbroken above the frost."

	sched := ComputePacedSchedule(story, 4.5, 90)
	if len(sched) < 3 {
		t.Fatalf("expected story to be split into >=3 paced segments, got %d: %+v", len(sched), sched)
	}
	if sched[0].Delay != 0 {
		t.Errorf("expected initial utterance to emit immediately (Delay=0), got %v", sched[0].Delay)
	}
	for i, seg := range sched {
		if utf8.RuneCountInString(seg.Text) > 90 {
			t.Errorf("segment %d exceeds 90 runes (%d): %q", i, utf8.RuneCountInString(seg.Text), seg.Text)
		}
		if i > 0 {
			if seg.Delay < DefaultMinChatSegmentDelay || seg.Delay > DefaultMaxChatSegmentDelay {
				t.Errorf("segment %d delay %v out of bounds [%v, %v]", i, seg.Delay, DefaultMinChatSegmentDelay, DefaultMaxChatSegmentDelay)
			}
		}
	}

	// Verify faster tok/s produces shorter delays
	fastSched := ComputePacedSchedule(story, 12.0, 90)
	if fastSched[1].Delay >= sched[1].Delay && sched[1].Delay > DefaultMinChatSegmentDelay {
		t.Errorf("expected faster tok/s (12.0) to produce shorter delay than 4.5 tok/s: got %v vs %v", fastSched[1].Delay, sched[1].Delay)
	}
	_ = time.Millisecond
}
