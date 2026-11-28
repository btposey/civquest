// Package archtest holds the architecture ratchets of ADR-041 section 5. They read the
// source and fail when it grows in a direction the design is moving away from. Every
// recorded list and ceiling here only moves down: when one fails, change the code, and when
// code is removed, lower the record.
package archtest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
)

const (
	moduleRoot  = "../.."
	system1Path = "github.com/btposey/civquest/orchestrator-go/internal/system1"
)

// allowedFSMFields lists the fields AgentFSM may hold; fields of embedded structs count.
// New per-activity state belongs in the activity's frame Local, and new memory in
// Step.Memory (ADR-036), not here.
var allowedFSMFields = []string{
	"climbMemory.pockets",
	"currentRoutine", "currentStage", "escapeLock", "eventSink", "gatherSkips", "hasOrphanNav",
	"kernel", "lastDiag", "lastFacts", "lastPos", "lastVitalsTick", "managed", "meter", "mu",
	"navTargets", "onRoutineTransition", "onStageTransition", "orphan", "orphanNav",
	"orphanTick", "planning", "previousRoutine", "previousStage", "rcptBatch", "rcptStatus",
	"recoverOutbox", "registry", "rendezvousOutbox", "routineEnteredTick", "scanAt",
	"seenHealth", "seenItems", "seenThreats", "spots", "startedReceipt", "startedTask",
	"stateEnteredTick", "stuckLog", "stuckTicks", "surveyBackoffUntil", "transitions",
	"vitalsEmitted", "walks", "watch", "workBackoffUntil",
}

// allowedTimers lists the suppression timers and job deadlines that already exist. A new one
// is a new private "do not try that again" (ADR-041).
var allowedTimers = []string{
	"internal/system1/expedition_activity.go:surveyBackoff.Until",
	"internal/system1/fsm.go:AgentFSM.gatherSkips",
	"internal/system1/fsm.go:AgentFSM.surveyBackoffUntil",
	"internal/system1/fsm.go:AgentFSM.workBackoffUntil",
	"internal/system1/gather_activity.go:gatherGaveUp.Until",
	"internal/system1/htpda/hierarchy.go:cooled.until",
	"internal/system1/htpda/kernel.go:Options.NoProgressBackoff",
	"internal/system1/plan_activity.go:planLocal.Backoff",
	"internal/system1/plan_memory.go:planBank.Backoff",
	"internal/system1/recover_activity.go:recoverLocal.WaitUntil",
	"internal/system1/state.go:unreachableTargetEntry.blacklistUntil",
	"internal/system1/work_backoff.go:workBackoff.Until",
}

// stateOwnFiles define AgentState; writes made there are its own.
var stateOwnFiles = map[string]bool{
	"internal/system1/state.go":         true,
	"internal/system1/runtime_state.go": true,
}

// maxOutsideStateMutations is the number of places outside stateOwnFiles that call a
// mutating AgentState method or write into an AgentState field. Only ever lower it.
const maxOutsideStateMutations = 371

func TestAgentFSMFieldsAreAllowed(t *testing.T) {
	pkg := load(t).pkg(t, system1Path)
	obj := pkg.types.Scope().Lookup("AgentFSM")
	if obj == nil {
		t.Fatal("AgentFSM not found")
	}
	var fields []string
	var walk func(prefix string, st *types.Struct)
	walk = func(prefix string, st *types.Struct) {
		for i := range st.NumFields() {
			f := st.Field(i)
			if f.Embedded() {
				if inner, ok := deref(f.Type()).Underlying().(*types.Struct); ok {
					walk(prefix+f.Name()+".", inner)
					continue
				}
			}
			fields = append(fields, prefix+f.Name())
		}
	}
	walk("", obj.Type().Underlying().(*types.Struct))
	compare(t, "AgentFSM field", fields, allowedFSMFields)
}

func TestNoNewPrivateTimers(t *testing.T) {
	tree := load(t)
	var found []string
	for _, rel := range tree.files {
		if !strings.HasPrefix(rel, "internal/system1/") && !strings.HasPrefix(rel, "internal/agent/") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(moduleRoot, rel), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			spec, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			ast.Inspect(spec.Type, func(n ast.Node) bool {
				st, ok := n.(*ast.StructType)
				if !ok {
					return true
				}
				for _, field := range st.Fields.List {
					for _, name := range field.Names {
						if isTimerName(name.Name) {
							found = append(found, rel+":"+spec.Name.Name+"."+name.Name)
						}
					}
				}
				return true
			})
			return false
		})
	}
	compare(t, "timer field (*Until, *Backoff, *Skips)", found, allowedTimers)
}

func TestAgentStateMutationsDoNotGrow(t *testing.T) {
	tree := load(t)
	mutating := mutatingMethods(tree.pkg(t, system1Path))
	if !mutating[system1Path+".AgentState.SetStage"] || mutating[system1Path+".AgentState.Stage"] {
		t.Fatal("mutating-method analysis is wrong: want SetStage mutating and Stage not")
	}

	byFile := map[string]int{}
	total := 0
	for _, pkg := range tree.checked {
		for i, file := range pkg.files {
			rel := pkg.rels[i]
			if stateOwnFiles[rel] {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				for _, target := range writeTargets(pkg.info, n) {
					if throughAgentState(pkg.info, target) {
						byFile[rel]++
						total++
					}
				}
				if call, ok := n.(*ast.CallExpr); ok {
					if sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr); ok {
						if fn, ok := pkg.info.Uses[sel.Sel].(*types.Func); ok && mutating[methodKey(fn)] &&
							(isAgentState(pkg.info.TypeOf(sel.X)) || throughAgentState(pkg.info, sel.X)) {
							byFile[rel]++
							total++
						}
					}
				}
				return true
			})
		}
	}
	if total > maxOutsideStateMutations {
		var lines []string
		for rel, n := range byFile {
			lines = append(lines, fmt.Sprintf("  %4d %s", n, rel))
		}
		sort.Strings(lines)
		t.Errorf("%d AgentState mutations outside its own files, recorded maximum %d. "+
			"Return the change in Step.Memory/Store or keep it in the frame Local (ADR-036).\n%s",
			total, maxOutsideStateMutations, strings.Join(lines, "\n"))
	} else if total < maxOutsideStateMutations {
		t.Logf("%d mutations, maximum %d: lower maxOutsideStateMutations", total, maxOutsideStateMutations)
	}
}

// TestSizeBudgets enforces the ceilings recorded in scripts/check-arch-budgets.sh. The
// script and the Java files are outside the Go module, so the test cache does not see
// changes to them: run with -count=1.
func TestSizeBudgets(t *testing.T) {
	load(t) // reading the Go tree makes a cached result expire when it changes
	out, err := exec.Command(filepath.Join(moduleRoot, "..", "scripts", "check-arch-budgets.sh"), "--strict").CombinedOutput()
	if err != nil {
		t.Fatalf("size budgets exceeded (%v). Ceilings only move down.\n%s", err, out)
	}
}

func isTimerName(name string) bool {
	for _, suffix := range []string{"Until", "Backoff", "Skips"} {
		if strings.HasSuffix(name, suffix) || name == strings.ToLower(suffix) {
			return true
		}
	}
	return false
}

// compare fails on an entry that is new and on a recorded entry that no longer exists, so
// that a removed one cannot come back.
func compare(t *testing.T, what string, found, allowed []string) {
	t.Helper()
	for _, f := range found {
		if !slices.Contains(allowed, f) {
			t.Errorf("new %s %q (ADR-041 section 5): keep the state in a frame Local or in Step.Memory", what, f)
		}
	}
	for _, a := range allowed {
		if !slices.Contains(found, a) {
			t.Errorf("%s %q no longer exists: delete it from the list", what, a)
		}
	}
}

type checkedPackage struct {
	files []*ast.File
	rels  []string
	info  *types.Info
	types *types.Package
}

type sourceTree struct {
	files   []string // every non-test Go file, relative to the module root
	checked map[string]*checkedPackage
}

func (s *sourceTree) pkg(t *testing.T, path string) *checkedPackage {
	t.Helper()
	p := s.checked[path]
	if p == nil {
		t.Fatalf("package %s was not loaded", path)
	}
	return p
}

var (
	loadOnce sync.Once
	loaded   *sourceTree
	loadErr  error
)

// load type-checks system1 and every package that imports it, from source, against the
// compiler's export data for their dependencies.
func load(t *testing.T) *sourceTree {
	t.Helper()
	loadOnce.Do(func() { loaded, loadErr = loadTree() })
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	return loaded
}

func loadTree() (*sourceTree, error) {
	tree := &sourceTree{checked: map[string]*checkedPackage{}}
	err := filepath.WalkDir(moduleRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		rel, err := filepath.Rel(moduleRoot, path)
		if err != nil {
			return err
		}
		if _, err := os.ReadFile(path); err != nil {
			return err
		}
		tree.files = append(tree.files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}

	cmd := exec.Command("go", "list", "-export", "-deps", "-json=ImportPath,Dir,GoFiles,Export,Imports", "./...")
	cmd.Dir = moduleRoot
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %v: %s", err, stderr.String())
	}
	type listed struct {
		ImportPath, Dir, Export string
		GoFiles, Imports        []string
	}
	exports := map[string]string{}
	var targets []listed
	for dec := json.NewDecoder(bytes.NewReader(out)); ; {
		var p listed
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		exports[p.ImportPath] = p.Export
		if p.ImportPath == system1Path || slices.Contains(p.Imports, system1Path) {
			targets = append(targets, p)
		}
	}

	root, err := filepath.Abs(moduleRoot)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	imp := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		if exports[path] == "" {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		return os.Open(exports[path])
	})
	for _, p := range targets {
		cp := &checkedPackage{info: &types.Info{
			Types: map[ast.Expr]types.TypeAndValue{},
			Defs:  map[*ast.Ident]types.Object{},
			Uses:  map[*ast.Ident]types.Object{},
		}}
		for _, name := range p.GoFiles {
			path := filepath.Join(p.Dir, name)
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return nil, err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return nil, err
			}
			cp.files = append(cp.files, file)
			cp.rels = append(cp.rels, filepath.ToSlash(rel))
		}
		cp.types, err = (&types.Config{Importer: imp}).Check(p.ImportPath, fset, cp.files, cp.info)
		if err != nil {
			return nil, fmt.Errorf("type check %s: %w", p.ImportPath, err)
		}
		tree.checked[p.ImportPath] = cp
	}
	return tree, nil
}

// mutatingVerbs name the methods of types declared elsewhere (atomics, the recipe graph)
// that change their receiver.
var mutatingVerbs = []string{
	"Add", "Append", "Clear", "CompareAndSwap", "Delete", "Ingest", "Insert", "Mark", "Pop",
	"Prune", "Push", "Put", "Record", "Remove", "Reset", "Set", "Store", "Swap", "Update", "Upsert",
}

// mutatingMethods returns the methods that change their receiver: system1 methods by what
// their body writes (to a fixed point over calls), others by their name.
func mutatingMethods(pkg *checkedPackage) map[string]bool {
	type method struct {
		key  string
		recv types.Object
		body *ast.BlockStmt
	}
	var methods []method
	declared := map[string]bool{}
	for _, file := range pkg.files {
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || fd.Body == nil || len(fd.Recv.List[0].Names) == 0 {
				continue
			}
			fn := pkg.info.Defs[fd.Name].(*types.Func)
			declared[methodKey(fn)] = true
			methods = append(methods, method{methodKey(fn), pkg.info.Defs[fd.Recv.List[0].Names[0]], fd.Body})
		}
	}
	mutating := map[string]bool{}
	// Calls out of system1 (and through its interfaces) are judged by name.
	for _, obj := range pkg.info.Uses {
		fn, ok := obj.(*types.Func)
		if !ok || fn.Signature().Recv() == nil || declared[methodKey(fn)] {
			continue
		}
		if slices.ContainsFunc(mutatingVerbs, func(v string) bool { return strings.HasPrefix(fn.Name(), v) }) {
			mutating[methodKey(fn)] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for _, m := range methods {
			if !mutating[m.key] && writesReceiver(pkg.info, m.recv, m.body, mutating) {
				mutating[m.key] = true
				changed = true
			}
		}
	}
	return mutating
}

// writesReceiver reports whether body writes through recv, directly, through a local that
// aliases part of it, or by calling a mutating method on it.
func writesReceiver(info *types.Info, recv types.Object, body *ast.BlockStmt, mutating map[string]bool) bool {
	owned := map[types.Object]bool{recv: true}
	rooted := func(e ast.Expr) bool {
		root := rootIdent(e)
		return root != nil && owned[info.Uses[root]]
	}
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			if s.Tok == token.DEFINE && len(s.Lhs) == len(s.Rhs) {
				for i, rhs := range s.Rhs {
					id, ok := s.Lhs[i].(*ast.Ident)
					if ok && rooted(rhs) && info.Defs[id] != nil && isReference(info.TypeOf(rhs)) {
						owned[info.Defs[id]] = true
					}
				}
			}
		case *ast.CallExpr:
			if sel, ok := ast.Unparen(s.Fun).(*ast.SelectorExpr); ok && rooted(sel.X) {
				if fn, ok := info.Uses[sel.Sel].(*types.Func); ok && mutating[methodKey(fn)] {
					found = true
				}
			}
		}
		for _, target := range writeTargets(info, n) {
			if rooted(target) {
				found = true
			}
		}
		return !found
	})
	return found
}

// writeTargets returns the expressions n stores into, leaving out plain variables.
func writeTargets(info *types.Info, n ast.Node) []ast.Expr {
	var targets []ast.Expr
	switch s := n.(type) {
	case *ast.AssignStmt:
		targets = s.Lhs
	case *ast.IncDecStmt:
		targets = []ast.Expr{s.X}
	case *ast.CallExpr:
		if id, ok := s.Fun.(*ast.Ident); ok && len(s.Args) > 0 {
			if b, ok := info.Uses[id].(*types.Builtin); ok && (b.Name() == "delete" || b.Name() == "clear") {
				return s.Args[:1]
			}
		}
	}
	return slices.DeleteFunc(slices.Clone(targets), func(e ast.Expr) bool {
		_, plain := ast.Unparen(e).(*ast.Ident)
		return plain
	})
}

func rootIdent(e ast.Expr) *ast.Ident {
	for {
		switch x := e.(type) {
		case *ast.Ident:
			return x
		case *ast.SelectorExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.SliceExpr:
			e = x.X
		case *ast.StarExpr:
			e = x.X
		case *ast.ParenExpr:
			e = x.X
		case *ast.UnaryExpr:
			e = x.X
		default:
			return nil
		}
	}
}

// throughAgentState reports whether e reaches its value through a field of an AgentState.
func throughAgentState(info *types.Info, e ast.Expr) bool {
	for {
		switch x := e.(type) {
		case *ast.SelectorExpr:
			if isAgentState(info.TypeOf(x.X)) {
				return true
			}
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.SliceExpr:
			e = x.X
		case *ast.StarExpr:
			e = x.X
		case *ast.ParenExpr:
			e = x.X
		default:
			return false
		}
	}
}

func isAgentState(t types.Type) bool {
	if t == nil {
		return false
	}
	named, ok := deref(t).(*types.Named)
	return ok && named.Obj().Name() == "AgentState" && named.Obj().Pkg().Path() == system1Path
}

func isReference(t types.Type) bool {
	switch t.Underlying().(type) {
	case *types.Pointer, *types.Map, *types.Slice:
		return true
	}
	return false
}

func deref(t types.Type) types.Type {
	if p, ok := t.(*types.Pointer); ok {
		return p.Elem()
	}
	return t
}

// methodKey names a method the same way whether it was checked from source or imported.
func methodKey(fn *types.Func) string {
	recv := fn.Signature().Recv()
	if recv == nil {
		return ""
	}
	if named, ok := deref(recv.Type()).(*types.Named); ok && named.Obj().Pkg() != nil {
		return named.Obj().Pkg().Path() + "." + named.Obj().Name() + "." + fn.Name()
	}
	return fn.FullName()
}
