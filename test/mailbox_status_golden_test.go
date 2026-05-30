package test

import (
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

type mailboxStatusGoldenPaneData struct {
	id            uint32
	name          string
	color         string
	trackedPRs    []proto.TrackedPR
	trackedIssues []proto.TrackedIssue
	host          string
	task          string
	unread        int
}

func (p *mailboxStatusGoldenPaneData) RenderScreen(bool) string { return "" }
func (p *mailboxStatusGoldenPaneData) CellAt(int, int, bool) render.ScreenCell {
	return render.ScreenCell{Char: " ", Width: 1}
}
func (p *mailboxStatusGoldenPaneData) CopyModeOverlay() *proto.ViewportOverlay { return nil }
func (p *mailboxStatusGoldenPaneData) CursorPos() (int, int)                   { return 0, 0 }
func (p *mailboxStatusGoldenPaneData) CursorHidden() bool                      { return true }
func (p *mailboxStatusGoldenPaneData) HasCursorBlock() bool                    { return false }
func (p *mailboxStatusGoldenPaneData) ID() uint32                              { return p.id }
func (p *mailboxStatusGoldenPaneData) Name() string                            { return p.name }
func (p *mailboxStatusGoldenPaneData) TrackedPRs() []proto.TrackedPR           { return p.trackedPRs }
func (p *mailboxStatusGoldenPaneData) TrackedIssues() []proto.TrackedIssue     { return p.trackedIssues }
func (p *mailboxStatusGoldenPaneData) Issue() string                           { return "" }
func (p *mailboxStatusGoldenPaneData) Host() string                            { return p.host }
func (p *mailboxStatusGoldenPaneData) Task() string                            { return p.task }
func (p *mailboxStatusGoldenPaneData) Color() string                           { return p.color }
func (p *mailboxStatusGoldenPaneData) Idle() bool                              { return false }
func (p *mailboxStatusGoldenPaneData) IsLead() bool                            { return false }
func (p *mailboxStatusGoldenPaneData) InCopyMode() bool                        { return false }
func (p *mailboxStatusGoldenPaneData) CopyModeSearch() string                  { return "" }
func (p *mailboxStatusGoldenPaneData) MailboxUnreadCount() int                 { return p.unread }

func TestGoldenMailboxStatusBadge(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		width  int
		active bool
		pane   *mailboxStatusGoldenPaneData
	}{
		{
			name:   "active_unread",
			width:  64,
			active: true,
			pane: &mailboxStatusGoldenPaneData{
				id:    1,
				name:  "pane-1",
				color: config.AccentColor(0),
				trackedPRs: []proto.TrackedPR{
					{Number: 42},
				},
				trackedIssues: []proto.TrackedIssue{
					{ID: "LAB-1993"},
				},
				host:   "gpu",
				task:   "build",
				unread: 3,
			},
		},
		{
			name:   "no_unread",
			width:  64,
			active: true,
			pane: &mailboxStatusGoldenPaneData{
				id:    1,
				name:  "pane-1",
				color: config.AccentColor(0),
				trackedPRs: []proto.TrackedPR{
					{Number: 42},
				},
				trackedIssues: []proto.TrackedIssue{
					{ID: "LAB-1993"},
				},
				host: "gpu",
				task: "build",
			},
		},
		{
			name:   "inactive_capped",
			width:  64,
			active: false,
			pane: &mailboxStatusGoldenPaneData{
				id:    2,
				name:  "pane-2",
				color: config.AccentColor(1),
				trackedPRs: []proto.TrackedPR{
					{Number: 101},
				},
				host:   "remote",
				task:   "review",
				unread: 12,
			},
		},
		{
			name:   "narrow_hides_badge",
			width:  12,
			active: true,
			pane: &mailboxStatusGoldenPaneData{
				id:     1,
				name:   "pane-1",
				color:  config.AccentColor(0),
				unread: 4,
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assertGolden(t, "mailbox_status_"+tt.name+".golden", renderMailboxStatusLine(tt.width, tt.active, tt.pane)+"\n")
		})
	}
}

func renderMailboxStatusLine(width int, active bool, pane *mailboxStatusGoldenPaneData) string {
	const (
		totalHeight = 4
		sessionName = "mailbox"
	)
	activePaneID := pane.id
	if !active {
		activePaneID = pane.id + 1000
	}
	root := mux.NewLeafByID(pane.id, 0, 0, width, totalHeight-render.GlobalBarHeight)
	comp := render.NewCompositor(width, totalHeight, sessionName)
	comp.TimeNow = fixedMailboxStatusTime
	raw := comp.RenderFullWithOverlay(root, activePaneID, func(id uint32) render.PaneData {
		if id == pane.id {
			return pane
		}
		return nil
	}, render.OverlayState{}, true)
	line := strings.Split(render.MaterializeGrid(raw, width, totalHeight), "\n")[0]
	return strings.TrimRight(line, " ")
}

func fixedMailboxStatusTime() time.Time {
	return time.Date(2026, 5, 30, 12, 34, 0, 0, time.UTC)
}
