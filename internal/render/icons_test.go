package render

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestStatusGlyphLiteralsAreCentralizedInIconSet(t *testing.T) {
	t.Parallel()

	statusGlyphs := map[rune]struct{}{
		'◇': {},
		'●': {},
		'○': {},
		'▶': {},
		'◯': {},
		'◆': {},
		'◈': {},
		'⚡': {},
		'⟳': {},
		'✕': {},
	}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}

	fset := token.NewFileSet()
	var offenders []string
	for _, path := range files {
		base := filepath.Base(path)
		if strings.HasSuffix(base, "_test.go") || base == "icons.go" {
			continue
		}

		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatalf("unquote %s: %v", fset.Position(lit.Pos()), err)
			}
			for _, r := range value {
				if _, ok := statusGlyphs[r]; ok {
					offenders = append(offenders, fset.Position(lit.Pos()).String())
					break
				}
			}
			return true
		})
	}

	if len(offenders) > 0 {
		t.Fatalf("status glyph literals must route through IconSet; found direct literals at:\n%s", strings.Join(offenders, "\n"))
	}
}
