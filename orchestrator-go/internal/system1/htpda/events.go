package htpda

import civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"

// Event is anything delivered to a frame. Dispatch is the only entry point.
type Event interface{ isEvent() }

// ReceiptStatus mirrors the bridge receipt status.
type ReceiptStatus = civquestv1.IntentReceipt_ReceiptStatus

// ReflexKind mirrors the bridge reflex type.
type ReflexKind = civquestv1.ReflexNotice_ReflexType

// MotorFacts is what the motor side reports with a receipt (ADR-036 §5).
// Defined here until the proto message lands (cq-2sr.11).
type MotorFacts struct {
	Stuck             bool
	Enclosed          bool
	BlockedBearingDeg float32
	StuckTicks        int32
	Progress          float64
	// BlockedByEntity: the obstruction is an alive entity (ADR-033 congestion), not terrain.
	BlockedByEntity bool
}

// External events (accepted by Dispatch).
type (
	// Tick is the 5 Hz pipeline step: expiry check, arbitration, then the top frame.
	Tick struct{}
	// Receipt is routed by Frame ID; only the top frame may receive it.
	Receipt struct {
		Frame  FrameID
		Status ReceiptStatus
		Facts  MotorFacts
	}
	// Reflex is routed by Frame ID like Receipt.
	Reflex struct {
		Frame  FrameID
		Kind   ReflexKind
		Popped bool
	}
	// Directive is the System 2 bridge event (ADR-031); delivered to the top frame.
	Directive struct {
		Action, Target, ImmediateGoal string
		Expires                       int64
	}
	// Cancel unwinds the whole stack from the top (death, shutdown).
	Cancel struct{ Reason string }
)

// Kernel-generated events (rejected by Dispatch when sent from outside).
type (
	// Started is the first Step after Init.
	Started struct{}
	// ChildDone carries a popped child's Result to its parent.
	ChildDone struct {
		Child  Kind
		ID     FrameID
		Result any
	}
	// Resumed is delivered when an arbiter-pushed preemptor pops off this frame.
	Resumed struct {
		From   Kind
		ID     FrameID
		Result any
	}
	// Expired is delivered to the top frame when its lease deadline has passed.
	// The frame is popped (or replaced) regardless of the Op it returns.
	Expired struct{ Deadline int64 }
)

func (Tick) isEvent()      {}
func (Receipt) isEvent()   {}
func (Reflex) isEvent()    {}
func (Directive) isEvent() {}
func (Cancel) isEvent()    {}
func (Started) isEvent()   {}
func (ChildDone) isEvent() {}
func (Resumed) isEvent()   {}
func (Expired) isEvent()   {}

// Timeout is the Result a parent receives when a child's lease expired and
// the child did not supply its own Result.
type Timeout struct {
	Kind     Kind
	Deadline int64
}

// Rejected is the Result a frame receives (as ChildDone with ID 0) when its
// OpPush/OpReplace could not be honoured (unknown kind, depth limit).
type Rejected struct {
	Kind   Kind
	Reason string
}

// IsProgress reports whether a receipt status counts as lease progress.
func IsProgress(s ReceiptStatus) bool {
	return s == civquestv1.IntentReceipt_RECEIPT_STATUS_EXECUTING ||
		s == civquestv1.IntentReceipt_RECEIPT_STATUS_COMPLETED
}
