package system1

import (
	"context"
	"math"
	"strings"
	"sync"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// ConstructionSkill executes autonomous physical construction of architectural blueprints
// subdivided into ADR-010 Spatial Quadrants (NW, NE, SW, SE) in strict bottom-up (Ymin -> Ymax) order.
// Supports subterranean excavation (Y < AnchorY), in-situ material taxonomy substitution, and
// directional property preservation without builder collisions or entrapment.
type ConstructionSkill struct {
	mu          sync.RWMutex
	coordinator *blueprints.ConstructionCoordinator
}

// NewConstructionSkill creates a new ConstructionSkill instance.
func NewConstructionSkill(coord ...*blueprints.ConstructionCoordinator) *ConstructionSkill {
	skill := &ConstructionSkill{}
	if len(coord) > 0 && coord[0] != nil {
		skill.coordinator = coord[0]
	}
	return skill
}

// SetCoordinator configures or overrides the active ConstructionCoordinator.
func (s *ConstructionSkill) SetCoordinator(coord *blueprints.ConstructionCoordinator) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.coordinator = coord
}

// GetCoordinator returns the active ConstructionCoordinator, if any.
func (s *ConstructionSkill) GetCoordinator() *blueprints.ConstructionCoordinator {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.coordinator
}

// Name returns the unique skill identifier.
func (s *ConstructionSkill) Name() string {
	return "blueprint_construction"
}

// Category returns the functional skill category.
func (s *ConstructionSkill) Category() string {
	return "construction"
}

// Preconditions returns true when a construction project is active and uncompleted,
// or when an affordable blueprint is recommended.
func (s *ConstructionSkill) Preconditions(facts *FactGraph) bool {
	if facts == nil {
		return false
	}

	// Active coordinator check
	s.mu.RLock()
	coord := s.coordinator
	s.mu.RUnlock()

	if coord != nil && !coord.IsComplete() {
		return true
	}

	// FactGraph blackboard trigger
	if facts.GetBool("construction.active") || facts.GetString("construction.project_id") != "" {
		return true
	}

	// Also triggered if a recommended blueprint is ready
	rec := facts.GetString("blueprint.recommended")
	return rec != "" && !facts.GetBool("construction.complete:"+rec)
}

// Interruptible returns true so high-priority combat or emergency reflexes preempt construction.
func (s *ConstructionSkill) Interruptible() bool {
	return true
}

// Execute consumes active blueprint quadrant work items and emits navigation or placement/excavation intents.
func (s *ConstructionSkill) Execute(ctx *SkillContext, facts *FactGraph) ([]*civquestv1.AgentIntent, error) {
	if ctx == nil || ctx.Frame == nil || ctx.Frame.Status == nil || ctx.Frame.Status.Position == nil {
		return nil, nil
	}

	s.mu.Lock()
	coord := s.coordinator
	s.mu.Unlock()

	if coord == nil || coord.IsComplete() {
		return nil, nil
	}

	agentID := ctx.AgentID
	if agentID == "" {
		agentID = "agent-builder"
	}

	// 1. Assign agent to an available quadrant if not already assigned
	quadrant, assigned := coord.GetAssignedQuadrant(agentID)
	if !assigned {
		allQuadrants := blueprints.AllQuadrants()
		for _, q := range allQuadrants {
			if !coord.IsQuadrantComplete(q) && coord.GetAssignedAgent(q) == "" {
				if err := coord.AssignAgent(q, agentID); err == nil {
					quadrant = q
					assigned = true
					if ctx.Construction != nil {
						ctx.Construction.Index = 0
					}
					break
				}
			}
		}
	}

	if !assigned {
		// All quadrants currently assigned or completed
		return nil, nil
	}

	// 2. Fetch ordered intents for this quadrant (strictly bottom-up Y_min -> Y_max)
	intents := coord.GetIntents(quadrant)
	if len(intents) == 0 {
		coord.MarkQuadrantComplete(quadrant)
		coord.UnassignAgent(agentID)
		return nil, nil
	}

	idx := 0
	if ctx.Construction != nil {
		idx = ctx.Construction.Index
	}

	if idx >= len(intents) {
		coord.MarkQuadrantComplete(quadrant)
		coord.UnassignAgent(agentID)
		s.OnComplete(ctx, facts)
		return nil, nil
	}

	out, advance := constructionWorkStep(ctx.Frame, quadrant, intents[idx], func(b *civquestv1.VisibleBlock) bool {
		return ctx.State.unbreakable(b, ctx.Frame.WorldTick)
	})
	if advance && ctx.Construction != nil {
		ctx.Construction.Index++
	}
	return out, nil
}

// constructionWorkStep is the pure core of one blueprint work item: approach the voxel, clear
// natural terrain obstructing it, or place the block. advance reports that the item is done
// (placed, or skipped for lacking a target voxel or having one that will not be cleared) and
// the caller's index moves on.
func constructionWorkStep(frame *civquestv1.TelemetryFrame, quadrant blueprints.Quadrant, targetWork *civquestv1.BuildBlockIntent,
	unbreakable func(*civquestv1.VisibleBlock) bool) ([]*civquestv1.AgentIntent, bool) {
	targetVoxel := targetWork.TargetVoxel
	if targetVoxel == nil {
		return nil, true
	}

	pos := frame.Status.Position
	dx := float64(targetVoxel.X) + 0.5 - pos.X
	dy := float64(targetVoxel.Y) + 0.5 - pos.Y
	dz := float64(targetVoxel.Z) + 0.5 - pos.Z
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)

	// 3. Proximity check: Move within 4.0 blocks interaction reach if too far
	if dist > 4.0 {
		// Stand at safe offset outside the target voxel to prevent builder self-entrapment
		navTarget := &civquestv1.Vec3{
			X: float64(targetVoxel.X) + 0.5,
			Y: float64(targetVoxel.Y),
			Z: float64(targetVoxel.Z) + 0.5,
		}
		// Apply safe offset based on quadrant to stand outside the structure's interior
		switch quadrant {
		case blueprints.QuadrantNW:
			navTarget.X -= 2.0
			navTarget.Z -= 2.0
		case blueprints.QuadrantNE:
			navTarget.X += 2.0
			navTarget.Z -= 2.0
		case blueprints.QuadrantSW:
			navTarget.X -= 2.0
			navTarget.Z += 2.0
		case blueprints.QuadrantSE:
			navTarget.X += 2.0
			navTarget.Z += 2.0
		}

		navIntent := &civquestv1.AgentIntent{
			Action: &civquestv1.AgentIntent_Navigate{
				Navigate: &civquestv1.NavigateIntent{
					Target:           navTarget,
					SpeedMultiplier:  1.0,
					ArrivalTolerance: 2.0,
				},
			},
		}
		return []*civquestv1.AgentIntent{navIntent}, false
	}

	// 4. Inspect current world voxel at target to detect subterranean obstruction
	targetBlockState := targetWork.BlockStateString
	var existingBlockAtVoxel string
	var obstruction *civquestv1.VisibleBlock
	for _, vb := range frame.InterestingBlocks {
		if vb != nil && vb.Pos != nil &&
			vb.Pos.X == targetVoxel.X &&
			vb.Pos.Y == targetVoxel.Y &&
			vb.Pos.Z == targetVoxel.Z {
			existingBlockAtVoxel, obstruction = vb.BlockId, vb
			break
		}
	}

	// If voxel is obstructed by solid natural terrain (subterranean excavation needed)
	if existingBlockAtVoxel != "" &&
		!strings.EqualFold(existingBlockAtVoxel, "minecraft:air") &&
		!strings.EqualFold(existingBlockAtVoxel, "air") &&
		!strings.HasPrefix(existingBlockAtVoxel, targetBlockState) {

		if unbreakable(obstruction) {
			return nil, true
		}
		// Emit excavation harvest intent to clear voxel
		digIntent := &civquestv1.AgentIntent{
			Action: &civquestv1.AgentIntent_InteractBlock{
				InteractBlock: &civquestv1.InteractBlockIntent{
					Pos:             targetVoxel,
					InteractionType: civquestv1.InteractBlockIntent_HARVEST_BREAK,
					BlockOrItemId:   existingBlockAtVoxel,
				},
			},
		}
		// Do not advance progress index until block is cleared
		return []*civquestv1.AgentIntent{digIntent}, false
	}

	// 5. Build carried inventory map to resolve generic in-situ taxonomy substitutions
	carried := make(map[string]int)
	if frame.Status != nil && frame.Status.Inventory != nil {
		for _, item := range frame.Status.Inventory {
			if item != nil && item.Count > 0 && item.ItemId != "" {
				norm := blueprints.NormalizeItemID(item.ItemId)
				carried[norm] += int(item.Count)
			}
		}
	}

	// Resolve in-situ material preserving directional properties (facing, axis, half, etc.)
	resolvedBlock := blueprints.ResolveInSituBlock(targetBlockState, carried, targetBlockState)

	// 6. Emit block placement intent
	placeIntent := &civquestv1.AgentIntent{
		Action: &civquestv1.AgentIntent_InteractBlock{
			InteractBlock: &civquestv1.InteractBlockIntent{
				Pos:             targetVoxel,
				InteractionType: civquestv1.InteractBlockIntent_PLACE_BLOCK,
				BlockOrItemId:   resolvedBlock,
			},
		},
	}
	return []*civquestv1.AgentIntent{placeIntent}, true
}

// OnComplete is called when a quadrant or entire construction project finishes.
func (s *ConstructionSkill) OnComplete(ctx *SkillContext, facts *FactGraph) {
	s.mu.RLock()
	coord := s.coordinator
	s.mu.RUnlock()

	if coord == nil {
		return
	}

	if coord.IsComplete() {
		// Register functional zone in CivicDB if available
		if ctx != nil && ctx.CivicDB != nil {
			bpPurpose := "SHELTER"
			zoneName := "Civic Settlement Structure"
			if facts != nil {
				if rec := facts.GetString("blueprint.recommended"); rec != "" {
					zoneName = "Constructed " + rec
				}
			}

			_, _ = coord.RegisterCompletedZone(context.Background(), blueprints.RegisterCompletedZoneRequest{
				ZoneType:     bpPurpose,
				Name:         zoneName,
				OwnerAgentID: ctx.AgentID,
				WorldTick:    ctx.Frame.WorldTick,
			})
		}
	}
}
