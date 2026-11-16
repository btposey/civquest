package system1

import (
	"strings"
	"sync"
	"testing"
)

func TestFactGraph_SetGet(t *testing.T) {
	fg := NewFactGraph()

	// 1. Bool
	if fg.GetBool("missing.bool") != false {
		t.Errorf("expected missing bool to return false")
	}
	fg.SetBool("threat.active", "Security", true, "Hostile mob detected")
	if !fg.GetBool("threat.active") {
		t.Errorf("expected threat.active to be true")
	}
	fact, ok := fg.Get("threat.active")
	if !ok || fact.Value.Bool != true || fact.Evidence != "Hostile mob detected" {
		t.Errorf("unexpected fact retrieved: %+v", fact)
	}

	// 2. Int
	val, ok := fg.GetInt("missing.int")
	if ok || val != 0 {
		t.Errorf("expected missing int to return (0, false)")
	}
	fg.SetInt("armory.population", "Armory", 5, "Settlement population: 5")
	val, ok = fg.GetInt("armory.population")
	if !ok || val != 5 {
		t.Errorf("expected armory.population to return (5, true), got (%d, %t)", val, ok)
	}

	// 3. Float
	fval, ok := fg.GetFloat("missing.float")
	if ok || fval != 0.0 {
		t.Errorf("expected missing float to return (0.0, false)")
	}
	fg.SetFloat("vital.temperature", "Environment", 36.6, "Normal temperature")
	fval, ok = fg.GetFloat("vital.temperature")
	if !ok || fval != 36.6 {
		t.Errorf("expected vital.temperature to return (36.6, true), got (%f, %t)", fval, ok)
	}

	// 4. String
	if fg.GetString("missing.string") != "" {
		t.Errorf("expected missing string to return empty string")
	}
	fg.SetString("armory.deficit_category", "Armory", "ARROWS", "Need arrows")
	if fg.GetString("armory.deficit_category") != "ARROWS" {
		t.Errorf("expected armory.deficit_category to be ARROWS, got %q", fg.GetString("armory.deficit_category"))
	}
}

func TestFactGraph_CloneAndAllFacts(t *testing.T) {
	fg := NewFactGraph()
	fg.SetBool("a", "Cat1", true, "ev1")
	fg.SetInt("b", "Cat2", 42, "ev2")

	all := fg.AllFacts()
	if len(all) != 2 {
		t.Fatalf("expected 2 facts, got %d", len(all))
	}
	all["c"] = Fact{Key: "c"}
	if _, ok := fg.Get("c"); ok {
		t.Errorf("modifying returned map affected original FactGraph")
	}

	clone := fg.Clone()
	if !clone.GetBool("a") || fg.GetString("missing") != "" {
		t.Errorf("clone facts mismatch")
	}
	clone.SetBool("d", "Cat3", true, "ev3")
	if _, ok := fg.Get("d"); ok {
		t.Errorf("modifying clone affected original FactGraph")
	}
}

func TestFactGraph_PruneExpired(t *testing.T) {
	fg := NewFactGraph()
	fg.Set(Fact{Key: "temp1", Category: "Temp", Value: FactValue{Bool: true}, ExpiresTick: 100})
	fg.Set(Fact{Key: "temp2", Category: "Temp", Value: FactValue{Bool: true}, ExpiresTick: 200})
	fg.Set(Fact{Key: "perm", Category: "Perm", Value: FactValue{Bool: true}, ExpiresTick: 0})

	pruned := fg.PruneExpired(150)
	if pruned != 1 {
		t.Errorf("expected 1 pruned fact, got %d", pruned)
	}
	if _, ok := fg.Get("temp1"); ok {
		t.Errorf("expected temp1 to be pruned")
	}
	if _, ok := fg.Get("temp2"); !ok {
		t.Errorf("expected temp2 to remain")
	}
	if _, ok := fg.Get("perm"); !ok {
		t.Errorf("expected perm to remain")
	}
}

func TestFactGraph_ToPromptContext(t *testing.T) {
	// Empty graph
	fg := NewFactGraph()
	if fg.ToPromptContext() != "" {
		t.Errorf("expected empty string for empty FactGraph, got %q", fg.ToPromptContext())
	}

	// Graph with facts
	fg.SetBool("security.threat", "Security", false, "Threat nominal")
	fg.SetBool("armory.sufficient", "Armory", false, "Deficit (need 12 arrows)")

	promptCtx := fg.ToPromptContext()
	expectedHeader := "### Verified Environmental Facts\n"
	if !strings.HasPrefix(promptCtx, expectedHeader) {
		t.Fatalf("expected prompt context to start with header, got:\n%s", promptCtx)
	}

	expectedArmory := "- **Armory**: Deficit (need 12 arrows)\n"
	expectedSecurity := "- **Security**: Threat nominal\n"

	if !strings.Contains(promptCtx, expectedArmory) {
		t.Errorf("missing expected Armory line in prompt context:\n%s", promptCtx)
	}
	if !strings.Contains(promptCtx, expectedSecurity) {
		t.Errorf("missing expected Security line in prompt context:\n%s", promptCtx)
	}

	// Check alphabetical order of categories: Armory must appear before Security
	armoryIdx := strings.Index(promptCtx, expectedArmory)
	securityIdx := strings.Index(promptCtx, expectedSecurity)
	if armoryIdx > securityIdx {
		t.Errorf("expected Armory to precede Security in sorted category output")
	}

	// Fallback formatting without evidence
	fgNoEv := NewFactGraph()
	fgNoEv.SetInt("counter", "Stats", 10, "")
	fgNoEv.SetString("mode", "Stats", "active", "")
	fgNoEv.SetBool("enabled", "Stats", true, "")
	noEvPrompt := fgNoEv.ToPromptContext()
	if !strings.Contains(noEvPrompt, "- **Stats**: counter: 10\n") {
		t.Errorf("expected formatted int fallback in:\n%s", noEvPrompt)
	}
	if !strings.Contains(noEvPrompt, "- **Stats**: mode: active\n") {
		t.Errorf("expected formatted string fallback in:\n%s", noEvPrompt)
	}
	if !strings.Contains(noEvPrompt, "- **Stats**: enabled: true\n") {
		t.Errorf("expected formatted bool fallback in:\n%s", noEvPrompt)
	}
}

func TestFactGraph_ConcurrentAccess(t *testing.T) {
	fg := NewFactGraph()
	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func(idx int) {
			defer wg.Done()
			fg.SetInt("key", "Cat", idx, "ev")
			fg.SetBool("active", "Cat", idx%2 == 0, "ev")
		}(i)
		go func() {
			defer wg.Done()
			_ = fg.GetBool("active")
			_, _ = fg.GetInt("key")
			_ = fg.ToPromptContext()
			_ = fg.Clone()
		}()
	}

	wg.Wait()
}
