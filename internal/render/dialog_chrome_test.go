package render

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/weill-labs/amux/internal/config"
)

// buildSampleChrome returns a chrome with a title, a query line, a few rows
// (one selected), and a footer — enough to exercise every cell kind.
func buildSampleChrome() dialogChrome {
	return dialogChrome{
		title:     "Choose Window",
		showQuery: true,
		query:     "ed",
		rows: []dialogRow{
			{text: "pane-1", desc: "~/github/amux", kind: dialogRowNormal},
			{text: "pane-2", desc: "editor", kind: dialogRowSelected},
			{text: "pane-3", desc: "logs", kind: dialogRowNormal},
		},
		footer: []footerHint{{key: "↑/↓", label: "choose"}, {key: "enter", label: "open"}, {key: "esc", label: "close"}},
	}
}

// TestDialogChromeRoundedBorders verifies the box uses rounded Unicode corners
// rather than the old ASCII "+ - |" set.
func TestDialogChromeRoundedBorders(t *testing.T) {
	t.Parallel()

	g := NewScreenGrid(80, 24)
	rect := buildSampleChrome().place(g, defaultDialogStyles())

	if rect.W <= 0 || rect.H <= 0 {
		t.Fatalf("place returned empty rect: %+v", rect)
	}

	corners := []struct {
		name string
		x, y int
		want string
	}{
		{"top-left", rect.X, rect.Y, "╭"},
		{"top-right", rect.X + rect.W - 1, rect.Y, "╮"},
		{"bottom-left", rect.X, rect.Y + rect.H - 1, "╰"},
		{"bottom-right", rect.X + rect.W - 1, rect.Y + rect.H - 1, "╯"},
	}
	for _, c := range corners {
		if got := g.Get(c.x, c.y).Char; got != c.want {
			t.Errorf("%s corner at (%d,%d) = %q, want %q", c.name, c.x, c.y, got, c.want)
		}
	}
}

// TestDialogChromeBordersAtDisplayColumns is the regression guard for the
// byte-indexed placement loop. Multi-byte border glyphs (╭ = 3 bytes) must
// land at the correct *display column*, so the right border │ must appear at
// exactly X+W-1 on every interior row — never shifted by byte width.
func TestDialogChromeBordersAtDisplayColumns(t *testing.T) {
	t.Parallel()

	g := NewScreenGrid(80, 24)
	rect := buildSampleChrome().place(g, defaultDialogStyles())

	for y := rect.Y; y < rect.Y+rect.H; y++ {
		if got := g.Get(rect.X, y).Char; got != "╭" && got != "│" && got != "╰" {
			t.Errorf("left border at (%d,%d) = %q, want a vertical/corner glyph", rect.X, y, got)
		}
		if got := g.Get(rect.X+rect.W-1, y).Char; got != "╮" && got != "│" && got != "╯" {
			t.Errorf("right border at (%d,%d) = %q, want a vertical/corner glyph", rect.X+rect.W-1, y, got)
		}
	}
}

// TestDialogChromeInteriorPadding verifies content is not flush against the
// border — there is a one-space gutter after the left border.
func TestDialogChromeInteriorPadding(t *testing.T) {
	t.Parallel()

	g := NewScreenGrid(80, 24)
	rect := buildSampleChrome().place(g, defaultDialogStyles())

	// First interior content row (the query line) sits at Y+1.
	row := rect.Y + 1
	if got := g.Get(rect.X, row).Char; got != "│" {
		t.Fatalf("left border at row %d = %q, want │", row, got)
	}
	if got := g.Get(rect.X+1, row).Char; got != " " {
		t.Errorf("interior gutter at (%d,%d) = %q, want a space", rect.X+1, row, got)
	}
}

// TestDialogChromeSelectionUsesMauveAccent verifies the selected row is painted
// with a Mauve background fill rather than the old inverted TextColor bar.
func TestDialogChromeSelectionUsesMauveAccent(t *testing.T) {
	t.Parallel()

	g := NewScreenGrid(80, 24)
	rect := buildSampleChrome().place(g, defaultDialogStyles())

	want := hexToColor(config.MauveHex)
	found := false
	for y := rect.Y; y < rect.Y+rect.H && !found; y++ {
		for x := rect.X + 1; x < rect.X+rect.W-1; x++ {
			if g.Get(x, y).Style.Bg == want {
				found = true
				break
			}
		}
	}
	if !found {
		t.Errorf("no cell painted with Mauve (%s) background; selection accent missing", config.MauveHex)
	}
}

// TestDialogChromeFooterRendered verifies the keybinding footer appears inside
// the box, just above the bottom border.
func TestDialogChromeFooterRendered(t *testing.T) {
	t.Parallel()

	g := NewScreenGrid(80, 24)
	chrome := buildSampleChrome()
	rect := chrome.place(g, defaultDialogStyles())

	boxText := gridRectToText(g, rect.X, rect.Y, rect.W, rect.H)
	if !strings.Contains(boxText, "esc close") {
		t.Errorf("footer text missing from box:\n%s", boxText)
	}
}

// TestDialogChromeToggleRendered verifies the title-bar radio toggle renders
// both options with the selected one marked, using IconSet glyphs.
func TestDialogChromeToggleRendered(t *testing.T) {
	t.Parallel()

	chrome := buildSampleChrome()
	chrome.toggle = newDialogToggle(0, "Tree", "Window")

	g := NewScreenGrid(80, 24)
	rect := chrome.place(g, defaultDialogStyles())

	topRow := gridRectToText(g, rect.X, rect.Y, rect.W, 1)
	for _, want := range []string{"Tree", "Window", DefaultIconSet().ToggleOn, DefaultIconSet().ToggleOff} {
		if !strings.Contains(topRow, want) {
			t.Errorf("title row missing %q: %q", want, topRow)
		}
	}
}

// TestDialogChromeIconColorAndRule verifies a content row renders a colored
// leading icon, a colored name, a dim desc, and that a rule row fills the
// trailing width with ─.
func TestDialogChromeIconColorAndRule(t *testing.T) {
	t.Parallel()

	mauve := hexToColor(config.MauveHex)
	chrome := dialogChrome{
		title:     "Choose Window",
		showQuery: true,
		rows: []dialogRow{
			{text: "amux", kind: dialogRowHeader, rule: true},
			{text: "pane-1", desc: "main", icon: "●", iconColor: mauve, textColor: mauve},
		},
		footer: []footerHint{{key: "esc", label: "close"}},
	}

	g := NewScreenGrid(60, 16)
	rect := chrome.place(g, defaultDialogStyles())
	body := gridRectToText(g, rect.X, rect.Y, rect.W, rect.H)

	for _, want := range []string{"amux", "──", "● pane-1", "main"} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered chrome missing %q:\n%s", want, body)
		}
	}

	// The icon cell should carry its own color.
	iconFound := false
	for y := rect.Y; y < rect.Y+rect.H && !iconFound; y++ {
		for x := rect.X; x < rect.X+rect.W; x++ {
			if c := g.Get(x, y); c.Char == "●" && c.Style.Fg == mauve {
				iconFound = true
				break
			}
		}
	}
	if !iconFound {
		t.Error("expected a ● icon cell painted with its iconColor")
	}
}

// TestDialogChromeListSpacers verifies blank separator rows sit between the
// query/header and the list, and between the list and the footer.
func TestDialogChromeListSpacers(t *testing.T) {
	t.Parallel()

	g := NewScreenGrid(60, 20)
	rect := buildSampleChrome().place(g, defaultDialogStyles())

	// Row layout: top, query, spacer, rows×3, spacer, footer, bottom.
	queryRow := rect.Y + 1
	spacerBefore := queryRow + 1
	firstRow := spacerBefore + 1
	footerRow := rect.Y + rect.H - 2
	spacerAfter := footerRow - 1

	interiorBlank := func(y int) bool {
		for x := rect.X + 1; x < rect.X+rect.W-1; x++ {
			if c := g.Get(x, y).Char; c != " " && c != "" {
				return false
			}
		}
		return true
	}

	if !interiorBlank(spacerBefore) {
		t.Errorf("expected blank spacer row between query and list at y=%d:\n%s", spacerBefore, gridRectToText(g, rect.X, spacerBefore, rect.W, 1))
	}
	if !interiorBlank(spacerAfter) {
		t.Errorf("expected blank spacer row between list and footer at y=%d:\n%s", spacerAfter, gridRectToText(g, rect.X, spacerAfter, rect.W, 1))
	}
	// The first list row should have content (not blank).
	if interiorBlank(firstRow) {
		t.Errorf("first list row at y=%d should not be blank", firstRow)
	}
}

// TestDialogChromeScrollbar verifies that when the row set overflows the
// visible window, a scrollbar thumb (┃) and track (│) render inside the right
// edge of the body.
func TestDialogChromeScrollbar(t *testing.T) {
	t.Parallel()

	chrome := buildSampleChrome()
	// 3 visible rows out of 12 total, window starting at offset 3.
	chrome.scroll = &dialogScroll{total: 12, offset: 3, visible: len(chrome.rows)}

	g := NewScreenGrid(80, 24)
	rect := chrome.place(g, defaultDialogStyles())

	col := rect.X + rect.W - 2 // gutter just inside the right border
	var thumb, track bool
	for y := rect.Y; y < rect.Y+rect.H; y++ {
		switch g.Get(col, y).Char {
		case "┃":
			thumb = true
		case "│":
			track = true
		}
	}
	if !thumb {
		t.Error("expected a scrollbar thumb (┃) inside the right edge")
	}
	if !track {
		t.Error("expected a scrollbar track (│) inside the right edge")
	}
}

// TestDialogChromeFooterKeysBold verifies footer key glyphs are bold while the
// dim labels are not — the crush-style two-weight footer.
func TestDialogChromeFooterKeysBold(t *testing.T) {
	t.Parallel()

	g := NewScreenGrid(80, 24)
	rect := buildSampleChrome().place(g, defaultDialogStyles())

	footerY := rect.Y + rect.H - 2 // footer sits just above the bottom border
	keyBold, labelPlain := false, false
	for x := rect.X + 1; x < rect.X+rect.W-1; x++ {
		cell := g.Get(x, footerY)
		switch cell.Char {
		case "e", "n", "t", "r", "c": // glyphs within "enter"/"esc" keys
			if cell.Style.Attrs&uv.AttrBold != 0 {
				keyBold = true
			}
		case "o", "p": // glyphs within the "open"/"choose" labels
			if cell.Style.Attrs&uv.AttrBold == 0 {
				labelPlain = true
			}
		}
	}
	if !keyBold {
		t.Error("expected at least one bold key glyph in footer")
	}
	if !labelPlain {
		t.Error("expected label glyphs to be non-bold in footer")
	}
}

// TestDialogChromeTitleRendered verifies the title is embedded in the top border row.
func TestDialogChromeTitleRendered(t *testing.T) {
	t.Parallel()

	g := NewScreenGrid(80, 24)
	rect := buildSampleChrome().place(g, defaultDialogStyles())

	topRow := gridRectToText(g, rect.X, rect.Y, rect.W, 1)
	if !strings.Contains(topRow, "Choose Window") {
		t.Errorf("title missing from top border row: %q", topRow)
	}
}
