package server

// task_create_parent_test.go — B-2: POST /api/tasks accepted a parentId
// without checking it, while PATCH ran the full validateReparent (existence in
// the workspace + cycle detection). Two consequences, both reachable by any
// logged-in user:
//
//   - parentId == the new task's own id writes a self-referential row, which
//     makes the progress roll-up undecidable;
//   - parentId naming a task in another tenant plants a cross-tenant pointer.
//
// The handler's store is a concrete *task.Store, so the HTTP round trip needs a
// database. Two cheaper checks carry the regression instead:
//
//  1. TestPostTasksValidatesParentBeforeCreate — an AST scan of handleTasks
//     proving the POST branch calls validateReparent *before* CreateTask. This
//     is the assertion that fails if someone removes the call again; the
//     store-backed tests (TestPostTasksRejectsForeignParent) prove the
//     behaviour on a real database.
//  2. TestSubtaskRouteTakesParentFromPath — POST /api/tasks/{id}/subtasks pins
//     the parent down to the path id, which is the one form that cannot be
//     forged.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// taskMethodCaseBody returns the body statements of `case http.MethodX:` inside
// fn, i.e. the source of one method branch of the tasks handler.
func taskMethodCaseBody(t *testing.T, file *ast.File, method string) []ast.Stmt {
	t.Helper()
	var found []ast.Stmt
	var walk func(n ast.Node) bool
	walk = func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CaseClause:
			for _, expr := range node.List {
				sel, ok := expr.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != method {
					continue
				}
				if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "http" {
					continue
				}
				found = node.Body
			}
		case *ast.FuncDecl:
			if node.Name.Name != "handleTasks" {
				return false
			}
		}
		return true
	}
	ast.Inspect(file, walk)
	if found == nil {
		t.Fatalf("no `case http.%s:` branch found in handleTasks", method)
	}
	return found
}

func parseServerSource(t *testing.T) *ast.File {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "server.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse server.go: %v", err)
	}
	return file
}

// callOrder reports the source position of the first call whose selector name
// is method inside the given statements, or token.NoPos when there is none.
// recv, when non-empty, additionally requires the receiver to be that
// identifier (used for the unexported helpers, which hang off *Server).
func callOrder(stmts []ast.Stmt, recv, method string) token.Pos {
	var found token.Pos
	for _, stmt := range stmts {
		ast.Inspect(stmt, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != method {
				return true
			}
			if recv != "" {
				id, ok := sel.X.(*ast.Ident)
				if !ok || id.Name != recv {
					return true
				}
			}
			if found == token.NoPos || call.Pos() < found {
				found = call.Pos()
			}
			return true
		})
	}
	return found
}

// The create path must validate the parent before the row is written. Ordering
// matters: validating after CreateTask would still have stored the bad parent.
func TestPostTasksValidatesParentBeforeCreate(t *testing.T) {
	file := parseServerSource(t)
	post := taskMethodCaseBody(t, file, "MethodPost")

	validateAt := callOrder(post, "s", "validateReparent")
	if validateAt == token.NoPos {
		t.Fatal("POST /api/tasks does not call validateReparent: a client-supplied parentId is written unchecked " +
			"(self-parenting rows and cross-tenant parent pointers)")
	}
	createAt := callOrder(post, "", "CreateTask")
	if createAt == token.NoPos {
		t.Fatal("could not find the CreateTask call in the POST branch; the guard is scanning the wrong code")
	}
	if validateAt > createAt {
		t.Error("validateReparent runs after CreateTask: the invalid parent is already persisted")
	}
}

// The re-parent check must survive as a single shared implementation. Two
// copies of the rule drift, and the copy that is forgotten is the one that
// leaves a hole.
func TestTaskParentValidationHasOneImplementation(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse internal/server: %v", err)
	}
	decls := 0
	for _, file := range pkgs["server"].Files {
		if hasSuffix(file.Name.Name, "_test.go") {
			continue
		}
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv == nil {
				continue
			}
			if fn.Name.Name == "validateReparent" {
				decls++
			}
		}
	}
	if decls != 1 {
		t.Errorf("found %d validateReparent definitions, want exactly 1 — every write path that sets parent_id "+
			"must go through the same check", decls)
	}
}

// --- B-3: the /children endpoint must filter, not just check the parent ---

// TestTaskChildrenFiltersChildrenByVisibility is the guard for the child read.
// The endpoint used to authorize the *parent* and then return every child
// verbatim, so one GET on a shared goal disclosed each private child's full
// record. The rule itself is unit-tested in the task package; what can only be
// checked here is that the handler applies it before answering.
func TestTaskChildrenFiltersChildrenByVisibility(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse internal/server: %v", err)
	}
	var handler *ast.File
	for name, f := range pkgs["server"].Files {
		if name == "task_hierarchy_handler.go" {
			handler = f
		}
	}
	if handler == nil {
		t.Fatal("task_hierarchy_handler.go not found")
	}

	var found bool
	for _, d := range handler.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "handleTaskChildren" {
			continue
		}
		found = true

		var filterAt, respondAt token.Pos
		ast.Inspect(fn, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "FilterReadableChildren":
				if filterAt == token.NoPos {
					filterAt = call.Pos()
				}
			case "writeJSON":
				if respondAt == token.NoPos {
					respondAt = call.Pos()
				}
			}
			return true
		})
		if filterAt == token.NoPos {
			t.Fatal("GET /api/tasks/{id}/children never calls task.FilterReadableChildren: " +
				"the parent's permission is being inherited by its private children")
		}
		if respondAt != token.NoPos && filterAt > respondAt {
			t.Error("the children are filtered after the response is written")
		}
	}
	if !found {
		t.Fatal("handleTaskChildren not found")
	}
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

// The sub-task route is the one place where the parent is not a free-form
// field: it is the path id, and the request body must not be able to name a
// different one.
func TestSubtaskRouteTakesParentFromPath(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse internal/server: %v", err)
	}
	var handler *ast.File
	for name, f := range pkgs["server"].Files {
		if name == "task_hierarchy_handler.go" {
			handler = f
		}
	}
	if handler == nil {
		t.Fatal("task_hierarchy_handler.go not found")
	}

	var found bool
	for _, d := range handler.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "handleTaskSubtasks" {
			continue
		}
		found = true
		ast.Inspect(fn, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.TypeSpec:
				st, ok := node.Type.(*ast.StructType)
				if !ok {
					return true
				}
				for _, fld := range st.Fields.List {
					if fld.Tag == nil {
						continue
					}
					if strings.Contains(fld.Tag.Value, `"parentId"`) {
						t.Error("the sub-task body accepts parentId: the parent must come from the path, " +
							"or a caller can attach the child to a different work item")
					}
				}
			case *ast.KeyValueExpr:
				key, ok := node.Key.(*ast.Ident)
				if !ok || key.Name != "ParentID" {
					return true
				}
				if id, ok := node.Value.(*ast.Ident); !ok || id.Name != "taskID" {
					t.Errorf("ParentID is set from %T, want the path parameter taskID", node.Value)
				}
			}
			return true
		})
	}
	if !found {
		t.Fatal("handleTaskSubtasks not found")
	}
}
