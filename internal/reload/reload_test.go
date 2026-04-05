package reload

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func fakeVersionedBinaryScript(build string) string {
	return fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "version" && "${2:-}" == "--json" ]]; then
	printf '{"build":"%s","checkpoint_version":2}\n'
	exit 0
fi

if [[ "${1:-}" == "version" ]]; then
	printf 'amux build: %s (checkpoint v2)\n'
	exit 0
fi

if [[ "${1:-}" == "install-terminfo" ]]; then
	exit 0
fi

printf 'unexpected args: %%s\n' "$*" >&2
exit 1
`, build, build)
}

func writeFakeVersionedBinary(tb testing.TB, path, build string) {
	tb.Helper()

	script := fakeVersionedBinaryScript(build)
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		tb.Fatalf("write fake binary %s: %v", path, err)
	}
}

func newInstallScriptToolDir(tb testing.TB, newBuild string) string {
	tb.Helper()

	goPath, err := exec.LookPath("go")
	if err != nil {
		tb.Fatalf("look up go: %v", err)
	}

	dir := tb.TempDir()
	writeTool := func(name, body string) {
		tb.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0755); err != nil {
			tb.Fatalf("write fake %s: %v", name, err)
		}
	}

	writeTool("go", fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "build" ]]; then
	out=""
	while [[ $# -gt 0 ]]; do
		case "$1" in
			-o)
				out="$2"
				shift 2
				;;
			*)
				shift
				;;
		esac
	done

	if [[ -z "$out" ]]; then
		echo "fake go build: missing -o" >&2
		exit 1
	fi

	cat >"$out" <<'EOF'
%s
EOF
	chmod +x "$out"
	exit 0
fi

exec %q "$@"
`, fakeVersionedBinaryScript(newBuild), goPath))
	writeTool("codesign", "#!/usr/bin/env bash\nexit 0\n")
	writeTool("xattr", "#!/usr/bin/env bash\nexit 0\n")

	return dir
}

func installScriptEnv(home, toolDir string) []string {
	env := append([]string{}, os.Environ()...)
	replacedHome := false
	replacedPath := false
	for i, e := range env {
		switch {
		case strings.HasPrefix(e, "HOME="):
			env[i] = "HOME=" + home
			replacedHome = true
		case strings.HasPrefix(e, "PATH="):
			env[i] = "PATH=" + toolDir + string(os.PathListSeparator) + strings.TrimPrefix(e, "PATH=")
			replacedPath = true
		}
	}
	if !replacedHome {
		env = append(env, "HOME="+home)
	}
	if !replacedPath {
		env = append(env, "PATH="+toolDir)
	}
	return env
}

func TestResetDebounceTimerCreatesTimer(t *testing.T) {
	t.Parallel()

	timer := resetDebounceTimer(nil, 20*time.Millisecond)
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()

	select {
	case <-timer.C:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected debounce timer to fire")
	}
}

func TestResetDebounceTimerDrainsExpiredUnreadTimer(t *testing.T) {
	t.Parallel()

	timer := time.NewTimer(20 * time.Millisecond)
	time.Sleep(50 * time.Millisecond)

	timer = resetDebounceTimer(timer, 80*time.Millisecond)
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()

	select {
	case <-timer.C:
		t.Fatal("debounce timer should not fire immediately after reset")
	case <-time.After(30 * time.Millisecond):
	}
}

func TestResetDebounceTimerHandlesExpiredDrainedTimer(t *testing.T) {
	t.Parallel()

	timer := time.NewTimer(20 * time.Millisecond)
	select {
	case <-timer.C:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected timer to expire before reset")
	}

	timer = resetDebounceTimer(timer, 80*time.Millisecond)
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()

	select {
	case <-timer.C:
		t.Fatal("debounce timer should not fire immediately after drained reset")
	case <-time.After(30 * time.Millisecond):
	}
}

func TestWatchEventMatchesTarget(t *testing.T) {
	t.Parallel()

	if !watchEventMatchesTarget(fsnotify.Event{Name: "/tmp/amux", Op: fsnotify.Write}, "amux", false) {
		t.Fatal("write event for target binary should match")
	}
	if !watchEventMatchesTarget(fsnotify.Event{Name: "/tmp/amux", Op: fsnotify.Create}, "amux", false) {
		t.Fatal("create event for target binary should match")
	}
	if watchEventMatchesTarget(fsnotify.Event{Name: "/tmp/amux", Op: fsnotify.Chmod}, "amux", false) {
		t.Fatal("chmod event for a regular target binary should not match")
	}
	if !watchEventMatchesTarget(fsnotify.Event{Name: "/tmp/amux", Op: fsnotify.Chmod}, "amux", true) {
		t.Fatal("chmod event for a symlink target binary should match")
	}
	if watchEventMatchesTarget(fsnotify.Event{Name: "/tmp/other", Op: fsnotify.Write}, "amux", false) {
		t.Fatal("write event for a different file should not match")
	}
	if watchEventMatchesTarget(fsnotify.Event{Name: "/tmp/amux", Op: fsnotify.Remove}, "amux", true) {
		t.Fatal("remove event for target binary should not match")
	}
}

func TestNormalizeExecutablePathPreservesSymlinkPath(t *testing.T) {
	t.Parallel()

	linkPath := filepath.Join(t.TempDir(), "amux-test")
	if err := os.Symlink(os.Args[0], linkPath); err != nil {
		t.Fatalf("symlink test binary: %v", err)
	}

	got, err := NormalizeExecutablePath(linkPath)
	if err != nil {
		t.Fatalf("NormalizeExecutablePath(%q): %v", linkPath, err)
	}
	if got != linkPath {
		t.Fatalf("NormalizeExecutablePath(%q) = %q, want %q", linkPath, got, linkPath)
	}
}

func TestResolveExecutablePreservesSymlinkPath(t *testing.T) {
	if os.Getenv("AMUX_RESOLVE_EXEC_HELPER") == "1" {
		execPath, err := ResolveExecutable()
		if err != nil {
			fmt.Fprint(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Print(execPath)
		os.Exit(0)
	}

	t.Parallel()
	if runtime.GOOS != "darwin" {
		t.Skip("os.Executable preserves the invoked symlink path on macOS only")
	}

	linkPath := filepath.Join(t.TempDir(), "amux-test")
	if err := os.Symlink(os.Args[0], linkPath); err != nil {
		t.Fatalf("symlink test binary: %v", err)
	}

	cmd := exec.Command(linkPath, "-test.run=TestResolveExecutablePreservesSymlinkPath")
	cmd.Env = append(os.Environ(),
		"AMUX_RESOLVE_EXEC_HELPER=1",
		"AMUX_SESSION=",
		"TMUX=",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("resolve executable helper failed: %v\n%s", err, out)
	}

	got := strings.TrimSpace(string(out))
	if got != linkPath {
		t.Fatalf("ResolveExecutable() via symlink = %q, want %q", got, linkPath)
	}
}

func TestDrainPendingReloadEvents(t *testing.T) {
	t.Parallel()

	events := make(chan fsnotify.Event, 4)
	errors := make(chan error, 2)
	events <- fsnotify.Event{Name: "/tmp/other", Op: fsnotify.Write}
	events <- fsnotify.Event{Name: "/tmp/amux", Op: fsnotify.Write}
	errors <- nil

	if !drainPendingReloadEvents(events, errors, "amux", false) {
		t.Fatal("drain should report a matching pending reload event")
	}
	if len(events) != 0 {
		t.Fatalf("drain should consume all pending events, got %d left", len(events))
	}
	if len(errors) != 0 {
		t.Fatalf("drain should consume pending errors, got %d left", len(errors))
	}
}

func TestDrainPendingReloadEventsNoMatch(t *testing.T) {
	t.Parallel()

	events := make(chan fsnotify.Event, 2)
	errors := make(chan error, 1)
	events <- fsnotify.Event{Name: "/tmp/other", Op: fsnotify.Write}
	errors <- nil

	if drainPendingReloadEvents(events, errors, "amux", false) {
		t.Fatal("drain should ignore unrelated pending events")
	}
}

func TestWatchBinaryDebounce(t *testing.T) {
	t.Parallel()

	// Create a temp directory with a fake binary
	dir := t.TempDir()
	binPath := filepath.Join(dir, "amux-test")
	if err := os.WriteFile(binPath, []byte("v1"), 0755); err != nil {
		t.Fatal(err)
	}

	triggerReload := make(chan struct{}, 1)
	ready := make(chan struct{})
	go WatchBinary(binPath, triggerReload, ready)
	<-ready

	// Write to the file multiple times in quick succession (simulates go build)
	for i := 0; i < 5; i++ {
		os.WriteFile(binPath, []byte("v2"), 0755)
		time.Sleep(20 * time.Millisecond)
	}

	// Should get exactly one trigger after debounce settles
	select {
	case <-triggerReload:
		// Good — got the debounced trigger
	case <-time.After(2 * time.Second):
		t.Fatal("expected reload trigger after debounce, got none")
	}

	// Should NOT get a second trigger (debounce coalesced all writes)
	select {
	case <-triggerReload:
		t.Fatal("got unexpected second reload trigger — debounce failed")
	case <-time.After(500 * time.Millisecond):
		// Good — no extra trigger
	}
}

func TestWatchBinaryIgnoresOtherFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	binPath := filepath.Join(dir, "amux-test")
	otherPath := filepath.Join(dir, "other-file")

	if err := os.WriteFile(binPath, []byte("v1"), 0755); err != nil {
		t.Fatal(err)
	}

	triggerReload := make(chan struct{}, 1)
	ready := make(chan struct{})
	go WatchBinary(binPath, triggerReload, ready)
	<-ready

	// Write to a different file in the same directory
	os.WriteFile(otherPath, []byte("noise"), 0644)
	time.Sleep(500 * time.Millisecond)

	// Should NOT trigger reload
	select {
	case <-triggerReload:
		t.Fatal("reload triggered by unrelated file change")
	case <-time.After(500 * time.Millisecond):
		// Good — ignored
	}
}

func TestWatchBinaryNilReady(t *testing.T) {
	t.Parallel()

	// Passing nil for the ready channel should not panic.
	dir := t.TempDir()
	binPath := filepath.Join(dir, "amux-test")
	if err := os.WriteFile(binPath, []byte("v1"), 0755); err != nil {
		t.Fatal(err)
	}

	triggerReload := make(chan struct{}, 1)
	go WatchBinary(binPath, triggerReload, nil)

	// Inherent race: cannot use ready channel since we're testing nil.
	// Generous 2s fallback timeout below handles slow CI.
	<-time.After(200 * time.Millisecond) // let watcher register
	os.WriteFile(binPath, []byte("v2"), 0755)

	select {
	case <-triggerReload:
		// Good — watcher works with nil ready channel
	case <-time.After(2 * time.Second):
		t.Fatal("expected reload trigger with nil ready channel")
	}
}

func TestWatchBinaryBadDirClosesReady(t *testing.T) {
	t.Parallel()

	// When the directory doesn't exist, watcher.Add fails and ready
	// should still be closed so callers don't block forever.
	ready := make(chan struct{})
	triggerReload := make(chan struct{}, 1)

	go WatchBinary("/nonexistent/path/amux-test", triggerReload, ready)

	select {
	case <-ready:
		// Good — ready was closed despite the error
	case <-time.After(2 * time.Second):
		t.Fatal("ready channel should be closed when watcher.Add fails")
	}
}

func TestWatchBinaryDeleteAndRecreate(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	binPath := filepath.Join(dir, "amux-test")

	if err := os.WriteFile(binPath, []byte("v1"), 0755); err != nil {
		t.Fatal(err)
	}

	triggerReload := make(chan struct{}, 1)
	ready := make(chan struct{})
	go WatchBinary(binPath, triggerReload, ready)
	<-ready

	// Delete and recreate (simulates build tools that replace via rename)
	os.Remove(binPath)
	time.Sleep(50 * time.Millisecond)
	os.WriteFile(binPath, []byte("v2"), 0755)

	// Should trigger reload after debounce
	select {
	case <-triggerReload:
		// Good
	case <-time.After(2 * time.Second):
		t.Fatal("expected reload trigger after delete+create, got none")
	}
}

func TestWatchBinaryInstallScriptSequence(t *testing.T) {
	t.Parallel()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", ".."))

	home := t.TempDir()
	binPath := filepath.Join(home, ".local", "bin", "amux")
	if err := os.MkdirAll(filepath.Dir(binPath), 0755); err != nil {
		t.Fatalf("creating install dir: %v", err)
	}
	writeFakeVersionedBinary(t, binPath, "oldbuild")

	triggerReload := make(chan struct{}, 1)
	ready := make(chan struct{})
	go WatchBinary(binPath, triggerReload, ready)
	<-ready

	toolDir := newInstallScriptToolDir(t, "newbuild")
	cmd := exec.Command("bash", filepath.Join(repoRoot, "scripts", "install.sh"), binPath)
	cmd.Dir = repoRoot
	cmd.Env = installScriptEnv(home, toolDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install script failed: %v\n%s", err, out)
	}

	select {
	case <-triggerReload:
	case <-time.After(2 * time.Second):
		t.Fatal("expected reload trigger after install script replacement, got none")
	}

	select {
	case <-triggerReload:
		t.Fatal("got unexpected second reload trigger after install script replacement")
	case <-time.After(500 * time.Millisecond):
	}
}

func TestWatchBinaryInstallScriptSequenceViaSymlinkPath(t *testing.T) {
	t.Parallel()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", ".."))

	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	binPath := filepath.Join(binDir, "amux")
	realPath := filepath.Join(binDir, "amux-real")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("creating install dir: %v", err)
	}
	writeFakeVersionedBinary(t, realPath, "oldbuild")
	if err := os.Symlink(realPath, binPath); err != nil {
		t.Fatalf("creating symlinked install path: %v", err)
	}

	triggerReload := make(chan struct{}, 1)
	ready := make(chan struct{})
	go WatchBinary(binPath, triggerReload, ready)
	<-ready

	toolDir := newInstallScriptToolDir(t, "newbuild")
	cmd := exec.Command("bash", filepath.Join(repoRoot, "scripts", "install.sh"), binPath)
	cmd.Dir = repoRoot
	cmd.Env = installScriptEnv(home, toolDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install script via symlink failed: %v\n%s", err, out)
	}

	select {
	case <-triggerReload:
	case <-time.After(2 * time.Second):
		t.Fatal("expected reload trigger after install script replaced symlink path, got none")
	}

	select {
	case <-triggerReload:
		t.Fatal("got unexpected second reload trigger after symlink install replacement")
	case <-time.After(500 * time.Millisecond):
	}
}
