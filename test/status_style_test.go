package test

import (
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

type statusStylePaneData struct {
	id            uint32
	name          string
	color         string
	trackedPRs    []proto.TrackedPR
	trackedIssues []proto.TrackedIssue
	host          string
	task          string
}

func (p *statusStylePaneData) RenderScreen(bool) string { return "" }
func (p *statusStylePaneData) CellAt(int, int, bool) render.ScreenCell {
	return render.ScreenCell{Char: " ", Width: 1}
}
func (p *statusStylePaneData) CopyModeOverlay() *proto.ViewportOverlay { return nil }
func (p *statusStylePaneData) CursorPos() (int, int)                   { return 0, 0 }
func (p *statusStylePaneData) CursorHidden() bool                      { return true }
func (p *statusStylePaneData) HasCursorBlock() bool                    { return false }
func (p *statusStylePaneData) ID() uint32                              { return p.id }
func (p *statusStylePaneData) Name() string                            { return p.name }
func (p *statusStylePaneData) TrackedPRs() []proto.TrackedPR           { return p.trackedPRs }
func (p *statusStylePaneData) TrackedIssues() []proto.TrackedIssue     { return p.trackedIssues }
func (p *statusStylePaneData) Issue() string                           { return "" }
func (p *statusStylePaneData) Host() string                            { return p.host }
func (p *statusStylePaneData) Task() string                            { return p.task }
func (p *statusStylePaneData) Color() string                           { return p.color }
func (p *statusStylePaneData) Idle() bool                              { return false }
func (p *statusStylePaneData) IsLead() bool                            { return false }
func (p *statusStylePaneData) ConnStatus() string                      { return "" }
func (p *statusStylePaneData) InCopyMode() bool                        { return false }
func (p *statusStylePaneData) CopyModeSearch() string                  { return "" }

func TestStatusStyleGoldenOutputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		style  string
		golden string
	}{
		{name: "compact", style: "compact", golden: "status_style_compact.golden"},
		{name: "powerline", style: "powerline", golden: "status_style_powerline.golden"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assertGolden(t, tt.golden, renderStatusStyleFrame(tt.style))
		})
	}
}

func renderStatusStyleFrame(style string) string {
	const (
		width        = 48
		totalHeight  = 5
		layoutHeight = totalHeight - render.GlobalBarHeight
		sessionName  = "style"
	)

	root := mux.NewLeafByID(1, 0, 0, width, layoutHeight)
	pane := &statusStylePaneData{
		id:    1,
		name:  "pane-1",
		color: config.AccentColor(0),
		trackedPRs: []proto.TrackedPR{
			{Number: 42},
		},
		trackedIssues: []proto.TrackedIssue{
			{ID: "LAB-1651"},
		},
		host: "gpu",
		task: "build",
	}

	comp := render.NewCompositor(width, totalHeight, sessionName)
	comp.SetStatusStyle(style)
	comp.TimeNow = func() time.Time { return time.Date(2026, 5, 7, 12, 34, 0, 0, time.UTC) }
	raw := comp.RenderFullWithOverlay(root, 1, func(id uint32) render.PaneData {
		if id == 1 {
			return pane
		}
		return nil
	}, render.OverlayState{}, true)
	return extractFrame(render.MaterializeGrid(raw, width, totalHeight), sessionName)
}
