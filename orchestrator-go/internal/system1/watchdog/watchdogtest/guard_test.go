package watchdogtest

import (
	"go/build"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Every package whose tests can run an agent fails on a watchdog firing: none may be added
// without its TestMain.
func TestEveryPackageThatRunsAgentsFailsOnAFiring(t *testing.T) {
	const module = "github.com/btposey/civquest/orchestrator-go"
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	var runners []string
	for _, p := range []string{"internal/system1", "internal/agent", "internal/eval", "internal/playtest"} {
		runners = append(runners, module+"/"+p)
	}
	checked := 0
	err = filepath.WalkDir(root, func(dir string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		ctx := build.Default
		ctx.BuildTags = []string{"livellm", "integration"}
		pkg, err := ctx.ImportDir(dir, 0)
		if err != nil || len(pkg.TestGoFiles)+len(pkg.XTestGoFiles) == 0 {
			return nil // no Go package or no tests here
		}
		self := module + strings.TrimPrefix(dir, root)
		imports := slices.Concat([]string{self}, pkg.Imports, pkg.TestImports, pkg.XTestImports)
		if !slices.ContainsFunc(imports, func(i string) bool { return slices.Contains(runners, i) }) {
			return nil
		}
		checked++
		for _, f := range slices.Concat(pkg.TestGoFiles, pkg.XTestGoFiles) {
			src, err := os.ReadFile(filepath.Join(dir, f))
			if err != nil {
				return err
			}
			if strings.Contains(string(src), "watchdogtest.Main(m)") {
				return nil
			}
		}
		t.Errorf("%s: add a TestMain that calls watchdogtest.Main(m)", self)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 8 {
		t.Fatalf("only %d packages checked: the search no longer finds them", checked)
	}
}
