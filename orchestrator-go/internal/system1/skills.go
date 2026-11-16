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
	State         *AgentState
	CivicDB       *db.CivicRegistryDB
	SafeNodes     []db.MapNode
	PavingTracker *RoadPavingTracker
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
	mu         sync.RWMutex
	evaluators map[string]Evaluator
	skills     map[string]Skill
	evalOrder  []string
	skillOrder []string
}

// NewEngineRegistry creates an empty EngineRegistry.
func NewEngineRegistry() *EngineRegistry {
	return &EngineRegistry{
		evaluators: make(map[string]Evaluator),
		skills:     make(map[string]Skill),
	}
}

// DefaultRegistry is the global singleton EngineRegistry instance.
var DefaultRegistry = NewEngineRegistry()

// RegisterEvaluator registers an Evaluator into the registry.
func (r *EngineRegistry) RegisterEvaluator(eval Evaluator) {
	if eval == nil || eval.Name() == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	name := eval.Name()
	if _, exists := r.evaluators[name]; !exists {
		r.evalOrder = append(r.evalOrder, name)
	}
	r.evaluators[name] = eval
}

// RegisterSkill registers a Skill into the registry.
func (r *EngineRegistry) RegisterSkill(skill Skill) {
	if skill == nil || skill.Name() == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	name := skill.Name()
	if _, exists := r.skills[name]; !exists {
		r.skillOrder = append(r.skillOrder, name)
	}
	r.skills[name] = skill
}

// GetEvaluator retrieves a registered Evaluator by name.
func (r *EngineRegistry) GetEvaluator(name string) (Evaluator, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.evaluators[name]
	return e, ok
}

// GetSkill retrieves a registered Skill by name.
func (r *EngineRegistry) GetSkill(name string) (Skill, bool) {
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
	order := make([]string, len(r.evalOrder))
	copy(order, r.evalOrder)
	evals := make(map[string]Evaluator, len(r.evaluators))
	for k, v := range r.evaluators {
		evals[k] = v
	}
	r.mu.RUnlock()

	for _, name := range order {
		if eval, ok := evals[name]; ok && eval != nil {
			_ = eval.Evaluate(ctx, graph)
		}
	}
	return graph
}

// SelectAndExecuteSkill inspects registered skills in registration order, checks preconditions,
// and executes the first skill whose preconditions are satisfied.
// If categories are provided, only skills matching one of the categories will be considered.
func (r *EngineRegistry) SelectAndExecuteSkill(ctx *SkillContext, facts *FactGraph, categories ...string) ([]*civquestv1.AgentIntent, string, bool) {
	if r == nil || facts == nil {
		return nil, "", false
	}

	r.mu.RLock()
	order := make([]string, len(r.skillOrder))
	copy(order, r.skillOrder)
	skills := make(map[string]Skill, len(r.skills))
	for k, v := range r.skills {
		skills[k] = v
	}
	r.mu.RUnlock()

	catFilter := make(map[string]bool, len(categories))
	for _, c := range categories {
		catFilter[c] = true
	}

	for _, name := range order {
		skill, ok := skills[name]
		if !ok || skill == nil {
			continue
		}
		if len(catFilter) > 0 && !catFilter[skill.Category()] {
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
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evaluators = make(map[string]Evaluator)
	r.skills = make(map[string]Skill)
	r.evalOrder = nil
	r.skillOrder = nil
}

// Package-level delegations to DefaultRegistry.

// RegisterEvaluator registers an evaluator on DefaultRegistry.
func RegisterEvaluator(eval Evaluator) {
	DefaultRegistry.RegisterEvaluator(eval)
}

// RegisterSkill registers a skill on DefaultRegistry.
func RegisterSkill(skill Skill) {
	DefaultRegistry.RegisterSkill(skill)
}

// EvaluateAll executes all registered evaluators on DefaultRegistry.
func EvaluateAll(ctx *EvaluatorContext) *FactGraph {
	return DefaultRegistry.EvaluateAll(ctx)
}

// SelectAndExecuteSkill evaluates skills on DefaultRegistry.
func SelectAndExecuteSkill(ctx *SkillContext, facts *FactGraph, categories ...string) ([]*civquestv1.AgentIntent, string, bool) {
	return DefaultRegistry.SelectAndExecuteSkill(ctx, facts, categories...)
}

// GetEvaluator retrieves an evaluator by name from DefaultRegistry.
func GetEvaluator(name string) (Evaluator, bool) {
	return DefaultRegistry.GetEvaluator(name)
}

// GetSkill retrieves a skill by name from DefaultRegistry.
func GetSkill(name string) (Skill, bool) {
	return DefaultRegistry.GetSkill(name)
}
