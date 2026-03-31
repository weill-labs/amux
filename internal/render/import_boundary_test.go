package render

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestRenderPackageDoesNotImportCopyMode(t *testing.T) {
	t.Parallel()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}

	dir := filepath.Dir(thisFile)
	matches, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatalf("filepath.Glob(%q): %v", dir, err)
	}

	fset := token.NewFileSet()
	for _, path := range matches {
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parser.ParseFile(%q): %v", path, err)
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("strconv.Unquote(%q): %v", spec.Path.Value, err)
			}
			if importPath == "github.com/weill-labs/amux/internal/copymode" {
				t.Fatalf("%s imports %q", strings.TrimPrefix(path, dir+"/"), importPath)
			}
		}
	}
}
