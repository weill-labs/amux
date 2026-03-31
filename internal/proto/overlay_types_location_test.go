package proto

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCopyModeOverlayTypesLiveInProto(t *testing.T) {
	t.Parallel()

	required := []string{
		"ViewportOverlay",
		"CursorPosition",
		"HighlightKind",
		"HighlightLine",
		"HighlightSpan",
		"Cell",
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}

	protoDir := filepath.Dir(thisFile)
	copymodeDir := filepath.Join(filepath.Dir(protoDir), "copymode")

	protoTypes := packageTypeDecls(t, protoDir)
	copymodeTypes := packageTypeDecls(t, copymodeDir)

	for _, name := range required {
		if !protoTypes[name] {
			t.Fatalf("internal/proto is missing type %q", name)
		}
		if copymodeTypes[name] {
			t.Fatalf("internal/copymode still declares type %q", name)
		}
	}
}

func packageTypeDecls(t *testing.T, dir string) map[string]bool {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatalf("filepath.Glob(%q): %v", dir, err)
	}

	decls := make(map[string]bool)
	fset := token.NewFileSet()
	for _, path := range matches {
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parser.ParseFile(%q): %v", path, err)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				decls[typeSpec.Name.Name] = true
			}
		}
	}
	return decls
}
