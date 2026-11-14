package blueprints

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
)

// Project lifecycle state constants.
const (
	StateSurveying   = "SURVEYING"
	StateAwaitingBOM = "AWAITING_BOM"
	StateClearing    = "CLEARING"
	StateBuilding    = "BUILDING"
	StateCompleted   = "COMPLETED"
	StateHalted      = "HALTED"
	StateAbandoned   = "ABANDONED"
)

var (
	ErrInsufficientMaterials  = errors.New("insufficient granary materials to transition to BUILDING")
	ErrInvalidStateTransition = errors.New("invalid construction project state transition")
	ErrProjectNotFound        = errors.New("construction project not found")
	ErrBlueprintRequired      = errors.New("blueprint required to verify BOM requirements")
)

// RegisterProjectRequest specifies initial parameters when chartering a new construction project.
type RegisterProjectRequest struct {
	ProjectID    string
	BlueprintID  string
	AnchorX      int
	AnchorY      int
	AnchorZ      int
	Quadrant     string // 'NW', 'NE', 'SW', 'SE', 'ALL'
	InitialState string // defaults to StateSurveying
	WorldTick    int64
}

// ProjectTracker manages the lifecycle state transitions and granary material reservations
// for active and planned architectural construction projects.
type ProjectTracker struct {
	civicDB *db.CivicRegistryDB
	mu      sync.RWMutex
}

// NewProjectTracker initializes a new construction project tracker backed by a CivicRegistryDB.
func NewProjectTracker(civicDB *db.CivicRegistryDB) *ProjectTracker {
	return &ProjectTracker{
		civicDB: civicDB,
	}
}

// RegisterProject registers a new project in the civic registry, starting in SURVEYING state.
func (pt *ProjectTracker) RegisterProject(ctx context.Context, req RegisterProjectRequest) (*db.ConstructionProject, error) {
	if pt.civicDB == nil {
		return nil, errors.New("civic registry database is nil")
	}
	if req.ProjectID == "" {
		return nil, errors.New("project_id cannot be empty")
	}
	if req.BlueprintID == "" {
		return nil, errors.New("blueprint_id cannot be empty")
	}
	if req.Quadrant == "" {
		req.Quadrant = db.QuadrantAll
	}
	if req.InitialState == "" {
		req.InitialState = StateSurveying
	}

	p := &db.ConstructionProject{
		ProjectID:             req.ProjectID,
		BlueprintID:           req.BlueprintID,
		Status:                req.InitialState,
		AnchorX:               req.AnchorX,
		AnchorY:               req.AnchorY,
		AnchorZ:               req.AnchorZ,
		Quadrant:              req.Quadrant,
		ProgressPercent:       0.0,
		MaterialsReservedJSON: "{}",
		CreatedTick:           req.WorldTick,
		UpdatedTick:           req.WorldTick,
	}

	if err := pt.civicDB.CreateProjectContext(ctx, p); err != nil {
		return nil, fmt.Errorf("registering project in civic registry: %w", err)
	}

	return p, nil
}

// GetProject retrieves project details by projectID.
func (pt *ProjectTracker) GetProject(ctx context.Context, projectID string) (*db.ConstructionProject, error) {
	if pt.civicDB == nil {
		return nil, errors.New("civic registry database is nil")
	}
	p, err := pt.civicDB.GetProjectContext(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, ErrProjectNotFound
	}
	return p, nil
}

// ListProjects retrieves all construction projects from the civic registry.
func (pt *ProjectTracker) ListProjects(ctx context.Context) ([]*db.ConstructionProject, error) {
	if pt.civicDB == nil {
		return nil, errors.New("civic registry database is nil")
	}
	return pt.civicDB.ListProjectsContext(ctx)
}

// CheckBOM evaluates the project's blueprint materials against current granary inventory.
func (pt *ProjectTracker) CheckBOM(ctx context.Context, bp *Blueprint) (*BOMReport, error) {
	if bp == nil {
		return nil, ErrBlueprintRequired
	}
	return ComputeBlueprintBOMAgainstGranary(ctx, bp, pt.civicDB)
}

// CanTransitionToBuilding determines if all materials exist in granary to enter BUILDING state.
func (pt *ProjectTracker) CanTransitionToBuilding(ctx context.Context, bp *Blueprint) (bool, *BOMReport, error) {
	report, err := pt.CheckBOM(ctx, bp)
	if err != nil {
		return false, nil, err
	}
	return report.IsSatisfied, report, nil
}

// TransitionProject handles state transitions according to ADR-010 construction rules.
// Enforces that transitioning from SURVEYING/AWAITING_BOM/CLEARING to BUILDING requires
// complete required materials in the settlement granary, automatically reserving them upon entry.
func (pt *ProjectTracker) TransitionProject(ctx context.Context, projectID string, targetState string, worldTick int64, bp *Blueprint) (*db.ConstructionProject, error) {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	p, err := pt.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}

	currentState := p.Status
	if currentState == targetState {
		return p, nil // Idempotent
	}

	// Terminal states cannot transition
	if currentState == StateCompleted || currentState == StateAbandoned {
		return nil, fmt.Errorf("%w: cannot transition from terminal state %s to %s",
			ErrInvalidStateTransition, currentState, targetState)
	}

	switch targetState {
	case StateSurveying:
		if currentState != StateHalted {
			return nil, fmt.Errorf("%w: cannot transition from %s to %s", ErrInvalidStateTransition, currentState, targetState)
		}

	case StateAwaitingBOM:
		if currentState != StateSurveying && currentState != StateClearing && currentState != StateHalted {
			return nil, fmt.Errorf("%w: cannot transition from %s to %s", ErrInvalidStateTransition, currentState, targetState)
		}

	case StateClearing:
		if currentState != StateSurveying && currentState != StateAwaitingBOM && currentState != StateHalted {
			return nil, fmt.Errorf("%w: cannot transition from %s to %s", ErrInvalidStateTransition, currentState, targetState)
		}

	case StateBuilding:
		// Transition to BUILDING is valid from SURVEYING, AWAITING_BOM, CLEARING, or HALTED.
		if currentState != StateSurveying && currentState != StateAwaitingBOM && currentState != StateClearing && currentState != StateHalted {
			return nil, fmt.Errorf("%w: cannot transition from %s to %s", ErrInvalidStateTransition, currentState, targetState)
		}

		// Enforce BOM check: materials MUST exist
		if bp == nil {
			return nil, ErrBlueprintRequired
		}

		report, err := pt.CheckBOM(ctx, bp)
		if err != nil {
			return nil, fmt.Errorf("checking BOM against granary: %w", err)
		}

		if !report.IsSatisfied {
			return nil, fmt.Errorf("%w: %d items missing (%+v)",
				ErrInsufficientMaterials, report.TotalMissing, report.Missing)
		}

		// Reserve required materials in granary
		reservedMap := make(map[string]int)
		for item, qty := range report.Required {
			if qty > 0 {
				if err := pt.civicDB.ReserveGranaryStockContext(ctx, item, qty, worldTick); err != nil {
					// Rollback any reservations made so far
					for rolledItem, rolledQty := range reservedMap {
						_ = pt.civicDB.ReleaseGranaryStockContext(ctx, rolledItem, rolledQty, worldTick)
					}
					return nil, fmt.Errorf("reserving granary stock for %s: %w", item, err)
				}
				reservedMap[item] = qty
			}
		}

		reservedJSON, err := json.Marshal(reservedMap)
		if err != nil {
			return nil, fmt.Errorf("marshaling reserved materials: %w", err)
		}

		if err := pt.civicDB.UpdateProjectMaterialsReservedContext(ctx, projectID, string(reservedJSON), worldTick); err != nil {
			return nil, fmt.Errorf("updating project materials reserved: %w", err)
		}

	case StateCompleted:
		if currentState != StateBuilding && currentState != StateHalted {
			return nil, fmt.Errorf("%w: cannot transition to COMPLETED from %s (must be BUILDING)",
				ErrInvalidStateTransition, currentState)
		}

		// Consume reserved materials from granary
		var reservedMap map[string]int
		if p.MaterialsReservedJSON != "" && p.MaterialsReservedJSON != "{}" {
			if err := json.Unmarshal([]byte(p.MaterialsReservedJSON), &reservedMap); err == nil {
				for item, qty := range reservedMap {
					_ = pt.civicDB.ConsumeReservedGranaryStockContext(ctx, item, qty, worldTick)
				}
			}
		}

		// Set progress to 100%
		_ = pt.civicDB.UpdateProjectProgressContext(ctx, projectID, 100.0, worldTick)

	case StateHalted:
		// Any active state can halt

	case StateAbandoned:
		// If materials were reserved, release them back to granary
		var reservedMap map[string]int
		if p.MaterialsReservedJSON != "" && p.MaterialsReservedJSON != "{}" {
			if err := json.Unmarshal([]byte(p.MaterialsReservedJSON), &reservedMap); err == nil {
				for item, qty := range reservedMap {
					_ = pt.civicDB.ReleaseGranaryStockContext(ctx, item, qty, worldTick)
				}
			}
		}
		_ = pt.civicDB.UpdateProjectMaterialsReservedContext(ctx, projectID, "{}", worldTick)

	default:
		return nil, fmt.Errorf("%w: unknown state %q", ErrInvalidStateTransition, targetState)
	}

	// Update status in civic registry
	if err := pt.civicDB.UpdateProjectStatusContext(ctx, projectID, targetState, worldTick); err != nil {
		return nil, fmt.Errorf("updating project status in civic registry: %w", err)
	}

	return pt.GetProject(ctx, projectID)
}

// UpdateProgress updates the construction progress percentage.
func (pt *ProjectTracker) UpdateProgress(ctx context.Context, projectID string, progressPercent float64, worldTick int64) error {
	if pt.civicDB == nil {
		return errors.New("civic registry database is nil")
	}
	return pt.civicDB.UpdateProjectProgressContext(ctx, projectID, progressPercent, worldTick)
}
