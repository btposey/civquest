package agent

import (
	"sync"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// frameSeam is the Go side of the ADR-036 §5 frame-ID seam. Every outgoing IntentBatch is
// stamped with the activity frame that emitted its intents (IntentBatch.frame_id). The mod
// echoes frame_id on every receipt, and System 1 routes receipts by it (a receipt whose frame
// is not on the stack is stale and dropped). A batch belongs to exactly one frame.
type frameSeam struct {
	mu       sync.Mutex
	lastEmit uint64
}

// noteEmit records the frame whose intents were most recently queued.
func (s *frameSeam) noteEmit(frame uint64) {
	if frame == 0 {
		return
	}
	s.mu.Lock()
	s.lastEmit = frame
	s.mu.Unlock()
}

// stamp sets batch.FrameId to the most recent emitting frame.
func (s *frameSeam) stamp(batch *civquestv1.IntentBatch) {
	if batch == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	batch.FrameId = s.lastEmit
}
