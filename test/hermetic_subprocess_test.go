package test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestAmuxSubprocessesUseHermeticHelper(t *testing.T) {
	t.Parallel()

	testDir := filepath.Join(repoRoot(t), "test")
	var offenders []string
	fset := token.NewFileSet()
	err := filepath.WalkDir(testDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") || filepath.Base(path) == "script_helpers_test.go" {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "exec" {
				return true
			}
			argIndex := 0
			switch sel.Sel.Name {
			case "Command":
			case "CommandContext":
				argIndex = 1
			default:
				return true
			}
			if len(call.Args) <= argIndex {
				return true
			}
			if id, ok := call.Args[argIndex].(*ast.Ident); ok && id.Name == "amuxBin" {
				pos := fset.Position(call.Pos())
				offenders = append(offenders, filepath.ToSlash(pos.Filename)+":"+strconv.Itoa(pos.Line))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("scan test subprocesses: %v", err)
	}
	if len(offenders) > 0 {
		t.Fatalf("exec.Command(amuxBin, ...) must go through the hermetic subprocess helper:\n%s", strings.Join(offenders, "\n"))
	}
}
