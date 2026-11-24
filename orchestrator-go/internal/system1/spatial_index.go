package system1

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// POI category constants matching ADR-022 and map_graph.db constraints.
const (
	POICategoryWorkstation = "WORKSTATION"
	POICategoryContainer   = "CONTAINER"
	POICategoryHabitation  = "HABITATION"
	POICategoryResource    = "RESOURCE"
	POICategoryHazard      = "HAZARD"
	POICategoryCrop        = "CROP"
	POICategorySettlement  = "SETTLEMENT"
)

// Elevation layer constants matching ADR-022 and map_graph.db constraints.
const (
	ElevationSurface     = "SURFACE"
	ElevationDugout      = "DUGOUT"
	ElevationUnderground = "UNDERGROUND"
	ElevationElevated    = "ELEVATED"
)

// SpatialIndex maintains a thread-safe, in-memory 16x16x16 voxel grid index of persistent POIs
// and learned trail corridors for sub-0.1ms zero-token System 1 spatial queries (ADR-022, ADR-023).
type SpatialIndex struct {
	mu                   sync.RWMutex
	buckets              map[[3]int32][]*db.AgentPOI
	poisByID             map[string]*db.AgentPOI
	corridorsByID        map[string]*db.TrailCorridor
	corridorsByEndpoints map[[2]string]*db.TrailCorridor
}

// NewSpatialIndex creates an initialized SpatialIndex.
func NewSpatialIndex() *SpatialIndex {
	return &SpatialIndex{
		buckets:              make(map[[3]int32][]*db.AgentPOI),
		poisByID:             make(map[string]*db.AgentPOI),
		corridorsByID:        make(map[string]*db.TrailCorridor),
		corridorsByEndpoints: make(map[[2]string]*db.TrailCorridor),
	}
}

func bucketCoord(v float64) int32 {
	return int32(math.Floor(v / 16.0))
}

func bucketKey(x, y, z float64) [3]int32 {
	return [3]int32{bucketCoord(x), bucketCoord(y), bucketCoord(z)}
}

// UpsertPOI inserts or updates a POI in the spatial index.
func (si *SpatialIndex) UpsertPOI(poi db.AgentPOI) {
	if si == nil || poi.POIID == "" {
		return
	}
	si.mu.Lock()
	defer si.mu.Unlock()
	si.upsertLocked(poi)
}

func (si *SpatialIndex) upsertLocked(poi db.AgentPOI) {
	cp := poi
	if existing, ok := si.poisByID[cp.POIID]; ok {
		oldKey := bucketKey(existing.X, existing.Y, existing.Z)
		newKey := bucketKey(cp.X, cp.Y, cp.Z)
		if oldKey == newKey {
			*existing = cp
			return
		}
		si.removeFromBucketLocked(oldKey, cp.POIID)
		si.poisByID[cp.POIID] = &cp
		si.buckets[newKey] = append(si.buckets[newKey], &cp)
		return
	}
	si.poisByID[cp.POIID] = &cp
	key := bucketKey(cp.X, cp.Y, cp.Z)
	si.buckets[key] = append(si.buckets[key], &cp)
}

func (si *SpatialIndex) removeFromBucketLocked(key [3]int32, poiID string) {
	list := si.buckets[key]
	for i, item := range list {
		if item != nil && item.POIID == poiID {
			last := len(list) - 1
			list[i] = list[last]
			list[last] = nil
			list = list[:last]
			break
		}
	}
	if len(list) == 0 {
		delete(si.buckets, key)
	} else {
		si.buckets[key] = list
	}
}

// GetPOI retrieves a defensive copy of a POI by ID.
func (si *SpatialIndex) GetPOI(id string) (*db.AgentPOI, bool) {
	if si == nil || id == "" {
		return nil, false
	}
	si.mu.RLock()
	defer si.mu.RUnlock()
	p, ok := si.poisByID[id]
	if !ok || p == nil {
		return nil, false
	}
	cp := *p
	return &cp, true
}

// RemovePOI removes a POI by ID from both the lookup map and its spatial bucket.
func (si *SpatialIndex) RemovePOI(id string) {
	if si == nil || id == "" {
		return
	}
	si.mu.Lock()
	defer si.mu.Unlock()
	p, ok := si.poisByID[id]
	if !ok || p == nil {
		return
	}
	delete(si.poisByID, id)
	si.removeFromBucketLocked(bucketKey(p.X, p.Y, p.Z), id)
}

// DecayPOIReliability reduces a POI's reliability in-memory by delta, clamped to [0.0, 1.0].
// Returns the updated reliability and whether the POI was found.
func (si *SpatialIndex) DecayPOIReliability(poiID string, delta float64) (float64, bool) {
	if si == nil || poiID == "" || delta <= 0 {
		return 0, false
	}
	si.mu.Lock()
	defer si.mu.Unlock()
	p, ok := si.poisByID[poiID]
	if !ok || p == nil {
		return 0, false
	}
	p.Reliability -= delta
	if p.Reliability < 0.0 {
		p.Reliability = 0.0
	}
	if p.Reliability > 1.0 {
		p.Reliability = 1.0
	}
	return p.Reliability, true
}

// poiAccessTarget returns the walkable access coordinate for a POI, falling back to the
// POI block center if Access coordinates are all unset while block coordinates are non-zero.
func poiAccessTarget(poi *db.AgentPOI) *civquestv1.Vec3 {
	if poi == nil {
		return nil
	}
	if poi.AccessX == 0 && poi.AccessY == 0 && poi.AccessZ == 0 && (poi.X != 0 || poi.Y != 0 || poi.Z != 0) {
		return &civquestv1.Vec3{X: poi.X, Y: poi.Y, Z: poi.Z}
	}
	return &civquestv1.Vec3{X: poi.AccessX, Y: poi.AccessY, Z: poi.AccessZ}
}

// poiBlockPos converts a POI's continuous world coordinates back into block grid coordinates.
func poiBlockPos(poi *db.AgentPOI) *civquestv1.BlockPosProto {
	if poi == nil {
		return nil
	}
	return &civquestv1.BlockPosProto{
		X: int32(math.Floor(poi.X)),
		Y: int32(math.Floor(poi.Y)),
		Z: int32(math.Floor(poi.Z)),
	}
}

// LoadFromPOIs replaces the contents of the SpatialIndex with the provided POI slice.
func (si *SpatialIndex) LoadFromPOIs(pois []db.AgentPOI) {
	if si == nil {
		return
	}
	si.mu.Lock()
	defer si.mu.Unlock()
	si.buckets = make(map[[3]int32][]*db.AgentPOI)
	si.poisByID = make(map[string]*db.AgentPOI, len(pois))
	for _, p := range pois {
		if p.POIID == "" {
			continue
		}
		si.upsertLocked(p)
	}
}

// AllPOIs returns defensive copies of all tracked POIs sorted deterministically by POIID.
func (si *SpatialIndex) AllPOIs() []*db.AgentPOI {
	if si == nil {
		return nil
	}
	si.mu.RLock()
	defer si.mu.RUnlock()
	out := make([]*db.AgentPOI, 0, len(si.poisByID))
	for _, p := range si.poisByID {
		if p == nil {
			continue
		}
		cp := *p
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].POIID < out[j].POIID
	})
	return out
}

// FindNearestPOI returns the closest active POI within maxDist matching the optional category,
// blockID, and elevationLayer filters. Empty filter strings match all values.
func (si *SpatialIndex) FindNearestPOI(pos *civquestv1.Vec3, category string, blockID string, maxDist float64, elevationLayer string) *db.AgentPOI {
	if si == nil || pos == nil || maxDist < 0 {
		return nil
	}

	cat := strings.TrimSpace(category)
	blk := strings.TrimSpace(blockID)
	layer := strings.TrimSpace(elevationLayer)
	maxDistSq := maxDist * maxDist

	si.mu.RLock()
	defer si.mu.RUnlock()

	if len(si.poisByID) == 0 {
		return nil
	}

	var best *db.AgentPOI
	var bestDistSq float64

	visitBucket := func(list []*db.AgentPOI) {
		for _, p := range list {
			if p == nil || !p.IsActive {
				continue
			}
			if cat != "" && !strings.EqualFold(p.Category, cat) {
				continue
			}
			if layer != "" && !strings.EqualFold(p.ElevationLayer, layer) {
				continue
			}
			if blk != "" && !matchesPOIBlockID(p.BlockID, blk) {
				continue
			}
			dx := p.X - pos.X
			dy := p.Y - pos.Y
			dz := p.Z - pos.Z
			distSq := dx*dx + dy*dy + dz*dz
			if distSq > maxDistSq {
				continue
			}
			if best == nil || distSq < bestDistSq || (distSq == bestDistSq && p.POIID < best.POIID) {
				best = p
				bestDistSq = distSq
			}
		}
	}

	si.forEachBucketInRangeLocked(pos, maxDist, visitBucket)

	if best == nil {
		return nil
	}
	cp := *best
	return &cp
}

// FindPOIsInRadius returns defensive copies of all active POIs within maxDist matching the
// optional category filter, sorted by distance ascending.
func (si *SpatialIndex) FindPOIsInRadius(pos *civquestv1.Vec3, category string, maxDist float64) []*db.AgentPOI {
	if si == nil || pos == nil || maxDist < 0 {
		return nil
	}

	cat := strings.TrimSpace(category)
	maxDistSq := maxDist * maxDist

	si.mu.RLock()
	defer si.mu.RUnlock()

	if len(si.poisByID) == 0 {
		return nil
	}

	type match struct {
		poi    *db.AgentPOI
		distSq float64
	}
	var matches []match

	visitBucket := func(list []*db.AgentPOI) {
		for _, p := range list {
			if p == nil || !p.IsActive {
				continue
			}
			if cat != "" && !strings.EqualFold(p.Category, cat) {
				continue
			}
			dx := p.X - pos.X
			dy := p.Y - pos.Y
			dz := p.Z - pos.Z
			distSq := dx*dx + dy*dy + dz*dz
			if distSq <= maxDistSq {
				matches = append(matches, match{poi: p, distSq: distSq})
			}
		}
	}

	si.forEachBucketInRangeLocked(pos, maxDist, visitBucket)

	if len(matches) == 0 {
		return nil
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].distSq != matches[j].distSq {
			return matches[i].distSq < matches[j].distSq
		}
		return matches[i].poi.POIID < matches[j].poi.POIID
	})

	out := make([]*db.AgentPOI, len(matches))
	for i, m := range matches {
		cp := *m.poi
		out[i] = &cp
	}
	return out
}

// HasSettlementPOI returns true if a verified settlement POI or habitation hearth exists in spatial memory (ADR-027).
// A consecrated settlement is anchored by either a POICategorySettlement entry or a POICategoryHabitation hearth
// (e.g. campfire, soul_campfire, bed, hearth) with reliability >= 0.5.
// If origin is nil or maxDist <= 0, any matching settlement POI in the index satisfies the query.
func (si *SpatialIndex) HasSettlementPOI(origin *civquestv1.Vec3, maxDist float64) bool {
	return si.FindSettlementHearth(origin, maxDist) != nil
}

// FindSettlementHearth returns the nearest verified settlement POI or habitation hearth in spatial memory (ADR-027).
// Returns nil if no valid settlement anchor has been recorded.
func (si *SpatialIndex) FindSettlementHearth(origin *civquestv1.Vec3, maxDist float64) *db.AgentPOI {
	if si == nil {
		return nil
	}
	si.mu.RLock()
	defer si.mu.RUnlock()

	var closest *db.AgentPOI
	minDistSq := math.MaxFloat64
	if maxDist > 0 {
		minDistSq = maxDist * maxDist
	}

	for _, poi := range si.poisByID {
		if poi == nil || poi.Reliability < 0.5 {
			continue
		}
		isSettlement := strings.EqualFold(poi.Category, POICategorySettlement)
		bLower := strings.ToLower(poi.BlockID)
		isHearth := strings.EqualFold(poi.Category, POICategoryHabitation) &&
			(strings.Contains(bLower, "campfire") || strings.Contains(bLower, "bed") || strings.Contains(bLower, "hearth"))
		if !isSettlement && !isHearth {
			continue
		}

		if origin == nil || maxDist <= 0 {
			cp := *poi
			return &cp
		}

		dx := poi.X - origin.X
		dy := poi.Y - origin.Y
		dz := poi.Z - origin.Z
		distSq := dx*dx + dy*dy + dz*dz
		if distSq <= minDistSq {
			minDistSq = distSq
			cp := *poi
			closest = &cp
		}
	}
	return closest
}

// IsSpecializedProfession returns true if the given profession is an advanced specialized urban/guild role
// (such as Farmer, Miner, Forester, Crafter, Builder, Rancher, Guard, Blacksmith) whose career duties
// and progression require a consecrated Clan Settlement POI (ADR-027).
// Wilderness pioneer archetypes (Explorer, Pioneer, Settler, Scout, Unassigned) return false.
func IsSpecializedProfession(prof string) bool {
	norm := strings.ToUpper(strings.TrimSpace(prof))
	switch norm {
	case "", "EXPLORER", "PIONEER", "SETTLER", "UNASSIGNED", "SCOUT":
		return false
	default:
		return true
	}
}

// FindPOIsAtBlockPos returns all active POIs at the given integer block coordinates.
func (si *SpatialIndex) FindPOIsAtBlockPos(x, y, z int32) []*db.AgentPOI {
	if si == nil {
		return nil
	}
	key := bucketKey(float64(x)+0.5, float64(y)+0.5, float64(z)+0.5)

	si.mu.RLock()
	defer si.mu.RUnlock()

	var out []*db.AgentPOI
	for _, p := range si.buckets[key] {
		if p == nil || !p.IsActive {
			continue
		}
		if int32(math.Floor(p.X)) == x && int32(math.Floor(p.Y)) == y && int32(math.Floor(p.Z)) == z {
			cp := *p
			out = append(out, &cp)
		}
	}
	return out
}

// FindPOIAtBlockPos returns the active POI at the given integer block coordinates, if any.
func (si *SpatialIndex) FindPOIAtBlockPos(x, y, z int32) *db.AgentPOI {
	pois := si.FindPOIsAtBlockPos(x, y, z)
	if len(pois) == 0 {
		return nil
	}
	return pois[0]
}

// FindPOIByTargetPos finds an active POI whose access position or block position
// matches target within tolerance (defaulting to 1.5 blocks if <= 0).
func (si *SpatialIndex) FindPOIByTargetPos(target *civquestv1.Vec3, tolerance float64) *db.AgentPOI {
	if si == nil || target == nil {
		return nil
	}
	if tolerance <= 0 {
		tolerance = 1.5
	}
	tolSq := tolerance * tolerance

	si.mu.RLock()
	defer si.mu.RUnlock()

	var best *db.AgentPOI
	var bestDistSq float64

	visitBucket := func(list []*db.AgentPOI) {
		for _, p := range list {
			if p == nil || !p.IsActive {
				continue
			}
			// Check access pos
			dxA := p.AccessX - target.X
			dyA := p.AccessY - target.Y
			dzA := p.AccessZ - target.Z
			distSqA := dxA*dxA + dyA*dyA + dzA*dzA
			if distSqA <= tolSq {
				if best == nil || distSqA < bestDistSq {
					best = p
					bestDistSq = distSqA
				}
				continue
			}

			// Check block center pos
			dxB := p.X - target.X
			dyB := p.Y - target.Y
			dzB := p.Z - target.Z
			distSqB := dxB*dxB + dyB*dyB + dzB*dzB
			if distSqB <= tolSq {
				if best == nil || distSqB < bestDistSq {
					best = p
					bestDistSq = distSqB
				}
			}
		}
	}

	si.forEachBucketInRangeLocked(target, tolerance+2.0, visitBucket)

	if best == nil {
		return nil
	}
	cp := *best
	return &cp
}

func (si *SpatialIndex) forEachBucketInRangeLocked(pos *civquestv1.Vec3, maxDist float64, fn func([]*db.AgentPOI)) {
	minBX := bucketCoord(pos.X - maxDist)
	maxBX := bucketCoord(pos.X + maxDist)
	minBY := bucketCoord(pos.Y - maxDist)
	maxBY := bucketCoord(pos.Y + maxDist)
	minBZ := bucketCoord(pos.Z - maxDist)
	maxBZ := bucketCoord(pos.Z + maxDist)

	spanX := int64(maxBX) - int64(minBX) + 1
	spanY := int64(maxBY) - int64(minBY) + 1
	spanZ := int64(maxBZ) - int64(minBZ) + 1

	// Iterate exact bucket range for local queries; fall back to populated bucket scan
	// when maxDist spans a huge volume relative to populated buckets.
	if spanX > 0 && spanY > 0 && spanZ > 0 &&
		spanX <= 64 && spanY <= 64 && spanZ <= 64 &&
		spanX*spanY*spanZ <= int64(len(si.buckets))*2+8 {
		for bx := minBX; bx <= maxBX; bx++ {
			for by := minBY; by <= maxBY; by++ {
				for bz := minBZ; bz <= maxBZ; bz++ {
					if list := si.buckets[[3]int32{bx, by, bz}]; len(list) > 0 {
						fn(list)
					}
				}
			}
		}
		return
	}

	for key, list := range si.buckets {
		if key[0] < minBX || key[0] > maxBX ||
			key[1] < minBY || key[1] > maxBY ||
			key[2] < minBZ || key[2] > maxBZ {
			continue
		}
		fn(list)
	}
}

func matchesPOIBlockID(stored, query string) bool {
	if strings.EqualFold(stored, query) {
		return true
	}
	if !strings.Contains(query, ":") {
		if len(stored) > len(query)+1 {
			offset := len(stored) - len(query)
			if (stored[offset-1] == ':' || stored[offset-1] == '_') && strings.EqualFold(stored[offset:], query) {
				return true
			}
		}
		if strings.EqualFold(query, "log") && (isLogBlockID(stored) || isLeavesBlockID(stored)) {
			return true
		}
	}
	if !strings.Contains(stored, ":") && len(query) > len(stored)+1 {
		offset := len(query) - len(stored)
		if (query[offset-1] == ':' || query[offset-1] == '_') && strings.EqualFold(query[offset:], stored) {
			return true
		}
	}
	return false
}

func isLeavesBlockID(blockID string) bool {
	lower := strings.ToLower(strings.TrimSpace(blockID))
	return strings.HasSuffix(lower, "_leaves") || lower == "minecraft:leaves" || lower == "leaves"
}

// ClassifyElevationLayer classifies a POI's vertical layer relative to selfY.
func ClassifyElevationLayer(poiY, selfY float64) string {
	dy := poiY - selfY
	switch {
	case dy >= 6.0:
		return ElevationElevated
	case dy <= -16.0:
		return ElevationUnderground
	case dy <= -4.0:
		return ElevationDugout
	default:
		return ElevationSurface
	}
}

// CategorizeBlockID maps a Minecraft block identifier to its ADR-022 POI category.
func CategorizeBlockID(blockID string) (category string, isPOI bool) {
	id := strings.ToLower(strings.TrimSpace(blockID))
	if id == "" {
		return "", false
	}
	name := id
	if idx := strings.LastIndex(id, ":"); idx >= 0 {
		name = id[idx+1:]
	}

	switch name {
	case "crafting_table", "furnace", "blast_furnace", "smoker",
		"anvil", "chipped_anvil", "damaged_anvil",
		"grindstone", "brewing_stand", "cartography_table",
		"smithing_table", "loom", "fletching_table", "stonecutter":
		return POICategoryWorkstation, true
	case "chest", "barrel", "trapped_chest":
		return POICategoryContainer, true
	case "campfire", "soul_campfire":
		return POICategoryHabitation, true
	case "lava", "fire", "soul_fire":
		return POICategoryHazard, true
	}

	if strings.Contains(name, "bed") && name != "bedrock" {
		return POICategoryHabitation, true
	}
	if strings.Contains(name, "_log") || strings.Contains(name, "stem") {
		return POICategoryResource, true
	}
	if strings.Contains(name, "_ore") {
		return POICategoryResource, true
	}
	if strings.HasSuffix(name, "_sapling") || name == "mangrove_propagule" {
		return POICategoryCrop, true
	}
	return "", false
}

// FormatPOIID returns the deterministic POI identifier for a category and block coordinate.
func FormatPOIID(category string, x, y, z int32) string {
	return fmt.Sprintf("poi_%s_%d_%d_%d", strings.ToLower(category), x, y, z)
}

func isPassableBlockID(blockID string) bool {
	if blockID == "" {
		return true
	}
	id := strings.ToLower(strings.TrimSpace(blockID))
	if idx := strings.LastIndex(id, ":"); idx >= 0 {
		id = id[idx+1:]
	}
	switch id {
	case "air", "cave_air", "void_air", "short_grass", "tall_grass", "grass", "fern", "snow", "torch", "wall_torch":
		return true
	default:
		return false
	}
}

// ComputePOIAccessPos determines a walkable adjacent coordinate next to a POI block,
// preferring unobstructed cardinal neighbors closest to selfPos.
func ComputePOIAccessPos(pos *civquestv1.BlockPosProto, selfPos *civquestv1.Vec3, occupied map[[3]int32]string) (float64, float64, float64) {
	if pos == nil {
		return 0, 0, 0
	}
	offsets := [4][3]int32{
		{1, 0, 0},
		{-1, 0, 0},
		{0, 0, 1},
		{0, 0, -1},
	}

	bestFound := false
	bestDistSq := math.MaxFloat64
	var bestX, bestY, bestZ float64

	for _, off := range offsets {
		nx, ny, nz := pos.X+off[0], pos.Y+off[1], pos.Z+off[2]
		if occupied != nil {
			if blk, exists := occupied[[3]int32{nx, ny, nz}]; exists && !isPassableBlockID(blk) {
				continue
			}
		}
		cx, cy, cz := float64(nx)+0.5, float64(ny), float64(nz)+0.5
		if selfPos == nil {
			return cx, cy, cz
		}
		dx := cx - selfPos.X
		dy := cy - selfPos.Y
		dz := cz - selfPos.Z
		distSq := dx*dx + dy*dy + dz*dz
		if !bestFound || distSq < bestDistSq {
			bestFound = true
			bestDistSq = distSq
			bestX, bestY, bestZ = cx, cy, cz
		}
	}

	if bestFound {
		return bestX, bestY, bestZ
	}
	return float64(pos.X) + 1.5, float64(pos.Y), float64(pos.Z) + 0.5
}

// IngestSensoryBlocks extracts POIs from frame.InterestingBlocks, assigns deterministic IDs,
// computes walkable access positions and elevation layers relative to surfaceY, updates the
// SpatialIndex, and returns newly discovered or meaningfully updated POIs for persistence.
func (si *SpatialIndex) IngestSensoryBlocks(frame *civquestv1.TelemetryFrame, surfaceY float64) []db.AgentPOI {
	if si == nil || frame == nil || len(frame.InterestingBlocks) == 0 {
		return nil
	}

	occupied := make(map[[3]int32]string, len(frame.InterestingBlocks))
	for _, b := range frame.InterestingBlocks {
		if b != nil && b.Pos != nil && b.BlockId != "" {
			occupied[[3]int32{b.Pos.X, b.Pos.Y, b.Pos.Z}] = b.BlockId
		}
	}

	var selfPos *civquestv1.Vec3
	if frame.Status != nil {
		selfPos = frame.Status.Position
	}

	si.mu.Lock()
	defer si.mu.Unlock()

	var updated []db.AgentPOI
	for _, b := range frame.InterestingBlocks {
		if b == nil || b.Pos == nil || b.BlockId == "" {
			continue
		}
		cat, isPOI := CategorizeBlockID(b.BlockId)
		if !isPOI {
			continue
		}

		poiID := FormatPOIID(cat, b.Pos.X, b.Pos.Y, b.Pos.Z)
		px := float64(b.Pos.X) + 0.5
		py := float64(b.Pos.Y)
		pz := float64(b.Pos.Z) + 0.5
		ax, ay, az := ComputePOIAccessPos(b.Pos, selfPos, occupied)
		layer := ClassifyElevationLayer(py, surfaceY)
		safety := 1.0
		if cat == POICategoryHazard {
			safety = 0.0
		}

		if existing, ok := si.poisByID[poiID]; ok && existing != nil {
			meaningfulChange := !existing.IsActive ||
				existing.BlockID != b.BlockId ||
				existing.ElevationLayer != layer ||
				existing.Reliability < 1.0 ||
				existing.SafetyScore != safety ||
				frame.WorldTick-existing.LastVerifiedTick >= 100 ||
				frame.WorldTick < existing.LastVerifiedTick

			existing.BlockID = b.BlockId
			existing.AccessX = ax
			existing.AccessY = ay
			existing.AccessZ = az
			existing.ElevationLayer = layer
			existing.SafetyScore = safety
			existing.LastVerifiedTick = frame.WorldTick
			existing.Reliability = 1.0
			existing.IsActive = true

			if meaningfulChange {
				updated = append(updated, *existing)
			}
			continue
		}

		poi := db.AgentPOI{
			POIID:            poiID,
			Category:         cat,
			BlockID:          b.BlockId,
			X:                px,
			Y:                py,
			Z:                pz,
			AccessX:          ax,
			AccessY:          ay,
			AccessZ:          az,
			ElevationLayer:   layer,
			SafetyScore:      safety,
			DiscoveredTick:   frame.WorldTick,
			LastVerifiedTick: frame.WorldTick,
			Reliability:      1.0,
			IsActive:         true,
		}
		si.upsertLocked(poi)
		updated = append(updated, poi)
	}

	return updated
}

// VerifyCloseRangePOIs inspects all active POIs within close range (<= maxDist Euclidean distance)
// of selfPos. If an active POI at that coordinate is missing from interestingBlocks or its category
// has changed, it is pruned from SpatialIndex and its ID is returned for persistence invalidation.
// If maxDist <= 0, the default threshold of 3.5 meters is used.
func (si *SpatialIndex) VerifyCloseRangePOIs(selfPos *civquestv1.Vec3, interestingBlocks []*civquestv1.VisibleBlock, maxDist float64) []string {
	if si == nil || selfPos == nil {
		return nil
	}
	if maxDist <= 0 {
		maxDist = 3.5
	}
	maxDistSq := maxDist * maxDist

	blockMap := make(map[[3]int32]*civquestv1.VisibleBlock, len(interestingBlocks))
	for _, b := range interestingBlocks {
		if b != nil && b.Pos != nil && b.BlockId != "" {
			blockMap[[3]int32{b.Pos.X, b.Pos.Y, b.Pos.Z}] = b
		}
	}

	si.mu.Lock()
	defer si.mu.Unlock()

	var prunedIDs []string
	prunedSet := make(map[string]struct{})

	visitBucket := func(list []*db.AgentPOI) {
		for _, p := range list {
			if p == nil || !p.IsActive {
				continue
			}
			dx := p.X - selfPos.X
			dy := p.Y - selfPos.Y
			dz := p.Z - selfPos.Z
			distSq := dx*dx + dy*dy + dz*dz
			if distSq > maxDistSq {
				continue
			}

			bx := int32(math.Floor(p.X))
			by := int32(math.Floor(p.Y))
			bz := int32(math.Floor(p.Z))

			blk, exists := blockMap[[3]int32{bx, by, bz}]
			invalid := false
			if !exists {
				invalid = true
			} else {
				cat, isPOI := CategorizeBlockID(blk.BlockId)
				if !isPOI || !strings.EqualFold(cat, p.Category) {
					invalid = true
				}
			}

			if invalid {
				if _, seen := prunedSet[p.POIID]; !seen {
					prunedSet[p.POIID] = struct{}{}
					p.IsActive = false
					prunedIDs = append(prunedIDs, p.POIID)
				}
			}
		}
	}

	si.forEachBucketInRangeLocked(selfPos, maxDist, visitBucket)

	for _, id := range prunedIDs {
		p, ok := si.poisByID[id]
		if ok && p != nil {
			delete(si.poisByID, id)
			si.removeFromBucketLocked(bucketKey(p.X, p.Y, p.Z), id)
		}
	}

	return prunedIDs
}

// UpsertCorridor registers or updates a learned TrailCorridor in the in-memory cache (ADR-023).
func (si *SpatialIndex) UpsertCorridor(c db.TrailCorridor) {
	if si == nil || c.ID == "" || c.FromPOIID == "" || c.ToPOIID == "" {
		return
	}
	si.mu.Lock()
	defer si.mu.Unlock()
	si.upsertCorridorLocked(c)
}

func (si *SpatialIndex) upsertCorridorLocked(c db.TrailCorridor) {
	if si.corridorsByID == nil {
		si.corridorsByID = make(map[string]*db.TrailCorridor)
	}
	if si.corridorsByEndpoints == nil {
		si.corridorsByEndpoints = make(map[[2]string]*db.TrailCorridor)
	}

	cp := c
	pair := [2]string{cp.FromPOIID, cp.ToPOIID}

	if old, exists := si.corridorsByID[cp.ID]; exists && old != nil {
		delete(si.corridorsByEndpoints, [2]string{old.FromPOIID, old.ToPOIID})
	}
	if oldPair, exists := si.corridorsByEndpoints[pair]; exists && oldPair != nil && oldPair.ID != cp.ID {
		delete(si.corridorsByID, oldPair.ID)
	}

	si.corridorsByID[cp.ID] = &cp
	si.corridorsByEndpoints[pair] = &cp
}

// GetCorridor retrieves a defensive copy of a TrailCorridor connecting fromPOIID to toPOIID.
func (si *SpatialIndex) GetCorridor(fromPOIID, toPOIID string) (*db.TrailCorridor, bool) {
	if si == nil || fromPOIID == "" || toPOIID == "" {
		return nil, false
	}
	si.mu.RLock()
	defer si.mu.RUnlock()
	if si.corridorsByEndpoints == nil {
		return nil, false
	}
	c, ok := si.corridorsByEndpoints[[2]string{fromPOIID, toPOIID}]
	if !ok || c == nil {
		return nil, false
	}
	cp := *c
	return &cp, true
}

// FindCorridorsFrom returns defensive copies of all known TrailCorridors originating at fromPOIID,
// ordered by traverse count descending, then total distance ascending.
func (si *SpatialIndex) FindCorridorsFrom(fromPOIID string) []*db.TrailCorridor {
	if si == nil || fromPOIID == "" {
		return nil
	}
	si.mu.RLock()
	defer si.mu.RUnlock()
	if si.corridorsByEndpoints == nil {
		return nil
	}

	var res []*db.TrailCorridor
	for pair, c := range si.corridorsByEndpoints {
		if pair[0] == fromPOIID && c != nil {
			cp := *c
			res = append(res, &cp)
		}
	}

	sort.Slice(res, func(i, j int) bool {
		if res[i].TraverseCount != res[j].TraverseCount {
			return res[i].TraverseCount > res[j].TraverseCount
		}
		if res[i].TotalDistance != res[j].TotalDistance {
			return res[i].TotalDistance < res[j].TotalDistance
		}
		return res[i].ID < res[j].ID
	})

	return res
}

// LoadCorridors bulk-loads trail corridors (e.g. from map_graph.db at startup).
func (si *SpatialIndex) LoadCorridors(corridors []db.TrailCorridor) {
	if si == nil || len(corridors) == 0 {
		return
	}
	si.mu.Lock()
	defer si.mu.Unlock()
	for _, c := range corridors {
		if c.ID != "" && c.FromPOIID != "" && c.ToPOIID != "" {
			si.upsertCorridorLocked(c)
		}
	}
}

// AllCorridors returns defensive copies of all cached TrailCorridors.
func (si *SpatialIndex) AllCorridors() []*db.TrailCorridor {
	if si == nil {
		return nil
	}
	si.mu.RLock()
	defer si.mu.RUnlock()
	res := make([]*db.TrailCorridor, 0, len(si.corridorsByID))
	for _, c := range si.corridorsByID {
		if c != nil {
			cp := *c
			res = append(res, &cp)
		}
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i].ID < res[j].ID
	})
	return res
}

// HasCorridorBetween checks whether a direct or reverse TrailCorridor connects two POIs.
func (si *SpatialIndex) HasCorridorBetween(fromPOIID, toPOIID string) bool {
	if si == nil || fromPOIID == "" || toPOIID == "" {
		return false
	}
	si.mu.RLock()
	defer si.mu.RUnlock()
	if _, ok := si.corridorsByEndpoints[[2]string{fromPOIID, toPOIID}]; ok {
		return true
	}
	if _, ok := si.corridorsByEndpoints[[2]string{toPOIID, fromPOIID}]; ok {
		return true
	}
	return false
}

// FindNearestCorridorIntersection locates the closest waypoint on any known TrailCorridor
// within maxDist of pos, returning the corridor, the waypoint index, the waypoint coords, and the distance.
func (si *SpatialIndex) FindNearestCorridorIntersection(pos *civquestv1.Vec3, maxDist float64) (*db.TrailCorridor, int, *civquestv1.Vec3, float64) {
	if si == nil || pos == nil {
		return nil, -1, nil, 0
	}
	si.mu.RLock()
	defer si.mu.RUnlock()

	var bestCorridor *db.TrailCorridor
	var bestWP *civquestv1.Vec3
	bestIdx := -1
	bestDist := maxDist
	if bestDist <= 0 {
		bestDist = 32.0
	}

	for _, c := range si.corridorsByID {
		if c == nil || c.WaypointsJSON == "" {
			continue
		}
		waypoints, err := DecodeWaypointsJSON(c.WaypointsJSON)
		if err != nil || len(waypoints) == 0 {
			continue
		}
		for idx, wp := range waypoints {
			if wp == nil {
				continue
			}
			d := Distance3D(pos, wp)
			if d < bestDist {
				bestDist = d
				bestIdx = idx
				bestWP = &civquestv1.Vec3{X: wp.X, Y: wp.Y, Z: wp.Z}
				cp := *c
				bestCorridor = &cp
			}
		}
	}

	if bestCorridor != nil {
		return bestCorridor, bestIdx, bestWP, bestDist
	}
	return nil, -1, nil, 0
}

// FindPortalPOI searches for the nearest portal or elevation transition POI (such as an entrance,
// campfire, habitation node, or staircase) within maxDist of pos.
func (si *SpatialIndex) FindPortalPOI(pos *civquestv1.Vec3, maxDist float64) *db.AgentPOI {
	if si == nil || pos == nil {
		return nil
	}
	if poi := si.FindNearestPOI(pos, POICategorySettlement, "", maxDist, ""); poi != nil {
		return poi
	}
	if poi := si.FindNearestPOI(pos, POICategoryHabitation, "campfire", maxDist, ""); poi != nil {
		return poi
	}
	if poi := si.FindNearestPOI(pos, POICategoryHabitation, "", maxDist, ""); poi != nil {
		return poi
	}
	return nil
}
