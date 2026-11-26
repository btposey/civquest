package system1

// Block-contact hazards for the emergency activity (bead cq-71l.13, ADR-036 frame-local).
//
// proto DamageSourceType has no CACTUS/VOID/SUFFOCATION values, so these are derived the same
// way drowning is (emergency_hazards.go): from sustained frame facts, not damage edges.
//
//   - cactus / sweet berry: a hazard block in the agent's own cell, or (cactus only)
//     horizontally adjacent while unattributed ("environmental") damage lands and food > 0
//     (starvation damage is unattributed too).
//   - suffocation: the eye cell is a full solid block while unattributed damage lands.
//   - void edge: standing on ground within voidEdgeMargin of a side whose neighbouring column
//     is empty for voidDropDepth blocks (the Java collector samples those cells) while another
//     neighbouring column shows ground (so sparse frames never read as a void).
//
// The result is a fact (threat.block_hazard) so IsEmergency / stage derivation see it, plus
// an escape intent built by blockHazardIntent. Everything is recomputed per frame; the
// emergency frame holds no extra state and resolves when the frame no longer reports it.

import (
	"math"
	"strings"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

type blockHazardKind string

const (
	blockHazardNone        blockHazardKind = ""
	blockHazardCactus      blockHazardKind = "CACTUS"
	blockHazardBerries     blockHazardKind = "SWEET_BERRY"
	blockHazardVoidEdge    blockHazardKind = "VOID_EDGE"
	blockHazardSuffocation blockHazardKind = "SUFFOCATION"
)

const (
	// voidDropDepth: a neighbouring column empty for this many blocks below the feet is a
	// damaging/void drop (vanilla fall damage starts above 3 blocks). Design budget.
	voidDropDepth = 4
	// voidEdgeMargin: distance (blocks) from the cell border toward the drop that counts as about
	// to step off; wider margins fire on ordinary walking along a ledge. Design budget.
	voidEdgeMargin = 0.15
	// contactEscapeDistance: how far away from contact hazards to retreat.
	contactEscapeDistance = 3.0
	// eyeHeight is the standing eye offset used for the suffocation cell.
	eyeHeight = 1.5
)

var horizontalDirs = [4][2]int32{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}

// blockHazard is one derived block-contact hazard with its escape plan.
type blockHazard struct {
	Kind   blockHazardKind
	Target *civquestv1.Vec3          // navigate here (nil when Dig is set)
	Dig    *civquestv1.BlockPosProto // break this block to free the agent
}

// unattributedDamage is damage with no source type/attacker: cactus, berries, suffocation.
func unattributedDamage(f *civquestv1.TelemetryFrame) bool {
	d := f.LastDamageTaken
	return d != nil && d.SourceType == civquestv1.DamageSourceType_DAMAGE_SOURCE_TYPE_UNSPECIFIED &&
		d.AttackerEntityId == 0 && d.AttackerName == "" && d.DamageAmount > 0
}

func isSuffocatingBlock(id string) bool {
	id = strings.ToLower(id)
	for _, no := range []string{"slab", "stairs", "wall", "button", "plate", "fence", "door", "pane", "bars", "carpet", "torch", "sign", "ladder", "vine", "leaves", "glass", "cactus", "bush", "water", "lava"} {
		if strings.Contains(id, no) {
			return false
		}
	}
	for _, yes := range []string{"stone", "dirt", "gravel", "sand", "planks", "_log", "_wood", "_ore", "deepslate", "granite", "diorite", "andesite", "bedrock", "netherrack", "clay", "terracotta", "concrete", "_wool", "mud", "podzol", "grass_block", "mycelium", "obsidian", "bricks", "basalt", "tuff", "calcite"} {
		if strings.Contains(id, yes) {
			return true
		}
	}
	return false
}

// detectBlockHazard derives the highest-priority block-contact hazard from one frame, or nil.
func detectBlockHazard(f *civquestv1.TelemetryFrame) *blockHazard {
	if f == nil || f.Status == nil || f.Status.Position == nil {
		return nil
	}
	p := f.Status.Position
	fx, fy, fz := int32(math.Floor(p.X)), int32(math.Floor(p.Y+1e-3)), int32(math.Floor(p.Z))
	blocks := map[[3]int32]string{}
	for _, b := range f.InterestingBlocks {
		if b != nil && b.Pos != nil {
			blocks[[3]int32{b.Pos.X, b.Pos.Y, b.Pos.Z}] = strings.ToLower(b.BlockId)
		}
	}
	at := func(x, y, z int32) string { return blocks[[3]int32{x, y, z}] }
	// Starvation damage is also unattributed but only lands at food 0: not block contact.
	dmg := unattributedDamage(f) && f.Status.FoodLevel > 0

	// Suffocation: eye cell inside a full block (life first).
	eyeY := int32(math.Floor(p.Y + eyeHeight))
	if dmg && isSuffocatingBlock(at(fx, eyeY, fz)) {
		for _, d := range horizontalDirs {
			x, z := fx+d[0], fz+d[1]
			if !isSuffocatingBlock(at(x, fy, z)) && !isSuffocatingBlock(at(x, fy+1, z)) {
				return &blockHazard{Kind: blockHazardSuffocation, Target: &civquestv1.Vec3{X: float64(x) + 0.5, Y: float64(fy), Z: float64(z) + 0.5}}
			}
		}
		return &blockHazard{Kind: blockHazardSuffocation, Dig: &civquestv1.BlockPosProto{X: fx, Y: eyeY, Z: fz}}
	}

	// Cactus / sweet berry contact: retreat away from the hazard cells.
	var hx, hz float64
	n := 0
	kind := blockHazardNone
	note := func(k blockHazardKind, x, z int32) {
		hx += float64(x) + 0.5
		hz += float64(z) + 0.5
		n++
		if kind == blockHazardNone || k == blockHazardCactus {
			kind = k
		}
	}
	classify := func(id string) blockHazardKind {
		switch {
		case strings.Contains(id, "cactus"):
			return blockHazardCactus
		case strings.Contains(id, "sweet_berry_bush"):
			return blockHazardBerries
		}
		return blockHazardNone
	}
	for dy := int32(0); dy <= 1; dy++ {
		if k := classify(at(fx, fy+dy, fz)); k != blockHazardNone {
			note(k, fx, fz)
		}
		if dmg {
			for _, d := range horizontalDirs {
				// Berry bushes only hurt on overlap (own cell) and are foraging targets: adjacent cactus only.
				if k := classify(at(fx+d[0], fy+dy, fz+d[1])); k == blockHazardCactus {
					note(k, fx+d[0], fz+d[1])
				}
			}
		}
	}
	if n > 0 {
		dx, dz := p.X-hx/float64(n), p.Z-hz/float64(n)
		l := math.Hypot(dx, dz)
		if l < 0.01 {
			dx, dz, l = 1, 0, 1
		}
		return &blockHazard{Kind: kind, Target: &civquestv1.Vec3{X: p.X + dx/l*contactEscapeDistance, Y: p.Y, Z: p.Z + dz/l*contactEscapeDistance}}
	}

	// Void edge: grounded, at the border of a cell whose neighbour column is a deep drop.
	// Sparse frames (sims, hand-built telemetry) list no neighbour ground at all, so a drop is only
	// believed when the frame also shows ground under at least one neighbouring column.
	columnEmpty := func(x, z int32) bool {
		for k := int32(1); k <= voidDropDepth; k++ {
			if at(x, fy-k, z) != "" {
				return false
			}
		}
		return true
	}
	sampled := false
	for _, d := range horizontalDirs {
		sampled = sampled || !columnEmpty(fx+d[0], fz+d[1])
	}
	if sampled && at(fx, fy-1, fz) != "" {
		fracX, fracZ := p.X-float64(fx)-0.5, p.Z-float64(fz)-0.5
		for _, d := range horizontalDirs {
			off := fracX*float64(d[0]) + fracZ*float64(d[1])
			if off < 0.5-voidEdgeMargin {
				continue
			}
			x, z := fx+d[0], fz+d[1]
			if at(x, fy, z) == "" && at(x, fy+1, z) == "" && columnEmpty(x, z) {
				return &blockHazard{Kind: blockHazardVoidEdge, Target: &civquestv1.Vec3{X: float64(fx) + 0.5, Y: float64(fy), Z: float64(fz) + 0.5}}
			}
		}
	}
	return nil
}

// blockHazardIntent builds the escape for a block hazard, deduping navigate re-issues the way
// hazardEscapeIntent does.
func blockHazardIntent(agentID string, f *civquestv1.TelemetryFrame, h *blockHazard, lastTarget *civquestv1.Vec3, lastTick, tick int64) *civquestv1.AgentIntent {
	if h == nil {
		return nil
	}
	if h.Dig != nil {
		if lastTick != 0 && tick-lastTick < 5 {
			return nil
		}
		return &civquestv1.AgentIntent{AgentId: agentID, Action: &civquestv1.AgentIntent_InteractBlock{InteractBlock: &civquestv1.InteractBlockIntent{
			Pos: h.Dig, InteractionType: civquestv1.InteractBlockIntent_HARVEST_BREAK,
		}}}
	}
	idle := f != nil && f.Status != nil && f.Status.NavigationState != civquestv1.NavigationState_NAVIGATION_STATE_PATHING
	same := lastTarget != nil && math.Hypot(lastTarget.X-h.Target.X, lastTarget.Z-h.Target.Z) <= 1.5
	if same && (tick-lastTick < 5 || (!idle && tick-lastTick < hazardNavReissueTicks)) {
		return nil
	}
	return &civquestv1.AgentIntent{AgentId: agentID, Action: &civquestv1.AgentIntent_Navigate{Navigate: &civquestv1.NavigateIntent{
		Target: h.Target, SpeedMultiplier: 1.2,
	}}}
}
