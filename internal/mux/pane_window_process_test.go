package mux

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()

	if cond() {
		return
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-timer.C:
			t.Fatal("timed out waiting for condition")
		case <-ticker.C:
			if cond() {
				return
			}
		}
	}
}

func TestProxyPaneFeedOutputSnapshotsAndClose(t *testing.T) {
	t.Parallel()

	var writes [][]byte
	var callbackSeq uint64
	var callbackData []byte
	p := NewProxyPaneWithScrollback(7, PaneMeta{
		Name:  "pane-7",
		Host:  DefaultHost,
		Color: "f5e0dc",
	}, 20, 1, 4, func(_ uint32, data []byte, seq uint64) {
		callbackData = append([]byte(nil), data...)
		callbackSeq = seq
	}, nil, func(data []byte) (int, error) {
		writes = append(writes, append([]byte(nil), data...))
		return len(data), nil
	})

	if !p.IsProxy() {
		t.Fatal("proxy pane should report IsProxy")
	}
	if got, err := p.Write([]byte("input")); err != nil || got != 5 {
		t.Fatalf("Write() = (%d, %v), want (5, nil)", got, err)
	}
	if len(writes) != 1 || string(writes[0]) != "input" {
		t.Fatalf("write override captured %q, want %q", writes, "input")
	}

	p.FeedOutput([]byte("first\r\nsecond"))
	if callbackSeq != 1 || string(callbackData) != "first\r\nsecond" {
		t.Fatalf("onOutput = seq %d data %q, want seq 1 data %q", callbackSeq, callbackData, "first\r\nsecond")
	}
	if p.OutputSeq() != 1 {
		t.Fatalf("OutputSeq() = %d, want 1", p.OutputSeq())
	}
	if got := p.Output(1); got != "second" {
		t.Fatalf("Output(1) = %q, want %q", got, "second")
	}
	if got := p.ScrollbackLines(); len(got) != 1 || got[0] != "first" {
		t.Fatalf("ScrollbackLines() = %v, want [first]", got)
	}
	if !p.ScreenContains("second") {
		t.Fatal("ScreenContains(second) = false, want true")
	}

	history, screen, seq := p.HistoryScreenSnapshot()
	if seq != 1 {
		t.Fatalf("HistoryScreenSnapshot seq = %d, want 1", seq)
	}
	if len(history) != 1 || history[0] != "first" {
		t.Fatalf("HistoryScreenSnapshot history = %v, want [first]", history)
	}
	if !strings.Contains(screen, "second") {
		t.Fatalf("HistoryScreenSnapshot screen = %q, want visible content", screen)
	}

	snap := p.CaptureSnapshot()
	if len(snap.History) != 1 || snap.History[0] != "first" {
		t.Fatalf("CaptureSnapshot history = %v, want [first]", snap.History)
	}
	if len(snap.Content) != 1 || snap.Content[0] != "second" {
		t.Fatalf("CaptureSnapshot content = %v, want [second]", snap.Content)
	}
	if p.Render() == "" || p.RenderScreen() == "" || p.RenderWithoutCursorBlock() == "" {
		t.Fatal("render helpers should return non-empty screen content")
	}
	if _, row := p.CursorPos(); row != 0 {
		t.Fatalf("CursorPos row = %d, want 0 for one-line proxy pane", row)
	}
	if p.CursorHidden() {
		t.Fatal("CursorHidden() = true, want false for default emulator state")
	}

	p.ReplayScreen("\r\nthird")
	if !p.ScreenContains("third") {
		t.Fatal("ReplayScreen should update the emulator state")
	}

	if err := p.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("second Close() = %v, want nil", err)
	}
}

func TestRestorePaneWithScrollbackUsesExistingPTYAndProcess(t *testing.T) {
	t.Parallel()

	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open: %v", err)
	}
	defer tty.Close()

	cmd := exec.Command("sleep", "5")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	waitUntil(t, time.Second, func() bool {
		return processName(cmd.Process.Pid) != ""
	})

	p, err := RestorePaneWithScrollback(9, PaneMeta{
		Name:  "pane-9",
		Host:  DefaultHost,
		Color: "f2cdcd",
	}, int(ptmx.Fd()), cmd.Process.Pid, 10, 4, DefaultScrollbackLines, nil, nil)
	if err != nil {
		t.Fatalf("RestorePaneWithScrollback: %v", err)
	}

	if got := p.PtmxFd(); got != int(ptmx.Fd()) {
		t.Fatalf("PtmxFd() = %d, want %d", got, ptmx.Fd())
	}
	if got := p.ProcessPid(); got != cmd.Process.Pid {
		t.Fatalf("ProcessPid() = %d, want %d", got, cmd.Process.Pid)
	}
	var shellName string
	waitUntil(t, time.Second, func() bool {
		shellName = p.ShellName()
		return shellName != ""
	})

	createdAt := time.Unix(123, 456)
	p.SetCreatedAt(createdAt)
	if got := p.CreatedAt(); !got.Equal(createdAt) {
		t.Fatalf("CreatedAt() = %v, want %v", got, createdAt)
	}

	p.ReplayScreen("hello")
	if !strings.Contains(p.Render(), "hello") {
		t.Fatalf("Render() = %q, want replayed content", p.Render())
	}

	p.ptmx = nil
	if err := p.Resize(12, 5); err != nil {
		t.Fatalf("Resize(): %v", err)
	}
	if cols, rows := p.EmulatorSize(); cols != 12 || rows != 5 {
		t.Fatalf("EmulatorSize() = %dx%d, want 12x5", cols, rows)
	}

	p.process = nil
	if err := p.Close(); err != nil {
		t.Fatalf("Close() after clearing process = %v", err)
	}
}

func TestPaneCwdAndProcessHelpers(t *testing.T) {
	t.Parallel()

	if got := PaneCwd(0); got != "" {
		t.Fatalf("PaneCwd(0) = %q, want empty", got)
	}

	if cwd := PaneCwd(os.Getpid()); cwd != "" && !filepath.IsAbs(cwd) {
		t.Fatalf("PaneCwd(os.Getpid()) = %q, want an absolute path or empty best-effort result", cwd)
	}

	var ts atomicInt64
	storeUnixTime(&ts, time.Unix(5, 7))
	if got := loadUnixTime(&ts); !got.Equal(time.Unix(5, 7)) {
		t.Fatalf("loadUnixTime() = %v, want %v", got, time.Unix(5, 7))
	}
	storeUnixTime(&ts, time.Time{})
	if got := loadUnixTime(&ts); !got.IsZero() {
		t.Fatalf("zero store/load = %v, want zero", got)
	}
}

type atomicInt64 struct{ value int64 }

func (a *atomicInt64) Load() int64   { return a.value }
func (a *atomicInt64) Store(v int64) { a.value = v }

func TestAgentStatusTracksBusyAndIdle(t *testing.T) {
	t.Parallel()

	proc, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("FindProcess: %v", err)
	}

	pane := &Pane{
		process:   proc,
		createdAt: time.Now().Add(-time.Minute),
	}

	idle := (&Pane{createdAt: pane.createdAt}).AgentStatus()
	if !idle.Idle || len(idle.ChildPIDs) != 0 || !idle.IdleSince.Equal(pane.createdAt) {
		t.Fatalf("idle-without-process = %+v, want idle since creation with no children", idle)
	}

	cmd := exec.Command("sleep", "5")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	waitUntil(t, time.Second, func() bool {
		return slices.Contains(childPIDs(os.Getpid()), cmd.Process.Pid)
	})

	if got := processName(cmd.Process.Pid); got == "" {
		t.Fatal("processName(child) = empty, want command name")
	}

	var busy AgentStatus
	waitUntil(t, time.Second, func() bool {
		busy = pane.AgentStatus()
		return !busy.Idle && slices.Contains(busy.ChildPIDs, cmd.Process.Pid) && busy.CurrentCommand != ""
	})

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill child: %v", err)
	}
	_, _ = cmd.Process.Wait()

	waitUntil(t, time.Second, func() bool {
		return !slices.Contains(childPIDs(os.Getpid()), cmd.Process.Pid)
	})

	waitUntil(t, time.Second, func() bool {
		idle = pane.AgentStatus()
		return idle.Idle && idle.CurrentCommand != ""
	})
	if idle.IdleSince.IsZero() {
		t.Fatal("idle AgentStatus IdleSince should be populated")
	}
}

func TestWindowZoomResolvePaneToggleMinimizeAndResizeBorder(t *testing.T) {
	t.Parallel()

	p1 := &Pane{ID: 1, Meta: PaneMeta{Name: "pane-1", Host: DefaultHost, Color: "f5e0dc"}, emulator: NewVTEmulatorWithScrollback(10, 5, DefaultScrollbackLines)}
	p2 := &Pane{ID: 2, Meta: PaneMeta{Name: "pane-2", Host: DefaultHost, Color: "f2cdcd"}, emulator: NewVTEmulatorWithScrollback(10, 5, DefaultScrollbackLines)}
	w := NewWindow(p1, 80, 24)
	if _, err := w.SplitRoot(SplitHorizontal, p2); err != nil {
		t.Fatalf("SplitRoot: %v", err)
	}

	if got := w.PaneCount(); got != 2 {
		t.Fatalf("PaneCount() = %d, want 2", got)
	}
	if got := w.ResolvePane("pane-2"); got != p2 {
		t.Fatalf("ResolvePane(name) = %+v, want pane-2", got)
	}
	if got := w.ResolvePane("2"); got != p2 {
		t.Fatalf("ResolvePane(id) = %+v, want pane-2", got)
	}
	if got := w.ResolvePane("pane-"); got == nil {
		t.Fatal("ResolvePane(prefix) = nil, want a matching pane")
	}

	if err := w.Zoom(1); err != nil {
		t.Fatalf("Zoom: %v", err)
	}
	if w.ZoomedPaneID != 1 || w.ActivePane != p1 {
		t.Fatalf("zoom state = zoomed %d active %d, want pane-1", w.ZoomedPaneID, w.ActivePane.ID)
	}
	if cols, rows := p1.EmulatorSize(); cols != 80 || rows != 23 {
		t.Fatalf("zoomed emulator size = %dx%d, want 80x23", cols, rows)
	}

	w.Resize(100, 30)
	if cols, rows := p1.EmulatorSize(); cols != 100 || rows != 29 {
		t.Fatalf("zoomed resize = %dx%d, want 100x29", cols, rows)
	}

	w.FocusPane(p2)
	if w.ZoomedPaneID != 0 {
		t.Fatalf("FocusPane on another pane should unzoom, got zoomed pane %d", w.ZoomedPaneID)
	}
	cell1 := w.Root.FindPane(1)
	if cols, rows := p1.EmulatorSize(); cols != cell1.W || rows != PaneContentHeight(cell1.H) {
		t.Fatalf("unzoom restored size = %dx%d, want %dx%d", cols, rows, cell1.W, PaneContentHeight(cell1.H))
	}

	name, minimized, err := w.ToggleMinimize()
	if err != nil {
		t.Fatalf("ToggleMinimize minimize: %v", err)
	}
	if name != "pane-2" || !minimized || !p2.Meta.Minimized {
		t.Fatalf("ToggleMinimize minimize = (%q, %v, minimized=%v)", name, minimized, p2.Meta.Minimized)
	}
	name, minimized, err = w.ToggleMinimize()
	if err != nil {
		t.Fatalf("ToggleMinimize restore: %v", err)
	}
	if name != "pane-2" || minimized || p2.Meta.Minimized {
		t.Fatalf("ToggleMinimize restore = (%q, %v, minimized=%v)", name, minimized, p2.Meta.Minimized)
	}

	borderY := w.Root.Children[0].H
	if !w.ResizeBorder(1, borderY, 1000) {
		t.Fatal("ResizeBorder should resize the shared border")
	}
	if w.Root.Children[1].H < PaneMinSize {
		t.Fatalf("ResizeBorder should clamp donor size, got %d", w.Root.Children[1].H)
	}
	if w.ResizeBorder(-1, -1, 5) {
		t.Fatal("ResizeBorder should fail for coordinates outside any border")
	}
}

func TestWindowSplitWithOptionsBackgroundPreservesZoomAndFocus(t *testing.T) {
	t.Parallel()

	p1 := &Pane{ID: 1, Meta: PaneMeta{Name: "pane-1", Host: DefaultHost, Color: "f5e0dc"}, emulator: NewVTEmulatorWithScrollback(10, 5, DefaultScrollbackLines)}
	p2 := &Pane{ID: 2, Meta: PaneMeta{Name: "pane-2", Host: DefaultHost, Color: "f2cdcd"}, emulator: NewVTEmulatorWithScrollback(10, 5, DefaultScrollbackLines)}
	p3 := &Pane{ID: 3, Meta: PaneMeta{Name: "pane-3", Host: DefaultHost, Color: "cba6f7"}, emulator: NewVTEmulatorWithScrollback(10, 5, DefaultScrollbackLines)}

	w := NewWindow(p1, 80, 24)
	if _, err := w.SplitRoot(SplitHorizontal, p2); err != nil {
		t.Fatalf("SplitRoot: %v", err)
	}
	if err := w.Zoom(1); err != nil {
		t.Fatalf("Zoom: %v", err)
	}

	if _, err := w.SplitWithOptions(SplitVertical, p3, SplitOptions{Background: true}); err != nil {
		t.Fatalf("SplitWithOptions: %v", err)
	}
	if w.ZoomedPaneID != 1 {
		t.Fatalf("ZoomedPaneID = %d, want 1", w.ZoomedPaneID)
	}
	if w.ActivePane != p1 {
		t.Fatalf("active pane = %v, want pane-1", w.ActivePane)
	}
	if w.Root.FindPane(3) == nil {
		t.Fatal("pane-3 should be present in the layout tree")
	}
	if cols, rows := p1.EmulatorSize(); cols != 80 || rows != 23 {
		t.Fatalf("zoomed pane size = %dx%d, want 80x23", cols, rows)
	}
	cell3 := w.Root.FindPane(3)
	if cell3 == nil {
		t.Fatal("pane-3 cell = nil, want visible leaf in layout tree")
	}
	if cols, rows := p3.EmulatorSize(); cols != cell3.W || rows != PaneContentHeight(cell3.H) {
		t.Fatalf("background pane size = %dx%d, want %dx%d", cols, rows, cell3.W, PaneContentHeight(cell3.H))
	}
}

func TestSnapshotWindowAndRebuildWindowFromSnapshot(t *testing.T) {
	t.Parallel()

	p1 := &Pane{ID: 1, Meta: PaneMeta{Name: "pane-1", Host: DefaultHost, Color: "f5e0dc", MinimizedSeq: 3}, emulator: NewVTEmulatorWithScrollback(10, 5, DefaultScrollbackLines)}
	p2 := &Pane{ID: 2, Meta: PaneMeta{Name: "pane-2", Host: DefaultHost, Color: "f2cdcd", Minimized: true, MinimizedSeq: 7}, emulator: NewVTEmulatorWithScrollback(12, 6, DefaultScrollbackLines)}
	w := NewWindow(p1, 80, 24)
	w.ID = 42
	w.Name = "main"
	if _, err := w.SplitRoot(SplitHorizontal, p2); err != nil {
		t.Fatalf("SplitRoot: %v", err)
	}
	w.ActivePane = p2
	w.ZoomedPaneID = 2

	ws := w.SnapshotWindow(3)
	if ws.ID != 42 || ws.Name != "main" || ws.Index != 3 {
		t.Fatalf("SnapshotWindow metadata = %+v", ws)
	}
	if ws.ActivePaneID != 2 || ws.ZoomedPaneID != 2 {
		t.Fatalf("SnapshotWindow active/zoom = (%d,%d), want (2,2)", ws.ActivePaneID, ws.ZoomedPaneID)
	}

	rebuilt := RebuildWindowFromSnapshot(ws, 80, 24, map[uint32]*Pane{1: p1, 2: p2})
	if rebuilt.ID != 42 || rebuilt.Name != "main" {
		t.Fatalf("RebuildWindowFromSnapshot metadata = %+v", rebuilt)
	}
	if rebuilt.ActivePane != p2 || rebuilt.ZoomedPaneID != 2 {
		t.Fatalf("rebuilt active/zoom = active %v zoom %d, want pane-2 and zoom 2", rebuilt.ActivePane, rebuilt.ZoomedPaneID)
	}
	if rebuilt.minimizeSeq != 7 {
		t.Fatalf("recoverMinimizeSeq = %d, want 7", rebuilt.minimizeSeq)
	}

	leaf := NewLeafByID(99, 1, 2, 3, 4)
	root := &LayoutCell{Dir: SplitVertical, Children: []*LayoutCell{leaf}, isLeaf: false}
	leaf.Parent = root
	if got := leaf.CellPaneID(); got != 99 {
		t.Fatalf("CellPaneID() = %d, want 99", got)
	}
	if got := root.FindByPaneID(99); got != leaf {
		t.Fatalf("FindByPaneID() = %v, want the leaf", got)
	}
	if got := root.FindByPaneID(100); got != nil {
		t.Fatalf("FindByPaneID(missing) = %v, want nil", got)
	}
}
