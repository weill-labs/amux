package render

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
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
		PaneNameOpen:  "{",
		PaneNameClose: "}",
		RemoteHost:    "R",
		PR:            "P",
		Issue:         "J",
		Task:          "T",
		CopyMode:      "C",
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

	for _, want := range []string{"A", "{pane-1}", "C /query", "P42", "JLAB-1647", "Rgpu", "T task"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered status missing %q:\n%s", want, rendered)
		}
	}
	for _, old := range []string{"●", "[pane-1]", "[copy]", "#42", "@gpu"} {
		if strings.Contains(rendered, old) {
			t.Fatalf("rendered status still contains old glyph %q:\n%s", old, rendered)
		}
	}
}

func TestNerdFontIconSetUsesPublishedGlyphs(t *testing.T) {
	t.Parallel()

	want := IconSet{
		PaneIdle:      "\uebb5", // nf-cod-circle_large
		PaneActive:    "\uebb4", // nf-cod-circle_large_filled
		PaneBusy:      "\ueb31", // nf-cod-pulse
		PaneLead:      "\ueb59", // nf-cod-star_full
		PaneEscalated: "\uea6c", // nf-cod-warning
		PaneStuck:     "\ueaaf", // nf-cod-bug
		PaneNameOpen:  "[",
		PaneNameClose: "]",
		RemoteHost:    "\ueb50", // nf-cod-server
		PR:            "\uf407", // nf-oct-git_pull_request
		Issue:         "\uf41b", // nf-oct-issue_opened
		Task:          "\ueb67", // nf-cod-tasklist
		CopyMode:      "\ueac0", // nf-cod-clippy
		ToggleOn:      "\uf192", // nf-fa-dot_circle_o
		ToggleOff:     "\uf10c", // nf-fa-circle_o
	}
	if got := NerdFontIconSet(); got != want {
		t.Fatalf("NerdFontIconSet() = %#v, want %#v", got, want)
	}
	for name, value := range iconSetFieldMap(want) {
		if value == "" {
			t.Fatalf("NerdFontIconSet().%s is empty", name)
		}
		if width := runewidth.StringWidth(value); width != 1 {
			t.Fatalf("NerdFontIconSet().%s width = %d, want 1", name, width)
		}
	}
}

func TestCompositorUsesNerdFontIconSetForPaneStatusMetadata(t *testing.T) {
	t.Parallel()

	icons := NerdFontIconSet()
	root := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, 96, 2)
	pane := &statusPaneData{
		id:            1,
		name:          "pane-1",
		trackedPRs:    []proto.TrackedPR{{Number: 42}},
		trackedIssues: []proto.TrackedIssue{{ID: "LAB-1650"}},
		host:          "gpu",
		task:          "build LAB-1650",
		color:         config.TextColorHex,
		copyMode:      true,
		copySearch:    "/query",
	}
	lookup := func(uint32) PaneData { return pane }

	comp := NewCompositor(96, 3, "test")
	comp.SetIconSet(icons)
	rendered := MaterializeGrid(comp.RenderFull(root, 1, lookup), 96, 3)

	for _, want := range []string{
		icons.PaneActive,
		icons.CopyMode + " /query",
		icons.PR + "42",
		icons.Issue + "LAB-1650",
		icons.RemoteHost + "gpu",
		icons.Task + " build LAB-1650",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("Nerd status missing %q:\n%s", want, rendered)
		}
	}
	for _, old := range []string{"●", "[copy]", "#42", "@gpu"} {
		if strings.Contains(rendered, old) {
			t.Fatalf("Nerd status still contains fallback marker %q:\n%s", old, rendered)
		}
	}
}

func TestNerdFontPaneStatusClippingKeepsGlyphWidths(t *testing.T) {
	t.Parallel()

	icons := NerdFontIconSet()
	root := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, 34, 2)
	pane := &statusPaneData{
		id:            1,
		name:          "pane-1",
		trackedPRs:    []proto.TrackedPR{{Number: 42}},
		trackedIssues: []proto.TrackedIssue{{ID: "LAB-1650-EXTRA-LONG"}},
		host:          "remote",
		task:          "long task name",
		color:         config.TextColorHex,
	}
	comp := NewCompositor(34, 3, "test")
	comp.SetIconSet(icons)
	rendered := MaterializeGrid(comp.RenderFull(root, 1, func(uint32) PaneData { return pane }), 34, 3)
	statusLine := strings.Split(rendered, "\n")[0]

	if width := runewidth.StringWidth(statusLine); width > 34 {
		t.Fatalf("status line width = %d, want at most 34:\n%s", width, rendered)
	}
	if !strings.Contains(statusLine, "…") {
		t.Fatalf("status line should be clipped with ellipsis:\n%s", rendered)
	}
	if !strings.Contains(statusLine, icons.PaneActive+" [pane-1]") {
		t.Fatalf("status line missing active Nerd prefix:\n%s", rendered)
	}
}

func TestIconSetPresetsAndHelpers(t *testing.T) {
	t.Parallel()

	if got := DefaultIconSet(); got != UnicodeIconSet() {
		t.Fatalf("DefaultIconSet() = %#v, want unicode preset", got)
	}
	if got := normalizeIconSet(IconSet{}); got != UnicodeIconSet() {
		t.Fatalf("normalizeIconSet(zero) = %#v, want unicode preset", got)
	}
	for name, want := range map[string]IconSet{
		config.ThemeIconsASCII:   ASCIIIconSet(),
		config.ThemeIconsUnicode: UnicodeIconSet(),
		config.ThemeIconsNerd:    NerdFontIconSet(),
	} {
		got, ok := IconSetForName(name)
		if !ok || got != want {
			t.Fatalf("IconSetForName(%q) = %#v, %v; want %#v, true", name, got, ok, want)
		}
	}
	if got, ok := IconSetForName("powerline"); ok || got != (IconSet{}) {
		t.Fatalf("IconSetForName(powerline) = %#v, %v; want zero, false", got, ok)
	}

	ascii := ASCIIIconSet()
	for name, value := range map[string]string{
		"PaneIdle":      ascii.PaneIdle,
		"PaneActive":    ascii.PaneActive,
		"PaneBusy":      ascii.PaneBusy,
		"PaneLead":      ascii.PaneLead,
		"PaneEscalated": ascii.PaneEscalated,
		"PaneStuck":     ascii.PaneStuck,
		"PaneNameOpen":  ascii.PaneNameOpen,
		"PaneNameClose": ascii.PaneNameClose,
		"RemoteHost":    ascii.RemoteHost,
		"PR":            ascii.PR,
		"Issue":         ascii.Issue,
		"Task":          ascii.Task,
		"CopyMode":      ascii.CopyMode,
	} {
		if len(value) != 1 {
			t.Fatalf("ASCIIIconSet().%s = %q, want single-character fallback", name, value)
		}
		r, _ := utf8.DecodeRuneInString(value)
		if r < 0x20 || r > 0x7e {
			t.Fatalf("ASCIIIconSet().%s = %q, want printable ASCII", name, value)
		}
	}

	for name, value := range iconSetFieldMap(NerdFontIconSet()) {
		r, size := utf8.DecodeRuneInString(value)
		if size == 0 || !isPrivateUseAreaRune(r) {
			t.Fatalf("NerdFontIconSet().%s = %q, want Nerd Font private-use glyph", name, value)
		}
		if width := runewidth.StringWidth(value); width != 1 {
			t.Fatalf("NerdFontIconSet().%s width = %d, want 1", name, width)
		}
	}

	comp := NewCompositor(20, 3, "test")
	comp.SetIconSet(DefaultIconSet())
	if got := comp.IconSet(); got != DefaultIconSet() {
		t.Fatalf("IconSet after no-op SetIconSet(default) = %#v, want default", got)
	}

	sentinel := IconSet{PaneLead: "L"}
	lead := &statusPaneData{lead: true}
	if got := paneStatusStateIcon(false, lead, sentinel); got != "L" {
		t.Fatalf("paneStatusStateIcon(lead) = %q, want L", got)
	}
}

func iconSetFieldMap(icons IconSet) map[string]string {
	return map[string]string{
		"PaneIdle":      icons.PaneIdle,
		"PaneActive":    icons.PaneActive,
		"PaneBusy":      icons.PaneBusy,
		"PaneLead":      icons.PaneLead,
		"PaneEscalated": icons.PaneEscalated,
		"PaneStuck":     icons.PaneStuck,
		"RemoteHost":    icons.RemoteHost,
		"PR":            icons.PR,
		"Issue":         icons.Issue,
		"Task":          icons.Task,
		"CopyMode":      icons.CopyMode,
	}
}

func isPrivateUseAreaRune(r rune) bool {
	return (r >= 0xe000 && r <= 0xf8ff) ||
		(r >= 0xf0000 && r <= 0xffffd) ||
		(r >= 0x100000 && r <= 0x10fffd)
}
