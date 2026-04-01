package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestParallelAudit(t *testing.T) {
	t.Parallel()

	findings, err := findMissingParallelCalls(".")
	if err != nil {
		t.Fatalf("findMissingParallelCalls: %v", err)
	}
	if len(findings) == 0 {
		return
	}

	var b strings.Builder
	b.WriteString("parallel-safe tests missing t.Parallel():\n")
	for _, finding := range findings {
		b.WriteString("  ")
		b.WriteString(finding.kind)
		b.WriteString(" ")
		b.WriteString(finding.file)
		b.WriteString(":")
		b.WriteString(finding.line)
		b.WriteString(" ")
		b.WriteString(finding.name)
		b.WriteString("\n")
	}
	t.Fatal(b.String())
}

type parallelFinding struct {
	kind string
	file string
	line string
	name string
}

func findMissingParallelCalls(root string) ([]parallelFinding, error) {
	var findings []parallelFinding
	fset := token.NewFileSet()

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch path {
			case ".git", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Name == nil || fn.Body == nil {
				continue
			}
			if !strings.HasPrefix(fn.Name.Name, "Test") || fn.Name.Name == "TestMain" || strings.Contains(fn.Name.Name, "SubprocessHelper") {
				continue
			}

			tName := testParamName(fn.Type)
			if tName != "" && !blockHasParallel(fn.Body, tName) && !hasNotParallelComment(file, fn.Pos(), fn.End()) {
				if !blockUsesUnsafeParallelSetup(fn.Body, tName) {
					pos := fset.Position(fn.Pos())
					findings = append(findings, parallelFinding{
						kind: "top",
						file: strings.TrimPrefix(path, "./"),
						line: strconv.Itoa(pos.Line),
						name: fn.Name.Name,
					})
				}
			}

			ast.Inspect(fn.Body, func(n ast.Node) bool {
				rangeStmt, ok := n.(*ast.RangeStmt)
				if !ok {
					return true
				}
				ast.Inspect(rangeStmt.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok || len(call.Args) < 2 {
						return true
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok || sel.Sel == nil || sel.Sel.Name != "Run" {
						return true
					}
					fnLit, ok := call.Args[1].(*ast.FuncLit)
					if !ok {
						return true
					}
					subTName := testParamName(fnLit.Type)
					if subTName == "" || blockHasParallel(fnLit.Body, subTName) || hasNotParallelComment(file, call.Pos(), call.End()) {
						return true
					}
					if blockUsesUnsafeParallelSetup(fnLit.Body, subTName) {
						return true
					}

					pos := fset.Position(call.Pos())
					findings = append(findings, parallelFinding{
						kind: "sub",
						file: strings.TrimPrefix(path, "./"),
						line: strconv.Itoa(pos.Line),
						name: fn.Name.Name + "/" + exprString(fset, call.Args[0]),
					})
					return true
				})
				return true
			})
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].file != findings[j].file {
			return findings[i].file < findings[j].file
		}
		if findings[i].line != findings[j].line {
			return findings[i].line < findings[j].line
		}
		return findings[i].name < findings[j].name
	})

	return findings, nil
}

func testParamName(ft *ast.FuncType) string {
	if ft == nil || ft.Params == nil || len(ft.Params.List) == 0 {
		return ""
	}
	field := ft.Params.List[0]
	if len(field.Names) == 0 {
		return ""
	}
	star, ok := field.Type.(*ast.StarExpr)
	if !ok {
		return ""
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok || sel.Sel == nil || sel.Sel.Name != "T" {
		return ""
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "testing" {
		return ""
	}
	return field.Names[0].Name
}

func blockHasParallel(body *ast.BlockStmt, tName string) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel == nil || sel.Sel.Name != "Parallel" {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if ok && id.Name == tName {
			found = true
			return false
		}
		return true
	})
	return found
}

func blockUsesUnsafeParallelSetup(body *ast.BlockStmt, tName string) bool {
	unsafe := false
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			if callUsesUnsafeParallelSetup(n, tName) {
				unsafe = true
				return false
			}
		case *ast.AssignStmt:
			for _, lhs := range n.Lhs {
				sel, ok := lhs.(*ast.SelectorExpr)
				if !ok {
					continue
				}
				id, ok := sel.X.(*ast.Ident)
				if !ok {
					continue
				}
				if id.Name == "os" {
					switch sel.Sel.Name {
					case "Stdin", "Stdout", "Stderr", "Args":
						unsafe = true
						return false
					}
				}
				if id.Name == "flag" && sel.Sel.Name == "CommandLine" {
					unsafe = true
					return false
				}
			}
		}
		return true
	})
	return unsafe
}

func callUsesUnsafeParallelSetup(call *ast.CallExpr, tName string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if ok {
		if id, ok := sel.X.(*ast.Ident); ok {
			if id.Name == tName && sel.Sel.Name == "Setenv" {
				return true
			}
			if id.Name == "os" && sel.Sel.Name == "Setenv" {
				return true
			}
			if id.Name == "runtime" && sel.Sel.Name == "GOMAXPROCS" {
				return true
			}
		}
	}

	id, ok := call.Fun.(*ast.Ident)
	if !ok {
		return false
	}
	switch id.Name {
	case "newRunSessionHarness", "newKeyedHostConn":
		return true
	default:
		return false
	}
}

func hasNotParallelComment(file *ast.File, start, end token.Pos) bool {
	for _, group := range file.Comments {
		if group.End() < start || group.Pos() > end {
			continue
		}
		for _, comment := range group.List {
			if strings.Contains(comment.Text, "Not parallel:") {
				return true
			}
		}
	}
	return false
}

func exprString(fset *token.FileSet, expr ast.Expr) string {
	var buf bytes.Buffer
	_ = printer.Fprint(&buf, fset, expr)
	return buf.String()
}
