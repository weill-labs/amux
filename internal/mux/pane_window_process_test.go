package mux

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/weill-labs/amux/internal/debugowner"
)

func TestWriteAll(t *testing.T) {
	t.Parallel()

	t.Run("retries short writes until complete", func(t *testing.T) {
		t.Parallel()

		payload := []byte("abcdef")
		var writes [][]byte
		call := 0

		got, err := writeAll(payload, func(data []byte) (int, error) {
			writes = append(writes, append([]byte(nil), data...))
			call++
			switch call {
			case 1:
				return 2, nil
			case 2:
				return 1, nil
			default:
				return len(data), nil
			}
		})
		if err != nil {
			t.Fatalf("writeAll() error = %v, want nil", err)
		}
		if got != len(payload) {
			t.Fatalf("writeAll() bytes = %d, want %d", got, len(payload))
		}

		wantWrites := [][]byte{
			[]byte("abcdef"),
			[]byte("cdef"),
			[]byte("def"),
		}
		if len(writes) != len(wantWrites) {
			t.Fatalf("write calls = %d, want %d", len(writes), len(wantWrites))
		}
		for i := range wantWrites {
			if string(writes[i]) != string(wantWrites[i]) {
				t.Fatalf("write call %d = %q, want %q", i, writes[i], wantWrites[i])
			}
		}
	})

	t.Run("returns partial byte count on writer error", func(t *testing.T) {
		t.Parallel()

		wantErr := errors.New("boom")
		got, err := writeAll([]byte("abcdef"), func(data []byte) (int, error) {
			return 2, wantErr
		})
		if !errors.Is(err, wantErr) {
			t.Fatalf("writeAll() error = %v, want %v", err, wantErr)
		}
		if got != 2 {
			t.Fatalf("writeAll() bytes = %d, want 2", got)
		}
	})

	tests := []struct {
		name      string
		writeFunc func([]byte) (int, error)
		wantErr   string
	}{
		{
			name: "rejects negative write counts",
			writeFunc: func([]byte) (int, error) {
				return -1, nil
			},
			wantErr: "invalid write count -1",
		},
		{
			name: "rejects oversized write counts",
			writeFunc: func(data []byte) (int, error) {
				return len(data) + 1, nil
			},
			wantErr: "invalid write count 4",
		},
		{
			name: "treats zero-byte writes as short writes",
			writeFunc: func([]byte) (int, error) {
				return 0, nil
			},
			wantErr: io.ErrShortWrite.Error(),
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := writeAll([]byte("abc"), tt.writeFunc)
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("writeAll() error = %v, want %q", err, tt.wantErr)
			}
			if got != 0 {
				t.Fatalf("writeAll() bytes = %d, want 0", got)
			}
		})
	}
}

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

func newProcessTestPane(t *testing.T, id uint32, name string, cmd *exec.Cmd) *Pane {
	t.Helper()

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: 40,
		Rows: 10,
	})
	if err != nil {
		t.Fatalf("start agent-status test shell: %v", err)
	}

	p := &Pane{
		ID:              id,
		Meta:            PaneMeta{Name: name, Host: DefaultHost},
		ptmx:            ptmx,
		cmd:             cmd,
		emulator:        NewVTEmulatorWithScrollback(40, 10, DefaultScrollbackLines),
		exitDone:        make(chan struct{}),
		createdAt:       time.Now().Add(-time.Minute),
		scrollbackLines: effectiveScrollbackLines(DefaultScrollbackLines),
		scrollbackLimit: effectiveScrollbackLines(DefaultScrollbackLines),
	}
	p.baseHistory.Store(&paneBaseHistory{})
	wireScrollbackCallbacks(p)
	p.Start()

	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Errorf("Close() = %v, want nil", err)
		}
		if err := p.WaitClosed(); err != nil {
			t.Errorf("WaitClosed() = %v, want nil", err)
		}
	})

	return p
}

func newAgentStatusTestPane(t *testing.T) *Pane {
	t.Helper()

	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}

	cmd := exec.Command(bashPath, "--noprofile", "--norc", "-i")
	cmd.Env = append(os.Environ(),
		"PS1=prompt> ",
		"HISTFILE=/dev/null",
	)
	return newProcessTestPane(t, 123, "pane-123", cmd)
}

func newBashPromptSelfForkTestPane(t *testing.T, markerFile string) *Pane {
	t.Helper()

	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}

	promptCommand := `printf started >"$MARKER_FILE"; PROMPT_COMMAND= PS1= bash --noprofile --norc -ic 'PROMPT_COMMAND= PS1= bash --noprofile --norc -ic "read -rt 0.3 _"; :'`
	cmd := exec.Command(bashPath, "--noprofile", "--norc", "-i")
	cmd.Env = append(os.Environ(),
		"MARKER_FILE="+markerFile,
		"PROMPT_COMMAND="+promptCommand,
		"PS1=prompt> ",
		"HISTFILE=/dev/null",
	)

	p := newProcessTestPane(t, 125, "pane-125", cmd)

	waitUntil(t, time.Second, func() bool {
		_, err := os.Stat(markerFile)
		return err == nil
	})
	waitUntil(t, time.Second, func() bool {
		return len(childPIDs(p.ProcessPid())) == 0
	})
	if err := os.Remove(markerFile); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove initial marker: %v", err)
	}

	return p
}

func newResizeSignalTestPane(t *testing.T, signalFile, readyFile string) *Pane {
	t.Helper()

	cmd := exec.Command("sh", "-c", `trap 'printf x >> "$SIGNAL_FILE"' WINCH; printf ready > "$READY_FILE"; while IFS= read -r line; do eval "$line"; done`)
	cmd.Env = append(os.Environ(),
		"SIGNAL_FILE="+signalFile,
		"READY_FILE="+readyFile,
	)

	p := newProcessTestPane(t, 124, "pane-124", cmd)

	waitUntil(t, time.Second, func() bool {
		_, err := os.Stat(readyFile)
		return err == nil
	})

	return p
}

func readSignalCount(t *testing.T, signalFile string) int {
	t.Helper()

	data, err := os.ReadFile(signalFile)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("ReadFile(%q): %v", signalFile, err)
	}
	return len(data)
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
	if err := p.WaitClosed(); err != nil {
		t.Fatalf("WaitClosed() = %v, want nil", err)
	}
	select {
	case <-p.actorDone:
	default:
		t.Fatal("actor goroutine should stop on Close")
	}
	if p.OutputSeq() != 1 {
		t.Fatalf("OutputSeq() after Close = %d, want 1", p.OutputSeq())
	}
	if err := p.Close(); err != nil {
		t.Fatalf("second Close() = %v, want nil", err)
	}
}

func TestPaneActorHelpersFallBackAfterActorChannelClose(t *testing.T) {
	t.Parallel()

	p := &Pane{
		ID:              1,
		emulator:        NewVTEmulatorWithScrollback(12, 2, DefaultScrollbackLines),
		scrollbackLines: DefaultScrollbackLines,
	}
	p.baseHistory.Store(&paneBaseHistory{})
	p.startActor()

	close(p.actorCommands)
	<-p.actorDone

	p.ReplayScreen("hello")
	if !p.ScreenContains("hello") {
		t.Fatal("ReplayScreen should still work after actor channel close")
	}
	if got := p.Output(1); got != "hello" {
		t.Fatalf("Output(1) = %q, want %q", got, "hello")
	}
}

func TestPaneActorHelpersWaitForActorShutdownBeforeFallback(t *testing.T) {
	t.Parallel()

	p := &Pane{
		ID:              1,
		emulator:        NewVTEmulatorWithScrollback(12, 2, DefaultScrollbackLines),
		scrollbackLines: DefaultScrollbackLines,
	}
	p.baseHistory.Store(&paneBaseHistory{})
	p.startActor()

	running := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		p.actorCommands <- paneCommand{
			run: func() {
				close(running)
				<-release
			},
			done: done,
		}
	}()
	<-running
	close(p.actorCommands)

	fallbackDone := make(chan struct{})
	go func() {
		p.ReplayScreen("hello")
		close(fallbackDone)
	}()

	select {
	case <-fallbackDone:
		t.Fatal("fallback should wait for actor shutdown")
	case <-time.After(20 * time.Millisecond):
	}

	close(release)
	<-done
	<-p.actorDone
	<-fallbackDone

	if !p.ScreenContains("hello") {
		t.Fatal("ReplayScreen should run after the actor drains")
	}
}

func TestStopActorDrainsBlockedSendersBeforeClose(t *testing.T) {
	oldProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(oldProcs)

	p := &Pane{
		ID:              1,
		emulator:        NewVTEmulatorWithScrollback(12, 2, DefaultScrollbackLines),
		scrollbackLines: DefaultScrollbackLines,
	}
	p.baseHistory.Store(&paneBaseHistory{})
	p.startActor()
	actorCommands := p.actorCommands

	running := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan struct{})
	go func() {
		actorCommands <- paneCommand{
			run: func() {
				close(running)
				<-release
			},
			done: firstDone,
		}
	}()
	<-running

	senderStarted := make(chan struct{})
	secondDone := make(chan struct{})
	sendPanic := make(chan any, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				sendPanic <- r
			}
		}()
		close(senderStarted)
		actorCommands <- paneCommand{
			run: func() {
				_, _ = p.emulator.Write([]byte("hello"))
			},
			done: secondDone,
		}
	}()
	<-senderStarted
	runtime.Gosched()

	stopDone := make(chan struct{})
	go func() {
		p.stopActor()
		close(stopDone)
	}()

	select {
	case <-stopDone:
		t.Fatal("stopActor returned before queued senders drained")
	case <-time.After(20 * time.Millisecond):
	}

	close(release)

	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for initial actor command to finish")
	}

	select {
	case <-secondDone:
	case r := <-sendPanic:
		t.Fatalf("blocked sender panicked during actor shutdown: %v", r)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for queued sender to drain before actor shutdown")
	}

	select {
	case <-stopDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for actor shutdown")
	}

	select {
	case r := <-sendPanic:
		t.Fatalf("blocked sender panicked during actor shutdown: %v", r)
	default:
	}

	if !p.ScreenContains("hello") {
		t.Fatal("queued sender command should run before actor shutdown")
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
	if err := p.WaitClosed(); err != nil {
		t.Fatalf("WaitClosed() after clearing process = %v", err)
	}
}

func TestCloseReapsShellProcess(t *testing.T) {
	t.Parallel()

	// Start a pane running a shell command that traps SIGHUP (simulating
	// a stubborn process that ignores the initial signal).
	p, err := NewPaneWithScrollback(99, PaneMeta{
		Name:  "pane-99",
		Host:  DefaultHost,
		Color: "f5e0dc",
	}, 40, 10, "test", DefaultScrollbackLines, nil, nil)
	if err != nil {
		t.Fatalf("NewPaneWithScrollback: %v", err)
	}
	p.Start()

	pid := p.ProcessPid()
	if pid == 0 {
		t.Fatal("expected non-zero PID")
	}

	// Verify the process is alive
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("shell should be alive before Close: %v", err)
	}

	if err := p.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}

	// After Close(), the process should be dead (SIGKILL fallback ensures this)
	waitUntil(t, 5*time.Second, func() bool {
		return syscall.Kill(pid, 0) != nil
	})
	if err := p.WaitClosed(); err != nil {
		t.Fatalf("WaitClosed() = %v", err)
	}
}

func TestCloseReturnsBeforeBackgroundTeardownCompletes(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}

	ptmxRead, ptmxWrite, err := os.Pipe()
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("os.Pipe: %v", err)
	}
	_ = ptmxWrite.Close()

	p := &Pane{
		ID:       1,
		ptmx:     ptmxRead,
		process:  cmd.Process,
		emulator: NewVTEmulatorWithScrollback(20, 4, DefaultScrollbackLines),
		exitDone: make(chan struct{}),
	}
	p.startActor()

	var releaseOnce sync.Once
	released := make(chan struct{})
	reaped := make(chan error, 1)
	go func() {
		<-released
		close(p.exitDone)
		reaped <- cmd.Wait()
	}()
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(released) })
		if err := cmd.Process.Kill(); err == nil {
			select {
			case <-reaped:
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting to reap child process")
			}
		}
	})

	start := time.Now()
	if err := p.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("Close() blocked for %v, want <100ms", elapsed)
	}

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- p.WaitClosed()
	}()

	select {
	case err := <-waitDone:
		t.Fatalf("WaitClosed() completed before release: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	releaseOnce.Do(func() { close(released) })

	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatalf("WaitClosed() = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for background close")
	}

	select {
	case <-reaped:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting to reap child process")
	}
}

func TestWaitClosedReturnsNilBeforeClose(t *testing.T) {
	t.Parallel()

	p := &Pane{}
	if err := p.WaitClosed(); err != nil {
		t.Fatalf("WaitClosed() before Close = %v, want nil", err)
	}
}

func TestCloseHandlesUnstartedProcessPane(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}

	ptmxRead, ptmxWrite, err := os.Pipe()
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("os.Pipe: %v", err)
	}
	_ = ptmxWrite.Close()

	p := &Pane{
		ID:       9,
		ptmx:     ptmxRead,
		cmd:      cmd,
		emulator: NewVTEmulatorWithScrollback(20, 4, DefaultScrollbackLines),
		exitDone: make(chan struct{}),
	}
	p.startActor()

	waitDone := make(chan error, 1)
	if err := p.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	go func() {
		waitDone <- p.WaitClosed()
	}()

	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatalf("WaitClosed() = %v, want nil", err)
		}
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		close(p.exitDone)
		select {
		case <-waitDone:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out releasing stuck WaitClosed")
		}
		t.Fatal("WaitClosed() blocked for unstarted process pane")
	}

	if cmd.ProcessState == nil {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("Wait() = %v, want nil", err)
		}
	}
}

func TestCloseAcceptsForbiddenOwnerInNonDebugBuilds(t *testing.T) {
	t.Parallel()

	p := NewProxyPaneWithScrollback(8, PaneMeta{
		Name:  "pane-8",
		Host:  DefaultHost,
		Color: "f5e0dc",
	}, 20, 1, 4, nil, nil, func(data []byte) (int, error) {
		return len(data), nil
	})

	p.SetCloseForbiddenOwner(&debugowner.Checker{})

	if err := p.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	if err := p.WaitClosed(); err != nil {
		t.Fatalf("WaitClosed() = %v, want nil", err)
	}
}

func TestPaneRespawnPreservesMetadataAndSuppressesExitCallback(t *testing.T) {
	t.Parallel()

	var exitReasonMu sync.Mutex
	var exitReasons []string
	p, err := NewPaneWithScrollback(99, PaneMeta{
		Name:  "pane-99",
		Host:  DefaultHost,
		Task:  "TASK-42",
		Color: "f5e0dc",
		KV: map[string]string{
			"issue": "LAB-593",
		},
	}, 40, 10, "test", DefaultScrollbackLines, nil, func(_ uint32, reason string) {
		exitReasonMu.Lock()
		exitReasons = append(exitReasons, reason)
		exitReasonMu.Unlock()
	})
	if err != nil {
		t.Fatalf("NewPaneWithScrollback: %v", err)
	}
	p.Start()
	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Errorf("Close() = %v, want nil", err)
		}
		if err := p.WaitClosed(); err != nil {
			t.Errorf("WaitClosed() = %v, want nil", err)
		}
	})

	tmpDir := t.TempDir()
	wantDir := tmpDir
	if resolved, err := filepath.EvalSymlinks(tmpDir); err == nil && resolved != "" {
		wantDir = resolved
	}

	if _, err := p.Write([]byte(fmt.Sprintf("cd %q && printf 'RESPAWN-OLD\\n'\n", tmpDir))); err != nil {
		t.Fatalf("write old marker: %v", err)
	}
	waitUntil(t, 2*time.Second, func() bool {
		return p.ScreenContains("RESPAWN-OLD")
	})

	oldPID := p.ProcessPid()
	if oldPID == 0 {
		t.Fatal("ProcessPid() before respawn = 0, want live shell")
	}

	if err := p.Respawn("test", wantDir); err != nil {
		t.Fatalf("Respawn(): %v", err)
	}
	if got := p.ProcessPid(); got == 0 || got == oldPID {
		t.Fatalf("ProcessPid() after respawn = %d, want new non-zero pid (old=%d)", got, oldPID)
	}
	if p.ScreenContains("RESPAWN-OLD") {
		t.Fatal("respawn should clear the old emulator state")
	}
	if p.Meta.Name != "pane-99" || p.Meta.Task != "TASK-42" || p.Meta.Color != "f5e0dc" {
		t.Fatalf("metadata after respawn = %+v", p.Meta)
	}
	if p.Meta.KV["issue"] != "LAB-593" {
		t.Fatalf("issue metadata after respawn = %q, want LAB-593", p.Meta.KV["issue"])
	}
	if p.Meta.Dir != wantDir {
		t.Fatalf("Meta.Dir after respawn = %q, want %q", p.Meta.Dir, wantDir)
	}
	if p.LiveCwd() != wantDir {
		t.Fatalf("LiveCwd() after respawn = %q, want %q", p.LiveCwd(), wantDir)
	}
	exitReasonMu.Lock()
	if len(exitReasons) != 0 {
		exitReasonMu.Unlock()
		t.Fatalf("respawn should suppress the old onExit callback, got %v", exitReasons)
	}
	exitReasonMu.Unlock()

	p.Start()
	if _, err := p.Write([]byte("pwd -P\n")); err != nil {
		t.Fatalf("write pwd after respawn: %v", err)
	}
	waitUntil(t, 5*time.Second, func() bool {
		return p.ScreenContains(wantDir)
	})
}

func TestPaneRespawnRejectsProxyPane(t *testing.T) {
	t.Parallel()

	p := NewProxyPaneWithScrollback(7, PaneMeta{
		Name:  "pane-7",
		Host:  "fake-host",
		Color: "f5e0dc",
	}, 20, 1, DefaultScrollbackLines, nil, nil, func(data []byte) (int, error) {
		return len(data), nil
	})

	if err := p.Respawn("test", "/tmp"); err == nil || err.Error() != "cannot respawn proxy pane" {
		t.Fatalf("Respawn() error = %v, want cannot respawn proxy pane", err)
	}
}

func TestResizeSkipsSIGWINCHWhenDimensionsDoNotChange(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	signalFile := filepath.Join(dir, "winch.log")
	readyFile := filepath.Join(dir, "ready")
	p := newResizeSignalTestPane(t, signalFile, readyFile)

	if err := p.Resize(40, 10); err != nil {
		t.Fatalf("Resize unchanged: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if got := readSignalCount(t, signalFile); got != 0 {
		t.Fatalf("SIGWINCH count after unchanged resize = %d, want 0", got)
	}

	if err := p.Resize(41, 10); err != nil {
		t.Fatalf("Resize changed: %v", err)
	}
	waitUntil(t, time.Second, func() bool {
		return readSignalCount(t, signalFile) >= 1
	})
	time.Sleep(150 * time.Millisecond)
	if got := readSignalCount(t, signalFile); got != 1 {
		t.Fatalf("SIGWINCH count after changed resize = %d, want 1", got)
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
	// Not parallel: this exercises foreground job detection against live
	// PTY-backed processes and waits for shell/job transitions.
	pane := newAgentStatusTestPane(t)

	idle := (&Pane{createdAt: pane.createdAt}).ForegroundJobState()
	if !idle.Idle || !idle.IdleSince.Equal(pane.createdAt) {
		t.Fatalf("idle-without-process = %+v, want idle since creation", idle)
	}

	waitUntil(t, time.Second, func() bool {
		idle = pane.ForegroundJobState()
		return idle.Idle
	})

	if _, err := pane.Write([]byte("sleep 30\n")); err != nil {
		t.Fatalf("start foreground job through shell: %v", err)
	}

	var busy ForegroundJobState
	waitUntil(t, time.Second, func() bool {
		busy = pane.ForegroundJobState()
		return !busy.Idle && busy.ForegroundProcessGroup != 0
	})

	status := pane.AgentStatus()
	if got := status.CurrentCommand; got == "" {
		t.Fatal("AgentStatus CurrentCommand = empty, want command name")
	}

	if _, err := pane.Write([]byte("\003")); err != nil {
		t.Fatalf("interrupt foreground job through shell: %v", err)
	}

	waitUntil(t, time.Second, func() bool {
		idle = pane.ForegroundJobState()
		return idle.Idle
	})
	if idle.IdleSince.IsZero() {
		t.Fatal("idle ForegroundJobState IdleSince should be populated")
	}
}

func TestAgentStatusTreatsPromptTimeBashSelfForkAsIdle(t *testing.T) {
	// Not parallel: this exercises prompt-time shell self-fork behavior against
	// a live PTY-backed process tree.
	dir := t.TempDir()
	markerFile := filepath.Join(dir, "prompt-marker")
	readyFile := filepath.Join(dir, "ready")
	pane := newBashPromptSelfForkTestPane(t, markerFile)

	cmd := "printf READY > " + strconv.Quote(readyFile) + "\n"
	if _, err := pane.Write([]byte(cmd)); err != nil {
		t.Fatalf("write trigger command: %v", err)
	}
	waitUntil(t, time.Second, func() bool {
		data, err := os.ReadFile(readyFile)
		return err == nil && string(data) == "READY"
	})
	waitUntil(t, time.Second, func() bool {
		_, err := os.Stat(markerFile)
		return err == nil
	})

	shellPID := pane.ProcessPid()
	shellName := processName(shellPID)
	if shellName == "" {
		t.Fatal("processName(shell) = empty, want bash")
	}

	waitUntil(t, time.Second, func() bool {
		children := childPIDs(shellPID)
		if len(children) != 1 || processName(children[0]) != shellName {
			return false
		}
		grandchildren := childPIDs(children[0])
		return len(grandchildren) == 1 && processName(grandchildren[0]) == shellName
	})

	status := pane.AgentStatus()
	if !status.Idle {
		t.Fatalf("prompt-time bash self-fork reported busy: %+v", status)
	}
	if status.CurrentCommand == "" {
		t.Fatalf("idle prompt-time bash self-fork should report a shell command: %+v", status)
	}
}

func TestWindowZoomResolvePaneAndResizeBorder(t *testing.T) {
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
	if got, err := w.ResolvePane("pane-2"); err != nil || got != p2 {
		t.Fatalf("ResolvePane(name) = (%+v, %v), want (%+v, nil)", got, err, p2)
	}
	if got, err := w.ResolvePane("2"); err != nil || got != p2 {
		t.Fatalf("ResolvePane(id) = (%+v, %v), want (%+v, nil)", got, err, p2)
	}
	if _, err := w.ResolvePane("pane-"); err == nil || err.Error() != `pane "pane-" not found` {
		t.Fatalf("ResolvePane(prefix) error = %v, want pane not found", err)
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

func TestWindowResolvePaneRejectsAmbiguousExactNames(t *testing.T) {
	t.Parallel()

	p1 := &Pane{ID: 1, Meta: PaneMeta{Name: "shared", Host: DefaultHost, Color: "f5e0dc"}, emulator: NewVTEmulatorWithScrollback(10, 5, DefaultScrollbackLines)}
	p2 := &Pane{ID: 2, Meta: PaneMeta{Name: "shared", Host: DefaultHost, Color: "f2cdcd"}, emulator: NewVTEmulatorWithScrollback(10, 5, DefaultScrollbackLines)}
	w := NewWindow(p1, 80, 24)
	if _, err := w.SplitRoot(SplitHorizontal, p2); err != nil {
		t.Fatalf("SplitRoot: %v", err)
	}

	if _, err := w.ResolvePane("shared"); err == nil || !strings.Contains(err.Error(), `pane "shared" is ambiguous`) {
		t.Fatalf("ResolvePane(shared) error = %v, want ambiguous", err)
	}
}

func TestWindowSplitWithOptionsKeepFocusPreservesZoomAndFocus(t *testing.T) {
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

	if _, err := w.SplitWithOptions(SplitVertical, p3, SplitOptions{KeepFocus: true}); err != nil {
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
		t.Fatalf("kept-focus pane size = %dx%d, want %dx%d", cols, rows, cell3.W, PaneContentHeight(cell3.H))
	}
}

func TestSnapshotWindowAndRebuildWindowFromSnapshot(t *testing.T) {
	t.Parallel()

	p1 := &Pane{ID: 1, Meta: PaneMeta{Name: "pane-1", Host: DefaultHost, Color: "f5e0dc"}, emulator: NewVTEmulatorWithScrollback(10, 5, DefaultScrollbackLines)}
	p2 := &Pane{ID: 2, Meta: PaneMeta{Name: "pane-2", Host: DefaultHost, Color: "f2cdcd"}, emulator: NewVTEmulatorWithScrollback(12, 6, DefaultScrollbackLines)}
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
