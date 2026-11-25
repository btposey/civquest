package system1

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestFactGraphFrozenRejectsWrites(t *testing.T) {
	g := NewFactGraph()
	g.SetBool("a", "t", true, "")
	g.Freeze()
	if !g.Frozen() {
		t.Fatal("expected frozen")
	}
	if !g.GetBool("a") {
		t.Fatal("reads must still work after Freeze")
	}
	for name, write := range map[string]func(){
		"SetBool":   func() { g.SetBool("b", "t", true, "") },
		"SetInt":    func() { g.SetInt("b", "t", 1, "") },
		"SetFloat":  func() { g.SetFloat("b", "t", 1, "") },
		"SetString": func() { g.SetString("b", "t", "x", "") },
		"Set":       func() { g.Set(Fact{Key: "b"}) },
	} {
		func() {
			defer func() {
				if r := recover(); r != ErrFactGraphFrozen {
					t.Errorf("%s: expected ErrFactGraphFrozen panic, got %v", name, r)
				}
			}()
			write()
		}()
	}
	if _, ok := g.Get("b"); ok {
		t.Fatal("frozen graph was mutated")
	}
}

func TestEvaluateAllReturnsFrozenGraph(t *testing.T) {
	g := NewDefaultEngineRegistry().EvaluateAll(&EvaluatorContext{})
	if !g.Frozen() {
		t.Fatal("EvaluateAll must return a frozen graph")
	}
}

// TestNoSkillWritesFacts is a source-level guard: Skill implementations (Execute/OnComplete
// and their helpers in *_skill(s).go) must never call FactGraph Set*. Facts come from Evaluators.
func TestNoSkillWritesFacts(t *testing.T) {
	re := regexp.MustCompile(`\b(facts|ctx\.Facts)\.(Set|SetBool|SetInt|SetFloat|SetString)\(`)
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		isSkillFile := strings.HasSuffix(f, "_skill.go") || strings.HasSuffix(f, "_skills.go") ||
			f == "foraging.go" || f == "activity_arbiter.go"
		if !isSkillFile {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if re.MatchString(line) {
				t.Errorf("%s:%d: skill writes to FactGraph: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
}
