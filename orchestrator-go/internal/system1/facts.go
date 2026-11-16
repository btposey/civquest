package system1

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// FactValue holds a typed value for a fact entry in the FactGraph.
type FactValue struct {
	Bool   bool                   `json:"bool,omitempty"`
	Int    int                    `json:"int,omitempty"`
	Float  float64                `json:"float,omitempty"`
	String string                 `json:"string,omitempty"`
	Data   map[string]interface{} `json:"data,omitempty"`
}

// Fact represents a single verified environmental or operational fact in the FactGraph.
type Fact struct {
	Key         string    `json:"key"`
	Category    string    `json:"category"`
	Value       FactValue `json:"value"`
	Confidence  float64   `json:"confidence"`
	Evidence    string    `json:"evidence"`
	ExpiresTick int64     `json:"expires_tick,omitempty"`
}

// FactGraph provides a thread-safe blackboard of verified environmental facts.
type FactGraph struct {
	mu    sync.RWMutex
	facts map[string]Fact
}

// NewFactGraph initializes an empty FactGraph.
func NewFactGraph() *FactGraph {
	return &FactGraph{
		facts: make(map[string]Fact),
	}
}

// Set stores a Fact directly in the graph.
func (g *FactGraph) Set(fact Fact) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.facts[fact.Key] = fact
}

// SetBool stores a boolean fact with 1.0 confidence.
func (g *FactGraph) SetBool(key, category string, val bool, evidence string) {
	g.Set(Fact{
		Key:        key,
		Category:   category,
		Value:      FactValue{Bool: val},
		Confidence: 1.0,
		Evidence:   evidence,
	})
}

// SetInt stores an integer fact with 1.0 confidence.
func (g *FactGraph) SetInt(key, category string, val int, evidence string) {
	g.Set(Fact{
		Key:        key,
		Category:   category,
		Value:      FactValue{Int: val},
		Confidence: 1.0,
		Evidence:   evidence,
	})
}

// SetFloat stores a float fact with 1.0 confidence.
func (g *FactGraph) SetFloat(key, category string, val float64, evidence string) {
	g.Set(Fact{
		Key:        key,
		Category:   category,
		Value:      FactValue{Float: val},
		Confidence: 1.0,
		Evidence:   evidence,
	})
}

// SetString stores a string fact with 1.0 confidence.
func (g *FactGraph) SetString(key, category string, val string, evidence string) {
	g.Set(Fact{
		Key:        key,
		Category:   category,
		Value:      FactValue{String: val},
		Confidence: 1.0,
		Evidence:   evidence,
	})
}

// Get retrieves a fact by its key.
func (g *FactGraph) Get(key string) (Fact, bool) {
	if g == nil {
		return Fact{}, false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	f, ok := g.facts[key]
	return f, ok
}

// GetBool returns the boolean value of a fact, or false if not found.
func (g *FactGraph) GetBool(key string) bool {
	if g == nil {
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	if f, ok := g.facts[key]; ok {
		return f.Value.Bool
	}
	return false
}

// GetInt returns the integer value of a fact, and whether it was found.
func (g *FactGraph) GetInt(key string) (int, bool) {
	if g == nil {
		return 0, false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	if f, ok := g.facts[key]; ok {
		return f.Value.Int, true
	}
	return 0, false
}

// GetFloat returns the float64 value of a fact, and whether it was found.
func (g *FactGraph) GetFloat(key string) (float64, bool) {
	if g == nil {
		return 0.0, false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	if f, ok := g.facts[key]; ok {
		return f.Value.Float, true
	}
	return 0.0, false
}

// GetString returns the string value of a fact, or empty string if not found.
func (g *FactGraph) GetString(key string) string {
	if g == nil {
		return ""
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	if f, ok := g.facts[key]; ok {
		return f.Value.String
	}
	return ""
}

// AllFacts returns a shallow copy of all facts currently in the graph.
func (g *FactGraph) AllFacts() map[string]Fact {
	if g == nil {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make(map[string]Fact, len(g.facts))
	for k, v := range g.facts {
		out[k] = v
	}
	return out
}

// Clone returns an independent deep copy of the FactGraph.
func (g *FactGraph) Clone() *FactGraph {
	if g == nil {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	clone := NewFactGraph()
	for k, v := range g.facts {
		clone.facts[k] = v
	}
	return clone
}

// PruneExpired removes any facts whose ExpiresTick is non-zero and <= currentTick.
func (g *FactGraph) PruneExpired(currentTick int64) int {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	pruned := 0
	for k, f := range g.facts {
		if f.ExpiresTick > 0 && currentTick >= f.ExpiresTick {
			delete(g.facts, k)
			pruned++
		}
	}
	return pruned
}

// ToPromptContext formats the facts into clean Markdown bullet points grouped by Category,
// suitable for injection into System 2 LLM prompts.
// Example:
//
//	### Verified Environmental Facts
//	- **Armory**: Deficit (need 12 arrows)
//	- **Security**: Threat nominal
func (g *FactGraph) ToPromptContext() string {
	if g == nil {
		return ""
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	if len(g.facts) == 0 {
		return ""
	}

	// Group facts by Category
	categories := make(map[string][]Fact)
	for _, fact := range g.facts {
		cat := strings.TrimSpace(fact.Category)
		if cat == "" {
			cat = "General"
		}
		categories[cat] = append(categories[cat], fact)
	}

	catNames := make([]string, 0, len(categories))
	for c := range categories {
		catNames = append(catNames, c)
	}
	sort.Strings(catNames)

	var sb strings.Builder
	sb.WriteString("### Verified Environmental Facts\n")
	for _, cat := range catNames {
		facts := categories[cat]
		sort.Slice(facts, func(i, j int) bool {
			return facts[i].Key < facts[j].Key
		})
		for _, f := range facts {
			content := strings.TrimSpace(f.Evidence)
			if content == "" {
				switch {
				case f.Value.String != "":
					content = fmt.Sprintf("%s: %s", f.Key, f.Value.String)
				case f.Value.Int != 0:
					content = fmt.Sprintf("%s: %d", f.Key, f.Value.Int)
				case f.Value.Float != 0:
					content = fmt.Sprintf("%s: %.2f", f.Key, f.Value.Float)
				case f.Value.Bool:
					content = fmt.Sprintf("%s: true", f.Key)
				default:
					content = fmt.Sprintf("%s: false", f.Key)
				}
			}
			sb.WriteString(fmt.Sprintf("- **%s**: %s\n", cat, content))
		}
	}
	return sb.String()
}
