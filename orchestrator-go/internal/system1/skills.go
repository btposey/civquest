package system1

import (
	"sync"

	"github.com/btposey/civquest/orchestrator-go/internal/db"
	civquestv1 "github.com/btposey/civquest/orchestrator-go/internal/proto/v1"
	"github.com/btposey/civquest/orchestrator-go/internal/xdg"
)

// EvaluatorContext provides afferent sensory, state, and civic data for evaluators.
type EvaluatorContext struct {
	AgentID       string
	Frame         *civquestv1.TelemetryFrame
	Config        *xdg.AgentIdentityConfig
	State         StateView
	CivicDB       *db.CivicRegistryDB
	SafeNodes     []db.MapNode
	PavingTracker *RoadPavingTracker
}

// newEvaluatorContext builds the read-only evaluator view of an FSMContext.
// A nil *AgentState is kept as a nil interface so `ctx.State != nil` guards stay accurate.
func newEvaluatorContext(ctx *FSMContext) *EvaluatorContext {
	ec := &EvaluatorContext{
		AgentID:       ctx.AgentID,
		Frame:         ctx.Frame,
		Config:        ctx.Config,
		CivicDB:       ctx.CivicDB,
		SafeNodes:     ctx.SafeNodes,
		PavingTracker: ctx.PavingTracker,
	}
	if ctx.State != nil {
		ec.State = ctx.State
	}
	return ec
}

// Evaluator defines a modular sensory and environmental evaluator producing facts.
type Evaluator interface {
	Name() string
	Category() string
	Evaluate(ctx *EvaluatorContext, graph *FactGraph) error
}

// SkillContext provides operational context for skill execution.
type SkillContext struct {
	AgentID       string
	Frame         *civquestv1.TelemetryFrame
	Config        *xdg.AgentIdentityConfig
	State         *AgentState
	CivicDB       *db.CivicRegistryDB
	SafeNodes     []db.MapNode
	PavingTracker *RoadPavingTracker
	Registry      *EngineRegistry

	// Shelter is the running Shelter frame's Local, bound only while that frame steps (nil otherwise).
	Shelter *shelterLocal
	// Forage is the running Foraging frame's Local, bound only while that frame steps (nil otherwise).
	Forage *forageLocal
	// Walk is the running bootstrap/exploration/shelter frame's walk cursor, bound only while it steps (nil otherwise).
	Walk *walkLocal
	// Directive is the running strategic_directive frame's Local, bound only while it steps (nil otherwise).
	Directive *directiveLocal
	// Construction is the driving activity's blueprint progress Local (nil: start at block 0, untracked).
	Construction *ConstructionLocal
}

// Skill defines a modular behavior skill activated by preconditions on the FactGraph.
type Skill interface {
	Name() string
	Category() string
	Preconditions(facts *FactGraph) bool
	Interruptible() bool
	Execute(ctx *SkillContext, facts *FactGraph) ([]*civquestv1.AgentIntent, error)
	OnComplete(ctx *SkillContext, facts *FactGraph)
}

// EngineRegistry manages modular evaluators and skills with thread-safe runtime registration.
type EngineRegistry struct {
	mu            sync.RWMutex
	evaluators    map[string]Evaluator
	skills        map[string]Skill
	evalOrder     []string
	skillOrder    []string
	evalSnapshot  []Evaluator
	skillSnapshot []Skill
}

// NewEngineRegistry creates an empty EngineRegistry.
func NewEngineRegistry() *EngineRegistry {
	return &EngineRegistry{
		evaluators: make(map[string]Evaluator),
		skills:     make(map[string]Skill),
	}
}

// RegisterEvaluator registers an Evaluator into the registry.
func (r *EngineRegistry) RegisterEvaluator(eval Evaluator) {
	if r == nil || eval == nil || eval.Name() == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	name := eval.Name()
	if _, exists := r.evaluators[name]; !exists {
		r.evalOrder = append(r.evalOrder, name)
	}
	r.evaluators[name] = eval
	r.rebuildEvalSnapshotLocked()
}

// RegisterSkill registers a Skill into the registry.
func (r *EngineRegistry) RegisterSkill(skill Skill) {
	if r == nil || skill == nil || skill.Name() == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	name := skill.Name()
	if _, exists := r.skills[name]; !exists {
		r.skillOrder = append(r.skillOrder, name)
	}
	r.skills[name] = skill
	r.rebuildSkillSnapshotLocked()
}

func (r *EngineRegistry) rebuildEvalSnapshotLocked() {
	snap := make([]Evaluator, 0, len(r.evalOrder))
	for _, name := range r.evalOrder {
		if e, ok := r.evaluators[name]; ok && e != nil {
			snap = append(snap, e)
		}
	}
	r.evalSnapshot = snap
}

func (r *EngineRegistry) rebuildSkillSnapshotLocked() {
	snap := make([]Skill, 0, len(r.skillOrder))
	for _, name := range r.skillOrder {
		if s, ok := r.skills[name]; ok && s != nil {
			snap = append(snap, s)
		}
	}
	r.skillSnapshot = snap
}

// GetEvaluator retrieves a registered Evaluator by name.
func (r *EngineRegistry) GetEvaluator(name string) (Evaluator, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.evaluators[name]
	return e, ok
}

// GetSkill retrieves a registered Skill by name.
func (r *EngineRegistry) GetSkill(name string) (Skill, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.skills[name]
	return s, ok
}

// EvaluateAll executes all registered evaluators against the EvaluatorContext and returns a populated FactGraph.
func (r *EngineRegistry) EvaluateAll(ctx *EvaluatorContext) *FactGraph {
	graph := NewFactGraph()
	if r == nil {
		return graph
	}

	r.mu.RLock()
	evals := r.evalSnapshot
	r.mu.RUnlock()

	for _, eval := range evals {
		if eval != nil {
			_ = eval.Evaluate(ctx, graph)
		}
	}
	graph.Freeze()
	return graph
}

func matchesCategory(skillCat string, categories []string) bool {
	if len(categories) == 0 {
		return true
	}
	for _, c := range categories {
		if skillCat == c {
			return true
		}
	}
	return false
}

// SelectAndExecuteSkill inspects registered skills in registration order, checks preconditions,
// and executes the first skill whose preconditions are satisfied.
// If categories are provided, only skills matching one of the categories will be considered.
func (r *EngineRegistry) SelectAndExecuteSkill(ctx *SkillContext, facts *FactGraph, categories ...string) ([]*civquestv1.AgentIntent, string, bool) {
	if r == nil || facts == nil {
		return nil, "", false
	}

	r.mu.RLock()
	skills := r.skillSnapshot
	r.mu.RUnlock()

	for _, skill := range skills {
		if skill == nil {
			continue
		}
		if !matchesCategory(skill.Category(), categories) {
			continue
		}
		if skill.Preconditions(facts) {
			intents, err := skill.Execute(ctx, facts)
			if err != nil || len(intents) == 0 {
				continue
			}
			return intents, skill.Name(), true
		}
	}
	return nil, "", false
}

// Reset clears all registered evaluators and skills.
func (r *EngineRegistry) Reset() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evaluators = make(map[string]Evaluator)
	r.skills = make(map[string]Skill)
	r.evalOrder = nil
	r.skillOrder = nil
	r.evalSnapshot = nil
	r.skillSnapshot = nil
}
