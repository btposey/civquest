package htpda

import (
	"fmt"
	"sort"
)

// Registry maps Kind -> Activity. Iteration order is sorted by Kind so that
// arbitration is deterministic regardless of registration order.
type Registry struct {
	byKind map[Kind]Activity
	order  []Kind
}

// NewRegistry builds a registry; duplicate or empty kinds are an error.
func NewRegistry(acts ...Activity) (*Registry, error) {
	r := &Registry{byKind: make(map[Kind]Activity, len(acts))}
	for _, a := range acts {
		if a == nil {
			return nil, fmt.Errorf("htpda: nil activity")
		}
		k := a.Kind()
		if k == "" {
			return nil, fmt.Errorf("htpda: activity %T has empty kind", a)
		}
		if _, dup := r.byKind[k]; dup {
			return nil, fmt.Errorf("htpda: duplicate activity kind %q", k)
		}
		r.byKind[k] = a
		r.order = append(r.order, k)
	}
	sort.Slice(r.order, func(i, j int) bool { return r.order[i] < r.order[j] })
	return r, nil
}

// Get returns the activity for kind.
func (r *Registry) Get(k Kind) (Activity, bool) {
	if r == nil {
		return nil, false
	}
	a, ok := r.byKind[k]
	return a, ok
}

// Kinds returns registered kinds in deterministic (sorted) order.
func (r *Registry) Kinds() []Kind {
	if r == nil {
		return nil
	}
	return append([]Kind(nil), r.order...)
}

// AnyStage is the fallback PriorityTable row consulted when a stage row has
// no entry for a kind.
const AnyStage Stage = "*"

// PriorityTable is configuration data: weight[stage][kind]. A kind with no
// positive weight in the stage row or the AnyStage row cannot win arbitration.
type PriorityTable map[Stage]map[Kind]float64

// Weight looks up the weight for kind in stage, falling back to AnyStage.
func (p PriorityTable) Weight(stage Stage, kind Kind) (float64, bool) {
	if row, ok := p[stage]; ok {
		if w, ok := row[kind]; ok {
			return w, true
		}
	}
	if row, ok := p[AnyStage]; ok {
		if w, ok := row[kind]; ok {
			return w, true
		}
	}
	return 0, false
}
