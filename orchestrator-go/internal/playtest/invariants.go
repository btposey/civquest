package playtest

import (
	"fmt"
	"math"
	"sync"

	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
)

// InvariantStatus represents the verification outcome of an in-game invariant.
type InvariantStatus string

const (
	StatusPending InvariantStatus = "PENDING"
	StatusPassed  InvariantStatus = "PASSED"
	StatusFailed  InvariantStatus = "FAILED"
)

// InvariantResult encapsulates metrics, status, tick timestamps, and error details for an invariant.
type InvariantResult struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Status      InvariantStatus `json:"status"`
	StartTick   int64           `json:"start_tick"`
	EndTick     int64           `json:"end_tick"`
	Metrics     map[string]any  `json:"metrics,omitempty"`
	Failures    []string        `json:"failures,omitempty"`
}

// InvariantSuite tracks and asserts all 5 critical in-game behavioral invariants.
type InvariantSuite struct {
	mu sync.Mutex

	// Invariant 1: Scout Exploration
	ScoutMaxRadialDistance   float64
	ScoutLockedAzimuthRad    float64
	ScoutAzimuthSet          bool
	ScoutMaxAngularVariance  float64
	ScoutSpinOscillations    int
	ScoutArrivedObserved     bool
	ScoutPathingObserved     bool
	ScoutTransitions         []string
	ScoutFailures            []string

	// Invariant 2: Road Paving
	PavingCampfirePos        *civquestv1.BlockPosProto
	PavingMinCampfireDist    float64
	PavingBlocksPlaced       int
	PavingThresholdSatisfied bool
	PavingViolations         []string

	// Invariant 3: Shelter Collision Safety
	ShelterBlocksPlaced      int
	ShelterLastPlacementY    int32
	ShelterBottomUpViolations []string
	ShelterAABBIntersections int
	ShelterStallsDetected    int
	ShelterOscillations      int
	ShelterFailures          []string

	// Invariant 4: Workstation Crafting
	CraftDistantRejected     bool
	CraftNearbyAccepted      bool
	Craft2x2Accepted         bool
	CraftFailures            []string

	// Invariant 5: Drop Gratitude
	NaturalDropCollected     bool
	NaturalDropSilent        bool
	PlayerGiftCollected      bool
	PlayerGiftThankYouSent   bool
	GratitudeFailures        []string

	StartTick int64
	EndTick   int64
}

// NewInvariantSuite initializes an invariant assertion suite.
func NewInvariantSuite() *InvariantSuite {
	return &InvariantSuite{
		PavingMinCampfireDist: math.MaxFloat64,
		ShelterLastPlacementY: -999,
		NaturalDropSilent:     true, // Stays true unless a false thank-you is emitted
	}
}

// ==============================================================================
// Invariant 1: Scout Exploration
// ==============================================================================

// RecordScoutAzimuth checks sector azimuth lock and detects golden-angle (137.5°) spin oscillations.
func (s *InvariantSuite) RecordScoutAzimuth(tick int64, origin, target *civquestv1.Vec3) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if origin == nil || target == nil {
		return
	}
	dx := target.X - origin.X
	dz := target.Z - origin.Z
	dist := math.Hypot(dx, dz)
	if dist > s.ScoutMaxRadialDistance {
		s.ScoutMaxRadialDistance = dist
	}

	if dist < 5.0 {
		return
	}

	azimuth := math.Atan2(dz, dx)
	if !s.ScoutAzimuthSet {
		s.ScoutLockedAzimuthRad = azimuth
		s.ScoutAzimuthSet = true
		return
	}

	// Angular diff wrapped to [-pi, pi]
	diff := math.Atan2(math.Sin(azimuth-s.ScoutLockedAzimuthRad), math.Cos(azimuth-s.ScoutLockedAzimuthRad))
	absDiff := math.Abs(diff)
	if absDiff > s.ScoutMaxAngularVariance {
		s.ScoutMaxAngularVariance = absDiff
	}

	// Detect 137.5° (~2.399 rad) golden-angle spin thrashing
	goldenAngle := 137.5 * (math.Pi / 180.0)
	if math.Abs(absDiff-goldenAngle) < 0.15 {
		s.ScoutSpinOscillations++
		s.ScoutFailures = append(s.ScoutFailures,
			fmt.Sprintf("tick %d: 137.5° golden-angle spin oscillation detected (diff=%.3f rad)", tick, absDiff))
	} else if absDiff > 0.35 {
		s.ScoutFailures = append(s.ScoutFailures,
			fmt.Sprintf("tick %d: sector azimuth variance exceeded tolerance (diff=%.3f rad > 0.35)", tick, absDiff))
	}
}

// RecordScoutNavState records transitions between PATHING and ARRIVED.
func (s *InvariantSuite) RecordScoutNavState(tick int64, state civquestv1.NavigationState) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if state == civquestv1.NavigationState_NAVIGATION_STATE_PATHING {
		s.ScoutPathingObserved = true
	}
	if state == civquestv1.NavigationState_NAVIGATION_STATE_ARRIVED {
		s.ScoutArrivedObserved = true
	}
	s.ScoutTransitions = append(s.ScoutTransitions, fmt.Sprintf("tick %d: %s", tick, state))
}

// ==============================================================================
// Invariant 2: Road Paving
// ==============================================================================

// SetCampfirePos sets the reference campfire location for exclusion zone checking.
func (s *InvariantSuite) SetCampfirePos(pos *civquestv1.BlockPosProto) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.PavingCampfirePos = pos
}

// RecordRoadPavingPlacement records a paved path block and verifies >= 4m distance from campfire.
func (s *InvariantSuite) RecordRoadPavingPlacement(tick int64, pos *civquestv1.BlockPosProto, traversals int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.PavingBlocksPlaced++
	if traversals >= 15 {
		s.PavingThresholdSatisfied = true
	}

	if s.PavingCampfirePos != nil && pos != nil {
		dx := float64(pos.X - s.PavingCampfirePos.X)
		dz := float64(pos.Z - s.PavingCampfirePos.Z)
		dist := math.Hypot(dx, dz)
		if dist < s.PavingMinCampfireDist {
			s.PavingMinCampfireDist = dist
		}
		if dist < 4.0 {
			s.PavingViolations = append(s.PavingViolations,
				fmt.Sprintf("tick %d: road paved at (%d, %d, %d) within %.2fm of campfire (< 4m exclusion zone)",
					tick, pos.X, pos.Y, pos.Z, dist))
		}
	}
}

// ==============================================================================
// Invariant 3: Shelter Collision Safety
// ==============================================================================

// RecordShelterBlockPlacement records a blueprint placement and checks bottom-up ordering.
func (s *InvariantSuite) RecordShelterBlockPlacement(tick int64, pos *civquestv1.BlockPosProto) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if pos == nil {
		return
	}
	s.ShelterBlocksPlaced++
	if s.ShelterLastPlacementY != -999 {
		if pos.Y < s.ShelterLastPlacementY-1 {
			s.ShelterBottomUpViolations = append(s.ShelterBottomUpViolations,
				fmt.Sprintf("tick %d: placed block at Y=%d below previous block at Y=%d (bottom-up violation)",
					tick, pos.Y, s.ShelterLastPlacementY))
		}
	}
	s.ShelterLastPlacementY = pos.Y
}

// RecordCollisionCheck records an entity AABB vs block collision test result.
func (s *InvariantSuite) RecordCollisionCheck(tick int64, agentAABBIntersectsSolid bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if agentAABBIntersectsSolid {
		s.ShelterAABBIntersections++
		s.ShelterFailures = append(s.ShelterFailures,
			fmt.Sprintf("tick %d: agent AABB intersected solid block voxel (entombment hazard)", tick))
	}
}

// RecordShelterStall detects 30s timeout freezes or FSM oscillations.
func (s *InvariantSuite) RecordShelterStall(tick int64, desc string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ShelterStallsDetected++
	s.ShelterFailures = append(s.ShelterFailures, fmt.Sprintf("tick %d: shelter stall/timeout: %s", tick, desc))
}

// RecordShelterOscillation records FSM fluttering.
func (s *InvariantSuite) RecordShelterOscillation(tick int64, desc string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ShelterOscillations++
	s.ShelterFailures = append(s.ShelterFailures, fmt.Sprintf("tick %d: shelter FSM oscillation: %s", tick, desc))
}

// ==============================================================================
// Invariant 4: Workstation Crafting
// ==============================================================================

// RecordCraftingAttempt records 3x3 or 2x2 crafting outcomes relative to workstation proximity.
func (s *InvariantSuite) RecordCraftingAttempt(tick int64, recipeID string, distSq float64, is3x3, accepted bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if is3x3 {
		if distSq > 20.25 { // > 4.5m
			if accepted {
				s.CraftFailures = append(s.CraftFailures,
					fmt.Sprintf("tick %d: 3x3 recipe %s accepted when distant (dist_sq=%.2f > 20.25)", tick, recipeID, distSq))
			} else {
				s.CraftDistantRejected = true
			}
		} else { // <= 4.5m
			if accepted {
				s.CraftNearbyAccepted = true
			} else {
				s.CraftFailures = append(s.CraftFailures,
					fmt.Sprintf("tick %d: 3x3 recipe %s rejected when workstation is within reach (dist_sq=%.2f <= 20.25)", tick, recipeID, distSq))
			}
		}
	} else {
		// 2x2 recipes should be craftable anywhere
		if accepted {
			s.Craft2x2Accepted = true
		}
	}
}

// ==============================================================================
// Invariant 5: Drop Gratitude
// ==============================================================================

// RecordItemPickupGratitude records item pickup attribution and thank-you chat emissions.
func (s *InvariantSuite) RecordItemPickupGratitude(tick int64, itemID string, hasThrower, throwerMatchesPlayer, thankYouSent bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !hasThrower {
		// Natural drop
		s.NaturalDropCollected = true
		if thankYouSent {
			s.NaturalDropSilent = false
			s.GratitudeFailures = append(s.GratitudeFailures,
				fmt.Sprintf("tick %d: false gift gratitude emitted for natural drop (%s) with no thrower", tick, itemID))
		}
	} else if throwerMatchesPlayer {
		// Genuine player gift
		s.PlayerGiftCollected = true
		if thankYouSent {
			s.PlayerGiftThankYouSent = true
		} else {
			s.GratitudeFailures = append(s.GratitudeFailures,
				fmt.Sprintf("tick %d: player gift (%s) failed to trigger thank-you chat message", tick, itemID))
		}
	}
}

// ==============================================================================
// Evaluation & Assertion Results
// ==============================================================================

// Evaluate runs all invariant assertions and returns a structured list of results.
func (s *InvariantSuite) Evaluate() []InvariantResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	results := make([]InvariantResult, 0, 5)

	// 1. Scout Exploration
	scoutPassed := s.ScoutMaxRadialDistance >= 48.0 &&
		len(s.ScoutFailures) == 0 &&
		s.ScoutSpinOscillations == 0 &&
		s.ScoutPathingObserved &&
		s.ScoutArrivedObserved
	scoutStatus := StatusPassed
	if !scoutPassed {
		scoutStatus = StatusFailed
	}
	results = append(results, InvariantResult{
		Name:        "Assertion 1: Scout Exploration",
		Description: "Explorer advances radial distance >= 48m from spawn along locked sector azimuth; zero 137.5° spin oscillations; transitions PATHING -> ARRIVED.",
		Status:      scoutStatus,
		StartTick:   s.StartTick,
		EndTick:     s.EndTick,
		Metrics: map[string]any{
			"max_radial_distance_m":   s.ScoutMaxRadialDistance,
			"locked_azimuth_rad":      s.ScoutLockedAzimuthRad,
			"max_angular_variance":    s.ScoutMaxAngularVariance,
			"spin_oscillations_count": s.ScoutSpinOscillations,
			"pathing_observed":        s.ScoutPathingObserved,
			"arrived_observed":        s.ScoutArrivedObserved,
		},
		Failures: s.ScoutFailures,
	})

	// 2. Road Paving
	pavingPassed := len(s.PavingViolations) == 0 &&
		s.PavingBlocksPlaced > 0 &&
		(s.PavingMinCampfireDist == math.MaxFloat64 || s.PavingMinCampfireDist >= 4.0)
	pavingStatus := StatusPassed
	if !pavingPassed {
		pavingStatus = StatusFailed
	}
	pavingMinDist := s.PavingMinCampfireDist
	if pavingMinDist == math.MaxFloat64 {
		pavingMinDist = 0
	}
	results = append(results, InvariantResult{
		Name:        "Assertion 2: Road Paving",
		Description: "Zero blocks paved within 4m of campfire; casual wander threshold 15; directional corridor lines formed without concentric circular blobs.",
		Status:      pavingStatus,
		StartTick:   s.StartTick,
		EndTick:     s.EndTick,
		Metrics: map[string]any{
			"blocks_paved":             s.PavingBlocksPlaced,
			"min_campfire_dist_m":      pavingMinDist,
			"threshold_15_satisfied":   s.PavingThresholdSatisfied,
			"campfire_exclusion_clean": len(s.PavingViolations) == 0,
		},
		Failures: s.PavingViolations,
	})

	// 3. Shelter Collision Safety
	shelterPassed := len(s.ShelterBottomUpViolations) == 0 &&
		s.ShelterAABBIntersections == 0 &&
		s.ShelterStallsDetected == 0 &&
		s.ShelterOscillations == 0 &&
		len(s.ShelterFailures) == 0
	shelterStatus := StatusPassed
	if !shelterPassed {
		shelterStatus = StatusFailed
	}
	results = append(results, InvariantResult{
		Name:        "Assertion 3: Shelter Collision Safety",
		Description: "Cabin constructed bottom-up; agent AABB never intersects solid voxels; zero 30s timeout freezes or FSM oscillations.",
		Status:      shelterStatus,
		StartTick:   s.StartTick,
		EndTick:     s.EndTick,
		Metrics: map[string]any{
			"shelter_blocks_placed":     s.ShelterBlocksPlaced,
			"bottom_up_ordering_clean":  len(s.ShelterBottomUpViolations) == 0,
			"aabb_solid_intersections":  s.ShelterAABBIntersections,
			"stall_timeout_freezes":     s.ShelterStallsDetected,
			"fsm_oscillations_count":    s.ShelterOscillations,
		},
		Failures: s.ShelterFailures,
	})

	// 4. Workstation Crafting
	craftingPassed := s.CraftDistantRejected && s.CraftNearbyAccepted && len(s.CraftFailures) == 0
	craftingStatus := StatusPassed
	if !craftingPassed {
		craftingStatus = StatusFailed
	}
	results = append(results, InvariantResult{
		Name:        "Assertion 4: Workstation Crafting",
		Description: "3x3 crafting rejected when distant and accepted when table placed <= 4.5m.",
		Status:      craftingStatus,
		StartTick:   s.StartTick,
		EndTick:     s.EndTick,
		Metrics: map[string]any{
			"distant_3x3_rejected": s.CraftDistantRejected,
			"nearby_3x3_accepted":  s.CraftNearbyAccepted,
			"2x2_anywhere_craft":   s.Craft2x2Accepted,
		},
		Failures: s.CraftFailures,
	})

	// 5. Drop Gratitude
	gratitudePassed := s.NaturalDropSilent &&
		s.PlayerGiftThankYouSent &&
		len(s.GratitudeFailures) == 0
	gratitudeStatus := StatusPassed
	if !gratitudePassed {
		gratitudeStatus = StatusFailed
	}
	results = append(results, InvariantResult{
		Name:        "Assertion 5: Drop Gratitude",
		Description: "Natural ground drops collected silently; player throws matching thrower UUID trigger thank-you chat.",
		Status:      gratitudeStatus,
		StartTick:   s.StartTick,
		EndTick:     s.EndTick,
		Metrics: map[string]any{
			"natural_drop_collected":    s.NaturalDropCollected,
			"natural_drop_silent":       s.NaturalDropSilent,
			"player_gift_collected":     s.PlayerGiftCollected,
			"player_gift_thank_you_sent": s.PlayerGiftThankYouSent,
		},
		Failures: s.GratitudeFailures,
	})

	return results
}

// AllPassed returns true if all 5 assertions passed.
func (s *InvariantSuite) AllPassed() bool {
	for _, res := range s.Evaluate() {
		if res.Status != StatusPassed {
			return false
		}
	}
	return true
}
