package client

import (
	"bytes"
	"fmt"
	"net"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/copymode"
	"github.com/weill-labs/amux/internal/mouse"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

type fakeDragTimer struct {
	callback func()
	stopped  bool
}

func (t *fakeDragTimer) Stop() bool {
	wasActive := !t.stopped
	t.stopped = true
	return wasActive
}

func (t *fakeDragTimer) Fire() {
	if t.stopped || t.callback == nil {
		return
	}
	t.stopped = true
	t.callback()
}

type fakeDragScheduler struct {
	timers []*fakeDragTimer
}

func (s *fakeDragScheduler) AfterFunc(_ time.Duration, fn func()) dragTimer {
	timer := &fakeDragTimer{callback: fn}
	s.timers = append(s.timers, timer)
	return timer
}

func (s *fakeDragScheduler) Latest() *fakeDragTimer {
	if len(s.timers) == 0 {
		return nil
	}
	return s.timers[len(s.timers)-1]
}

func stubCopyToClipboard(cr *ClientRenderer, fn func(string)) {
	cr.CopyToClipboard = fn
}

func readCommandMessage(t *testing.T, conn net.Conn) *proto.Message {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	msg, err := proto.ReadMsg(conn)
	if err != nil {
		t.Fatalf("read command message: %v", err)
	}
	return msg
}

func readFocusCommand(t *testing.T, conn net.Conn, paneID string) {
	t.Helper()

	msg := readCommandMessage(t, conn)
	if msg.Type != proto.MsgTypeCommand || msg.CmdName != "focus" || len(msg.CmdArgs) != 1 || msg.CmdArgs[0] != paneID {
		t.Fatalf("focus command = %q %v, want focus [%s]", msg.CmdName, msg.CmdArgs, paneID)
	}
}

func startTestRenderLoop(t *testing.T, cr *ClientRenderer) chan *RenderMsg {
	t.Helper()

	msgCh := make(chan *RenderMsg, 64)
	done := make(chan struct{})
	go func() {
		defer close(done)
		cr.RenderCoalesced(msgCh, func(string) {})
	}()
	t.Cleanup(func() {
		msgCh <- &RenderMsg{Typ: RenderMsgExit}
		<-done
	})
	return msgCh
}

func buildMultiWindowRendererAt(t *testing.T, activeWindowID uint32) *ClientRenderer {
	t.Helper()

	snap := multiWindow80x23()
	snap.ActiveWindowID = activeWindowID
	for _, ws := range snap.Windows {
		if ws.ID == activeWindowID {
			snap.ActivePaneID = ws.ActivePaneID
			snap.Root = ws.Root
			break
		}
	}

	cr := NewClientRenderer(80, 24)
	cr.HandleLayout(snap)
	return cr
}

func threeWindow80x23(activeWindowID uint32) *proto.LayoutSnapshot {
	windows := []proto.WindowSnapshot{
		{
			ID: 1, Name: "editor", Index: 1, ActivePaneID: 1,
			Root:  proto.CellSnapshot{X: 0, Y: 0, W: 80, H: 23, IsLeaf: true, Dir: -1, PaneID: 1},
			Panes: []proto.PaneSnapshot{{ID: 1, Name: "pane-1", Host: "local", Task: "server", Color: "f5e0dc"}},
		},
		{
			ID: 2, Name: "logs", Index: 2, ActivePaneID: 2,
			Root:  proto.CellSnapshot{X: 0, Y: 0, W: 80, H: 23, IsLeaf: true, Dir: -1, PaneID: 2},
			Panes: []proto.PaneSnapshot{{ID: 2, Name: "pane-2", Host: "local", Task: "tail", Color: "f2cdcd"}},
		},
		{
			ID: 3, Name: "docs", Index: 3, ActivePaneID: 3,
			Root:  proto.CellSnapshot{X: 0, Y: 0, W: 80, H: 23, IsLeaf: true, Dir: -1, PaneID: 3},
			Panes: []proto.PaneSnapshot{{ID: 3, Name: "pane-3", Host: "local", Task: "notes", Color: "cba6f7"}},
		},
	}

	snap := &proto.LayoutSnapshot{
		SessionName:    "test",
		Width:          80,
		Height:         23,
		Windows:        windows,
		ActiveWindowID: activeWindowID,
	}
	for _, ws := range windows {
		if ws.ID == activeWindowID {
			snap.ActivePaneID = ws.ActivePaneID
			snap.Root = ws.Root
			break
		}
	}
	return snap
}

func buildThreeWindowRendererAt(t *testing.T, activeWindowID uint32) *ClientRenderer {
	t.Helper()

	cr := NewClientRenderer(80, 24)
	cr.HandleLayout(threeWindow80x23(activeWindowID))
	return cr
}

func globalBarClickColumn(t *testing.T, cr *ClientRenderer, label string) int {
	t.Helper()

	lines := strings.Split(cr.Capture(true), "\n")
	if len(lines) == 0 {
		t.Fatal("capture should include a global bar")
	}
	bar := lines[len(lines)-1]
	col := strings.Index(bar, label)
	if col < 0 {
		t.Fatalf("global bar %q missing %q", bar, label)
	}
	return utf8.RuneCountInString(bar[:col])
}

func globalBarRow(t *testing.T, cr *ClientRenderer) int {
	t.Helper()

	layout := cr.VisibleLayout()
	if layout == nil {
		t.Fatal("visible layout missing")
	}
	return layout.H
}

func paneStatusTargetCenter(t *testing.T, cr *ClientRenderer, paneID uint32) (int, int) {
	t.Helper()

	layout := cr.VisibleLayout()
	if layout == nil {
		t.Fatal("visible layout missing")
	}
	var cell *mux.LayoutCell
	layout.Walk(func(candidate *mux.LayoutCell) {
		if cell == nil && candidate.CellPaneID() == paneID {
			cell = candidate
		}
	})
	if cell == nil {
		t.Fatalf("pane %d not found in visible layout", paneID)
	}
	return cell.X + cell.W/2, cell.Y
}

func stackedColumn80x23(activePaneID uint32) *proto.LayoutSnapshot {
	leftColumn := proto.CellSnapshot{
		X: 0, Y: 0, W: 39, H: 23,
		Dir: int(mux.SplitHorizontal),
		Children: []proto.CellSnapshot{
			{X: 0, Y: 0, W: 39, H: 11, IsLeaf: true, Dir: -1, PaneID: 1},
			{X: 0, Y: 12, W: 39, H: 11, IsLeaf: true, Dir: -1, PaneID: 3},
		},
	}
	root := proto.CellSnapshot{
		X: 0, Y: 0, W: 80, H: 23,
		Dir: int(mux.SplitVertical),
		Children: []proto.CellSnapshot{
			leftColumn,
			{X: 40, Y: 0, W: 39, H: 23, IsLeaf: true, Dir: -1, PaneID: 2},
		},
	}
	panes := []proto.PaneSnapshot{
		{ID: 1, Name: "pane-1", Host: "local", Color: "f5e0dc", ColumnIndex: 0},
		{ID: 2, Name: "pane-2", Host: "local", Color: "f2cdcd", ColumnIndex: 1},
		{ID: 3, Name: "pane-3", Host: "local", Color: "cba6f7", ColumnIndex: 0},
	}
	return &proto.LayoutSnapshot{
		SessionName:  "test",
		ActivePaneID: activePaneID,
		Width:        80,
		Height:       23,
		Root:         root,
		Panes:        panes,
		Windows: []proto.WindowSnapshot{{
			ID: 1, Name: "window-1", Index: 1, ActivePaneID: activePaneID,
			Root:  root,
			Panes: panes,
		}},
		ActiveWindowID: 1,
	}
}

func buildColumnDragRenderer(t *testing.T, activePaneID uint32) *ClientRenderer {
	t.Helper()

	cr := NewClientRenderer(80, 24)
	cr.HandleLayout(stackedColumn80x23(activePaneID))
	return cr
}

func visiblePaneCell(t *testing.T, cr *ClientRenderer, paneID uint32) *mux.LayoutCell {
	t.Helper()

	layout := cr.VisibleLayout()
	if layout == nil {
		t.Fatal("visible layout missing")
	}
	cell := layout.FindByPaneID(paneID)
	if cell == nil {
		t.Fatalf("pane-%d missing from visible layout", paneID)
	}
	return cell
}

func paneCenterPoint(t *testing.T, cr *ClientRenderer, paneID uint32) (int, int) {
	t.Helper()

	cell := visiblePaneCell(t, cr, paneID)
	return cell.X + cell.W/2, cell.Y + cell.H/2
}

func paneEdgePoint(t *testing.T, cr *ClientRenderer, paneID uint32, edge string) (int, int) {
	t.Helper()

	cell := visiblePaneCell(t, cr, paneID)
	switch edge {
	case "left":
		return cell.X + 1, cell.Y + cell.H/2
	case "right":
		return cell.X + cell.W - 2, cell.Y + cell.H/2
	case "top":
		return cell.X + cell.W/2, cell.Y + 1
	case "bottom":
		return cell.X + cell.W/2, cell.Y + cell.H - 2
	default:
		t.Fatalf("unknown pane edge %q", edge)
		return 0, 0
	}
}

func windowEdgePoint(t *testing.T, cr *ClientRenderer, edge string) (int, int) {
	t.Helper()

	layout := cr.VisibleLayout()
	if layout == nil {
		t.Fatal("visible layout missing")
	}
	switch edge {
	case "left":
		return layout.X, layout.Y + layout.H/2
	case "right":
		return layout.X + layout.W - 1, layout.Y + layout.H/2
	case "top":
		return layout.X + layout.W/2, layout.Y
	case "bottom":
		return layout.X + layout.W/2, layout.Y + layout.H - 1
	default:
		t.Fatalf("unknown window edge %q", edge)
		return 0, 0
	}
}

func TestPaneDropZoneHelpers(t *testing.T) {
	t.Parallel()

	t.Run("logical root skips anchored lead and lead targets are rejected", func(t *testing.T) {
		t.Parallel()

		root := proto.CellSnapshot{
			X: 0, Y: 0, W: 80, H: 23,
			Dir: int(mux.SplitVertical),
			Children: []proto.CellSnapshot{
				{X: 0, Y: 0, W: 39, H: 23, IsLeaf: true, Dir: -1, PaneID: 1},
				{X: 40, Y: 0, W: 39, H: 23, IsLeaf: true, Dir: -1, PaneID: 2},
			},
		}
		panes := []proto.PaneSnapshot{
			{ID: 1, Name: "pane-1", Host: "local", Color: "f5e0dc", Lead: true},
			{ID: 2, Name: "pane-2", Host: "local", Color: "f2cdcd"},
		}
		snap := &proto.LayoutSnapshot{
			SessionName:  "test",
			ActivePaneID: 2,
			Width:        80,
			Height:       23,
			Root:         root,
			Panes:        panes,
			Windows: []proto.WindowSnapshot{{
				ID: 1, Name: "window-1", Index: 1, ActivePaneID: 2,
				Root:  root,
				Panes: panes,
			}},
			ActiveWindowID: 1,
		}

		cr := NewClientRenderer(80, 24)
		cr.HandleLayout(snap)

		layout := cr.VisibleLayout()
		if layout == nil {
			t.Fatal("visible layout missing")
		}
		if got := logicalRootCell(cr, layout); got != layout.Children[1] {
			t.Fatalf("logicalRootCell() = %#v, want right child %#v", got, layout.Children[1])
		}

		x, y := paneCenterPoint(t, cr, 1)
		if got := resolvePaneDropTarget(cr, layout, 2, x, y); got != nil {
			t.Fatalf("resolvePaneDropTarget() = %#v, want nil for lead target", got)
		}
	})

	t.Run("placement helpers cover non-left branches", func(t *testing.T) {
		t.Parallel()

		if dir, first := edgePlacement("right"); dir != mux.SplitVertical || first {
			t.Fatalf("edgePlacement(right) = (%v, %v), want (vertical, false)", dir, first)
		}
		if dir, first := edgePlacement("top"); dir != mux.SplitHorizontal || !first {
			t.Fatalf("edgePlacement(top) = (%v, %v), want (horizontal, true)", dir, first)
		}
		if dir, first := edgePlacement("bottom"); dir != mux.SplitHorizontal || first {
			t.Fatalf("edgePlacement(bottom) = (%v, %v), want (horizontal, false)", dir, first)
		}
		if got := normalizedCoord(0, 0, 0); got != 0.5 {
			t.Fatalf("normalizedCoord(0, 0, 0) = %v, want 0.5", got)
		}
	})

	t.Run("edge and preview helpers cover top bottom and nil paths", func(t *testing.T) {
		t.Parallel()

		cell := &mux.LayoutCell{X: 10, Y: 20, W: 9, H: 7}
		if edge, _ := nearestDropEdge(cell.X+cell.W/2, cell.Y, cell); edge != "top" {
			t.Fatalf("nearestDropEdge(top) = %q, want top", edge)
		}
		if edge, _ := nearestDropEdge(cell.X+cell.W/2, cell.Y+cell.H-1, cell); edge != "bottom" {
			t.Fatalf("nearestDropEdge(bottom) = %q, want bottom", edge)
		}
		if canSplitDrop(nil, "left") {
			t.Fatal("canSplitDrop(nil, left) = true, want false")
		}
		if !canSplitDrop(cell, "top") {
			t.Fatal("canSplitDrop(cell, top) = false, want true")
		}
		if got := fullPanePreview(nil); got != nil {
			t.Fatalf("fullPanePreview(nil) = %#v, want nil", got)
		}
		if got := splitPanePreview(nil, "left"); got != nil {
			t.Fatalf("splitPanePreview(nil, left) = %#v, want nil", got)
		}
		if got := splitPanePreview(cell, "right"); got == nil || got.X != 15 || got.Y != 20 || got.W != 4 || got.H != 7 {
			t.Fatalf("splitPanePreview(right) = %#v, want X=15 Y=20 W=4 H=7", got)
		}
		if got := splitPanePreview(cell, "top"); got == nil || got.X != 10 || got.Y != 20 || got.W != 9 || got.H != 3 {
			t.Fatalf("splitPanePreview(top) = %#v, want X=10 Y=20 W=9 H=3", got)
		}
		if got := splitPanePreview(cell, "bottom"); got == nil || got.X != 10 || got.Y != 24 || got.W != 9 || got.H != 3 {
			t.Fatalf("splitPanePreview(bottom) = %#v, want X=10 Y=24 W=9 H=3", got)
		}
	})

	t.Run("too-small edge targets do not produce split drops", func(t *testing.T) {
		t.Parallel()

		left := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, 5, 6)
		right := mux.NewLeaf(&mux.Pane{ID: 2}, 6, 0, 4, 6)
		root := &mux.LayoutCell{
			X: 0, Y: 0, W: 10, H: 6,
			Dir:      mux.SplitVertical,
			Children: []*mux.LayoutCell{left, right},
		}
		left.Parent = root
		right.Parent = root

		cr := NewClientRenderer(10, 7)
		if got := resolvePaneDropTarget(cr, root, 1, right.X+right.W-1, right.Y+right.H/2); got != nil {
			t.Fatalf("resolvePaneDropTarget() = %#v, want nil for unsplittable target", got)
		}
	})
}

func TestHandleDisplayPaneSelectionSendsFocusCommand(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	if !cr.ShowDisplayPanes() {
		t.Fatal("ShowDisplayPanes should succeed")
	}

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	done := make(chan struct{})
	go func() {
		handleDisplayPaneSelection(cr, sender, '2', nil)
		close(done)
	}()

	msg := readCommandMessage(t, serverConn)
	if msg.Type != proto.MsgTypeCommand {
		t.Fatalf("message type = %d, want %d", msg.Type, proto.MsgTypeCommand)
	}
	if msg.CmdName != "focus" {
		t.Fatalf("command = %q, want focus", msg.CmdName)
	}
	if len(msg.CmdArgs) != 1 || msg.CmdArgs[0] != "2" {
		t.Fatalf("command args = %v, want [2]", msg.CmdArgs)
	}
	<-done
	if cr.DisplayPanesActive() {
		t.Fatal("display-panes overlay should hide after selection")
	}
}

func TestSplitBindingArgsInjectsActivePane(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)

	got, ok := splitBindingArgs(cr, config.Binding{Action: "split", Args: []string{"root", "v", "--focus"}})
	if !ok {
		t.Fatal("splitBindingArgs should succeed when layout is ready")
	}
	want := []string{"pane-1", "root", "v", "--focus"}
	if len(got) != len(want) {
		t.Fatalf("splitBindingArgs length = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("splitBindingArgs[%d] = %q, want %q (full=%v)", i, got[i], want[i], got)
		}
	}
}

func TestSplitBindingArgsRejectsUnknownActivePane(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(80, 24)

	if got, ok := splitBindingArgs(cr, config.Binding{Action: "split", Args: []string{"v", "--focus"}}); ok || got != nil {
		t.Fatalf("splitBindingArgs without layout = (%v, %t), want (nil, false)", got, ok)
	}
}

func TestHandleSplitBindingShowsErrorWhenLayoutNotReady(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(80, 24)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	t.Cleanup(sender.Close)

	var rendered bytes.Buffer
	handleSplitBinding(cr, sender, config.Binding{Action: "split", Args: []string{"v", "--focus"}}, &rendered)

	if !strings.Contains(rendered.String(), "\a") {
		t.Fatalf("split binding error should ring bell, got %q", rendered.String())
	}
	if got := cr.prefixMessage(); got != "cannot split: layout not ready yet" {
		t.Fatalf("split binding feedback = %q, want %q", got, "cannot split: layout not ready yet")
	}
	if err := serverConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	if _, err := proto.ReadMsg(serverConn); err == nil {
		t.Fatal("split binding without layout should not send a command")
	} else if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
		t.Fatalf("read command message error = %v, want timeout", err)
	}
}

func TestHandleSplitBindingSendsCommandWhenLayoutReady(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	t.Cleanup(sender.Close)

	var rendered bytes.Buffer
	done := make(chan struct{})
	go func() {
		handleSplitBinding(cr, sender, config.Binding{Action: "split", Args: []string{"root", "v", "--focus"}}, &rendered)
		close(done)
	}()

	msg := readCommandMessage(t, serverConn)
	if msg.Type != proto.MsgTypeCommand {
		t.Fatalf("message type = %d, want %d", msg.Type, proto.MsgTypeCommand)
	}
	if msg.CmdName != "split" {
		t.Fatalf("command = %q, want split", msg.CmdName)
	}
	if want := []string{"pane-1", "root", "v", "--focus"}; len(msg.CmdArgs) != len(want) {
		t.Fatalf("command args length = %v, want %v", msg.CmdArgs, want)
	} else {
		for i := range want {
			if msg.CmdArgs[i] != want[i] {
				t.Fatalf("command args[%d] = %q, want %q (full=%v)", i, msg.CmdArgs[i], want[i], msg.CmdArgs)
			}
		}
	}
	<-done
	if rendered.Len() != 0 {
		t.Fatalf("successful split binding should not render immediate feedback, got %q", rendered.String())
	}
}

func TestHandleMouseEventClickSendsFocusCommand(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	var drag dragState
	done := make(chan struct{})
	go func() {
		handleMouseEvent(mouse.Event{
			Action: mouse.Press,
			Button: mouse.ButtonLeft,
			X:      60,
			Y:      5,
		}, cr, sender, &drag, nil)
		close(done)
	}()

	msg := readCommandMessage(t, serverConn)
	if msg.Type != proto.MsgTypeCommand {
		t.Fatalf("message type = %d, want %d", msg.Type, proto.MsgTypeCommand)
	}
	if msg.CmdName != "focus" {
		t.Fatalf("command = %q, want focus", msg.CmdName)
	}
	if len(msg.CmdArgs) != 1 || msg.CmdArgs[0] != "2" {
		t.Fatalf("command args = %v, want [2]", msg.CmdArgs)
	}
	<-done
}

func TestHandleMouseEventStatusLinePressStartsPaneDragAndShowsSourceOverlay(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	t.Cleanup(sender.Close)

	var drag dragState
	done := make(chan struct{})
	go func() {
		handleMouseEvent(mouse.Event{
			Action: mouse.Press,
			Button: mouse.ButtonLeft,
			X:      60,
			Y:      0,
		}, cr, sender, &drag, nil)
		close(done)
	}()

	msg := readCommandMessage(t, serverConn)
	if msg.CmdName != "focus" || len(msg.CmdArgs) != 1 || msg.CmdArgs[0] != "2" {
		t.Fatalf("status-line press focus command = %q %v, want focus [2]", msg.CmdName, msg.CmdArgs)
	}
	<-done
	if !drag.PaneDragActive {
		t.Fatal("status-line press should start pane drag mode")
	}
	if drag.PaneDragPaneID != 2 {
		t.Fatalf("drag source pane = %d, want 2", drag.PaneDragPaneID)
	}
	labels := cr.overlayState().PaneLabels
	if len(labels) != 1 || labels[0].PaneID != 2 || labels[0].Label != "drag" {
		t.Fatalf("drag overlay labels = %+v, want pane-2 [drag]", labels)
	}
}

func TestHandleMouseEventPaneDragTogglesFocusedPaneCursor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		motions     []mouse.Event
		release     mouse.Event
		wantCommand string
		wantArgs    []string
	}{
		{
			name: "cancel on release without drop target",
			release: mouse.Event{
				Action: mouse.Release,
				Button: mouse.ButtonLeft,
				X:      10,
				Y:      0,
				LastX:  10,
				LastY:  0,
			},
		},
		{
			name: "restore cursor after dropping on pane center swap target",
			release: mouse.Event{
				Action: mouse.Release,
				Button: mouse.ButtonLeft,
				X:      0,
				Y:      0,
				LastX:  0,
				LastY:  0,
			},
			wantCommand: "swap",
			wantArgs:    []string{"1", "2"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cr := buildTestRenderer(t)

			clientConn, serverConn := net.Pipe()
			t.Cleanup(func() {
				clientConn.Close()
				serverConn.Close()
			})

			sender := newMessageSender(clientConn)
			t.Cleanup(sender.Close)

			var drag dragState
			handleMouseEvent(mouse.Event{
				Action: mouse.Press,
				Button: mouse.ButtonLeft,
				X:      10,
				Y:      0,
			}, cr, sender, &drag, nil)
			if tt.wantCommand == "swap" {
				x, y := paneCenterPoint(t, cr, 2)
				tt.motions = []mouse.Event{{
					Action: mouse.Motion,
					Button: mouse.ButtonLeft,
					X:      x,
					Y:      y,
					LastX:  10,
					LastY:  0,
				}}
				tt.release = mouse.Event{
					Action: mouse.Release,
					Button: mouse.ButtonLeft,
					X:      x,
					Y:      y,
					LastX:  x,
					LastY:  y,
				}
			}
			for _, ev := range tt.motions {
				handleMouseEvent(ev, cr, sender, &drag, nil)
			}

			duringDrag := cr.RenderDiff()
			if strings.Contains(duringDrag, render.ShowCursor) {
				t.Fatalf("drag render should hide the focused pane cursor, output=%q", duringDrag)
			}

			if tt.wantCommand == "" {
				handleMouseEvent(tt.release, cr, sender, &drag, nil)
				assertNoMessage(t, serverConn)
			} else {
				done := make(chan struct{})
				go func() {
					handleMouseEvent(tt.release, cr, sender, &drag, nil)
					close(done)
				}()
				msg := readCommandMessage(t, serverConn)
				if msg.CmdName != tt.wantCommand || !slices.Equal(msg.CmdArgs, tt.wantArgs) {
					t.Fatalf("release command = %q %v, want %q %v", msg.CmdName, msg.CmdArgs, tt.wantCommand, tt.wantArgs)
				}
				<-done
			}

			afterDrag := cr.RenderDiff()
			if !strings.Contains(afterDrag, render.ShowCursor) {
				t.Fatalf("post-drag render should restore the focused pane cursor, output=%q", afterDrag)
			}
		})
	}
}

func TestHandleMouseEventPaneDragHoveringPaneCenterShowsPlaceholderPreview(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	t.Cleanup(sender.Close)

	var drag dragState
	centerX, centerY := paneCenterPoint(t, cr, 2)
	handleMouseEvent(mouse.Event{
		Action: mouse.Press,
		Button: mouse.ButtonLeft,
		X:      10,
		Y:      0,
	}, cr, sender, &drag, nil)
	handleMouseEvent(mouse.Event{
		Action: mouse.Motion,
		Button: mouse.ButtonLeft,
		X:      centerX,
		Y:      centerY,
		LastX:  10,
		LastY:  0,
	}, cr, sender, &drag, nil)

	labels := cr.overlayState().PaneLabels
	if len(labels) != 1 {
		t.Fatalf("pane drag labels = %+v, want source drag label only", labels)
	}
	if labels[0].PaneID != 1 || labels[0].Label != "drag" {
		t.Fatalf("source drag label = %+v, want pane-1 drag", labels[0])
	}

	cr.RenderDiff()
	display := cr.CaptureDisplay()
	if !strings.Contains(display, "░") {
		t.Fatalf("display capture missing gray placeholder preview:\n%s", display)
	}
	if strings.Contains(display, "[swap]") {
		t.Fatalf("display capture should not show the old swap label preview:\n%s", display)
	}
}

func TestHandleMouseEventPaneDragReleaseOnPaneCenterSendsSwapCommand(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	t.Cleanup(sender.Close)

	var drag dragState
	centerX, centerY := paneCenterPoint(t, cr, 2)
	handleMouseEvent(mouse.Event{
		Action: mouse.Press,
		Button: mouse.ButtonLeft,
		X:      10,
		Y:      0,
	}, cr, sender, &drag, nil)
	handleMouseEvent(mouse.Event{
		Action: mouse.Motion,
		Button: mouse.ButtonLeft,
		X:      centerX,
		Y:      centerY,
		LastX:  10,
		LastY:  0,
	}, cr, sender, &drag, nil)

	done := make(chan struct{})
	go func() {
		handleMouseEvent(mouse.Event{
			Action: mouse.Release,
			Button: mouse.ButtonLeft,
			X:      centerX,
			Y:      centerY,
			LastX:  centerX,
			LastY:  centerY,
		}, cr, sender, &drag, nil)
		close(done)
	}()

	msg := readCommandMessage(t, serverConn)
	if msg.CmdName != "swap" || len(msg.CmdArgs) != 2 || msg.CmdArgs[0] != "1" || msg.CmdArgs[1] != "2" {
		t.Fatalf("swap command = %q %v, want swap [1 2]", msg.CmdName, msg.CmdArgs)
	}
	<-done
	if drag.PaneDragActive {
		t.Fatal("pane drag should clear after release")
	}
	if labels := cr.overlayState().PaneLabels; len(labels) != 0 {
		t.Fatalf("drag overlay should clear after release, got %+v", labels)
	}
}

func TestHandleMouseEventPaneDragReleaseOnPaneEdgeSendsDropPaneCommand(t *testing.T) {
	t.Parallel()

	cr := buildColumnDragRenderer(t, 1)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	t.Cleanup(sender.Close)

	var drag dragState
	edgeX, edgeY := paneEdgePoint(t, cr, 2, "left")
	done := make(chan struct{})
	go func() {
		handleMouseEvent(mouse.Event{
			Action: mouse.Press,
			Button: mouse.ButtonLeft,
			X:      10,
			Y:      0,
		}, cr, sender, &drag, nil)
		handleMouseEvent(mouse.Event{
			Action: mouse.Motion,
			Button: mouse.ButtonLeft,
			X:      edgeX,
			Y:      edgeY,
			LastX:  10,
			LastY:  0,
		}, cr, sender, &drag, nil)
		handleMouseEvent(mouse.Event{
			Action: mouse.Release,
			Button: mouse.ButtonLeft,
			X:      edgeX,
			Y:      edgeY,
			LastX:  edgeX,
			LastY:  edgeY,
		}, cr, sender, &drag, nil)
		close(done)
	}()

	msg := readCommandMessage(t, serverConn)
	if msg.CmdName != "drop-pane" || len(msg.CmdArgs) != 3 || msg.CmdArgs[0] != "1" || msg.CmdArgs[1] != "2" || msg.CmdArgs[2] != "left" {
		t.Fatalf("drop-pane command = %q %v, want drop-pane [1 2 left]", msg.CmdName, msg.CmdArgs)
	}
	<-done
}

func TestHandleMouseEventPaneDragReleaseOnWindowEdgeSendsRootDropCommand(t *testing.T) {
	t.Parallel()

	snap := twoPane80x23()
	snap.ActivePaneID = 2
	snap.Windows[0].ActivePaneID = 2
	cr := NewClientRenderer(80, 24)
	cr.HandleLayout(snap)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	t.Cleanup(sender.Close)

	var drag dragState
	rootX, rootY := windowEdgePoint(t, cr, "left")
	done := make(chan struct{})
	go func() {
		handleMouseEvent(mouse.Event{
			Action: mouse.Press,
			Button: mouse.ButtonLeft,
			X:      60,
			Y:      0,
		}, cr, sender, &drag, nil)
		handleMouseEvent(mouse.Event{
			Action: mouse.Motion,
			Button: mouse.ButtonLeft,
			X:      rootX,
			Y:      rootY,
			LastX:  60,
			LastY:  0,
		}, cr, sender, &drag, nil)
		handleMouseEvent(mouse.Event{
			Action: mouse.Release,
			Button: mouse.ButtonLeft,
			X:      rootX,
			Y:      rootY,
			LastX:  rootX,
			LastY:  rootY,
		}, cr, sender, &drag, nil)
		close(done)
	}()

	msg := readCommandMessage(t, serverConn)
	if msg.CmdName != "drop-pane" || len(msg.CmdArgs) != 3 || msg.CmdArgs[0] != "2" || msg.CmdArgs[1] != "root" || msg.CmdArgs[2] != "left" {
		t.Fatalf("root drop command = %q %v, want drop-pane [2 root left]", msg.CmdName, msg.CmdArgs)
	}
	<-done
}

func TestResolvePaneDropTargetUsesZoneTargets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		cr         *ClientRenderer
		sourcePane uint32
		point      func(t *testing.T, cr *ClientRenderer) (int, int)
		wantCmds   []paneDragCommand
	}{
		{
			name:       "center zone swaps hovered pane",
			cr:         buildTestRenderer(t),
			sourcePane: 1,
			point: func(t *testing.T, cr *ClientRenderer) (int, int) {
				return paneCenterPoint(t, cr, 2)
			},
			wantCmds: []paneDragCommand{{
				name: "swap",
				args: []string{"1", "2"},
			}},
		},
		{
			name:       "edge zone splits hovered pane at nearest edge",
			cr:         buildColumnDragRenderer(t, 1),
			sourcePane: 1,
			point: func(t *testing.T, cr *ClientRenderer) (int, int) {
				return paneEdgePoint(t, cr, 2, "left")
			},
			wantCmds: []paneDragCommand{{
				name: "drop-pane",
				args: []string{"1", "2", "left"},
			}},
		},
		{
			name:       "window boundary takes root split precedence",
			cr:         buildTestRenderer(t),
			sourcePane: 2,
			point: func(t *testing.T, cr *ClientRenderer) (int, int) {
				return windowEdgePoint(t, cr, "left")
			},
			wantCmds: []paneDragCommand{{
				name: "drop-pane",
				args: []string{"2", "root", "left"},
			}},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			x, y := tt.point(t, tt.cr)
			target := resolvePaneDropTarget(tt.cr, tt.cr.VisibleLayout(), tt.sourcePane, x, y)
			if target == nil {
				t.Fatal("resolvePaneDropTarget() = nil, want target")
			}
			if !slices.EqualFunc(target.commands, tt.wantCmds, func(a, b paneDragCommand) bool {
				return a.name == b.name && slices.Equal(a.args, b.args)
			}) {
				t.Fatalf("commands = %+v, want %+v", target.commands, tt.wantCmds)
			}
		})
	}
}

func TestCaptureDisplayShowsPaneDragOverlay(t *testing.T) {
	t.Parallel()

	cr := buildColumnDragRenderer(t, 2)
	cr.showPaneDragOverlay(2, &render.DropIndicatorOverlay{
		X: 0,
		Y: 12,
		W: 39,
		H: 10,
	})
	cr.RenderDiff()

	display := cr.CaptureDisplay()
	if !strings.Contains(display, "[drag]") {
		t.Fatalf("display capture missing drag label:\n%s", display)
	}
	if !strings.Contains(display, "░░") {
		t.Fatalf("display capture missing placeholder overlay:\n%s", display)
	}

	cr.hidePaneDragOverlay()
	cr.RenderDiff()
	if cleared := cr.CaptureDisplay(); strings.Contains(cleared, "[drag]") || strings.Contains(cleared, "░░") {
		t.Fatalf("display capture should clear drag overlay:\n%s", cleared)
	}
}

func TestHandleMouseEventGlobalBarClickSendsSelectWindowCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		activeWindow uint32
		sourceWindow int
		clickLabel   string
		wantWindow   string
	}{
		{
			name:         "click second tab from first window",
			activeWindow: 1,
			sourceWindow: 2,
			clickLabel:   "2:logs",
			wantWindow:   "2",
		},
		{
			name:         "click first tab from second window",
			activeWindow: 2,
			sourceWindow: 1,
			clickLabel:   "1:editor",
			wantWindow:   "1",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cr := buildMultiWindowRendererAt(t, tt.activeWindow)

			clientConn, serverConn := net.Pipe()
			t.Cleanup(func() {
				clientConn.Close()
				serverConn.Close()
			})

			sender := newMessageSender(clientConn)
			t.Cleanup(sender.Close)

			var drag dragState
			x := globalBarClickColumn(t, cr, tt.clickLabel)
			y := globalBarRow(t, cr)

			handleMouseEvent(mouse.Event{
				Action: mouse.Press,
				Button: mouse.ButtonLeft,
				X:      x,
				Y:      y,
			}, cr, sender, &drag, nil)
			if !drag.WindowTabActive || drag.WindowDragSourceIndex != tt.sourceWindow {
				t.Fatalf("window drag state after press = %+v", drag)
			}
			assertNoMessage(t, serverConn)

			done := make(chan struct{})
			go func() {
				handleMouseEvent(mouse.Event{
					Action: mouse.Release,
					Button: mouse.ButtonLeft,
					X:      x,
					Y:      y,
				}, cr, sender, &drag, nil)
				close(done)
			}()

			msg := readCommandMessage(t, serverConn)
			if msg.Type != proto.MsgTypeCommand {
				t.Fatalf("message type = %d, want %d", msg.Type, proto.MsgTypeCommand)
			}
			if msg.CmdName != "select-window" {
				t.Fatalf("command = %q, want select-window", msg.CmdName)
			}
			if len(msg.CmdArgs) != 1 || msg.CmdArgs[0] != tt.wantWindow {
				t.Fatalf("command args = %v, want [%s]", msg.CmdArgs, tt.wantWindow)
			}
			<-done
			if drag.WindowTabActive {
				t.Fatalf("window drag should clear on release, got %+v", drag)
			}
		})
	}
}

func TestHandleMouseEventGlobalBarDragSendsReorderWindowCommand(t *testing.T) {
	t.Parallel()

	cr := buildThreeWindowRendererAt(t, 2)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	t.Cleanup(sender.Close)

	var drag dragState
	pressX := globalBarClickColumn(t, cr, "1:editor")
	hoverX := globalBarClickColumn(t, cr, "3:docs") + len("3:docs") - 1
	y := globalBarRow(t, cr)

	handleMouseEvent(mouse.Event{
		Action: mouse.Press,
		Button: mouse.ButtonLeft,
		X:      pressX,
		Y:      y,
	}, cr, sender, &drag, nil)
	assertNoMessage(t, serverConn)

	handleMouseEvent(mouse.Event{
		Action: mouse.Motion,
		Button: mouse.ButtonLeft,
		X:      hoverX,
		Y:      y,
	}, cr, sender, &drag, nil)

	if indicator := cr.overlayState().WindowDropIndicator; indicator == nil {
		t.Fatal("window drag motion should show a drop indicator")
	}

	done := make(chan struct{})
	go func() {
		handleMouseEvent(mouse.Event{
			Action: mouse.Release,
			Button: mouse.ButtonLeft,
			X:      hoverX,
			Y:      y,
		}, cr, sender, &drag, nil)
		close(done)
	}()

	msg := readCommandMessage(t, serverConn)
	if msg.Type != proto.MsgTypeCommand {
		t.Fatalf("message type = %d, want %d", msg.Type, proto.MsgTypeCommand)
	}
	if msg.CmdName != "reorder-window" {
		t.Fatalf("command = %q, want reorder-window", msg.CmdName)
	}
	if len(msg.CmdArgs) != 2 || msg.CmdArgs[0] != "1" || msg.CmdArgs[1] != "3" {
		t.Fatalf("command args = %v, want [1 3]", msg.CmdArgs)
	}
	<-done
	if indicator := cr.overlayState().WindowDropIndicator; indicator != nil {
		t.Fatalf("window drag release should clear the drop indicator, got %+v", indicator)
	}
}

func TestHandleMouseEventGlobalBarClickOutsideTabsDoesNothing(t *testing.T) {
	t.Parallel()

	cr := buildMultiWindowRenderer(t)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	t.Cleanup(sender.Close)

	var drag dragState
	x := globalBarClickColumn(t, cr, "panes")
	y := globalBarRow(t, cr)

	handleMouseEvent(mouse.Event{
		Action: mouse.Press,
		Button: mouse.ButtonLeft,
		X:      x,
		Y:      y,
	}, cr, sender, &drag, nil)

	assertNoMessage(t, serverConn)
}

func TestHandleMouseEventGlobalBarClickSingleWindowDoesNothing(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	t.Cleanup(sender.Close)

	var drag dragState
	x := globalBarClickColumn(t, cr, "amux")
	y := globalBarRow(t, cr)

	handleMouseEvent(mouse.Event{
		Action: mouse.Press,
		Button: mouse.ButtonLeft,
		X:      x,
		Y:      y,
	}, cr, sender, &drag, nil)

	assertNoMessage(t, serverConn)
}

func TestHandleMouseEventGlobalBarHelpClickTogglesHelpBar(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	msgCh := startTestRenderLoop(t, cr)
	cr.RenderDiff()

	var drag dragState
	x := globalBarClickColumn(t, cr, "? help")
	y := globalBarRow(t, cr)

	handleMouseEvent(mouse.Event{
		Action: mouse.Press,
		Button: mouse.ButtonLeft,
		X:      x,
		Y:      y,
	}, cr, nil, &drag, msgCh)
	if !cr.HelpBarActive() {
		t.Fatal("global bar help click should show the help bar")
	}

	handleMouseEvent(mouse.Event{
		Action: mouse.Press,
		Button: mouse.ButtonLeft,
		X:      x,
		Y:      y,
	}, cr, nil, &drag, msgCh)
	if cr.HelpBarActive() {
		t.Fatal("second global bar help click should hide the help bar")
	}
}

func TestHandleMouseEventGlobalBarClickSelectsWindowWhenHelpIsHidden(t *testing.T) {
	t.Parallel()

	cr := buildMultiWindowRenderer(t)
	cr.Resize(44, 24)

	if strings.Contains(cr.Capture(true), "? help") {
		t.Fatal("narrow global bar should hide the help toggle")
	}

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	t.Cleanup(sender.Close)

	var drag dragState
	x := globalBarClickColumn(t, cr, "2:logs")
	y := globalBarRow(t, cr)

	handleMouseEvent(mouse.Event{
		Action: mouse.Press,
		Button: mouse.ButtonLeft,
		X:      x,
		Y:      y,
	}, cr, sender, &drag, nil)
	assertNoMessage(t, serverConn)

	done := make(chan struct{})
	go func() {
		handleMouseEvent(mouse.Event{
			Action: mouse.Release,
			Button: mouse.ButtonLeft,
			X:      x,
			Y:      y,
		}, cr, sender, &drag, nil)
		close(done)
	}()

	msg := readCommandMessage(t, serverConn)
	if msg.Type != proto.MsgTypeCommand {
		t.Fatalf("message type = %d, want %d", msg.Type, proto.MsgTypeCommand)
	}
	if msg.CmdName != "select-window" {
		t.Fatalf("command = %q, want select-window", msg.CmdName)
	}
	if len(msg.CmdArgs) != 1 || msg.CmdArgs[0] != "2" {
		t.Fatalf("command args = %v, want [2]", msg.CmdArgs)
	}
	<-done
}

func TestHandleMouseEventPaneDragReleaseOnWindowTabMovesPaneToWindow(t *testing.T) {
	t.Parallel()

	cr := buildMultiWindowRenderer(t)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	t.Cleanup(sender.Close)

	var drag dragState
	startX, startY := paneStatusTargetCenter(t, cr, 2)
	tabX := globalBarClickColumn(t, cr, "2:logs")
	tabY := globalBarRow(t, cr)

	handleMouseEvent(mouse.Event{
		Action: mouse.Press,
		Button: mouse.ButtonLeft,
		X:      startX,
		Y:      startY,
	}, cr, sender, &drag, nil)
	readFocusCommand(t, serverConn, "2")
	handleMouseEvent(mouse.Event{
		Action: mouse.Motion,
		Button: mouse.ButtonLeft,
		X:      tabX,
		Y:      tabY,
		LastX:  startX,
		LastY:  startY,
	}, cr, sender, &drag, nil)
	handleMouseEvent(mouse.Event{
		Action: mouse.Release,
		Button: mouse.ButtonLeft,
		X:      tabX,
		Y:      tabY,
		LastX:  tabX,
		LastY:  tabY,
	}, cr, sender, &drag, nil)

	msg := readCommandMessage(t, serverConn)
	if msg.Type != proto.MsgTypeCommand {
		t.Fatalf("message type = %d, want %d", msg.Type, proto.MsgTypeCommand)
	}
	if msg.CmdName != "move-pane-to-window" {
		t.Fatalf("command = %q, want move-pane-to-window", msg.CmdName)
	}
	if want := []string{"2", "2"}; !slices.Equal(msg.CmdArgs, want) {
		t.Fatalf("command args = %v, want %v", msg.CmdArgs, want)
	}
}

func TestHandleMouseEventPaneDragHoverWindowTabTriggersDwellSelect(t *testing.T) {
	t.Parallel()

	cr := buildMultiWindowRenderer(t)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	t.Cleanup(sender.Close)

	scheduler := &fakeDragScheduler{}
	drag := dragState{afterFunc: scheduler.AfterFunc}

	startX, startY := paneStatusTargetCenter(t, cr, 2)
	tabX := globalBarClickColumn(t, cr, "2:logs")
	tabY := globalBarRow(t, cr)

	handleMouseEvent(mouse.Event{
		Action: mouse.Press,
		Button: mouse.ButtonLeft,
		X:      startX,
		Y:      startY,
	}, cr, sender, &drag, nil)
	readFocusCommand(t, serverConn, "2")
	handleMouseEvent(mouse.Event{
		Action: mouse.Motion,
		Button: mouse.ButtonLeft,
		X:      tabX,
		Y:      tabY,
		LastX:  startX,
		LastY:  startY,
	}, cr, sender, &drag, nil)

	timer := scheduler.Latest()
	if timer == nil {
		t.Fatal("expected dwell timer to be scheduled")
	}
	timer.Fire()

	msg := readCommandMessage(t, serverConn)
	if msg.Type != proto.MsgTypeCommand {
		t.Fatalf("message type = %d, want %d", msg.Type, proto.MsgTypeCommand)
	}
	if msg.CmdName != "select-window" {
		t.Fatalf("command = %q, want select-window", msg.CmdName)
	}
	if want := []string{"2"}; !slices.Equal(msg.CmdArgs, want) {
		t.Fatalf("command args = %v, want %v", msg.CmdArgs, want)
	}
	if !drag.PaneDragActive {
		t.Fatal("pane drag should remain active after dwell activation")
	}
}

func TestHandleMouseEventPaneDragHoverWindowTabCancelsDwellWhenLeavingTab(t *testing.T) {
	t.Parallel()

	cr := buildMultiWindowRenderer(t)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	t.Cleanup(sender.Close)

	scheduler := &fakeDragScheduler{}
	drag := dragState{afterFunc: scheduler.AfterFunc}

	startX, startY := paneStatusTargetCenter(t, cr, 2)
	tabX := globalBarClickColumn(t, cr, "2:logs")
	tabY := globalBarRow(t, cr)

	handleMouseEvent(mouse.Event{
		Action: mouse.Press,
		Button: mouse.ButtonLeft,
		X:      startX,
		Y:      startY,
	}, cr, sender, &drag, nil)
	readFocusCommand(t, serverConn, "2")
	handleMouseEvent(mouse.Event{
		Action: mouse.Motion,
		Button: mouse.ButtonLeft,
		X:      tabX,
		Y:      tabY,
		LastX:  startX,
		LastY:  startY,
	}, cr, sender, &drag, nil)

	timer := scheduler.Latest()
	if timer == nil {
		t.Fatal("expected dwell timer to be scheduled")
	}

	handleMouseEvent(mouse.Event{
		Action: mouse.Motion,
		Button: mouse.ButtonLeft,
		X:      10,
		Y:      10,
		LastX:  tabX,
		LastY:  tabY,
	}, cr, sender, &drag, nil)

	timer.Fire()
	assertNoMessage(t, serverConn)
}

func TestHandleMouseEventGlobalBarSessionClickDoesNotToggleHelpWhenHidden(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	cr.Resize(36, 24)

	if strings.Contains(cr.Capture(true), "? help") {
		t.Fatal("narrow global bar should hide the help toggle")
	}

	msgCh := startTestRenderLoop(t, cr)
	cr.RenderDiff()

	var drag dragState
	x := globalBarClickColumn(t, cr, "test")
	y := globalBarRow(t, cr)

	handleMouseEvent(mouse.Event{
		Action: mouse.Press,
		Button: mouse.ButtonLeft,
		X:      x,
		Y:      y,
	}, cr, nil, &drag, msgCh)
	if cr.HelpBarActive() {
		t.Fatal("clicking the session name should not toggle the help bar when help is hidden")
	}
}

func TestPaneAllowsMouseCopySelection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(*ClientRenderer)
		pane  uint32
		want  bool
	}{
		{
			name: "missing pane",
			pane: 99,
			want: false,
		},
		{
			name: "regular pane",
			pane: 1,
			want: true,
		},
		{
			name: "alt screen pane",
			setup: func(cr *ClientRenderer) {
				cr.HandlePaneOutput(1, []byte("\x1b[?1049h"))
			},
			pane: 1,
			want: false,
		},
		{
			name: "app mouse pane",
			setup: func(cr *ClientRenderer) {
				cr.HandlePaneOutput(1, []byte("\x1b[?1002h\x1b[?1006h"))
			},
			pane: 1,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cr := buildTestRenderer(t)
			if tt.setup != nil {
				tt.setup(cr)
			}

			if got := paneAllowsMouseCopySelection(cr, tt.pane); got != tt.want {
				t.Fatalf("paneAllowsMouseCopySelection(%d) = %v, want %v", tt.pane, got, tt.want)
			}
		})
	}
}

func TestHandleMouseEventBorderPressClearsCopyDragState(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	layout := cr.VisibleLayout()
	if layout == nil {
		t.Fatal("visible layout missing")
	}

	borderX := -1
	for x := 0; x < 80; x++ {
		if layout.FindBorderAt(x, 5) != nil {
			borderX = x
			break
		}
	}
	if borderX < 0 {
		t.Fatal("expected a vertical border in the test layout")
	}

	var drag dragState
	drag.CopyModeActive = true
	drag.CopyModePaneID = 1
	drag.CopyMoved = true

	handleMouseEvent(mouse.Event{
		Action: mouse.Press,
		Button: mouse.ButtonLeft,
		X:      borderX,
		Y:      5,
	}, cr, nil, &drag, nil)

	if !drag.Active {
		t.Fatal("border press should start a resize drag")
	}
	if drag.CopyModeActive {
		t.Fatal("border press should clear copy-mode drag state")
	}
	if drag.CopyModePaneID != 0 {
		t.Fatalf("border press should clear copy-mode pane id, got %d", drag.CopyModePaneID)
	}
}

func TestHandleMouseEventBorderDragMotionOverAppMousePaneKeepsDragActive(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	cr.HandlePaneOutput(2, []byte("\x1b[?1002h\x1b[?1006h"))

	layout := cr.VisibleLayout()
	if layout == nil {
		t.Fatal("visible layout missing")
	}

	borderX := -1
	for x := 0; x < 80; x++ {
		if layout.FindBorderAt(x, 5) != nil {
			borderX = x
			break
		}
	}
	if borderX < 0 {
		t.Fatal("expected a vertical border in the test layout")
	}

	var drag dragState
	handleMouseEvent(mouse.Event{
		Action: mouse.Press,
		Button: mouse.ButtonLeft,
		X:      borderX,
		Y:      5,
	}, cr, nil, &drag, nil)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	t.Cleanup(sender.Close)

	done := make(chan struct{})
	go func() {
		handleMouseEvent(mouse.Event{
			Action: mouse.Motion,
			Button: mouse.ButtonLeft,
			X:      60,
			Y:      5,
			LastX:  borderX,
			LastY:  5,
		}, cr, sender, &drag, nil)
		close(done)
	}()

	msg := readCommandMessage(t, serverConn)
	if msg.Type != proto.MsgTypeCommand {
		t.Fatalf("message type = %d, want %d", msg.Type, proto.MsgTypeCommand)
	}
	if msg.CmdName != "resize-border" {
		t.Fatalf("command = %q, want resize-border", msg.CmdName)
	}
	if got, want := msg.CmdArgs, []string{fmt.Sprintf("%d", borderX), "5", fmt.Sprintf("%d", 60-borderX)}; !slices.Equal(got, want) {
		t.Fatalf("command args = %v, want %v", got, want)
	}
	<-done

	if !drag.Active {
		t.Fatal("border drag motion over app-mouse pane should keep the drag active")
	}
	if drag.BorderX != 60 {
		t.Fatalf("border drag x = %d, want 60", drag.BorderX)
	}
	assertNoMessage(t, serverConn)
}

func TestHandleMouseEventDragStartsCopyModeAndCopiesSelection(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	var drag dragState

	var copied string
	stubCopyToClipboard(cr, func(text string) {
		copied = text
	})

	y := mux.StatusLineRows
	handleMouseEvent(mouse.Event{
		Action: mouse.Press,
		Button: mouse.ButtonLeft,
		X:      0,
		Y:      y,
	}, cr, sender, &drag, nil)

	if cr.InCopyMode(1) {
		t.Fatal("pane-1 should not enter copy mode until the drag moves")
	}

	handleMouseEvent(mouse.Event{
		Action: mouse.Motion,
		Button: mouse.ButtonLeft,
		X:      4,
		Y:      y,
		LastX:  0,
		LastY:  y,
	}, cr, sender, &drag, nil)

	cm := cr.CopyModeForPane(1)
	if cm == nil {
		t.Fatal("pane-1 should enter copy mode on mouse drag")
	}
	if got := cm.SelectedText(); got != "hello" {
		t.Fatalf("selected text during drag = %q, want %q", got, "hello")
	}

	handleMouseEvent(mouse.Event{
		Action: mouse.Release,
		Button: mouse.ButtonLeft,
		X:      4,
		Y:      y,
		LastX:  4,
		LastY:  y,
	}, cr, sender, &drag, nil)

	if copied != "hello" {
		t.Fatalf("copied text = %q, want %q", copied, "hello")
	}
	if cr.InCopyMode(1) {
		t.Fatal("pane-1 should exit copy mode after mouse drag copy")
	}
}

func TestHandleMouseEventQueuedDragStartsCopyModeAndCopiesSelection(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	msgCh := startTestRenderLoop(t, cr)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	var drag dragState

	var copied string
	stubCopyToClipboard(cr, func(text string) {
		copied = text
	})

	y := mux.StatusLineRows
	handleMouseEvent(mouse.Event{
		Action: mouse.Press,
		Button: mouse.ButtonLeft,
		X:      0,
		Y:      y,
	}, cr, sender, &drag, msgCh)
	handleMouseEvent(mouse.Event{
		Action: mouse.Motion,
		Button: mouse.ButtonLeft,
		X:      4,
		Y:      y,
		LastX:  0,
		LastY:  y,
	}, cr, sender, &drag, msgCh)
	handleMouseEvent(mouse.Event{
		Action: mouse.Release,
		Button: mouse.ButtonLeft,
		X:      4,
		Y:      y,
		LastX:  4,
		LastY:  y,
	}, cr, sender, &drag, msgCh)

	if copied != "hello" {
		t.Fatalf("copied text = %q, want %q", copied, "hello")
	}
	if cr.InCopyMode(1) {
		t.Fatal("pane-1 should exit copy mode after queued mouse drag copy")
	}
}

func TestHandleMouseEventDragMotionWithMissingPaneDoesNotEnterCopyMode(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(80, 24)
	cr.HandleLayout(&proto.LayoutSnapshot{
		SessionName:  "test",
		ActivePaneID: 99,
		Width:        80,
		Height:       23,
		Root: proto.CellSnapshot{
			X: 0, Y: 0, W: 80, H: 23,
			IsLeaf: true, Dir: -1, PaneID: 99,
		},
	})

	var drag dragState
	drag.CopyModePaneID = 99

	handleMouseEvent(mouse.Event{
		Action: mouse.Motion,
		Button: mouse.ButtonLeft,
		X:      1,
		Y:      mux.StatusLineRows,
	}, cr, nil, &drag, nil)

	if cr.InCopyMode(99) {
		t.Fatal("missing pane should not enter copy mode on mouse drag")
	}
	if drag.CopyModeActive {
		t.Fatal("missing pane should leave copy-mode drag inactive")
	}
}

func TestHandleMouseEventCopyModeDragCopiesSelectionAndExits(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	cr.EnterCopyMode(1)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	var drag dragState

	var copied string
	stubCopyToClipboard(cr, func(text string) {
		copied = text
	})

	y := mux.StatusLineRows
	handleMouseEvent(mouse.Event{
		Action: mouse.Press,
		Button: mouse.ButtonLeft,
		X:      0,
		Y:      y,
	}, cr, sender, &drag, nil)
	handleMouseEvent(mouse.Event{
		Action: mouse.Motion,
		Button: mouse.ButtonLeft,
		X:      4,
		Y:      y,
		LastX:  0,
		LastY:  y,
	}, cr, sender, &drag, nil)
	handleMouseEvent(mouse.Event{
		Action: mouse.Release,
		Button: mouse.ButtonLeft,
		X:      4,
		Y:      y,
		LastX:  4,
		LastY:  y,
	}, cr, sender, &drag, nil)

	if copied != "hello" {
		t.Fatalf("copied text = %q, want %q", copied, "hello")
	}
	if cr.InCopyMode(1) {
		t.Fatal("pane-1 should exit copy mode after mouse drag copy")
	}
}

func TestHandleMouseEventCopyModeDoubleClickSelectsWordAndArmsCopy(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	cr.EnterCopyMode(1)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	var drag dragState

	y := mux.StatusLineRows
	for i := 0; i < 2; i++ {
		handleMouseEvent(mouse.Event{
			Action: mouse.Press,
			Button: mouse.ButtonLeft,
			X:      1,
			Y:      y,
		}, cr, sender, &drag, nil)
		handleMouseEvent(mouse.Event{
			Action: mouse.Release,
			Button: mouse.ButtonLeft,
			X:      1,
			Y:      y,
		}, cr, sender, &drag, nil)
	}

	cm := cr.CopyModeForPane(1)
	if cm == nil {
		t.Fatal("pane-1 should remain in copy mode until delayed word copy fires")
	}
	if got := cm.SelectedText(); got != "hello" {
		t.Fatalf("double click selected %q, want %q", got, "hello")
	}
	if drag.PendingWordCopyPaneID != 1 {
		t.Fatalf("pending word copy pane = %d, want 1", drag.PendingWordCopyPaneID)
	}
}

func TestHandleMouseEventQueuedCopyModeDoubleClickSelectsWordAndArmsCopy(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	cr.EnterCopyMode(1)
	msgCh := startTestRenderLoop(t, cr)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	var drag dragState

	y := mux.StatusLineRows
	for i := 0; i < 2; i++ {
		handleMouseEvent(mouse.Event{
			Action: mouse.Press,
			Button: mouse.ButtonLeft,
			X:      1,
			Y:      y,
		}, cr, sender, &drag, msgCh)
		handleMouseEvent(mouse.Event{
			Action: mouse.Release,
			Button: mouse.ButtonLeft,
			X:      1,
			Y:      y,
		}, cr, sender, &drag, msgCh)
	}

	cm := cr.CopyModeForPane(1)
	if cm == nil {
		t.Fatal("pane-1 should remain in copy mode until delayed word copy fires")
	}
	if got := cm.SelectedText(); got != "hello" {
		t.Fatalf("double click selected %q, want %q", got, "hello")
	}
	if drag.PendingWordCopyPaneID != 1 {
		t.Fatalf("pending word copy pane = %d, want 1", drag.PendingWordCopyPaneID)
	}
}

func TestHandleMouseEventCopyModeTripleClickCopiesLine(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	cr.EnterCopyMode(1)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	var drag dragState

	var copied string
	stubCopyToClipboard(cr, func(text string) {
		copied = text
	})

	y := mux.StatusLineRows
	for i := 0; i < 3; i++ {
		handleMouseEvent(mouse.Event{
			Action: mouse.Press,
			Button: mouse.ButtonLeft,
			X:      1,
			Y:      y,
		}, cr, sender, &drag, nil)
		handleMouseEvent(mouse.Event{
			Action: mouse.Release,
			Button: mouse.ButtonLeft,
			X:      1,
			Y:      y,
		}, cr, sender, &drag, nil)
	}

	if copied != "hello from pane 1\n" {
		t.Fatalf("triple click copied %q, want %q", copied, "hello from pane 1\n")
	}
	if cr.InCopyMode(1) {
		t.Fatal("pane-1 should exit copy mode after triple-click line copy")
	}
}

func TestHandleMouseEventQueuedScrollUpAndDownUsesCopyMode(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	msgCh := startTestRenderLoop(t, cr)

	y := mux.StatusLineRows
	handleMouseEvent(mouse.Event{
		Button: mouse.ScrollUp,
		X:      0,
		Y:      y,
	}, cr, nil, nil, msgCh)

	if !cr.InCopyMode(1) {
		t.Fatal("scroll up should enter copy mode on a regular pane")
	}
	cm := cr.CopyModeForPane(1)
	if cm == nil {
		t.Fatal("pane-1 copy mode missing after scroll up")
	}
	if !cm.ScrollExit() {
		t.Fatal("scroll up should arm scroll-exit")
	}

	handleMouseEvent(mouse.Event{
		Button: mouse.ScrollDown,
		X:      0,
		Y:      y,
	}, cr, nil, nil, msgCh)

	if cr.InCopyMode(1) {
		t.Fatal("scroll down back to live view should exit copy mode when scroll-exit is armed")
	}
}

func TestHandleMouseEventForwardsAppMouseClickToPane(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	cr.HandlePaneOutput(1, []byte("\x1b[?1000h\x1b[?1006h"))

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	t.Cleanup(sender.Close)

	var drag dragState
	y := mux.StatusLineRows

	pressDone := make(chan struct{}, 1)
	go func() {
		handleMouseEvent(mouse.Event{
			Action: mouse.Press,
			Button: mouse.ButtonLeft,
			X:      0,
			Y:      y,
		}, cr, sender, &drag, nil)
		pressDone <- struct{}{}
	}()

	press := readCommandMessage(t, serverConn)
	if press.Type != proto.MsgTypeInputPane {
		t.Fatalf("press message type = %d, want %d", press.Type, proto.MsgTypeInputPane)
	}
	if press.PaneID != 1 {
		t.Fatalf("press pane id = %d, want 1", press.PaneID)
	}
	if got := string(press.PaneData); got != "\x1b[<0;1;1M" {
		t.Fatalf("press pane data = %q, want %q", got, "\x1b[<0;1;1M")
	}
	<-pressDone
	if drag.Active || drag.PaneDragActive || drag.CopyModeActive || drag.CopyModePaneID != 0 {
		t.Fatalf("press should not start local mouse handling, got %+v", drag)
	}
	assertNoMessage(t, serverConn)

	releaseDone := make(chan struct{}, 1)
	go func() {
		handleMouseEvent(mouse.Event{
			Action: mouse.Release,
			Button: mouse.ButtonLeft,
			X:      0,
			Y:      y,
		}, cr, sender, &drag, nil)
		releaseDone <- struct{}{}
	}()

	release := readCommandMessage(t, serverConn)
	if release.Type != proto.MsgTypeInputPane {
		t.Fatalf("release message type = %d, want %d", release.Type, proto.MsgTypeInputPane)
	}
	if release.PaneID != 1 {
		t.Fatalf("release pane id = %d, want 1", release.PaneID)
	}
	if got := string(release.PaneData); got != "\x1b[<0;1;1m" {
		t.Fatalf("release pane data = %q, want %q", got, "\x1b[<0;1;1m")
	}
	<-releaseDone
	assertNoMessage(t, serverConn)
}

func TestHandleMouseEventAppMousePressFocusesInactivePaneBeforePassthrough(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	cr.HandlePaneOutput(2, []byte("\x1b[?1000h\x1b[?1006h"))

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	t.Cleanup(sender.Close)

	var drag dragState
	x, y := paneCenterPoint(t, cr, 2)
	target := mouseTargetAt(cr.VisibleLayout(), x, y)
	if target == nil {
		t.Fatal("expected pane-2 mouse target")
	}

	pressDone := make(chan struct{}, 1)
	go func() {
		handleMouseEvent(mouse.Event{
			Action: mouse.Press,
			Button: mouse.ButtonLeft,
			X:      x,
			Y:      y,
		}, cr, sender, &drag, nil)
		pressDone <- struct{}{}
	}()

	focus := readCommandMessage(t, serverConn)
	if focus.Type != proto.MsgTypeCommand {
		t.Fatalf("focus message type = %d, want %d", focus.Type, proto.MsgTypeCommand)
	}
	if focus.CmdName != "focus" || len(focus.CmdArgs) != 1 || focus.CmdArgs[0] != "2" {
		t.Fatalf("focus command = %q %v, want focus [2]", focus.CmdName, focus.CmdArgs)
	}

	press := readCommandMessage(t, serverConn)
	if press.Type != proto.MsgTypeInputPane {
		t.Fatalf("press message type = %d, want %d", press.Type, proto.MsgTypeInputPane)
	}
	if press.PaneID != 2 {
		t.Fatalf("press pane id = %d, want 2", press.PaneID)
	}
	if got := string(press.PaneData); got != fmt.Sprintf("\x1b[<0;%d;%dM", target.localX+1, target.localY+1) {
		t.Fatalf("press pane data = %q, want %q", got, fmt.Sprintf("\x1b[<0;%d;%dM", target.localX+1, target.localY+1))
	}
	<-pressDone
	assertNoMessage(t, serverConn)

	releaseDone := make(chan struct{}, 1)
	go func() {
		handleMouseEvent(mouse.Event{
			Action: mouse.Release,
			Button: mouse.ButtonLeft,
			X:      x,
			Y:      y,
		}, cr, sender, &drag, nil)
		releaseDone <- struct{}{}
	}()

	release := readCommandMessage(t, serverConn)
	if release.Type != proto.MsgTypeInputPane {
		t.Fatalf("release message type = %d, want %d", release.Type, proto.MsgTypeInputPane)
	}
	if release.PaneID != 2 {
		t.Fatalf("release pane id = %d, want 2", release.PaneID)
	}
	if got := string(release.PaneData); got != fmt.Sprintf("\x1b[<0;%d;%dm", target.localX+1, target.localY+1) {
		t.Fatalf("release pane data = %q, want %q", got, fmt.Sprintf("\x1b[<0;%d;%dm", target.localX+1, target.localY+1))
	}
	<-releaseDone
	assertNoMessage(t, serverConn)
}

func TestCopyModeHelpersSetCursorStartSelectionAndCopy(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	cr.EnterCopyMode(1)

	if action := cr.CopyModeSetCursor(1, 1, 0); action != copymode.ActionRedraw {
		t.Fatalf("CopyModeSetCursor() = %d, want ActionRedraw", action)
	}
	if !cr.IsDirty() {
		t.Fatal("CopyModeSetCursor should mark the renderer dirty")
	}

	if action := cr.CopyModeStartSelection(1); action != copymode.ActionRedraw {
		t.Fatalf("CopyModeStartSelection() = %d, want ActionRedraw", action)
	}

	cm := cr.CopyModeForPane(1)
	if cm == nil {
		t.Fatal("pane-1 copy mode missing")
	}
	if action := cm.HandleInput([]byte{'l', 'y'}); action != copymode.ActionYank {
		t.Fatalf("selection yank = %d, want ActionYank", action)
	}

	var copied string
	stubCopyToClipboard(cr, func(text string) {
		copied = text
	})

	cr.CopyModeCopySelection(1)
	if copied != "el" {
		t.Fatalf("copied text = %q, want %q", copied, "el")
	}
	if cr.InCopyMode(1) {
		t.Fatal("pane-1 should exit copy mode after copy")
	}
}

func TestCopyModeCopySelectionAppendsCopyBuffer(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)

	var copied []string
	stubCopyToClipboard(cr, func(text string) {
		copied = append(copied, text)
	})

	cr.EnterCopyMode(1)
	cm := cr.CopyModeForPane(1)
	if cm == nil {
		t.Fatal("pane-1 copy mode missing")
	}
	cr.CopyModeSetCursor(1, 0, 0)
	cr.CopyModeStartSelection(1)
	if action := cm.HandleInput([]byte{'l', 'y'}); action != copymode.ActionYank {
		t.Fatalf("initial yank = %d, want ActionYank", action)
	}
	cr.CopyModeCopySelection(1)

	cr.EnterCopyMode(1)
	cm = cr.CopyModeForPane(1)
	if cm == nil {
		t.Fatal("pane-1 copy mode missing after re-enter")
	}
	cr.CopyModeSetCursor(1, 2, 0)
	cr.CopyModeStartSelection(1)
	if action := cm.HandleInput([]byte{'l', 'A'}); action != copymode.ActionYank {
		t.Fatalf("append yank = %d, want ActionYank", action)
	}
	cr.CopyModeCopySelection(1)

	if len(copied) != 2 {
		t.Fatalf("clipboard writes = %d, want 2", len(copied))
	}
	if copied[0] != "he" {
		t.Fatalf("first clipboard write = %q, want %q", copied[0], "he")
	}
	if copied[1] != "hell" {
		t.Fatalf("second clipboard write = %q, want %q", copied[1], "hell")
	}
	if got := cr.CopyBuffer(); got != "hell" {
		t.Fatalf("copyBuffer = %q, want %q", got, "hell")
	}
}
