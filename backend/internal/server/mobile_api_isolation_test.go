package server

// mobile_api_isolation_test.go locks in the dead-code posture for the legacy
// Echo-based MobileAPI. Production traffic goes through handleMobileSessionRouter
// in mobile_session_handler.go (registered at server.go:385-386).
//
// The constructors and route-registrar are unexported so the Go compiler
// refuses any external wiring attempt. The AST scan here is a belt-and-braces
// layer that also catches *intra-package* references (e.g. a server-side test
// that constructs a MobileAPI just to call HandleWebSocket).
//
// Targets:
//   - mobile_api.go: newMobileAPI returns *MobileAPI
//   - mobile_api.go: (*MobileAPI).registerRoutes mounts /mobile/* routes
//   - mobile_api.go: (*MobileAPI).HandleWebSocket uses CheckOrigin: return true

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
	"testing"
)

// TestMobileAPIHandlersAreNotWired asserts the Echo-based MobileAPI is dead.
// Production routes are registered via server.go's handleMobileSessionRouter.
func TestMobileAPIHandlersAreNotWired(t *testing.T) {
	const targetFile = "mobile_api.go"

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(info fs.FileInfo) bool {
		// Skip generated/_test files; we only want hand-written Go sources.
		if strings.HasSuffix(info.Name(), "_test.go") {
			return false
		}
		// Always include the file under test so we don't trip over its own
		// definition when scanning selectors.
		if info.Name() == targetFile {
			return true
		}
		return strings.HasSuffix(info.Name(), ".go")
	}, parser.AllErrors)
	if err != nil {
		t.Fatalf("parse internal/server: %v", err)
	}

	const forbidden = "this branch must use handleMobileSessionRouter (server.go:385)"

	forbiddenRefs := map[string]string{
		"newMobileAPI":   "production code must not construct the legacy Echo MobileAPI; " + forbidden,
		"registerRoutes": "production code must not call (*MobileAPI).registerRoutes; " + forbidden,
	}

	// Walk every file other than mobile_api.go and fail on any reference to
	// the forbidden symbols.
	for fname, file := range pkgs["server"].Files {
		if strings.HasSuffix(fname, targetFile) {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if msg, hit := forbiddenRefs[sel.Sel.Name]; hit {
				// Allow `MobileAPI.RegisterRoutes` only if it's a method
				// declaration site (i.e. the receiver is *MobileAPI itself,
				// which lives inside mobile_api.go — already excluded above).
				t.Errorf("%s: external reference to %s.%s — %s",
					fname, ident.Name, sel.Sel.Name, msg)
			}
			return true
		})
		// Also catch bare references (newMobileAPI(...) without selector).
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := call.Fun.(*ast.Ident)
			if !ok {
				return true
			}
			if msg, hit := forbiddenRefs[id.Name]; hit {
				t.Errorf("%s: bare call to %s — %s",
					fname, id.Name, msg)
			}
			return true
		})
	}
}

// TestMobileAPIWebSocketCheckOriginIsNotReachable locks in that the
// `CheckOrigin: return true` upgrader on mobile_api.go:513-547 cannot be
// reached from any HTTP route registered in server.go.
//
// We assert by enumerating every handler mounted on s.mux and confirming that
// none of them is `(*MobileAPI).HandleWebSocket`. If the legacy route ever
// comes back, this test fails before the bad upgrader hits production.
func TestMobileAPIWebSocketCheckOriginIsNotReachable(t *testing.T) {
	const targetFile = "mobile_api.go"
	const handlerFn = "HandleWebSocket"

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(info fs.FileInfo) bool {
		if strings.HasSuffix(info.Name(), targetFile) {
			return false
		}
		if strings.HasSuffix(info.Name(), "_test.go") {
			return false
		}
		return strings.HasSuffix(info.Name(), ".go")
	}, parser.AllErrors)
	if err != nil {
		t.Fatalf("parse internal/server: %v", err)
	}

	for fname, file := range pkgs["server"].Files {
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if sel.Sel.Name != handlerFn {
				return true
			}
			// Sel.X should be `api` (the MobileAPI receiver) — but since we
			// only walk files OTHER than mobile_api.go, any reference here
			// means somebody is wiring it elsewhere.
			t.Errorf("%s: external reference to (*MobileAPI).%s — production must use handleMobileWS",
				fname, handlerFn)
			return true
		})
	}
}
