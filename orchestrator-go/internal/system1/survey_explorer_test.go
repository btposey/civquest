package system1

import (
	"math"
	"strings"
	"testing"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

func makeUniformSurveyReport(ox, oy, oz float64, tick int64, featureType, blockID string, dist float32, occluded, open bool) *civquestv1.HorizonSurveyReport {
	sectors := make([]*civquestv1.HorizonSector, 36)
	for i := 0; i < 36; i++ {
		bearing := float32(i * 10)
		dx, dz := bearingToUnitVector(bearing)
		sectors[i] = &civquestv1.HorizonSector{
			SectorIndex: int32(i),
			BearingDeg:  bearing,
			HitDistance: dist,
			FeatureType: featureType,
			HitBlockId:  blockID,
			HitX:        ox + dx*float64(dist),
			HitY:        oy,
			HitZ:        oz + dz*float64(dist),
			Occluded:    occluded,
			OpenHorizon: open,
		}
	}
	return &civquestv1.HorizonSurveyReport{
		OriginX:       ox,
		OriginY:       oy,
		OriginZ:       oz,
		CompletedTick: tick,
		MaxRange:      64.0,
		Sectors:       sectors,
	}
}

func makeIdleExploreFrame(agentID string, tick int64, x, y, z float64, navState civquestv1.NavigationState) *civquestv1.TelemetryFrame {
	return &civquestv1.TelemetryFrame{
		WorldTick: tick,
		TimeOfDay: 3000,
		Weather:   civquestv1.Weather_WEATHER_CLEAR,
		Status: &civquestv1.AgentStatus{
			AgentId:         agentID,
			Health:          20.0,
			MaxHealth:       20.0,
			FoodLevel:       20,
			Position:        &civquestv1.Vec3{X: x, Y: y, Z: z},
			NavigationState: navState,
		},
	}
}

func TestHorizonBearingMathAndSummary(t *testing.T) {
	// Verify exact Minecraft native yaw math: dx = -sin(rad), dz = cos(rad)
	cases := []struct {
		bearing float32
		wantDX  float64
		wantDZ  float64
		wantDir string
	}{
		{0, 0, 1, "S"},
		{90, -1, 0, "W"},
		{180, 0, -1, "N"},
		{270, 1, 0, "E"},
	}
	for _, tc := range cases {
		dx, dz := bearingToUnitVector(tc.bearing)
		if math.Abs(dx-tc.wantDX) > 1e-6 || math.Abs(dz-tc.wantDZ) > 1e-6 {
			t.Fatalf("bearing %.0f: got (%.6f, %.6f), want (%.6f, %.6f)", tc.bearing, dx, dz, tc.wantDX, tc.wantDZ)
		}
		gotBearing := vectorToBearingDeg(dx, dz)
		if angularDiffDeg(gotBearing, tc.bearing) > 1e-3 {
			t.Fatalf("inverse bearing for (%.2f, %.2f): got %.2f, want %.2f", dx, dz, gotBearing, tc.bearing)
		}
		if dir := primaryCardinalForBearing(tc.bearing); dir != tc.wantDir {
			t.Fatalf("bearing %.0f: got cardinal %s, want %s", tc.bearing, dir, tc.wantDir)
		}
	}

	report := makeUniformSurveyReport(0, 64, 0, 100, "OPEN_PLAINS", "minecraft:air", 64.0, false, true)
	// N (180°): oak forest canopy at 42m
	report.Sectors[18].FeatureType = "FOREST_CANOPY"
	report.Sectors[18].HitBlockId = "minecraft:oak_leaves"
	report.Sectors[18].HitDistance = 42.0
	report.Sectors[18].OpenHorizon = false
	// S (0°): ridge obstruction at 8m
	report.Sectors[0].FeatureType = "RIDGE_OBSTRUCTION"
	report.Sectors[0].HitBlockId = "minecraft:stone"
	report.Sectors[0].HitDistance = 8.0
	report.Sectors[0].Occluded = true
	report.Sectors[0].OpenHorizon = false
	// W (90°): water at 38m
	report.Sectors[9].FeatureType = "WATER"
	report.Sectors[9].HitBlockId = "minecraft:water"
	report.Sectors[9].HitDistance = 38.0
	report.Sectors[9].OpenHorizon = false

	summary := FormatHorizonSurveySummary(report)
	for _, substr := range []string{"N: oak forest canopy at 42m", "E: open plains 64m+", "S: ridge obstruction at 8m", "W: water at 38m"} {
		if !strings.Contains(summary, substr) {
			t.Fatalf("expected summary %q to contain %q", summary, substr)
		}
	}
}
