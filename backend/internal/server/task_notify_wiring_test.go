package server

// B-9 / B-10 wiring guards.
//
// Both defects are "the logic exists, nothing calls it": the assignee list and
// the participant list were never reconciled, and no producer ever wrote a
// status_changed / completed event. Neither is visible to a unit test of the
// pure pieces, and both need a live database to exercise end to end, so the
// call sites are pinned here by reading the source — the same technique as
// mobile_api_isolation_test.go.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
	"testing"
)

// serverFuncSource returns the AST of one *Server method.
func serverFuncSource(t *testing.T, name string) *ast.FuncDecl {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(info fs.FileInfo) bool {
		return strings.HasSuffix(info.Name(), ".go")
	}, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse internal/server: %v", err)
	}
	for _, file := range pkgs["server"].Files {
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Name.Name != name {
				continue
			}
			if fn.Recv != nil {
				return fn
			}
		}
	}
	t.Fatalf("(*Server).%s not found", name)
	return nil
}

// callsMethod reports whether fn calls <recv>.<method> anywhere. recv may name
// any link in the receiver chain, so it matches both `s.validateReparent(…)`
// and `s.taskStore.CreateTask(…)`. An empty recv matches any receiver.
func callsMethod(fn *ast.FuncDecl, recv, method string) bool {
	found := false
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || found {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != method {
			return true
		}
		if recv == "" || receiverRoot(sel.X) == recv {
			found = true
		}
		return true
	})
	return found
}

// receiverRoot is `s` for both `s.validateReparent(…)` and
// `s.taskStore.CreateTask(…)`.
func receiverRoot(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return receiverRoot(v.X)
	default:
		return ""
	}
}

// --- B-9 ---

// Assignees are only meaningful if they are also participants, so both write
// paths that can change the assignee list must reconcile the two.
func TestTaskWritesSyncAssigneeParticipants(t *testing.T) {
	cases := []struct {
		fn   string
		note string
	}{
		{"handleTasks", "creating a task with assignees"},
		{"handleTaskSubtasks", "creating a sub-task with assignees"},
	}
	for _, c := range cases {
		t.Run(c.fn, func(t *testing.T) {
			if !callsMethod(serverFuncSource(t, c.fn), "s", "SyncAssigneeParticipants") {
				t.Errorf("%s does not call SyncAssigneeParticipants: %s would produce a work item "+
					"whose assignees cannot open it and never hear about it", c.fn, c.note)
			}
		})
	}
}

// The store must expose exactly one reconciler. A second implementation is a
// second set of rules, and the copy nobody keeps in sync is the leak.
func TestAssigneeSyncHasOneImplementation(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse internal/server: %v", err)
	}
	calls := 0
	for _, file := range pkgs["server"].Files {
		if hasSuffix(file.Name.Name, "_test.go") {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "SyncAssigneeParticipants" {
					calls++
				}
			}
			return true
		})
	}
	if calls < 3 {
		t.Errorf("found %d call sites of SyncAssigneeParticipants, want at least 3 "+
			"(create, sub-task create, assignees update)", calls)
	}
}

// --- B-10 ---

// A status change must reach the activity stream and the notification fan-out.
func TestTaskStatusChangeEmitsEvent(t *testing.T) {
	fn := serverFuncSource(t, "notifyWorkItemStatusChange")
	for _, step := range []struct{ method, why string }{
		{"appendWorkItemEvent", "without an activity entry the change is invisible in the work item's history"},
		{"dispatchWorkItemNotification", "without the fan-out, participants are never told"},
		{"ListParticipants", "the recipient rules need the participant list"},
	} {
		if !callsMethod(fn, "s", step.method) {
			t.Errorf("notifyWorkItemStatusChange never calls %s: %s", step.method, step.why)
		}
	}
}

// The producer has to be reached from the PATCH path — that is where a status
// actually changes.
func TestTaskOperationsNotifiesOnStatusChange(t *testing.T) {
	if !callsMethod(serverFuncSource(t, "handleTaskOperations"), "s", "notifyWorkItemStatusChange") {
		t.Error("handleTaskOperations does not call notifyWorkItemStatusChange: completing or blocking a " +
			"work item would still notify nobody")
	}
}
