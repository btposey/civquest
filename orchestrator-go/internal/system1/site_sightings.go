package system1

// Sightings that weigh in the choice of a settlement site (cq-6l3.20): ore and cave mouths come
// from the sensed blocks; livestock is noted from the animals in view.

import (
	"fmt"
	"math"
	"strings"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

const (
	// One cave mouth is reported from many of its ceiling blocks: within this distance they are one.
	caveMouthSpread = 12.0
	// One herd is noted once per cell of this size.
	livestockCell = 32.0
)

// knownNearLocked reports whether a sighting of blockID is recorded within dist of pos.
func (si *SpatialIndex) knownNearLocked(blockID string, pos *civquestv1.BlockPosProto, dist float64) bool {
	for _, p := range si.poisByID {
		if p != nil && p.BlockID == blockID && math.Hypot(p.X-float64(pos.X), p.Z-float64(pos.Z)) <= dist {
			return true
		}
	}
	return false
}

// IngestLivestock notes where untamed livestock has been seen, returning new sightings.
func (si *SpatialIndex) IngestLivestock(frame *civquestv1.TelemetryFrame) []db.AgentPOI {
	if si == nil || frame == nil {
		return nil
	}
	var added []db.AgentPOI
	si.mu.Lock()
	defer si.mu.Unlock()
	for _, e := range frame.VisibleEntities {
		if !isPassiveFoodAnimal(e) || e.Position == nil || bareID(e.EntityType) == "rabbit" {
			continue
		}
		id := fmt.Sprintf("livestock_%d_%d", int(math.Floor(e.Position.X/livestockCell)), int(math.Floor(e.Position.Z/livestockCell)))
		if _, known := si.poisByID[id]; known {
			continue
		}
		poi := db.AgentPOI{
			POIID: id, Category: POICategoryResource, BlockID: db.LivestockBlockID,
			X: e.Position.X, Y: e.Position.Y, Z: e.Position.Z,
			AccessX: e.Position.X, AccessY: e.Position.Y, AccessZ: e.Position.Z,
			ElevationLayer: ElevationSurface, SafetyScore: 1,
			DiscoveredTick: frame.WorldTick, LastVerifiedTick: frame.WorldTick, Reliability: 0.8, IsActive: true,
		}
		si.upsertLocked(poi)
		added = append(added, poi)
	}
	return added
}

// siteSightings is what the agent has seen near pos that speaks for settling there.
func (s *AgentState) siteSightings(pos *civquestv1.Vec3) db.SiteFeatures {
	var f db.SiteFeatures
	if s == nil || pos == nil {
		return f
	}
	si := s.SpatialIndex()
	if si == nil {
		return f
	}
	for _, p := range si.FindPOIsInRadius(pos, POICategoryResource, db.SiteFactorRadius) {
		switch {
		case p.BlockID == db.CaveEntranceBlockID:
			f.Cave = true
		case p.BlockID == db.LivestockBlockID:
			f.Livestock = true
		case strings.HasSuffix(p.BlockID, "_ore"):
			f.Ore = true
		}
	}
	return f
}
