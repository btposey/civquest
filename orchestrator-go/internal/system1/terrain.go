package system1

import (
	"math"
	"strings"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// TerrainType classifies the local environmental and elevation topology within sensory frustum.
type TerrainType string

const (
	TerrainTypeGeneral         TerrainType = "GENERAL"
	TerrainTypeRiverFord       TerrainType = "RIVER_FORD"
	TerrainTypeValley          TerrainType = "VALLEY"
	TerrainTypeRidge           TerrainType = "RIDGE"
	TerrainTypeFlatlands       TerrainType = "FLATLANDS"
	TerrainTypeDepression      TerrainType = "DEPRESSION"
	TerrainTypeDenseVegetation TerrainType = "DENSE_VEGETATION"
	TerrainTypeWaterway        TerrainType = "WATERWAY"
	TerrainTypeMountainPass    TerrainType = "MOUNTAIN_PASS"
)

// TerrainProfile contains extracted topographic statistics from local sensory frustum.
type TerrainProfile struct {
	Type               TerrainType
	ElevationVariance  float64
	MinY               int32
	MaxY               int32
	WaterBlockCount    int
	FoliageBlockCount  int
	FordTarget         *civquestv1.Vec3
	ValleyAxisX        float64
	ValleyAxisZ        float64
	RidgeAxisX         float64
	RidgeAxisZ         float64
	DepressionCenter   *civquestv1.Vec3
	DepressionRadius   float64
	PassTarget         *civquestv1.Vec3
	DistantPOITarget   *civquestv1.Vec3
	TrailblazeAzimuthX float64
	TrailblazeAzimuthZ float64
}

// ClassifyTerrain performs a sub-5ms deterministic analysis of visible blocks in the sensory frustum.
func ClassifyTerrain(frame *civquestv1.TelemetryFrame, pos *civquestv1.Vec3) TerrainProfile {
	profile := TerrainProfile{
		Type:               TerrainTypeGeneral,
		MinY:               int32(math.MaxInt32),
		MaxY:               int32(math.MinInt32),
		TrailblazeAzimuthX: 1.0, // Default Eastward corridor
		TrailblazeAzimuthZ: 0.0,
	}
	if frame == nil || pos == nil || len(frame.InterestingBlocks) == 0 {
		return profile
	}

	agentY := int32(math.Floor(pos.Y))
	var waterBlocks []*civquestv1.BlockPosProto
	var foliageBlocks []*civquestv1.BlockPosProto
	var surfaceHeights []float64

	// Grid heightmap within 16-block radius
	heightGrid := make(map[[2]int32]int32)
	blockGrid := make(map[[3]int32]string)

	for _, b := range frame.InterestingBlocks {
		if b == nil || b.Pos == nil {
			continue
		}
		p := b.Pos
		id := strings.ToLower(b.BlockId)
		blockGrid[[3]int32{p.X, p.Y, p.Z}] = id

		if p.Y < profile.MinY {
			profile.MinY = p.Y
		}
		if p.Y > profile.MaxY {
			profile.MaxY = p.Y
		}

		key := [2]int32{p.X, p.Z}
		if cur, ok := heightGrid[key]; !ok || p.Y > cur {
			heightGrid[key] = p.Y
		}

		// Water detection
		if strings.Contains(id, "water") {
			waterBlocks = append(waterBlocks, p)
		}

		// Dense foliage detection at eye/head level (agentY to agentY + 2)
		if (strings.Contains(id, "leaves") || strings.Contains(id, "vine") || strings.Contains(id, "_log") || strings.Contains(id, "_wood")) &&
			p.Y >= agentY && p.Y <= agentY+2 {
			foliageBlocks = append(foliageBlocks, p)
		}
	}

	profile.WaterBlockCount = len(waterBlocks)
	profile.FoliageBlockCount = len(foliageBlocks)

	for _, h := range heightGrid {
		surfaceHeights = append(surfaceHeights, float64(h))
	}

	if len(surfaceHeights) > 0 {
		var sum float64
		for _, h := range surfaceHeights {
			sum += h
		}
		mean := sum / float64(len(surfaceHeights))
		var varianceSum float64
		for _, h := range surfaceHeights {
			diff := h - mean
			varianceSum += diff * diff
		}
		profile.ElevationVariance = varianceSum / float64(len(surfaceHeights))
	}

	// 1. River Ford Detection:
	// A shallow water line (width <= 2, water at agentY or agentY-1) between solid ground on both sides
	if len(waterBlocks) > 0 && len(waterBlocks) <= 18 {
		if ford := findRiverFord(pos, waterBlocks, blockGrid); ford != nil {
			profile.Type = TerrainTypeRiverFord
			profile.FordTarget = ford
			return profile
		}
	}

	// 2. Broad Waterway Detection:
	// A wide contiguous water body (> 3 blocks wide)
	if len(waterBlocks) >= 20 {
		profile.Type = TerrainTypeWaterway
		return profile
	}

	// 3. Dense Vegetation Trailblazing:
	// High density of leaf/log/vine blocks obstructing eye level
	if len(foliageBlocks) >= 10 {
		profile.Type = TerrainTypeDenseVegetation
		profile.TrailblazeAzimuthX = 1.0
		profile.TrailblazeAzimuthZ = 0.0
		return profile
	}

	// 4. Sunken Depression / Sinkhole Detection:
	// A local cluster of blocks significantly below agent position (dy <= -3) surrounded by higher terrain
	if depCenter, depRadius := findDepression(pos, heightGrid); depCenter != nil {
		profile.Type = TerrainTypeDepression
		profile.DepressionCenter = depCenter
		profile.DepressionRadius = depRadius
		return profile
	}

	// 5. Mountain Pass (Col / Saddle) Detection:
	// High terrain flanking (dy >= +4) with a navigable lower saddle
	if pass := findMountainPass(pos, heightGrid); pass != nil {
		profile.Type = TerrainTypeMountainPass
		profile.PassTarget = pass
		return profile
	}

	// 6. Deep Valley / Canyon Highway Detection:
	// Elevated walls on opposing sides (North/South or East/West) with a clear level corridor
	if axisX, axisZ, isValley := findValleyAxis(pos, heightGrid); isValley {
		profile.Type = TerrainTypeValley
		profile.ValleyAxisX = axisX
		profile.ValleyAxisZ = axisZ
		return profile
	}

	// 7. Hill Crest / Ridgeline Detection:
	// Agent is elevated relative to flanking slopes on two sides
	if axisX, axisZ, isRidge := findRidgeAxis(pos, heightGrid); isRidge {
		profile.Type = TerrainTypeRidge
		profile.RidgeAxisX = axisX
		profile.RidgeAxisZ = axisZ
		return profile
	}

	// 8. Flatlands / Desert / Plains Distant POI Detection:
	// Low elevation variance across visible surface blocks
	if profile.ElevationVariance < 1.2 && len(surfaceHeights) >= 12 {
		profile.Type = TerrainTypeFlatlands
		profile.DistantPOITarget = findDistantPOI(pos, frame.InterestingBlocks)
		return profile
	}

	return profile
}

// findRiverFord detects if a shallow water stream is crossing ahead with a walkable opposite bank.
func findRiverFord(pos *civquestv1.Vec3, waterBlocks []*civquestv1.BlockPosProto, blockGrid map[[3]int32]string) *civquestv1.Vec3 {
	agentX := int32(math.Floor(pos.X))
	agentY := int32(math.Floor(pos.Y))
	agentZ := int32(math.Floor(pos.Z))

	for _, w := range waterBlocks {
		distSq := (w.X-agentX)*(w.X-agentX) + (w.Z-agentZ)*(w.Z-agentZ)
		if distSq <= 36 && (w.Y == agentY || w.Y == agentY-1) {
			// Check if opposite bank (1-2 blocks past water in X or Z) is solid ground and dry
			for _, step := range []int32{2, 3} {
				// Test +X crossing
				oppBlock := [3]int32{w.X + step, w.Y, w.Z}
				if id, ok := blockGrid[oppBlock]; ok && !strings.Contains(id, "water") {
					return &civquestv1.Vec3{
						X: float64(oppBlock[0]) + 0.5,
						Y: float64(oppBlock[1]),
						Z: float64(oppBlock[2]) + 0.5,
					}
				}
				// Test +Z crossing
				oppBlockZ := [3]int32{w.X, w.Y, w.Z + step}
				if id, ok := blockGrid[oppBlockZ]; ok && !strings.Contains(id, "water") {
					return &civquestv1.Vec3{
						X: float64(oppBlockZ[0]) + 0.5,
						Y: float64(oppBlockZ[1]),
						Z: float64(oppBlockZ[2]) + 0.5,
					}
				}
			}
		}
	}
	return nil
}

// findDepression checks if a sunken area (dy <= -3) lies within 8 blocks of the agent.
func findDepression(pos *civquestv1.Vec3, heightGrid map[[2]int32]int32) (*civquestv1.Vec3, float64) {
	agentX := int32(math.Floor(pos.X))
	agentY := int32(math.Floor(pos.Y))
	agentZ := int32(math.Floor(pos.Z))

	var sunkenPoints [][2]int32
	for coord, h := range heightGrid {
		dx := coord[0] - agentX
		dz := coord[1] - agentZ
		if dx*dx+dz*dz <= 64 && h <= agentY-3 {
			sunkenPoints = append(sunkenPoints, coord)
		}
	}
	if len(sunkenPoints) >= 4 {
		var avgX, avgZ float64
		for _, p := range sunkenPoints {
			avgX += float64(p[0])
			avgZ += float64(p[1])
		}
		avgX /= float64(len(sunkenPoints))
		avgZ /= float64(len(sunkenPoints))
		return &civquestv1.Vec3{X: avgX + 0.5, Y: pos.Y, Z: avgZ + 0.5}, 5.0
	}
	return nil, 0.0
}

// findMountainPass identifies a saddle / col notch where high terrain dips between peaks.
func findMountainPass(pos *civquestv1.Vec3, heightGrid map[[2]int32]int32) *civquestv1.Vec3 {
	agentX := int32(math.Floor(pos.X))
	agentY := int32(math.Floor(pos.Y))
	agentZ := int32(math.Floor(pos.Z))

	var highCliffs [][2]int32
	for coord, h := range heightGrid {
		dx := coord[0] - agentX
		dz := coord[1] - agentZ
		if dx*dx+dz*dz <= 100 && h >= agentY+4 {
			highCliffs = append(highCliffs, coord)
		}
	}
	// If surrounded partially by high cliffs, look for a notch with h <= agentY + 2
	if len(highCliffs) >= 6 {
		for coord, h := range heightGrid {
			dx := coord[0] - agentX
			dz := coord[1] - agentZ
			if dx*dx+dz*dz <= 64 && h <= agentY+2 && (dx != 0 || dz != 0) {
				return &civquestv1.Vec3{
					X: float64(coord[0]) + 0.5,
					Y: float64(h),
					Z: float64(coord[1]) + 0.5,
				}
			}
		}
	}
	return nil
}

// findValleyAxis detects if terrain forms a canyon or valley trough (flanked by higher ground).
func findValleyAxis(pos *civquestv1.Vec3, heightGrid map[[2]int32]int32) (float64, float64, bool) {
	agentX := int32(math.Floor(pos.X))
	agentY := int32(math.Floor(pos.Y))
	agentZ := int32(math.Floor(pos.Z))

	// Check North-South elevated walls (East-West trough)
	northWall := heightGrid[[2]int32{agentX, agentZ - 4}]
	southWall := heightGrid[[2]int32{agentX, agentZ + 4}]
	eastPath := heightGrid[[2]int32{agentX + 4, agentZ}]
	westPath := heightGrid[[2]int32{agentX - 4, agentZ}]

	if northWall >= agentY+2 && southWall >= agentY+2 && (math.Abs(float64(eastPath-agentY)) <= 1.0 || math.Abs(float64(westPath-agentY)) <= 1.0) {
		return 1.0, 0.0, true // Valley axis points East-West
	}

	// Check East-West elevated walls (North-South trough)
	eastWall := heightGrid[[2]int32{agentX + 4, agentZ}]
	westWall := heightGrid[[2]int32{agentX - 4, agentZ}]
	southPath := heightGrid[[2]int32{agentX, agentZ + 4}]

	if eastWall >= agentY+2 && westWall >= agentY+2 && math.Abs(float64(southPath-agentY)) <= 1.0 {
		return 0.0, 1.0, true // Valley axis points North-South
	}

	return 0.0, 0.0, false
}

// findRidgeAxis detects if agent is along a crest with downward slopes on flanking sides.
func findRidgeAxis(pos *civquestv1.Vec3, heightGrid map[[2]int32]int32) (float64, float64, bool) {
	agentX := int32(math.Floor(pos.X))
	agentY := int32(math.Floor(pos.Y))
	agentZ := int32(math.Floor(pos.Z))

	// Check East-West crest (North & South drop)
	northDrop := heightGrid[[2]int32{agentX, agentZ - 4}]
	southDrop := heightGrid[[2]int32{agentX, agentZ + 4}]
	eastCrest := heightGrid[[2]int32{agentX + 4, agentZ}]

	if northDrop <= agentY-2 && southDrop <= agentY-2 && math.Abs(float64(eastCrest-agentY)) <= 1.0 {
		return 1.0, 0.0, true // Ridge axis points East
	}

	// Check North-South crest (East & West drop)
	eastDrop := heightGrid[[2]int32{agentX + 4, agentZ}]
	westDrop := heightGrid[[2]int32{agentX - 4, agentZ}]
	southCrest := heightGrid[[2]int32{agentX, agentZ + 4}]

	if eastDrop <= agentY-2 && westDrop <= agentY-2 && math.Abs(float64(southCrest-agentY)) <= 1.0 {
		return 0.0, 1.0, true // Ridge axis points South
	}

	return 0.0, 0.0, false
}

// findDistantPOI finds a prominent landmark block (e.g. tree log, outcropping) in the distance.
func findDistantPOI(pos *civquestv1.Vec3, blocks []*civquestv1.VisibleBlock) *civquestv1.Vec3 {
	var bestPOI *civquestv1.Vec3
	maxDistSq := 0.0

	for _, b := range blocks {
		if b == nil || b.Pos == nil {
			continue
		}
		id := strings.ToLower(b.BlockId)
		if strings.Contains(id, "_log") || strings.Contains(id, "stone") || strings.Contains(id, "sandstone") {
			dx := float64(b.Pos.X) + 0.5 - pos.X
			dz := float64(b.Pos.Z) + 0.5 - pos.Z
			dSq := dx*dx + dz*dz
			if dSq > maxDistSq && dSq <= 256.0 {
				maxDistSq = dSq
				bestPOI = &civquestv1.Vec3{
					X: float64(b.Pos.X) + 0.5,
					Y: float64(b.Pos.Y),
					Z: float64(b.Pos.Z) + 0.5,
				}
			}
		}
	}
	return bestPOI
}

// EvaluateTerrainExploration selects an exploration target based on local terrain heuristics.
func EvaluateTerrainExploration(
	agentID string,
	frame *civquestv1.TelemetryFrame,
	pos *civquestv1.Vec3,
	state *AgentState,
	cfg *xdg.AgentIdentityConfig,
) *civquestv1.AgentIntent {
	if frame == nil || pos == nil || state == nil {
		return nil
	}

	profile := ClassifyTerrain(frame, pos)

	var targetX, targetY, targetZ float64
	var goalLabel string

	switch profile.Type {
	case TerrainTypeRiverFord:
		if profile.FordTarget != nil {
			targetX = profile.FordTarget.X
			targetY = profile.FordTarget.Y
			targetZ = profile.FordTarget.Z
			goalLabel = "CROSS_FORD: Fording shallow stream and marking crossing point"
		}
	case TerrainTypeValley:
		targetX = pos.X + profile.ValleyAxisX*16.0
		targetY = pos.Y
		targetZ = pos.Z + profile.ValleyAxisZ*16.0
		goalLabel = "VALLEY_HIGHWAY: Advancing along natural valley corridor"

	case TerrainTypeRidge:
		targetX = pos.X + profile.RidgeAxisX*14.0
		targetY = pos.Y
		targetZ = pos.Z + profile.RidgeAxisZ*14.0
		goalLabel = "RIDGE_VANTAGE: Cresting ridgeline to survey surrounding horizon"

	case TerrainTypeDepression:
		// Circle the outer rim perimeter tangentially rather than plunging in
		if profile.DepressionCenter != nil {
			dx := pos.X - profile.DepressionCenter.X
			dz := pos.Z - profile.DepressionCenter.Z
			angle := math.Atan2(dz, dx) + 0.5 // 30-degree orbital step along outer rim
			r := math.Max(profile.DepressionRadius+2.0, 7.0)
			targetX = profile.DepressionCenter.X + math.Cos(angle)*r
			targetY = pos.Y
			targetZ = profile.DepressionCenter.Z + math.Sin(angle)*r
			goalLabel = "SKIRT_DEPRESSION: Contouring outer rim of sunken depression"
		}

	case TerrainTypeDenseVegetation:
		targetX = pos.X + profile.TrailblazeAzimuthX*12.0
		targetY = pos.Y
		targetZ = pos.Z + profile.TrailblazeAzimuthZ*12.0
		goalLabel = "TRAILBLAZE_CANOPY: Cutting straight trailblazer corridor through dense forest"

	case TerrainTypeMountainPass:
		if profile.PassTarget != nil {
			targetX = profile.PassTarget.X
			targetY = profile.PassTarget.Y
			targetZ = profile.PassTarget.Z
			goalLabel = "MOUNTAIN_PASS: Traversing mountain saddle into next valley"
		}

	case TerrainTypeFlatlands:
		if profile.DistantPOITarget != nil {
			targetX = profile.DistantPOITarget.X
			targetY = profile.DistantPOITarget.Y
			targetZ = profile.DistantPOITarget.Z
			goalLabel = "FLATLANDS_EXPEDITION: Marching toward distant landmark in open terrain"
		} else {
			targetX = pos.X + 20.0
			targetY = pos.Y
			targetZ = pos.Z
			goalLabel = "FLATLANDS_EXPEDITION: Long-distance compass march across open flatlands"
		}

	case TerrainTypeWaterway:
		// Broad water body: explore along bank
		targetX = pos.X + 10.0
		targetY = pos.Y
		targetZ = pos.Z + 4.0
		goalLabel = "WATERWAY_TRANSIT: Scouting along navigable river channel"

	default:
		return nil
	}

	if goalLabel != "" && state != nil {
		g := state.GetGoals()
		g.ImmediateGoal = goalLabel
		state.SetGoals(g)
	}

	state.lastExploreTick = frame.WorldTick
	return &civquestv1.AgentIntent{
		AgentId: agentID,
		Action: &civquestv1.AgentIntent_Navigate{
			Navigate: &civquestv1.NavigateIntent{
				Target: &civquestv1.Vec3{
					X: math.Round(targetX*10) / 10,
					Y: math.Round(targetY*10) / 10,
					Z: math.Round(targetZ*10) / 10,
				},
				SpeedMultiplier:  1.0,
				ArrivalTolerance: 2,
			},
		},
	}
}
