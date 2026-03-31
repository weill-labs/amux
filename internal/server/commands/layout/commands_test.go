package layout

import (
	"reflect"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
	commandpkg "github.com/weill-labs/amux/internal/server/commands"
)

type fakeLayoutContext struct {
	splitActorPaneID uint32
	splitArgs        SplitArgs
	splitCalled      bool
	splitResult      commandpkg.Result

	spawnActorPaneID uint32
	spawnArgs        SpawnArgs
	spawnCalled      bool
	spawnResult      commandpkg.Result

	focusActorPaneID uint32
	focusDirection   string
	focusCalled      bool
	focusResult      commandpkg.Result

	killActorPaneID uint32
	killArgs        KillArgs
	killCalled      bool
	killResult      commandpkg.Result

	copyModeActorPaneID uint32
	copyModeOptions     CopyModeOptions
	copyModeCalled      bool
	copyModeResult      commandpkg.Result

	equalizeWidths  bool
	equalizeHeights bool
	equalizeCalled  bool
	equalizeResult  commandpkg.Result

	resizePaneActorPaneID uint32
	resizePanePaneRef     string
	resizePaneDirection   string
	resizePaneDelta       int
	resizePaneCalled      bool
	resizePaneResult      commandpkg.Result

	setLeadActorPaneID uint32
	setLeadPaneRef     string
	setLeadCalled      bool
	setLeadResult      commandpkg.Result
}

func (f *fakeLayoutContext) Split(actorPaneID uint32, args SplitArgs) commandpkg.Result {
	f.splitActorPaneID = actorPaneID
	f.splitArgs = args
	f.splitCalled = true
	return f.splitResult
}

func (f *fakeLayoutContext) Focus(actorPaneID uint32, direction string) commandpkg.Result {
	f.focusActorPaneID = actorPaneID
	f.focusDirection = direction
	f.focusCalled = true
	return f.focusResult
}

func (f *fakeLayoutContext) Spawn(actorPaneID uint32, args SpawnArgs) commandpkg.Result {
	f.spawnActorPaneID = actorPaneID
	f.spawnArgs = args
	f.spawnCalled = true
	return f.spawnResult
}

func (f *fakeLayoutContext) Zoom(uint32, string) commandpkg.Result {
	return commandpkg.Result{}
}

func (f *fakeLayoutContext) Reset(uint32, string) commandpkg.Result {
	return commandpkg.Result{}
}

func (f *fakeLayoutContext) Kill(actorPaneID uint32, args KillArgs) commandpkg.Result {
	f.killActorPaneID = actorPaneID
	f.killArgs = args
	f.killCalled = true
	return f.killResult
}

func (f *fakeLayoutContext) Undo() commandpkg.Result {
	return commandpkg.Result{}
}

func (f *fakeLayoutContext) CopyMode(actorPaneID uint32, opts CopyModeOptions) commandpkg.Result {
	f.copyModeActorPaneID = actorPaneID
	f.copyModeOptions = opts
	f.copyModeCalled = true
	return f.copyModeResult
}

func (f *fakeLayoutContext) NewWindow(string) commandpkg.Result {
	return commandpkg.Result{}
}

func (f *fakeLayoutContext) SelectWindow(string) commandpkg.Result {
	return commandpkg.Result{}
}

func (f *fakeLayoutContext) NextWindow() commandpkg.Result {
	return commandpkg.Result{}
}

func (f *fakeLayoutContext) PrevWindow() commandpkg.Result {
	return commandpkg.Result{}
}

func (f *fakeLayoutContext) RenameWindow(string) commandpkg.Result {
	return commandpkg.Result{}
}

func (f *fakeLayoutContext) ResizeBorder(int, int, int) commandpkg.Result {
	return commandpkg.Result{}
}

func (f *fakeLayoutContext) ResizeActive(string, int) commandpkg.Result {
	return commandpkg.Result{}
}

func (f *fakeLayoutContext) ResizePane(actorPaneID uint32, paneRef, direction string, delta int) commandpkg.Result {
	f.resizePaneActorPaneID = actorPaneID
	f.resizePanePaneRef = paneRef
	f.resizePaneDirection = direction
	f.resizePaneDelta = delta
	f.resizePaneCalled = true
	return f.resizePaneResult
}

func (f *fakeLayoutContext) Equalize(widths, heights bool) commandpkg.Result {
	f.equalizeWidths = widths
	f.equalizeHeights = heights
	f.equalizeCalled = true
	return f.equalizeResult
}

func (f *fakeLayoutContext) ResizeWindow(int, int) commandpkg.Result {
	return commandpkg.Result{}
}

func (f *fakeLayoutContext) SetLead(actorPaneID uint32, paneRef string) commandpkg.Result {
	f.setLeadActorPaneID = actorPaneID
	f.setLeadPaneRef = paneRef
	f.setLeadCalled = true
	return f.setLeadResult
}

func (f *fakeLayoutContext) UnsetLead(uint32) commandpkg.Result {
	return commandpkg.Result{}
}

func (f *fakeLayoutContext) ToggleLead(uint32) commandpkg.Result {
	return commandpkg.Result{}
}

func TestSplitParsesArgsAndDelegates(t *testing.T) {
	t.Parallel()

	ctx := &fakeLayoutContext{
		splitResult: commandpkg.Result{Output: "split\n"},
	}

	got := Split(ctx, 42, []string{"pane-1", "root", "--vertical", "--host", "dev", "--name", "worker", "--task", "build", "--color", "blue", "--focus"})
	wantArgs := SplitArgs{
		PaneRef:   "pane-1",
		RootLevel: true,
		Dir:       mux.SplitVertical,
		HostName:  "dev",
		Name:      "worker",
		Task:      "build",
		Color:     "blue",
		Focus:     true,
	}

	if !ctx.splitCalled {
		t.Fatal("Split() did not call context")
	}
	if ctx.splitActorPaneID != 42 {
		t.Fatalf("actorPaneID = %d, want 42", ctx.splitActorPaneID)
	}
	if !reflect.DeepEqual(ctx.splitArgs, wantArgs) {
		t.Fatalf("parsed args = %+v, want %+v", ctx.splitArgs, wantArgs)
	}
	if got.Output != "split\n" {
		t.Fatalf("result output = %q, want %q", got.Output, "split\n")
	}
}

func TestFocusDefaultsToNext(t *testing.T) {
	t.Parallel()

	ctx := &fakeLayoutContext{
		focusResult: commandpkg.Result{Output: "focused\n"},
	}

	got := Focus(ctx, 7, nil)

	if !ctx.focusCalled {
		t.Fatal("Focus() did not call context")
	}
	if ctx.focusActorPaneID != 7 {
		t.Fatalf("actorPaneID = %d, want 7", ctx.focusActorPaneID)
	}
	if ctx.focusDirection != "next" {
		t.Fatalf("direction = %q, want %q", ctx.focusDirection, "next")
	}
	if got.Output != "focused\n" {
		t.Fatalf("result output = %q, want %q", got.Output, "focused\n")
	}
}

func TestSpawnParsesArgsAndDelegates(t *testing.T) {
	t.Parallel()

	ctx := &fakeLayoutContext{
		spawnResult: commandpkg.Result{Output: "spawned\n"},
	}

	got := Spawn(ctx, 12, []string{"--spiral", "--focus", "--host", "dev", "--name", "worker", "--task", "build", "--color", "rosewater"})
	wantArgs := SpawnArgs{
		Focus:        true,
		Spiral:       true,
		HostExplicit: true,
		Meta: mux.PaneMeta{
			Name:  "worker",
			Host:  "dev",
			Task:  "build",
			Color: "rosewater",
		},
	}

	if !ctx.spawnCalled {
		t.Fatal("Spawn() did not call context")
	}
	if ctx.spawnActorPaneID != 12 {
		t.Fatalf("actorPaneID = %d, want 12", ctx.spawnActorPaneID)
	}
	if !reflect.DeepEqual(ctx.spawnArgs, wantArgs) {
		t.Fatalf("spawn args = %+v, want %+v", ctx.spawnArgs, wantArgs)
	}
	if got.Output != "spawned\n" {
		t.Fatalf("result output = %q, want %q", got.Output, "spawned\n")
	}
}

func TestKillParsesArgsAndDelegates(t *testing.T) {
	t.Parallel()

	ctx := &fakeLayoutContext{
		killResult: commandpkg.Result{Output: "killed\n"},
	}

	got := Kill(ctx, 9, []string{"--cleanup", "--timeout", "2s", "pane-7"})
	wantArgs := KillArgs{
		PaneRef: "pane-7",
		Cleanup: true,
		Timeout: 2 * time.Second,
	}

	if !ctx.killCalled {
		t.Fatal("Kill() did not call context")
	}
	if ctx.killActorPaneID != 9 {
		t.Fatalf("actorPaneID = %d, want 9", ctx.killActorPaneID)
	}
	if !reflect.DeepEqual(ctx.killArgs, wantArgs) {
		t.Fatalf("kill args = %+v, want %+v", ctx.killArgs, wantArgs)
	}
	if got.Output != "killed\n" {
		t.Fatalf("result output = %q, want %q", got.Output, "killed\n")
	}
}

func TestCopyModeParsesArgsAndDelegates(t *testing.T) {
	t.Parallel()

	ctx := &fakeLayoutContext{
		copyModeResult: commandpkg.Result{Output: "copy\n"},
	}

	got := CopyMode(ctx, 3, []string{"pane-1", "--wait", "ui=copy-mode-shown", "--timeout", "25ms"})
	wantOpts := CopyModeOptions{
		PaneRef:           "pane-1",
		WaitCopyModeShown: true,
		WaitTimeout:       25 * time.Millisecond,
	}

	if !ctx.copyModeCalled {
		t.Fatal("CopyMode() did not call context")
	}
	if ctx.copyModeActorPaneID != 3 {
		t.Fatalf("actorPaneID = %d, want 3", ctx.copyModeActorPaneID)
	}
	if !reflect.DeepEqual(ctx.copyModeOptions, wantOpts) {
		t.Fatalf("copy mode options = %+v, want %+v", ctx.copyModeOptions, wantOpts)
	}
	if got.Output != "copy\n" {
		t.Fatalf("result output = %q, want %q", got.Output, "copy\n")
	}
}

func TestEqualizeParsesModesAndDelegates(t *testing.T) {
	t.Parallel()

	ctx := &fakeLayoutContext{
		equalizeResult: commandpkg.Result{Output: "equalized\n"},
	}

	got := Equalize(ctx, []string{"--all"})

	if !ctx.equalizeCalled {
		t.Fatal("Equalize() did not call context")
	}
	if !ctx.equalizeWidths || !ctx.equalizeHeights {
		t.Fatalf("equalize flags = (%t, %t), want (true, true)", ctx.equalizeWidths, ctx.equalizeHeights)
	}
	if got.Output != "equalized\n" {
		t.Fatalf("result output = %q, want %q", got.Output, "equalized\n")
	}
}

func TestResizePaneDefaultsDeltaAndDelegates(t *testing.T) {
	t.Parallel()

	ctx := &fakeLayoutContext{
		resizePaneResult: commandpkg.Result{Output: "resized\n"},
	}

	got := ResizePane(ctx, 5, []string{"pane-2", "left"})

	if !ctx.resizePaneCalled {
		t.Fatal("ResizePane() did not call context")
	}
	if ctx.resizePaneActorPaneID != 5 {
		t.Fatalf("actorPaneID = %d, want 5", ctx.resizePaneActorPaneID)
	}
	if ctx.resizePanePaneRef != "pane-2" || ctx.resizePaneDirection != "left" || ctx.resizePaneDelta != 1 {
		t.Fatalf("resize pane args = (%q, %q, %d), want (%q, %q, %d)", ctx.resizePanePaneRef, ctx.resizePaneDirection, ctx.resizePaneDelta, "pane-2", "left", 1)
	}
	if got.Output != "resized\n" {
		t.Fatalf("result output = %q, want %q", got.Output, "resized\n")
	}
}

func TestSetLeadPassesOptionalPaneRef(t *testing.T) {
	t.Parallel()

	ctx := &fakeLayoutContext{
		setLeadResult: commandpkg.Result{Output: "lead\n"},
	}

	got := SetLead(ctx, 11, []string{"pane-9"})

	if !ctx.setLeadCalled {
		t.Fatal("SetLead() did not call context")
	}
	if ctx.setLeadActorPaneID != 11 {
		t.Fatalf("actorPaneID = %d, want 11", ctx.setLeadActorPaneID)
	}
	if ctx.setLeadPaneRef != "pane-9" {
		t.Fatalf("paneRef = %q, want %q", ctx.setLeadPaneRef, "pane-9")
	}
	if got.Output != "lead\n" {
		t.Fatalf("result output = %q, want %q", got.Output, "lead\n")
	}
}
