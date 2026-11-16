package eval

import (
	"path/filepath"
	"testing"

	"github.com/btposey/civquest/orchestrator-go/internal/blueprints"
	"github.com/btposey/civquest/orchestrator-go/internal/db"
	"github.com/btposey/civquest/orchestrator-go/internal/system1"
)

func TestEval_BlueprintEvaluator_Integration(t *testing.T) {
	eval, ok := system1.DefaultRegistry.GetEvaluator("blueprint")
	if !ok || eval == nil {
		t.Fatalf("expected 'blueprint' evaluator in DefaultRegistry")
	}

	tmpDir := t.TempDir()
	civicDB, err := db.OpenCivicRegistryDB(filepath.Join(tmpDir, "eval_civic.db"), "clan_test")
	if err != nil {
		t.Fatalf("failed creating civic db: %v", err)
	}
	defer civicDB.Close()

	bp := &blueprints.Blueprint{
		ID:      "eval_cabin",
		Name:    "Eval Cabin",
		Purpose: blueprints.PurposeShelter,
		BOM: map[string]int{
			"#minecraft:planks": 5,
		},
	}

	bpEval := NewBlueprintEvaluator(bp)
	fg := system1.NewFactGraph()

	evalCtx := &system1.EvaluatorContext{
		CivicDB: civicDB,
	}

	if err := bpEval.Evaluate(evalCtx, fg); err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}

	// With 0 stock, should be unaffordable
	if fg.GetBool("blueprint.affordable:eval_cabin") {
		t.Errorf("expected eval_cabin to be unaffordable with 0 stock")
	}
}
