package playtest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
)

// Layer identifies which architectural layer generated an event.
type Layer string

const (
	LayerPuppeteering Layer = "PUPPETEERING"
	LayerSystem1      Layer = "COGNITIVE_SYSTEM1"
	LayerSystem2      Layer = "COGNITIVE_SYSTEM2"
)

// TraceEvent represents a structured diagnostic event emitted during playtest scenarios.
type TraceEvent struct {
	Timestamp string         `json:"timestamp"`
	WorldTick int64          `json:"world_tick"`
	Layer     Layer          `json:"layer"`
	Category  string         `json:"category"`
	AgentID   string         `json:"agent_id"`
	Summary   string         `json:"summary"`
	Details   map[string]any `json:"details,omitempty"`
}

// AABB represents an axis-aligned bounding box for spatial collision diagnostics.
type AABB struct {
	MinX float64 `json:"min_x"`
	MinY float64 `json:"min_y"`
	MinZ float64 `json:"min_z"`
	MaxX float64 `json:"max_x"`
	MaxY float64 `json:"max_y"`
	MaxZ float64 `json:"max_z"`
}

// Tracer manages file handles and streaming serialization for deep diagnostic logging
// across puppeteering, System 1, and System 2 layers.
type Tracer struct {
	mu           sync.Mutex
	outputDir    string
	secondaryDir string

	puppetFile *os.File
	sys1File   *os.File
	sys2File   *os.File
	traceFile  *os.File

	events []TraceEvent
}

// NewTracer initializes log files in outputDir (e.g. build/playtest/) and optionally
// replicates to secondaryDir (e.g. $XDG_STATE_HOME/civquest/logs/).
func NewTracer(outputDir, secondaryDir string) (*Tracer, error) {
	if outputDir == "" {
		outputDir = "build/playtest"
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed creating playtest trace directory %q: %w", outputDir, err)
	}

	puppetPath := filepath.Join(outputDir, "puppeteering.jsonl")
	sys1Path := filepath.Join(outputDir, "cognitive_system1.jsonl")
	sys2Path := filepath.Join(outputDir, "cognitive_system2.jsonl")
	tracePath := filepath.Join(outputDir, "playtest_trace.jsonl")

	puppetF, err := os.OpenFile(puppetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("failed opening %s: %w", puppetPath, err)
	}
	sys1F, err := os.OpenFile(sys1Path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		_ = puppetF.Close()
		return nil, fmt.Errorf("failed opening %s: %w", sys1Path, err)
	}
	sys2F, err := os.OpenFile(sys2Path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		_ = puppetF.Close()
		_ = sys1F.Close()
		return nil, fmt.Errorf("failed opening %s: %w", sys2Path, err)
	}
	traceF, err := os.OpenFile(tracePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		_ = puppetF.Close()
		_ = sys1F.Close()
		_ = sys2F.Close()
		return nil, fmt.Errorf("failed opening %s: %w", tracePath, err)
	}

	if secondaryDir != "" {
		_ = os.MkdirAll(secondaryDir, 0o755)
	}

	return &Tracer{
		outputDir:    outputDir,
		secondaryDir: secondaryDir,
		puppetFile:   puppetF,
		sys1File:     sys1F,
		sys2File:     sys2F,
		traceFile:    traceF,
		events:       make([]TraceEvent, 0, 512),
	}, nil
}

// Close flushes and closes all underlying file handles.
func (t *Tracer) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	var firstErr error
	if t.puppetFile != nil {
		if err := t.puppetFile.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		t.puppetFile = nil
	}
	if t.sys1File != nil {
		if err := t.sys1File.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		t.sys1File = nil
	}
	if t.sys2File != nil {
		if err := t.sys2File.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		t.sys2File = nil
	}
	if t.traceFile != nil {
		if err := t.traceFile.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		t.traceFile = nil
	}

	// Copy to secondary dir if specified
	if t.secondaryDir != "" && t.secondaryDir != t.outputDir {
		files := []string{"puppeteering.jsonl", "cognitive_system1.jsonl", "cognitive_system2.jsonl", "playtest_trace.jsonl"}
		for _, f := range files {
			src := filepath.Join(t.outputDir, f)
			dst := filepath.Join(t.secondaryDir, f)
			if data, err := os.ReadFile(src); err == nil {
				_ = os.WriteFile(dst, data, 0o644)
			}
		}
	}

	return firstErr
}

// OutputDir returns the primary directory containing trace logs.
func (t *Tracer) OutputDir() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.outputDir
}

// Events returns a copy of all recorded events.
func (t *Tracer) Events() []TraceEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	cp := make([]TraceEvent, len(t.events))
	copy(cp, t.events)
	return cp
}

func (t *Tracer) emit(layer Layer, category, agentID, summary string, tick int64, details map[string]any) {
	ev := TraceEvent{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		WorldTick: tick,
		Layer:     layer,
		Category:  category,
		AgentID:   agentID,
		Summary:   summary,
		Details:   details,
	}

	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	line := append(data, '\n')

	t.mu.Lock()
	defer t.mu.Unlock()

	t.events = append(t.events, ev)

	if t.traceFile != nil {
		_, _ = t.traceFile.Write(line)
	}

	switch layer {
	case LayerPuppeteering:
		if t.puppetFile != nil {
			_, _ = t.puppetFile.Write(line)
		}
	case LayerSystem1:
		if t.sys1File != nil {
			_, _ = t.sys1File.Write(line)
		}
	case LayerSystem2:
		if t.sys2File != nil {
			_, _ = t.sys2File.Write(line)
		}
	}
}

// ==============================================================================
// Puppeteering Diagnostic Logging
// ==============================================================================

// LogPuppetCoordinates logs physical entity coordinates and look rotations.
func (t *Tracer) LogPuppetCoordinates(agentID string, tick int64, pos *civquestv1.Vec3, yaw, pitch float32) {
	if pos == nil {
		return
	}
	t.emit(LayerPuppeteering, "PUPPET_COORDINATES", agentID,
		fmt.Sprintf("Agent %s at (%.2f, %.2f, %.2f) yaw=%.1f pitch=%.1f", agentID, pos.X, pos.Y, pos.Z, yaw, pitch),
		tick, map[string]any{
			"x":     pos.X,
			"y":     pos.Y,
			"z":     pos.Z,
			"yaw":   yaw,
			"pitch": pitch,
		})
}

// LogPuppetNavigationState logs Baritone / voxel navigation state changes.
func (t *Tracer) LogPuppetNavigationState(agentID string, tick int64, state civquestv1.NavigationState, target *civquestv1.Vec3, dist float64) {
	details := map[string]any{
		"nav_state": state.String(),
		"distance":  dist,
	}
	if target != nil {
		details["target"] = map[string]float64{"x": target.X, "y": target.Y, "z": target.Z}
	}
	t.emit(LayerPuppeteering, "PUPPET_NAVIGATION_STATE", agentID,
		fmt.Sprintf("Agent %s nav state: %s (dist=%.2fm)", agentID, state, dist),
		tick, details)
}

// LogPuppetBlockCollision logs block placement collision results with entity AABB info.
func (t *Tracer) LogPuppetBlockCollision(agentID string, tick int64, pos *civquestv1.BlockPosProto, blockID string, accepted bool, reason string, agentBox, blockBox AABB) {
	result := "ACCEPTED"
	if !accepted {
		result = "REJECTED"
	}
	t.emit(LayerPuppeteering, "PUPPET_BLOCK_COLLISION", agentID,
		fmt.Sprintf("Block place %s at (%d, %d, %d): %s (%s)", blockID, pos.GetX(), pos.GetY(), pos.GetZ(), result, reason),
		tick, map[string]any{
			"pos":        map[string]int32{"x": pos.GetX(), "y": pos.GetY(), "z": pos.GetZ()},
			"block_id":   blockID,
			"result":     result,
			"reason":     reason,
			"agent_aabb": agentBox,
			"block_aabb": blockBox,
		})
}

// LogPuppetCraftingProximity logs crafting table proximity checks and 3x3 reach evaluation.
func (t *Tracer) LogPuppetCraftingProximity(agentID string, tick int64, recipeID string, workstationPos *civquestv1.BlockPosProto, distSq float64, withinReach, accepted bool) {
	result := "ACCEPTED"
	if !accepted {
		result = "REJECTED"
	}
	details := map[string]any{
		"recipe_id":    recipeID,
		"dist_sq":      distSq,
		"within_reach": withinReach,
		"result":       result,
	}
	if workstationPos != nil {
		details["workstation_pos"] = map[string]int32{"x": workstationPos.GetX(), "y": workstationPos.GetY(), "z": workstationPos.GetZ()}
	}
	t.emit(LayerPuppeteering, "PUPPET_CRAFTING_PROXIMITY", agentID,
		fmt.Sprintf("Craft %s: %s (dist_sq=%.2f <= 20.25 reach: %t)", recipeID, result, distSq, withinReach),
		tick, details)
}

// LogPuppetItemPickup logs ground drop pickups with thrower UUID vs natural drop attribution.
func (t *Tracer) LogPuppetItemPickup(agentID string, tick int64, itemID string, count int32, throwerUUID, playerUUID string, isNaturalDrop, thankYouEmitted bool, chatMsg string) {
	t.emit(LayerPuppeteering, "PUPPET_ITEM_PICKUP", agentID,
		fmt.Sprintf("Picked up %dx %s (natural=%t, thrower=%q, thank_you=%t)", count, itemID, isNaturalDrop, throwerUUID, thankYouEmitted),
		tick, map[string]any{
			"item_id":           itemID,
			"count":             count,
			"thrower_uuid":      throwerUUID,
			"player_uuid":       playerUUID,
			"is_natural_drop":   isNaturalDrop,
			"thank_you_emitted": thankYouEmitted,
			"thank_you_chat":    chatMsg,
		})
}

// ==============================================================================
// System 1 Cognitive Diagnostic Logging
// ==============================================================================

// LogSystem1FSMTransition logs HFSM state transitions with debounce and oscillation tracking.
func (t *Tracer) LogSystem1FSMTransition(agentID string, tick int64, from, to system1.State, reason string, oscillationDetected bool, windowCount int) {
	t.emit(LayerSystem1, "S1_FSM_TRANSITION", agentID,
		fmt.Sprintf("State transition: %s -> %s (reason: %q, oscillation: %t)", from, to, reason, oscillationDetected),
		tick, map[string]any{
			"from_state":           string(from),
			"to_state":             string(to),
			"reason":               reason,
			"oscillation_detected": oscillationDetected,
			"window_transitions":   windowCount,
		})
}

// LogSystem1UtilityScores logs utility function evaluations and chosen action.
func (t *Tracer) LogSystem1UtilityScores(agentID string, tick int64, scores map[string]float64, chosenAction string) {
	t.emit(LayerSystem1, "S1_UTILITY_SCORES", agentID,
		fmt.Sprintf("Utility scores evaluated -> chosen action: %s", chosenAction),
		tick, map[string]any{
			"scores":        scores,
			"chosen_action": chosenAction,
		})
}

// LogSystem1RadialScouting logs sector azimuth locking, angular variance, and radial distance.
func (t *Tracer) LogSystem1RadialScouting(agentID string, tick int64, origin *civquestv1.Vec3, lockedAzimuth, curAzimuth, variance, radialDist float64, legIndex int, spinOscillation bool) {
	t.emit(LayerSystem1, "S1_RADIAL_SCOUTING", agentID,
		fmt.Sprintf("Radial scout leg %d: dist=%.1fm, azimuth=%.3frad (variance=%.3f, spin_oscillation=%t)", legIndex, radialDist, curAzimuth, variance, spinOscillation),
		tick, map[string]any{
			"locked_azimuth_rad":            lockedAzimuth,
			"current_azimuth_rad":           curAzimuth,
			"angular_variance_rad":          variance,
			"radial_distance_m":             radialDist,
			"leg_index":                     legIndex,
			"golden_angle_spin_oscillation": spinOscillation,
		})
}

// LogSystem1RoadPaving logs road paving desire paths, traversal thresholds, and campfire exclusion.
func (t *Tracer) LogSystem1RoadPaving(agentID string, tick int64, pos *civquestv1.BlockPosProto, traversals, threshold int, campfireDist float64, inExclusionZone bool, action string) {
	t.emit(LayerSystem1, "S1_ROAD_PAVING", agentID,
		fmt.Sprintf("Road paving at (%d, %d, %d): traversals=%d/%d, campfire_dist=%.1fm, in_exclusion=%t -> action=%s",
			pos.GetX(), pos.GetY(), pos.GetZ(), traversals, threshold, campfireDist, inExclusionZone, action),
		tick, map[string]any{
			"pos":               map[string]int32{"x": pos.GetX(), "y": pos.GetY(), "z": pos.GetZ()},
			"traversal_count":   traversals,
			"threshold":         threshold,
			"campfire_dist_m":   campfireDist,
			"in_exclusion_zone": inExclusionZone,
			"action":            action,
		})
}

// ==============================================================================
// System 2 Cognitive Diagnostic Logging
// ==============================================================================

// LogSystem2ContemplationTrigger logs nighttime contemplation evaluation.
func (t *Tracer) LogSystem2ContemplationTrigger(agentID string, tick, timeOfDay int64, trigger string, debounced bool) {
	t.emit(LayerSystem2, "S2_CONTEMPLATION_TRIGGER", agentID,
		fmt.Sprintf("Nighttime contemplation trigger: %s at tick %d (timeOfDay=%d, debounced=%t)", trigger, tick, timeOfDay, debounced),
		tick, map[string]any{
			"time_of_day": timeOfDay,
			"trigger":     trigger,
			"debounced":   debounced,
		})
}

// LogSystem2LedgerUpdate logs strategic ledger updates.
func (t *Tracer) LogSystem2LedgerUpdate(agentID string, tick, dayNumber int64, completed, newGoals []string, summary string) {
	t.emit(LayerSystem2, "S2_STRATEGIC_LEDGER_UPDATE", agentID,
		fmt.Sprintf("Strategic ledger day %d update: %s", dayNumber, summary),
		tick, map[string]any{
			"day_number":      dayNumber,
			"goals_completed": completed,
			"new_goals":       newGoals,
			"summary":         summary,
		})
}

// LogSystem2VectorRAG logs episodic/reflex vector memory additions.
func (t *Tracer) LogSystem2VectorRAG(agentID string, tick int64, memoryType, text string, embeddingDim int) {
	t.emit(LayerSystem2, "S2_VECTOR_RAG_MEMORY", agentID,
		fmt.Sprintf("Vector RAG memory [%s]: %s (dim=%d)", memoryType, text, embeddingDim),
		tick, map[string]any{
			"memory_type":   memoryType,
			"text":          text,
			"embedding_dim": embeddingDim,
		})
}
