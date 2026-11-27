package agent

import (
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1/vertical"
)

// surveyFromFeet moves a horizon survey's origin from the surveyor's eyes to their feet. The
// origin becomes a place to walk to and to settle, and eye height is somewhere nobody can
// stand (play test 7).
func surveyFromFeet(frame *civquestv1.TelemetryFrame) {
	if s := frame.GetHorizonSurvey(); s != nil {
		s.OriginY -= vertical.DefaultEyeHeight
	}
}
