package render

import (
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

type ansiGoldenPaneData struct {
	id     uint32
	name   string
	color  string
	idle   bool
	hidden bool
	emu    *vt.SafeEmulator
}

func (p *ansiGoldenPaneData) RenderScreen(bool) string { return p.emu.Render() }
func (p *ansiGoldenPaneData) CellAt(col, row int, active bool) ScreenCell {
	return CellFromUV(p.emu.CellAt(col, row))
}
func (p *ansiGoldenPaneData) CursorPos() (int, int)               { pos := p.emu.CursorPosition(); return pos.X, pos.Y }
func (p *ansiGoldenPaneData) CursorHidden() bool                  { return p.hidden }
func (p *ansiGoldenPaneData) HasCursorBlock() bool                { return false }
func (p *ansiGoldenPaneData) ID() uint32                          { return p.id }
func (p *ansiGoldenPaneData) Name() string                        { return p.name }
func (p *ansiGoldenPaneData) TrackedPRs() []proto.TrackedPR       { return nil }
func (p *ansiGoldenPaneData) TrackedIssues() []proto.TrackedIssue { return nil }
func (p *ansiGoldenPaneData) Issue() string                       { return "" }
func (p *ansiGoldenPaneData) Host() string                        { return "local" }
func (p *ansiGoldenPaneData) Task() string                        { return "" }
func (p *ansiGoldenPaneData) Color() string                       { return p.color }
func (p *ansiGoldenPaneData) Idle() bool                          { return p.idle }
func (p *ansiGoldenPaneData) IsLead() bool                        { return false }
func (p *ansiGoldenPaneData) ConnStatus() string                  { return "" }
func (p *ansiGoldenPaneData) InCopyMode() bool                    { return false }
func (p *ansiGoldenPaneData) CopyModeSearch() string              { return "" }
func (p *ansiGoldenPaneData) CopyModeOverlay() *proto.ViewportOverlay {
	return nil
}

func newANSIGoldenPaneData(t *testing.T, width int, rendered string) *ansiGoldenPaneData {
	t.Helper()

	emu := vt.NewSafeEmulator(width, 1)
	if _, err := emu.Write([]byte(rendered)); err != nil {
		t.Fatalf("emu.Write: %v", err)
	}

	return &ansiGoldenPaneData{
		id:     1,
		name:   "pane-1",
		color:  "f5e0dc",
		hidden: true,
		emu:    emu,
	}
}

func normalizeANSITestOutput(s string) string {
	replacer := strings.NewReplacer(
		"\x1b", "<ESC>",
		"\a", "<BEL>",
	)
	return replacer.Replace(s)
}

func assertANSIGolden(t *testing.T, name, actual string) {
	t.Helper()

	path := filepath.Join("testdata", name)
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden %s: %v", path, err)
	}
	want := strings.TrimSuffix(string(expected), "\n")
	if actual != want {
		t.Fatalf("golden mismatch: %s\n\n--- expected ---\n%s\n--- actual ---\n%s", path, want, actual)
	}
}

func TestRenderPaneContentGoldenPreservesHyperlinksAndUnderlineMetadata(t *testing.T) {
	t.Parallel()

	rendered := ansi.SetHyperlink("https://example.com") +
		"Hi" +
		ansi.ResetHyperlink() +
		" " +
		ansi.NewStyle().
			UnderlineStyle(ansi.UnderlineCurly).
			UnderlineColor(color.RGBA{R: 1, G: 2, B: 3, A: 255}).
			String() +
		"U" +
		ansi.ResetStyle +
		" plain"
	pd := newANSIGoldenPaneData(t, 20, rendered)
	cell := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, 20, 2)
	comp := NewCompositor(20, 3, "test")

	var buf strings.Builder
	comp.renderPaneContent(&buf, cell, false, pd)

	assertANSIGolden(t, "pane_content_hyperlink_underline.golden", normalizeANSITestOutput(buf.String()))
}

func TestRenderPaneContentGoldenClosesAndReopensHyperlinksOnLinkChange(t *testing.T) {
	t.Parallel()

	rendered := ansi.SetHyperlink("https://one.example") +
		"AB" +
		ansi.SetHyperlink("https://two.example") +
		"CD" +
		ansi.ResetHyperlink() +
		"E"
	pd := newANSIGoldenPaneData(t, 20, rendered)
	cell := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, 20, 2)
	comp := NewCompositor(20, 3, "test")

	var buf strings.Builder
	comp.renderPaneContent(&buf, cell, false, pd)

	assertANSIGolden(t, "pane_content_link_change.golden", normalizeANSITestOutput(buf.String()))
}

func TestRenderDiffPreservesHyperlinksAndUnderlineMetadata(t *testing.T) {
	t.Parallel()

	rendered := ansi.SetHyperlink("https://example.com") +
		"Hi" +
		ansi.ResetHyperlink() +
		" " +
		ansi.NewStyle().
			UnderlineStyle(ansi.UnderlineCurly).
			UnderlineColor(color.RGBA{R: 1, G: 2, B: 3, A: 255}).
			String() +
		"U" +
		ansi.ResetStyle +
		" plain"
	pd := newANSIGoldenPaneData(t, 20, rendered)
	root := mux.NewLeaf(&mux.Pane{ID: 1, Meta: mux.PaneMeta{Name: "pane-1"}}, 0, 0, 20, 2)
	comp := NewCompositor(20, 3, "test")
	comp.TimeNow = func() time.Time {
		return time.Date(2026, time.March, 31, 12, 34, 0, 0, time.UTC)
	}

	diff := normalizeANSITestOutput(comp.RenderDiff(root, 0, func(id uint32) PaneData {
		if id == 1 {
			return pd
		}
		return nil
	}))

	for _, want := range []string{
		"<ESC>]8;;https://example.com<BEL>",
		"<ESC>]8;;<BEL>",
		"4:3",
		"58;2;1;2;3",
	} {
		if !strings.Contains(diff, want) {
			t.Fatalf("RenderDiff missing %q in:\n%s", want, diff)
		}
	}

	if got := strings.Count(diff, "<ESC>]8;;https://example.com<BEL>"); got != 1 {
		t.Fatalf("hyperlink open count = %d, want 1 in:\n%s", got, diff)
	}
	if got := strings.Count(diff, "<ESC>]8;;<BEL>"); got != 1 {
		t.Fatalf("hyperlink close count = %d, want 1 in:\n%s", got, diff)
	}
}
