package system1

import (
	"strings"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// CardinalDirs provides standard cardinal offsets: North (0, -1), South (0, 1), East (1, 0), West (-1, 0).
var CardinalDirs = [][2]int32{
	{0, -1}, // North
	{1, 0},  // East
	{0, 1},  // South
	{-1, 0}, // West
}

// IsDiggableObstruction checks if a block identifier corresponds to a mineable terrain or foliage block.
func IsDiggableObstruction(id string) bool {
	s := strings.ToLower(strings.TrimSpace(id))
	if idx := strings.Index(s, ":"); idx != -1 {
		s = s[idx+1:]
	}
	switch {
	case s == "grass" || strings.HasSuffix(s, "short_grass") || strings.HasSuffix(s, "tall_grass") || strings.Contains(s, "seagrass"):
		return false // walked through, never in the way and nothing to step onto
	}
	return strings.HasSuffix(s, "_ore") ||
		strings.Contains(s, "dirt") ||
		strings.Contains(s, "grass") ||
		strings.Contains(s, "stone") ||
		strings.Contains(s, "cobblestone") ||
		strings.Contains(s, "deepslate") ||
		strings.Contains(s, "sand") ||
		strings.Contains(s, "gravel") ||
		strings.Contains(s, "clay") ||
		strings.Contains(s, "mud") ||
		strings.Contains(s, "log") ||
		strings.Contains(s, "wood") ||
		strings.Contains(s, "stem") ||
		strings.Contains(s, "planks") ||
		strings.Contains(s, "leaves") ||
		strings.Contains(s, "foliage")
}

// FindVisibleBlockAt locates a block from InterestingBlocks at the exact integer coordinates (x, y, z).
func FindVisibleBlockAt(frame *civquestv1.TelemetryFrame, x, y, z int32) *civquestv1.VisibleBlock {
	if frame == nil {
		return nil
	}
	for _, b := range frame.InterestingBlocks {
		if b == nil || b.Pos == nil {
			continue
		}
		if b.Pos.X == x && b.Pos.Y == y && b.Pos.Z == z {
			return b
		}
	}
	return nil
}
