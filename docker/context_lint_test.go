// SPDX-License-Identifier: Apache-2.0
// Copyright © 2026 Eldara Tech

package docker

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// allowedBareBackground lists files (relative to module root) and function names
// where bare context.Background() is a known legitimate use.
var allowedBareBackground = map[string]string{
	"views/configs/model.go": "addConfig",
	"views/secrets/model.go": "addSecret",
	"commands/api/api.go":    "New",
	// RefreshSnapshot has no context parameter to inherit from, and the call it
	// makes is bounded: SnapshotWith applies the 30s deadline itself, so the
	// timeout lives in one place instead of at every call site.
	"docker/snapshot.go": "RefreshSnapshot",
	// deployStack is the TUI's entry point, called from Bubble Tea commands that
	// hold no context. Everything under it takes one, so the caller that does
	// have a context to give — the release engine — reaches the child process.
	"docker/stack_deploy.go": "deployStack",
}

func TestNoBareContextBackground(t *testing.T) {
	root := moduleRoot(t)

	dirs := []string{
		filepath.Join(root, "views"),
		filepath.Join(root, "docker"),
	}

	var violations []string

	for _, dir := range dirs {
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				if info.Name() == "integration-tests" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}

			rel, _ := filepath.Rel(root, path)
			vs := checkFile(t, path, rel)
			violations = append(violations, vs...)
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", dir, err)
		}
	}

	for _, v := range violations {
		t.Errorf("%s", v)
	}
}

// checkFile parses a single Go file and returns violations for bare context.Background() calls.
func checkFile(t *testing.T, path, rel string) []string {
	t.Helper()

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", rel, err)
	}

	var violations []string
	var stack []ast.Node

	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return false
		}
		defer func() { stack = append(stack, n) }()

		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if !isContextBackground(call) {
			return true
		}

		// Check if the parent is a context.With* wrapping this call as first arg.
		if parentWraps(stack, call) {
			return true
		}

		// Check allow-list: match file and enclosing function.
		pos := fset.Position(call.Pos())
		if fn := enclosingFunc(stack); fn != "" {
			if allowed, ok := allowedBareBackground[rel]; ok && allowed == fn {
				return true
			}
		}

		violations = append(violations,
			"bare context.Background() at "+rel+":"+itoa(pos.Line)+
				" — wrap with context.WithTimeout or add to allow-list")
		return true
	})

	return violations
}

// isContextBackground returns true if call is context.Background().
func isContextBackground(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return ident.Name == "context" && sel.Sel.Name == "Background"
}

// parentWraps checks whether the immediate parent CallExpr is context.WithTimeout,
// context.WithCancel, or context.WithDeadline with call as first argument.
func parentWraps(stack []ast.Node, call *ast.CallExpr) bool {
	if len(stack) == 0 {
		return false
	}
	parent, ok := stack[len(stack)-1].(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := parent.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	if ident.Name != "context" {
		return false
	}
	switch sel.Sel.Name {
	case "WithTimeout", "WithCancel", "WithDeadline":
	default:
		return false
	}
	if len(parent.Args) == 0 {
		return false
	}
	return parent.Args[0] == call
}

// enclosingFunc returns the name of the innermost enclosing function, or "".
func enclosingFunc(stack []ast.Node) string {
	for i := len(stack) - 1; i >= 0; i-- {
		switch fn := stack[i].(type) {
		case *ast.FuncDecl:
			return fn.Name.Name
		case *ast.FuncLit:
			// Check if the FuncLit is assigned in a variable declaration.
			if i > 0 {
				if assign, ok := stack[i-1].(*ast.AssignStmt); ok {
					for _, lhs := range assign.Lhs {
						if ident, ok := lhs.(*ast.Ident); ok {
							return ident.Name
						}
					}
				}
			}
			return ""
		}
	}
	return ""
}

// moduleRoot finds the module root by walking up from the test file's directory.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find module root (go.mod)")
		}
		dir = parent
	}
}

// itoa is a minimal int-to-string to avoid importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
