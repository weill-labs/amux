package render

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
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

func TestCompositorUsesConfiguredIconSetForPaneStatus(t *testing.T) {
	t.Parallel()

	icons := IconSet{
		PaneIdle:      "I",
		PaneActive:    "A",
		PaneBusy:      "B",
		PaneLead:      "L",
		PaneEscalated: "E",
		PaneStuck:     "S",
		RemoteHost:    "R",
		PR:            "P",
		Issue:         "J",
		CopyMode:      "C",
		Connected:     "Z",
		Reconnecting:  "Y",
		Disconnected:  "X",
	}
	root := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, 80, 2)
	pane := &statusPaneData{
		id:            1,
		name:          "pane-1",
		trackedPRs:    []proto.TrackedPR{{Number: 42}},
		trackedIssues: []proto.TrackedIssue{{ID: "LAB-1647"}},
		host:          "gpu",
		task:          "task",
		color:         config.TextColorHex,
		connStatus:    string(proto.Connected),
		copyMode:      true,
		copySearch:    "/query",
	}
	lookup := func(uint32) PaneData { return pane }

	full := NewCompositor(80, 3, "test")
	full.SetIconSet(icons)
	assertStatusUsesSentinelIcons(t, MaterializeGrid(full.RenderFull(root, 1, lookup), 80, 3))

	diff := NewCompositor(80, 3, "test")
	diff.SetIconSet(icons)
	assertStatusUsesSentinelIcons(t, MaterializeGrid(diff.RenderDiffWithOverlay(root, 1, lookup, OverlayState{}), 80, 3))
}

func assertStatusUsesSentinelIcons(t *testing.T, rendered string) {
	t.Helper()

	for _, want := range []string{"A", "C /query", "P42", "JLAB-1647", "Rgpu", "Z", "task"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered status missing %q:\n%s", want, rendered)
		}
	}
	for _, old := range []string{"●", "[copy]", "#42", "@gpu", "⚡"} {
		if strings.Contains(rendered, old) {
			t.Fatalf("rendered status still contains old glyph %q:\n%s", old, rendered)
		}
	}
}
