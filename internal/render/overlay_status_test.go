package render

import (
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/copymode"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

type statusPaneData struct {
	id            uint32
	name          string
	trackedPRs    []proto.TrackedPR
	trackedIssues []proto.TrackedIssue
	host          string
	task          string
	color         string
	connStatus    string
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
func (p *statusPaneData) Host() string                        { return p.host }
func (p *statusPaneData) Task() string                        { return p.task }
func (p *statusPaneData) Color() string                       { return p.color }
func (p *statusPaneData) Minimized() bool                     { return false }
func (p *statusPaneData) Idle() bool                          { return p.idle }
func (p *statusPaneData) IsLead() bool                        { return p.lead }
func (p *statusPaneData) ConnStatus() string                  { return p.connStatus }
func (p *statusPaneData) InCopyMode() bool                    { return p.copyMode }
func (p *statusPaneData) CopyModeSearch() string              { return p.copySearch }
func (p *statusPaneData) HasCursorBlock() bool                { return false }
func (p *statusPaneData) CopyModeOverlay() *copymode.ViewportOverlay {
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
				connStatus: "reconnecting",
				copyMode:   true,
				copySearch: "/query",
			},
			contains: []string{"●", "[pane-1]", "[copy] /query", "@gpu-box", "⟳", "train"},
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
				id:         3,
				name:       "pane-3",
				color:      config.TextColorHex,
				connStatus: "disconnected",
			},
			contains: []string{"○", "[pane-3]", "✕"},
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
	if !strings.Contains(raw, StrikeOn+"#42") {
		t.Fatalf("raw status output missing completed PR styling:\n%q", raw)
	}
	if !strings.Contains(raw, StrikeOn+"LAB-450") {
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

func TestRenderPaneStatusHintsWhenActivePaneHasNoIssueMetadata(t *testing.T) {
	t.Parallel()

	cell := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, 60, 3)
	buf := strings.Builder{}
	renderPaneStatus(&buf, cell, true, &statusPaneData{
		id:   1,
		name: "pane-1",
		trackedPRs: []proto.TrackedPR{
			{Number: 42},
		},
		color: config.TextColorHex,
	})

	line := MaterializeGrid(buf.String(), cell.W, 1)
	for _, want := range []string{"[pane-1]", "#42", "set issue"} {
		if !strings.Contains(line, want) {
			t.Fatalf("status line %q missing %q", line, want)
		}
	}
}

func TestRenderPaneStatusSkipsMissingIssueHintForInactivePane(t *testing.T) {
	t.Parallel()

	cell := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, 60, 3)
	buf := strings.Builder{}
	renderPaneStatus(&buf, cell, false, &statusPaneData{
		id:   1,
		name: "pane-1",
		trackedPRs: []proto.TrackedPR{
			{Number: 42},
		},
		color: config.TextColorHex,
	})

	line := MaterializeGrid(buf.String(), cell.W, 1)
	if strings.Contains(line, "set issue") {
		t.Fatalf("inactive status line should not show the missing-issue hint: %q", line)
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

	lines, styles, x, y := chooserOverlayLayout(24, 12, overlay)
	if len(lines) == 0 || len(styles) != len(lines) {
		t.Fatalf("chooserOverlayLayout returned lines=%v styles=%v", lines, styles)
	}
	if x < 0 || y < 0 {
		t.Fatalf("chooser overlay origin = (%d,%d), want non-negative", x, y)
	}

	grid := NewScreenGrid(24, 12)
	buildChooserOverlayCells(grid, overlay)
	if got := grid.Get(x+1, y).Char; got == "" {
		t.Fatal("chooser title row should populate grid cells")
	}
	selectedRow := -1
	for i, style := range styles {
		if style == chooserRowSelected {
			selectedRow = i
			break
		}
	}
	if selectedRow < 0 {
		t.Fatal("chooserOverlayLayout should mark one row as selected")
	}
	selected := grid.Get(x+1, y+selectedRow)
	if selected.Style.Bg == nil {
		t.Fatal("selected chooser row should have a background color")
	}

	buf := strings.Builder{}
	renderChooserOverlay(&buf, 24, 12, overlay)
	got := MaterializeGrid(buf.String(), 24, 12)
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
	grid := NewScreenGrid(20, 8)
	buildChooserOverlayCells(grid, overlay)
	lines, styles, x, y := chooserOverlayLayout(20, 8, overlay)
	if len(lines) == 0 {
		t.Fatal("chooserOverlayLayout should produce lines")
	}
	selectedRow := -1
	baseRow := -1
	for i, style := range styles {
		switch style {
		case chooserRowSelected:
			selectedRow = i
		case chooserRowNormal:
			if baseRow < 0 {
				baseRow = i
			}
		}
	}
	if selectedRow < 0 || baseRow < 0 {
		t.Fatalf("chooser styles = %v, want selected and normal rows", styles)
	}
	selectedCell := grid.Get(x+1, y+selectedRow)
	baseCell := grid.Get(x+1, y+baseRow)
	if selectedCell.Style.Bg == nil || baseCell.Style.Bg == nil {
		t.Fatal("chooser overlay cells should include background colors")
	}
	if sameColor(selectedCell.Style.Bg, baseCell.Style.Bg) {
		t.Fatal("selected chooser row should not use the same background as a normal row")
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
		id:         1,
		name:       "pane-1",
		host:       "remote-host",
		task:       "sync",
		color:      config.TextColorHex,
		connStatus: "connected",
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
	for _, want := range []string{"@remote-host", "sync", "⚡"} {
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
		host:       "remote-host",
		task:       "sync",
		color:      config.TextColorHex,
		connStatus: "connected",
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
	for _, want := range []string{"#42, #314, LAB-339", "@remote-host", "sync", "⚡"} {
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

func TestBuildStatusCellsHintsWhenActivePaneHasNoIssueMetadata(t *testing.T) {
	t.Parallel()

	cell := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, 40, 4)
	grid := NewScreenGrid(40, 4)
	buildStatusCells(grid, cell, true, &statusPaneData{
		id:   1,
		name: "pane-1",
		trackedPRs: []proto.TrackedPR{
			{Number: 42},
		},
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
	for _, want := range []string{"#42", "set issue"} {
		if !strings.Contains(line, want) {
			t.Fatalf("status row %q missing %q", line, want)
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

func TestPadOrTrim(t *testing.T) {
	t.Parallel()

	if got := padOrTrim("abcdef", 4); got != "abcd" {
		t.Fatalf("padOrTrim trim = %q, want %q", got, "abcd")
	}
	if got := padOrTrim("abc", 5); got != "abc  " {
		t.Fatalf("padOrTrim pad = %q, want %q", got, "abc  ")
	}
	if got := padOrTrim("abc", 0); got != "" {
		t.Fatalf("padOrTrim zero width = %q, want empty", got)
	}
}

func TestChooserCellStyle(t *testing.T) {
	t.Parallel()

	border := uv.Style{Fg: hexToColor(config.TextColorHex)}
	text := uv.Style{Fg: hexToColor(config.TextColorHex), Bg: hexToColor(config.Surface0Hex)}
	dim := uv.Style{Fg: hexToColor(config.DimColorHex), Bg: hexToColor(config.Surface0Hex)}
	selected := uv.Style{Fg: hexToColor(config.Surface0Hex), Bg: hexToColor(config.TextColorHex)}

	if got := chooserCellStyle(chooserRowSelected, true, border, text, dim, selected); !sameColor(got.Fg, border.Fg) {
		t.Fatal("border cells should always use the border style")
	}
	if got := chooserCellStyle(chooserRowDim, false, border, text, dim, selected); !sameColor(got.Fg, dim.Fg) {
		t.Fatal("dim rows should use the dim style")
	}
	if got := chooserCellStyle(chooserRowSelected, false, border, text, dim, selected); !sameColor(got.Bg, selected.Bg) {
		t.Fatal("selected rows should use the selected style")
	}
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
