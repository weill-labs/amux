package render

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

type statusPaneData struct {
	id            uint32
	name          string
	trackedPRs    []proto.TrackedPR
	trackedIssues []proto.TrackedIssue
	issue         string
	host          string
	task          string
	color         string
	copyMode      bool
	copySearch    string
	idle          bool
	lead          bool
	screen        string
	cursorHidden  bool
}

func (p *statusPaneData) RenderScreen(bool) string { return p.screen }
func (p *statusPaneData) CellAt(col, row int, active bool) ScreenCell {
	return ScreenCell{Char: " ", Width: 1}
}
func (p *statusPaneData) CursorPos() (int, int)               { return 0, 0 }
func (p *statusPaneData) CursorHidden() bool                  { return p.cursorHidden }
func (p *statusPaneData) ID() uint32                          { return p.id }
func (p *statusPaneData) Name() string                        { return p.name }
func (p *statusPaneData) TrackedPRs() []proto.TrackedPR       { return p.trackedPRs }
func (p *statusPaneData) TrackedIssues() []proto.TrackedIssue { return p.trackedIssues }
func (p *statusPaneData) Issue() string                       { return p.issue }
func (p *statusPaneData) Host() string                        { return p.host }
func (p *statusPaneData) Task() string                        { return p.task }
func (p *statusPaneData) Color() string                       { return p.color }
func (p *statusPaneData) Minimized() bool                     { return false }
func (p *statusPaneData) Idle() bool                          { return p.idle }
func (p *statusPaneData) IsLead() bool                        { return p.lead }
func (p *statusPaneData) InCopyMode() bool                    { return p.copyMode }
func (p *statusPaneData) CopyModeSearch() string              { return p.copySearch }
func (p *statusPaneData) HasCursorBlock() bool                { return false }
func (p *statusPaneData) CopyModeOverlay() *proto.ViewportOverlay {
	return nil
}

func TestRenderPaneStatusVariants(t *testing.T) {
	t.Parallel()

	cell := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, 60, 3)
	tests := []struct {
		name     string
		active   bool
		pane     *statusPaneData
		contains []string
	}{
		{
			name:   "active copy mode remote pane",
			active: true,
			pane: &statusPaneData{
				id:         1,
				name:       "pane-1",
				host:       "gpu-box",
				task:       "train",
				color:      config.TextColorHex,
				copyMode:   true,
				copySearch: "/query",
			},
			contains: []string{"●", "[pane-1]", "[copy] /query", "@gpu-box", "train"},
		},
		{
			name:   "inactive idle pane",
			active: false,
			pane: &statusPaneData{
				id:    2,
				name:  "pane-2",
				color: config.TextColorHex,
				idle:  true,
			},
			contains: []string{"◇", "[pane-2]"},
		},
		{
			name:   "inactive busy pane",
			active: false,
			pane: &statusPaneData{
				id:    3,
				name:  "pane-3",
				color: config.TextColorHex,
			},
			contains: []string{"○", "[pane-3]"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			buf := strings.Builder{}
			renderPaneStatus(&buf, cell, tt.active, tt.pane)
			line := MaterializeGrid(buf.String(), cell.W, 1)
			for _, want := range tt.contains {
				if !strings.Contains(line, want) {
					t.Fatalf("status line %q missing %q", line, want)
				}
			}
		})
	}
}

func TestRenderPaneStatusShowsMetadata(t *testing.T) {
	t.Parallel()

	cell := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, 60, 3)
	buf := strings.Builder{}
	renderPaneStatus(&buf, cell, true, &statusPaneData{
		id:   1,
		name: "pane-1",
		trackedPRs: []proto.TrackedPR{
			{Number: 42},
			{Number: 314},
		},
		trackedIssues: []proto.TrackedIssue{
			{ID: "LAB-339"},
		},
		host:  "gpu-box",
		task:  "train",
		color: config.TextColorHex,
	})

	line := MaterializeGrid(buf.String(), cell.W, 1)
	for _, want := range []string{"[pane-1]", "#42, #314, LAB-339", "@gpu-box", "train"} {
		if !strings.Contains(line, want) {
			t.Fatalf("status line %q missing %q", line, want)
		}
	}
}

func TestRenderPaneStatusStylesCompletedMetadataInANSIOnly(t *testing.T) {
	t.Parallel()

	cell := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, 60, 3)
	buf := strings.Builder{}
	renderPaneStatus(&buf, cell, true, &statusPaneData{
		id:   1,
		name: "pane-1",
		trackedPRs: []proto.TrackedPR{
			{Number: 42, Status: proto.TrackedStatusCompleted},
			{Number: 314, Status: proto.TrackedStatusActive},
		},
		trackedIssues: []proto.TrackedIssue{
			{ID: "LAB-450", Status: proto.TrackedStatusCompleted},
		},
		color: config.TextColorHex,
	})

	raw := buf.String()
	if !regexp.MustCompile(`\x1b\[[0-9;]*9m#`).MatchString(raw) {
		t.Fatalf("raw status output missing completed PR styling:\n%q", raw)
	}
	if !regexp.MustCompile(`\x1b\[[0-9;]*9mL`).MatchString(raw) {
		t.Fatalf("raw status output missing completed issue styling:\n%q", raw)
	}

	line := MaterializeGrid(raw, cell.W, 1)
	if !strings.Contains(line, "#314, #42, LAB-450") {
		t.Fatalf("materialized status line %q should keep plain metadata text", line)
	}
}

func TestRenderPaneStatusTruncatesMetadata(t *testing.T) {
	t.Parallel()

	cell := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, 34, 3)
	buf := strings.Builder{}
	renderPaneStatus(&buf, cell, false, &statusPaneData{
		id:   1,
		name: "pane-1",
		trackedPRs: []proto.TrackedPR{
			{Number: 101},
			{Number: 202},
		},
		trackedIssues: []proto.TrackedIssue{
			{ID: "LAB-339"},
			{ID: "LAB-340"},
		},
		host:  "gpu",
		task:  "sync",
		color: config.TextColorHex,
	})

	line := strings.TrimRight(MaterializeGrid(buf.String(), cell.W, 1), " ")
	for _, want := range []string{"#101", "…", "@gpu", "sync"} {
		if !strings.Contains(line, want) {
			t.Fatalf("status line %q missing %q", line, want)
		}
	}
	if strings.Contains(line, ", …") {
		t.Fatalf("status line %q should trim trailing separators before ellipsis", line)
	}
}

func TestRenderPaneStatusClipsLongTaskToPaneWidth(t *testing.T) {
	t.Parallel()

	cell := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, 24, 3)
	buf := strings.Builder{}
	renderPaneStatus(&buf, cell, true, &statusPaneData{
		id:    1,
		name:  "pane-1",
		task:  "sync-build-with-a-very-long-name",
		color: config.TextColorHex,
	})

	line := strings.SplitN(MaterializeGrid(buf.String(), 48, 1), "\n", 2)[0]
	visible := string([]rune(line)[:cell.W])
	if !strings.Contains(visible, "…") {
		t.Fatalf("visible status line %q should include an ellipsis when clipped", visible)
	}
	for col, ch := range []rune(line)[cell.W:] {
		if ch != ' ' {
			t.Fatalf("status line spilled past pane width at col %d: %q in %q", cell.W+col, string(ch), line)
		}
	}
}

func TestRenderPaneStatusFallsBackToPlainIssueKV(t *testing.T) {
	t.Parallel()

	cell := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, 60, 3)
	buf := strings.Builder{}
	renderPaneStatus(&buf, cell, true, &statusPaneData{
		id:    1,
		name:  "pane-1",
		issue: "LAB-698",
		trackedPRs: []proto.TrackedPR{
			{Number: 42},
		},
		color: config.TextColorHex,
	})

	line := MaterializeGrid(buf.String(), cell.W, 1)
	for _, want := range []string{"[pane-1]", "#42", "LAB-698"} {
		if !strings.Contains(line, want) {
			t.Fatalf("status line %q missing %q", line, want)
		}
	}
}

func TestTruncateRunes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		max  int
		want string
	}{
		{in: "abc", max: 0, want: ""},
		{in: "abc", max: 1, want: "a"},
		{in: "abcdef", max: 4, want: "abc…"},
		{in: "漢字テスト", max: 3, want: "漢字…"},
		{in: "ok", max: 5, want: "ok"},
	}

	for _, tt := range tests {
		if got := truncateRunes(tt.in, tt.max); got != tt.want {
			t.Fatalf("truncateRunes(%q, %d) = %q, want %q", tt.in, tt.max, got, tt.want)
		}
	}
}

func TestChooserOverlayLayoutAndRendering(t *testing.T) {
	t.Parallel()

	overlay := &ChooserOverlay{
		Title: "choose-window",
		Query: "pane",
		Rows: []ChooserOverlayRow{
			{Text: "1:editor", Selectable: true},
			{Text: "2:logs", Selectable: false},
			{Text: "3:gpu", Selectable: true},
			{Text: "4:wide-row-with-long-name", Selectable: true},
		},
		Selected: 2,
	}

	grid := NewScreenGrid(60, 16)
	buildChooserOverlayCells(grid, overlay)

	// The selected row (index 2) must carry the Mauve accent background.
	mauve := hexToColor(config.MauveHex)
	found := false
	for y := 0; y < grid.Height && !found; y++ {
		for x := 0; x < grid.Width; x++ {
			if grid.Get(x, y).Style.Bg == mauve {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatal("selected chooser row should use the Mauve accent background")
	}

	buf := strings.Builder{}
	renderChooserOverlay(&buf, 60, 16, overlay)
	got := MaterializeGrid(buf.String(), 60, 16)
	for _, want := range []string{"choose-window", "> pane", "1:editor", "3:gpu"} {
		if !strings.Contains(got, want) {
			t.Fatalf("chooser render missing %q in:\n%s", want, got)
		}
	}
}

func TestPaneOverlayPlacementAndRendering(t *testing.T) {
	t.Parallel()

	left := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, 6, 5)
	right := mux.NewLeaf(&mux.Pane{ID: 2}, 7, 0, 2, 5)
	root := &mux.LayoutCell{
		X: 0, Y: 0, W: 9, H: 5,
		Dir:      mux.SplitVertical,
		Children: []*mux.LayoutCell{left, right},
	}
	left.Parent = root
	right.Parent = root

	if badge, _, _ := paneOverlayPlacement(left, "a"); badge != "[a]" {
		t.Fatalf("wide pane badge = %q, want %q", badge, "[a]")
	}
	if badge, _, _ := paneOverlayPlacement(right, "b"); badge != "b" {
		t.Fatalf("narrow pane badge = %q, want %q", badge, "b")
	}

	labels := []PaneOverlayLabel{{PaneID: 1, Label: "a"}, {PaneID: 2, Label: "b"}}
	lookup := func(id uint32) PaneData {
		return &statusPaneData{id: id, name: "pane", color: config.TextColorHex}
	}

	grid := NewScreenGrid(9, 5)
	buildPaneOverlayCells(grid, root, lookup, labels)
	if grid.Get(1, 3).Char == "" {
		t.Fatal("pane overlay should populate the grid for visible labels")
	}

	buf := strings.Builder{}
	renderPaneOverlay(&buf, root, lookup, labels)
	got := MaterializeGrid(buf.String(), 9, 5)
	if !strings.Contains(got, "[a]") || !strings.Contains(got, "b") {
		t.Fatalf("pane overlay render missing labels:\n%s", got)
	}
}

func TestClipLineAndMaterializeGridHandleWideChars(t *testing.T) {
	t.Parallel()

	hyperlink := "\x1b]8;;https://example.com\x07界B\x1b]8;;\x07"
	if got := MaterializeGrid(clipLine(hyperlink, 2), 2, 1); got != "界" {
		t.Fatalf("clipLine wide-char result = %q, want %q", got, "界")
	}

	if got := MaterializeGrid("界A", 4, 1); got != "界 A" {
		t.Fatalf("MaterializeGrid wide chars = %q, want %q", got, "界 A")
	}
	if got := MaterializeGrid("A\tB", 10, 1); got != "A       B" {
		t.Fatalf("MaterializeGrid tab expansion = %q, want %q", got, "A       B")
	}
}

func TestCompositorUtilitySettersAndClearPrevGrid(t *testing.T) {
	t.Parallel()

	comp := NewCompositor(20, 6, "old")
	if got := comp.LayoutHeight(); got != 5 {
		t.Fatalf("LayoutHeight() = %d, want 5", got)
	}

	comp.SetSessionName("new")
	comp.SetWindows([]WindowInfo{{Index: 1, Name: "main", IsActive: true}})
	if comp.sessionName != "new" || len(comp.windows) != 1 {
		t.Fatalf("compositor setters did not persist state: %+v", comp)
	}

	root := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, 20, 5)
	lookup := func(uint32) PaneData {
		return &statusPaneData{id: 1, name: "pane-1", color: config.TextColorHex}
	}
	comp.RenderDiff(root, 1, lookup)
	if comp.LastGrid() == nil {
		t.Fatal("RenderDiff should populate prevGrid")
	}
	comp.ClearPrevGrid()
	if comp.LastGrid() != nil {
		t.Fatal("ClearPrevGrid should clear prevGrid")
	}
}

func TestBuildChooserOverlayCellsSelectedRowUsesDistinctStyle(t *testing.T) {
	t.Parallel()

	overlay := &ChooserOverlay{
		Title: "chooser",
		Rows: []ChooserOverlayRow{
			{Text: "first", Selectable: true},
			{Text: "second", Selectable: true},
		},
		Selected: 1,
	}
	grid := NewScreenGrid(30, 16)
	buildChooserOverlayCells(grid, overlay)

	selBg := hexToColor(config.MauveHex)
	normBg := hexToColor(config.Surface0Hex)
	var sawSelected, sawNormal bool
	for y := 0; y < grid.Height; y++ {
		for x := 0; x < grid.Width; x++ {
			switch grid.Get(x, y).Style.Bg {
			case selBg:
				sawSelected = true
			case normBg:
				sawNormal = true
			}
		}
	}
	if !sawSelected {
		t.Fatal("selected chooser row should use the Mauve accent background")
	}
	if !sawNormal {
		t.Fatal("expected normal rows on the Surface0 background")
	}
	if sameColor(selBg, normBg) {
		t.Fatal("selected and normal backgrounds must differ")
	}
}

func sameColor(a, b interface {
	RGBA() (uint32, uint32, uint32, uint32)
}) bool {
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

func TestBuildPaneOverlayCellsUsesPaneColorWhenAvailable(t *testing.T) {
	t.Parallel()

	root := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, 8, 4)
	grid := NewScreenGrid(8, 4)
	buildPaneOverlayCells(grid, root, func(uint32) PaneData {
		return &statusPaneData{id: 1, color: config.GreenHex}
	}, []PaneOverlayLabel{{PaneID: 1, Label: "g"}})

	cell := grid.Get(2, 2)
	if cell.Char == "" {
		t.Fatal("pane overlay badge should write visible cells")
	}
	if cell.Style.Fg == nil {
		t.Fatal("pane overlay badge should colorize the badge")
	}
	want := hexToColor(config.GreenHex)
	if got, ok := cell.Style.Fg.(interface {
		RGBA() (uint32, uint32, uint32, uint32)
	}); !ok || !sameColor(got, want) {
		t.Fatal("pane overlay badge should use the pane color when available")
	}
}

func TestBuildStatusCellsPreservesRemoteMetadata(t *testing.T) {
	t.Parallel()

	cell := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, 40, 4)
	grid := NewScreenGrid(40, 4)
	buildStatusCells(grid, cell, false, &statusPaneData{
		id:    1,
		name:  "pane-1",
		host:  "remote-host",
		task:  "sync",
		color: config.TextColorHex,
	})

	var row strings.Builder
	for x := 0; x < 40; x++ {
		ch := grid.Get(x, 0).Char
		if ch == "" {
			ch = " "
		}
		row.WriteString(ch)
	}
	line := strings.TrimRight(row.String(), " ")
	for _, want := range []string{"@remote-host", "sync"} {
		if !strings.Contains(line, want) {
			t.Fatalf("status row %q missing %q", line, want)
		}
	}
}

func TestBuildStatusCellsShowsPaneMetadata(t *testing.T) {
	t.Parallel()

	cell := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, 56, 4)
	grid := NewScreenGrid(56, 4)
	buildStatusCells(grid, cell, false, &statusPaneData{
		id:   1,
		name: "pane-1",
		trackedPRs: []proto.TrackedPR{
			{Number: 42},
			{Number: 314},
		},
		trackedIssues: []proto.TrackedIssue{
			{ID: "LAB-339"},
		},
		host:  "remote-host",
		task:  "sync",
		color: config.TextColorHex,
	})

	var row strings.Builder
	for x := 0; x < 56; x++ {
		ch := grid.Get(x, 0).Char
		if ch == "" {
			ch = " "
		}
		row.WriteString(ch)
	}
	line := strings.TrimRight(row.String(), " ")
	for _, want := range []string{"#42, #314, LAB-339", "@remote-host", "sync"} {
		if !strings.Contains(line, want) {
			t.Fatalf("status row %q missing %q", line, want)
		}
	}
}

func TestBuildStatusCellsStylesCompletedMetadata(t *testing.T) {
	t.Parallel()

	cell := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, 56, 4)
	grid := NewScreenGrid(56, 4)
	buildStatusCells(grid, cell, true, &statusPaneData{
		id:   1,
		name: "pane-1",
		trackedPRs: []proto.TrackedPR{
			{Number: 42, Status: proto.TrackedStatusCompleted},
			{Number: 314, Status: proto.TrackedStatusActive},
		},
		trackedIssues: []proto.TrackedIssue{
			{ID: "LAB-450", Status: proto.TrackedStatusCompleted},
		},
		color: config.TextColorHex,
	})

	var row strings.Builder
	for x := 0; x < 56; x++ {
		ch := grid.Get(x, 0).Char
		if ch == "" {
			ch = " "
		}
		row.WriteString(ch)
	}
	line := row.String()
	findRuneIndex := func(haystack, needle string) int {
		hr := []rune(haystack)
		nr := []rune(needle)
		for i := 0; i+len(nr) <= len(hr); i++ {
			if string(hr[i:i+len(nr)]) == needle {
				return i
			}
		}
		return -1
	}

	for _, token := range []string{"#42", "LAB-450"} {
		start := findRuneIndex(line, token)
		if start < 0 {
			t.Fatalf("status row %q missing %q", line, token)
		}
		for offset := 0; offset < len([]rune(token)); offset++ {
			if grid.Get(start+offset, 0).Style.Attrs&uv.AttrStrikethrough == 0 {
				t.Fatalf("cell %q at x=%d should be strikethrough", token, start+offset)
			}
		}
	}

	start := findRuneIndex(line, "#314")
	if start < 0 {
		t.Fatalf("status row %q missing %q", line, "#314")
	}
	for offset := 0; offset < len([]rune("#314")); offset++ {
		if grid.Get(start+offset, 0).Style.Attrs&uv.AttrStrikethrough != 0 {
			t.Fatalf("active metadata cell at x=%d should not be strikethrough", start+offset)
		}
	}
}

func TestBuildStatusCellsClipsLongTaskToPaneWidth(t *testing.T) {
	t.Parallel()

	cell := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, 24, 4)
	grid := NewScreenGrid(24, 4)
	buildStatusCells(grid, cell, true, &statusPaneData{
		id:    1,
		name:  "pane-1",
		task:  "sync-build-with-a-very-long-name",
		color: config.TextColorHex,
	})

	var row strings.Builder
	for x := 0; x < 24; x++ {
		ch := grid.Get(x, 0).Char
		if ch == "" {
			ch = " "
		}
		row.WriteString(ch)
	}
	line := strings.TrimRight(row.String(), " ")
	if !strings.Contains(line, "sync") {
		t.Fatalf("status row %q should keep the task prefix", line)
	}
	if !strings.Contains(line, "…") {
		t.Fatalf("status row %q should include an ellipsis when clipped", line)
	}
}

func TestBuildPowerlineStatusCellsClipsLongTaskToPaneWidth(t *testing.T) {
	t.Parallel()

	const paneWidth = 24
	cell := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, paneWidth, 4)
	grid := NewScreenGrid(paneWidth+1, 4)
	grid.Set(paneWidth, 0, ScreenCell{Char: "│", Width: 1})
	buildStatusCellsPressedWithIconsAndStyle(grid, cell, true, false, &statusPaneData{
		id:    1,
		name:  "pane-1",
		task:  "sync-build-with-a-very-long-name",
		color: config.TextColorHex,
	}, DefaultIconSet(), "powerline")

	var row strings.Builder
	for x := 0; x < paneWidth; x++ {
		ch := grid.Get(x, 0).Char
		if ch == "" {
			ch = " "
		}
		row.WriteString(ch)
	}
	line := strings.TrimRight(row.String(), " ")
	if !strings.Contains(line, "sync") {
		t.Fatalf("status row %q should keep the task prefix", line)
	}
	if !strings.Contains(line, "…") {
		t.Fatalf("status row %q should include an ellipsis when clipped", line)
	}
	if got := grid.Get(paneWidth, 0).Char; got != "│" {
		t.Fatalf("cell past pane edge = %q, want sentinel border", got)
	}
}

func TestBuildPowerlineStatusCellsUsesSeparatorColorTransitions(t *testing.T) {
	t.Parallel()

	const paneWidth = 48
	cell := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, paneWidth, 4)
	grid := NewScreenGrid(paneWidth, 4)
	buildStatusCellsPressedWithIconsAndStyle(grid, cell, true, false, &statusPaneData{
		id:    1,
		name:  "pane-1",
		host:  "gpu",
		task:  "build",
		color: config.AccentColor(0),
		trackedPRs: []proto.TrackedPR{
			{Number: 42},
		},
	}, DefaultIconSet(), "powerline")

	separators := powerlineSeparatorCells(grid, paneWidth)
	if len(separators) < 3 {
		t.Fatalf("powerline status row has %d separators, want at least 3", len(separators))
	}

	assertCellColors(t, separators[0], config.AccentColor(0), config.Surface1Hex)
	assertCellColors(t, separators[1], config.Surface1Hex, config.GreenHex)
	assertCellColors(t, separators[2], config.GreenHex, config.Surface1Hex)
}

func TestRenderPaneStatusPowerlineFullANSI(t *testing.T) {
	t.Parallel()

	cell := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, 64, 4)
	buf := strings.Builder{}
	renderPaneStatusPressedWithProfileAndIconsAndStyle(&buf, cell, true, true, &statusPaneData{
		id:    1,
		name:  "pane-1",
		host:  "gpu",
		task:  "build",
		color: config.AccentColor(0),
		trackedIssues: []proto.TrackedIssue{
			{ID: "LAB-1651"},
		},
	}, defaultColorProfile, DefaultIconSet(), config.StatusStylePowerline)

	raw := buf.String()
	if !strings.Contains(raw, powerlineRightSeparator) {
		t.Fatalf("raw status output missing powerline separator:\n%q", raw)
	}
	if !strings.Contains(raw, bgHexSequence(config.Surface1Hex, defaultColorProfile)) {
		t.Fatalf("pressed powerline status should use pressed background in ANSI:\n%q", raw)
	}

	line := MaterializeGrid(raw, cell.W, 1)
	if !strings.Contains(line, "pane-1") {
		t.Fatalf("powerline status line %q missing pane name", line)
	}
	if strings.Contains(line, "[pane-1]") {
		t.Fatalf("powerline status line should omit pane name brackets: %q", line)
	}
	for _, want := range []string{"LAB-1651", "@gpu", "build", powerlineRightSeparator} {
		if !strings.Contains(line, want) {
			t.Fatalf("powerline status line %q missing %q", line, want)
		}
	}
}

func TestBuildPowerlineStatusCellsStylesCompletedMetadata(t *testing.T) {
	t.Parallel()

	cell := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, 64, 4)
	grid := NewScreenGrid(64, 4)
	buildStatusCellsPressedWithIconsAndStyle(grid, cell, true, false, &statusPaneData{
		id:   1,
		name: "pane-1",
		trackedPRs: []proto.TrackedPR{
			{Number: 42, Status: proto.TrackedStatusCompleted},
			{Number: 314, Status: proto.TrackedStatusActive},
		},
		trackedIssues: []proto.TrackedIssue{
			{ID: "LAB-450", Status: proto.TrackedStatusCompleted},
		},
		color: config.TextColorHex,
	}, DefaultIconSet(), config.StatusStylePowerline)

	for _, token := range []string{"#42", "LAB-450"} {
		start := findRowLabel(grid, 0, cell.W, token)
		if start < 0 {
			t.Fatalf("status row %q missing %q", gridRowText(grid, 0, cell.W), token)
		}
		for offset := 0; offset < len([]rune(token)); offset++ {
			if grid.Get(start+offset, 0).Style.Attrs&uv.AttrStrikethrough == 0 {
				t.Fatalf("cell %q at x=%d should be strikethrough", token, start+offset)
			}
		}
	}

	start := findRowLabel(grid, 0, cell.W, "#314")
	if start < 0 {
		t.Fatalf("status row %q missing %q", gridRowText(grid, 0, cell.W), "#314")
	}
	for offset := 0; offset < len("#314"); offset++ {
		if grid.Get(start+offset, 0).Style.Attrs&uv.AttrStrikethrough != 0 {
			t.Fatalf("active metadata cell at x=%d should not be strikethrough", start+offset)
		}
	}
}

func TestPowerlineStatusHelpers(t *testing.T) {
	t.Parallel()

	if got := normalizeStatusStyle("made-up"); got != config.StatusStyleCompact {
		t.Fatalf("normalizeStatusStyle invalid = %q, want compact", got)
	}
	if got := statusBarBaseBgHex(true); got != config.Surface1Hex {
		t.Fatalf("statusBarBaseBgHex(true) = %q, want %q", got, config.Surface1Hex)
	}
	if got := alternatePowerlineBgHex(config.Surface1Hex); got != config.Surface0Hex {
		t.Fatalf("alternatePowerlineBgHex(surface1) = %q, want %q", got, config.Surface0Hex)
	}

	segments := []paneStatusSegment{
		{text: " ", role: paneStatusSegmentBackground},
		{text: "name", role: paneStatusSegmentPaneBold},
		{text: " ", role: paneStatusSegmentBackground},
		{text: "", role: paneStatusSegmentText},
	}
	if _, ok := previousVisiblePaneStatusRole(segments, 0); ok {
		t.Fatal("first segment should not have a previous visible role")
	}
	if got, ok := nextVisiblePaneStatusRole(segments, 0); !ok || got != paneStatusSegmentPaneBold {
		t.Fatalf("next role = (%v, %v), want pane bold", got, ok)
	}
	if _, ok := nextVisiblePaneStatusRole(segments, len(segments)-1); ok {
		t.Fatal("last segment should not have a next visible role")
	}
	if got, ok := lastVisiblePaneStatusRole(segments); !ok || got != paneStatusSegmentPaneBold {
		t.Fatalf("last role = (%v, %v), want pane bold", got, ok)
	}
	if _, ok := lastVisiblePaneStatusRole([]paneStatusSegment{{text: " ", role: paneStatusSegmentBackground}}); ok {
		t.Fatal("background-only segments should not have a visible role")
	}

	for _, tt := range []struct {
		role paneStatusSegmentRole
		want string
	}{
		{role: paneStatusSegmentDim, want: config.DimColorHex},
		{role: paneStatusSegmentYellow, want: config.YellowHex},
		{role: paneStatusSegmentRed, want: config.RedHex},
		{role: paneStatusSegmentBackground, want: config.Surface0Hex},
	} {
		if got := panePowerlineRoleBgHex(tt.role, config.BlueHex, config.Surface0Hex); got != tt.want {
			t.Fatalf("panePowerlineRoleBgHex(%v) = %q, want %q", tt.role, got, tt.want)
		}
	}

	cells := appendStyledStatusCells(nil, "⚡", statusCellStyle{fgHex: config.GreenHex, bgHex: config.Surface0Hex})
	if len(cells) != 2 || cells[0].width != 2 || cells[1].width != 0 {
		t.Fatalf("wide status cells = %+v, want visible cell and continuation", cells)
	}
	cells = append(cells, styledStatusCell{char: "skip", width: 0})
	var buf strings.Builder
	writeStyledStatusCellsWithProfile(&buf, cells, defaultColorProfile)
	if strings.Contains(buf.String(), "skip") {
		t.Fatalf("width-zero status cell should not be written: %q", buf.String())
	}

	ansiStyle := statusCellStyleANSIWithProfile(statusCellStyle{
		fgHex:         config.TextColorHex,
		bgHex:         config.Surface0Hex,
		bold:          true,
		strikethrough: true,
	}, defaultColorProfile)
	for _, want := range []string{Bold, StrikeOn} {
		if !strings.Contains(ansiStyle, want) {
			t.Fatalf("status style ANSI %q missing %q", ansiStyle, want)
		}
	}

	narrow := buildPowerlinePaneStatusCells(2, true, false, &statusPaneData{
		id:    1,
		name:  "pane-1",
		color: config.TextColorHex,
	}, DefaultIconSet())
	if len(narrow) != 2 {
		t.Fatalf("narrow powerline cells len = %d, want 2", len(narrow))
	}
}

func powerlineSeparatorCells(grid *ScreenGrid, width int) []ScreenCell {
	var cells []ScreenCell
	for x := 0; x < width; x++ {
		cell := grid.Get(x, 0)
		if cell.Char == powerlineRightSeparator {
			cells = append(cells, cell)
		}
	}
	return cells
}

func assertCellColors(t *testing.T, cell ScreenCell, wantFgHex, wantBgHex string) {
	t.Helper()

	wantFg := hexToColor(wantFgHex)
	gotFg, ok := cell.Style.Fg.(interface {
		RGBA() (uint32, uint32, uint32, uint32)
	})
	if !ok || !sameColor(gotFg, wantFg) {
		t.Fatalf("separator fg = %v, want %s", cell.Style.Fg, wantFgHex)
	}

	wantBg := hexToColor(wantBgHex)
	gotBg, ok := cell.Style.Bg.(interface {
		RGBA() (uint32, uint32, uint32, uint32)
	})
	if !ok || !sameColor(gotBg, wantBg) {
		t.Fatalf("separator bg = %v, want %s", cell.Style.Bg, wantBgHex)
	}
}

func gridRowText(grid *ScreenGrid, y, width int) string {
	var row strings.Builder
	for x := 0; x < width; x++ {
		ch := grid.Get(x, y).Char
		if ch == "" {
			ch = " "
		}
		row.WriteString(ch)
	}
	return row.String()
}

func TestBuildStatusCellsDoesNotStartWideGlyphAtPaneRightEdge(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		pane *statusPaneData
	}{
		{
			name: "wide_task_glyph",
			pane: &statusPaneData{
				id:    1,
				name:  "name-123456789012345678",
				task:  "⌛",
				color: config.TextColorHex,
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for width := 1; width <= 32; width++ {
				width := width
				t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
					t.Parallel()

					cell := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, width, 4)
					grid := NewScreenGrid(width+1, 4)
					grid.Set(width, 0, ScreenCell{Char: "│", Width: 1})
					buildStatusCells(grid, cell, true, tt.pane)

					assertNoWideStatusCellCrossesPane(t, grid, cell)
					if got := grid.Get(width, 0).Char; got != "│" {
						t.Fatalf("cell past pane edge = %q, want sentinel border", got)
					}
				})
			}
		})
	}
}

func assertNoWideStatusCellCrossesPane(t *testing.T, grid *ScreenGrid, cell *mux.LayoutCell) {
	t.Helper()

	for col := 0; col < cell.W; col++ {
		sc := grid.Get(cell.X+col, cell.Y)
		if sc.Width <= 1 {
			continue
		}
		if col+sc.Width > cell.W {
			t.Fatalf("wide status cell %q at pane col %d width %d crosses pane width %d", sc.Char, col, sc.Width, cell.W)
		}
		for offset := 1; offset < sc.Width; offset++ {
			if got := grid.Get(cell.X+col+offset, cell.Y).Width; got != 0 {
				t.Fatalf("wide status cell %q at pane col %d missing continuation at offset %d: width=%d", sc.Char, col, offset, got)
			}
		}
	}
}

func TestBuildGlobalBarCellsTruncatesMessages(t *testing.T) {
	t.Parallel()

	grid := NewScreenGrid(28, 2)
	buildGlobalBarCells(grid, "session", 2, 28, 1, nil, "error 漢字 details", time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC))
	var row strings.Builder
	for x := 0; x < 28; x++ {
		ch := grid.Get(x, 1).Char
		if ch == "" {
			ch = " "
		}
		row.WriteString(ch)
	}
	if !strings.Contains(row.String(), "error") {
		t.Fatalf("global bar row %q should contain the message prefix", row.String())
	}
}

func TestBuildGlobalBarCellsColorsActiveTab(t *testing.T) {
	t.Parallel()

	grid := NewScreenGrid(40, 1)
	buildGlobalBarCells(grid, "session", 2, 40, 0, []WindowInfo{
		{Index: 1, Name: "dev", IsActive: true},
		{Index: 2, Name: "logs", IsActive: false},
	}, "", time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC))

	activeStart := findRowLabel(grid, 0, 40, "1:dev")
	if activeStart < 0 {
		t.Fatal("active tab label missing from global bar")
	}
	inactiveStart := findRowLabel(grid, 0, 40, "2:logs")
	if inactiveStart < 0 {
		t.Fatal("inactive tab label missing from global bar")
	}

	wantActive := hexToColor(config.BlueHex)
	wantInactive := hexToColor(config.TextColorHex)
	for i := 0; i < len("1:dev"); i++ {
		cell := grid.Get(activeStart+i, 0)
		if cell.Style.Fg == nil {
			t.Fatal("active tab cells should have a foreground color")
		}
		got, ok := cell.Style.Fg.(interface {
			RGBA() (uint32, uint32, uint32, uint32)
		})
		if !ok || !sameColor(got, wantActive) {
			t.Fatalf("active tab cell %q should use the active tab color", cell.Char)
		}
	}
	for i := 0; i < len("2:logs"); i++ {
		cell := grid.Get(inactiveStart+i, 0)
		if cell.Style.Fg == nil {
			t.Fatal("inactive tab cells should have a foreground color")
		}
		got, ok := cell.Style.Fg.(interface {
			RGBA() (uint32, uint32, uint32, uint32)
		})
		if !ok || !sameColor(got, wantInactive) {
			t.Fatalf("inactive tab cell %q should use the default text color", cell.Char)
		}
	}
}

func TestBuildGlobalBarWindowTabsMarksZoomedWindow(t *testing.T) {
	t.Parallel()

	tabs := buildGlobalBarWindowTabs([]WindowInfo{
		{Index: 1, Name: "main", IsActive: true, Zoomed: true},
		{Index: 2, Name: "logs", IsActive: false},
	})

	if len(tabs) != 2 {
		t.Fatalf("buildGlobalBarWindowTabs returned %d tabs, want 2", len(tabs))
	}
	if got := tabs[0].display; got != "1:mainZ" {
		t.Fatalf("zoomed tab display = %q, want %q", got, "1:mainZ")
	}
	if got := tabs[1].display; got != "2:logs" {
		t.Fatalf("unzoomed tab display = %q, want %q", got, "2:logs")
	}
}

func TestBuildGlobalBarCellsPowerline(t *testing.T) {
	t.Parallel()

	const width = 64
	grid := NewScreenGrid(width, 2)
	buildGlobalBarCellsWithStyle(grid, "session", 3, width, 1, []WindowInfo{
		{Index: 1, Name: "editor", IsActive: true},
		{Index: 2, Name: "logs", IsActive: false},
	}, "", time.Date(2025, 1, 1, 12, 34, 0, 0, time.UTC), config.StatusStylePowerline)

	row := gridRowText(grid, 1, width)
	for _, want := range []string{"amux", powerlineRightSeparator, "1:editor", "2:logs", powerlineLeftSeparator, "? help", "12:34"} {
		if !strings.Contains(row, want) {
			t.Fatalf("powerline global bar row %q missing %q", row, want)
		}
	}

	titleEnd := findRowLabel(grid, 1, width, powerlineRightSeparator)
	if titleEnd < 0 {
		t.Fatalf("powerline global bar row %q missing title separator", row)
	}
	assertCellColors(t, grid.Get(titleEnd, 1), config.BlueHex, config.Surface0Hex)
}

func TestRenderGlobalBarPowerlineFullANSI(t *testing.T) {
	t.Parallel()

	var buf strings.Builder
	renderGlobalBarWithProfileAndStyle(&buf, "session", 2, 42, 0, nil, "error details that must truncate", time.Date(2025, 1, 1, 12, 34, 0, 0, time.UTC), defaultColorProfile, config.StatusStylePowerline)

	raw := buf.String()
	if !strings.Contains(raw, powerlineRightSeparator) {
		t.Fatalf("raw global bar output missing powerline separator:\n%q", raw)
	}
	if !strings.Contains(raw, fgHexSequence(config.RedHex, defaultColorProfile)) {
		t.Fatalf("message powerline global bar should use error foreground:\n%q", raw)
	}

	line := MaterializeGrid(raw, 42, 1)
	for _, want := range []string{"amux", "session", "error details"} {
		if !strings.Contains(line, want) {
			t.Fatalf("powerline global bar line %q missing %q", line, want)
		}
	}
}

func TestCompositorSetStatusStyleInvalidatesGrid(t *testing.T) {
	t.Parallel()

	comp := NewCompositor(20, 6, "status")
	root := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, 20, 5)
	lookup := func(uint32) PaneData {
		return &statusPaneData{id: 1, name: "pane-1", color: config.TextColorHex}
	}

	comp.RenderDiff(root, 1, lookup)
	if comp.LastGrid() == nil {
		t.Fatal("RenderDiff should populate prevGrid")
	}
	comp.SetStatusStyle(config.StatusStyleCompact)
	if comp.LastGrid() == nil {
		t.Fatal("setting the current status style should keep prevGrid")
	}

	comp.SetStatusStyle(config.StatusStylePowerline)
	if got := comp.StatusStyle(); got != config.StatusStylePowerline {
		t.Fatalf("StatusStyle() = %q, want %q", got, config.StatusStylePowerline)
	}
	if comp.LastGrid() != nil {
		t.Fatal("changing status style should invalidate prevGrid")
	}

	comp.RenderDiff(root, 1, lookup)
	if comp.LastGrid() == nil {
		t.Fatal("RenderDiff should repopulate prevGrid")
	}
	comp.SetStatusStyle(config.StatusStylePowerline)
	if comp.LastGrid() == nil {
		t.Fatal("setting the unchanged powerline status style should keep prevGrid")
	}

	comp.SetStatusStyle("invalid")
	if got := comp.StatusStyle(); got != config.StatusStyleCompact {
		t.Fatalf("invalid status style should normalize to compact, got %q", got)
	}
	if comp.LastGrid() != nil {
		t.Fatal("normalizing to a different status style should invalidate prevGrid")
	}
}

func findRowLabel(grid *ScreenGrid, y, width int, label string) int {
	labelRunes := []rune(label)
	for x := 0; x+len(labelRunes) <= width; x++ {
		match := true
		for i, want := range labelRunes {
			if got := []rune(grid.Get(x+i, y).Char); len(got) != 1 || got[0] != want {
				match = false
				break
			}
		}
		if match {
			return x
		}
	}
	return -1
}

func TestRenderPaneStatusLeadIndicator(t *testing.T) {
	t.Parallel()

	cell := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, 60, 3)

	// Active lead pane should show ▶ icon and [lead] tag
	var buf strings.Builder
	renderPaneStatus(&buf, cell, true, &statusPaneData{
		id: 1, name: "pane-1", color: "f5e0dc", lead: true,
	})
	output := buf.String()
	if !strings.Contains(output, "▶") {
		t.Error("active lead pane should show ▶ icon")
	}
	if !strings.Contains(output, "[lead]") {
		t.Error("lead pane should show [lead] tag")
	}

	// Inactive lead pane should also show ▶ icon
	buf.Reset()
	renderPaneStatus(&buf, cell, false, &statusPaneData{
		id: 1, name: "pane-1", color: "f5e0dc", lead: true, idle: true,
	})
	output = buf.String()
	if !strings.Contains(output, "▶") {
		t.Error("inactive lead pane should show ▶ icon")
	}
	if !strings.Contains(output, "[lead]") {
		t.Error("inactive lead pane should show [lead] tag")
	}

	// Non-lead pane should show ● icon and no [lead] tag
	buf.Reset()
	renderPaneStatus(&buf, cell, true, &statusPaneData{
		id: 2, name: "pane-2", color: "a6e3a1",
	})
	output = buf.String()
	if strings.Contains(output, "▶") {
		t.Error("non-lead pane should not show ▶ icon")
	}
	if strings.Contains(output, "[lead]") {
		t.Error("non-lead pane should not show [lead] tag")
	}
}

func TestBuildStatusCellsLeadIndicator(t *testing.T) {
	t.Parallel()

	cell := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, 60, 3)
	tests := []struct {
		name     string
		active   bool
		pane     *statusPaneData
		contains []string
	}{
		{
			name:   "active lead pane",
			active: true,
			pane: &statusPaneData{
				id:    1,
				name:  "pane-1",
				color: config.TextColorHex,
				lead:  true,
			},
			contains: []string{"▶", "[pane-1]", "[lead]"},
		},
		{
			name:   "inactive lead pane",
			active: false,
			pane: &statusPaneData{
				id:    1,
				name:  "pane-1",
				color: config.TextColorHex,
				lead:  true,
				idle:  true,
			},
			contains: []string{"▶", "[pane-1]", "[lead]"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			grid := NewScreenGrid(cell.W, cell.H)
			buildStatusCells(grid, cell, tt.active, tt.pane)

			var row strings.Builder
			for x := 0; x < cell.W; x++ {
				ch := grid.Get(x, 0).Char
				if ch == "" {
					ch = " "
				}
				row.WriteString(ch)
			}
			line := strings.TrimRight(row.String(), " ")
			for _, want := range tt.contains {
				if !strings.Contains(line, want) {
					t.Fatalf("status row %q missing %q", line, want)
				}
			}
		})
	}
}
