package eval

import (
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// A walk aimed past the edge of filled ground stops at the perimeter wall, on the ground.
func TestFillGroundPerimeterStopsWalkers(t *testing.T) {
	w := NewBlankWorld()
	w.FillGround(-5, -5, 5, 5, "")
	s := NewPlaytestSim(PlaytestSimConfig{World: w, Start: SimVec{X: 0.5, Y: 64, Z: 0.5}})
	for _, to := range [][2]float64{{40, 0.5}, {-40, 0.5}, {0.5, 40}, {0.5, -40}, {40, 40}} {
		s.Pos = SimVec{X: 0.5, Y: 64, Z: 0.5}
		s.applyIntent(&civquestv1.AgentIntent{Action: &civquestv1.AgentIntent_Navigate{Navigate: &civquestv1.NavigateIntent{
			Target: &civquestv1.Vec3{X: to[0], Y: 64, Z: to[1]}, SpeedMultiplier: 1,
		}}})
		for i := 0; i < 200; i++ {
			s.advanceBody(4)
		}
		if s.Pos.X < -5 || s.Pos.X >= 6 || s.Pos.Z < -5 || s.Pos.Z >= 6 || s.Pos.Y != 64 {
			t.Errorf("walking to %v ended outside the ground at (%.2f,%.2f,%.2f)", to, s.Pos.X, s.Pos.Y, s.Pos.Z)
		}
	}
}
