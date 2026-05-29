package test

import (
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

type mirrorStatusGoldenPaneData struct {
	id                 uint32
	name               string
	host               string
	color              string
	task               string
	trackedPRs         []proto.TrackedPR
	mirrorState        string
	remotePaneName     string
	reconnectInSeconds int
	lastError          string
}

func (p *mirrorStatusGoldenPaneData) RenderScreen(bool) string { return "" }
func (p *mirrorStatusGoldenPaneData) CellAt(int, int, bool) render.ScreenCell {
	return render.ScreenCell{Char: " ", Width: 1}
}
func (p *mirrorStatusGoldenPaneData) CopyModeOverlay() *proto.ViewportOverlay { return nil }
func (p *mirrorStatusGoldenPaneData) CursorPos() (int, int)                   { return 0, 0 }
func (p *mirrorStatusGoldenPaneData) CursorHidden() bool                      { return true }
func (p *mirrorStatusGoldenPaneData) HasCursorBlock() bool                    { return false }
func (p *mirrorStatusGoldenPaneData) ID() uint32                              { return p.id }
func (p *mirrorStatusGoldenPaneData) Name() string                            { return p.name }
func (p *mirrorStatusGoldenPaneData) TrackedPRs() []proto.TrackedPR           { return p.trackedPRs }
func (p *mirrorStatusGoldenPaneData) TrackedIssues() []proto.TrackedIssue     { return nil }
func (p *mirrorStatusGoldenPaneData) Issue() string                           { return "" }
func (p *mirrorStatusGoldenPaneData) Host() string                            { return p.host }
func (p *mirrorStatusGoldenPaneData) Task() string                            { return p.task }
func (p *mirrorStatusGoldenPaneData) Color() string                           { return p.color }
func (p *mirrorStatusGoldenPaneData) Idle() bool                              { return false }
func (p *mirrorStatusGoldenPaneData) IsLead() bool                            { return false }
func (p *mirrorStatusGoldenPaneData) InCopyMode() bool                        { return false }
func (p *mirrorStatusGoldenPaneData) CopyModeSearch() string                  { return "" }
func (p *mirrorStatusGoldenPaneData) MirrorStatus() (state, remotePaneName string, reconnectInSeconds int, lastError string, ok bool) {
	return p.mirrorState, p.remotePaneName, p.reconnectInSeconds, p.lastError, p.mirrorState != ""
}

func TestGoldenMirrorConnectionStatusLines(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		state              string
		reconnectInSeconds int
		lastError          string
	}{
		{name: "connected", state: "connected"},
		{name: "reconnecting", state: "reconnecting", reconnectInSeconds: 3},
		{name: "gone", state: "dead", lastError: "remote pane exited"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			raw := renderMirrorConnectionStatusFrame(96, tt.state, tt.reconnectInSeconds, tt.lastError, "pane-1786")
			frame := extractFrame(render.MaterializeGrid(raw, 96, 8), "mirror")
			assertGolden(t, "mirror_status_"+tt.name+".golden", frame+"\n")

			colorMap := render.ExtractColorMap(raw, 96, 8)
			assertGolden(t, "mirror_status_"+tt.name+".color", colorMap+"\n")
		})
	}
}

func TestGoldenMirrorConnectionNarrowDropOrder(t *testing.T) {
	t.Parallel()

	raw := renderMirrorConnectionNarrowFrame(40)
	frame := extractFrame(render.MaterializeGrid(raw, 40, 6), "mirror")
	assertGolden(t, "mirror_status_narrow.golden", frame+"\n")
}

func renderMirrorConnectionStatusFrame(width int, state string, reconnectInSeconds int, lastError, remotePaneName string) string {
	const (
		totalHeight = 8
		sessionName = "mirror"
	)
	layoutHeight := totalHeight - render.GlobalBarHeight
	local := &mux.Pane{ID: 1, Meta: mux.PaneMeta{Name: "pane-70", Host: mux.DefaultHost, Color: config.AccentColor(0)}}
	mirrorPane := &mux.Pane{ID: 2, Meta: mux.PaneMeta{Name: "pane-91", Host: "hetzner-1", Color: config.AccentColor(1)}}
	window := mux.NewWindow(local, width, layoutHeight)
	if _, err := window.Split(mux.SplitVertical, mirrorPane); err != nil {
		panic(err)
	}
	window.FocusPane(local)

	panes := map[uint32]render.PaneData{
		1: &mirrorStatusGoldenPaneData{
			id:    1,
			name:  "pane-70",
			host:  mux.DefaultHost,
			color: config.AccentColor(0),
		},
		2: &mirrorStatusGoldenPaneData{
			id:                 2,
			name:               "pane-91",
			host:               "hetzner-1",
			color:              config.AccentColor(1),
			task:               "~/amux",
			trackedPRs:         []proto.TrackedPR{{Number: 817}},
			mirrorState:        state,
			remotePaneName:     remotePaneName,
			reconnectInSeconds: reconnectInSeconds,
			lastError:          lastError,
		},
	}

	comp := render.NewCompositor(width, totalHeight, sessionName)
	comp.TimeNow = fixedMirrorStatusTime
	return comp.RenderFullWithOverlay(window.Root, window.ActivePane.ID, func(id uint32) render.PaneData {
		return panes[id]
	}, render.OverlayState{}, true)
}

func renderMirrorConnectionNarrowFrame(width int) string {
	const (
		totalHeight = 6
		sessionName = "mirror"
	)
	layoutHeight := totalHeight - render.GlobalBarHeight
	root := mux.NewLeafByID(1, 0, 0, width, layoutHeight)
	pane := &mirrorStatusGoldenPaneData{
		id:             1,
		name:           "pane-91",
		host:           "hetzner-1",
		color:          config.AccentColor(0),
		task:           "~/very/long/amux/worktree",
		trackedPRs:     []proto.TrackedPR{{Number: 817}},
		mirrorState:    "connected",
		remotePaneName: "pane-1786-remote-long-name",
	}

	comp := render.NewCompositor(width, totalHeight, sessionName)
	comp.TimeNow = fixedMirrorStatusTime
	return comp.RenderFullWithOverlay(root, 1, func(id uint32) render.PaneData {
		if id == 1 {
			return pane
		}
		return nil
	}, render.OverlayState{}, true)
}

func fixedMirrorStatusTime() time.Time {
	return time.Date(2026, 5, 29, 12, 34, 0, 0, time.UTC)
}
