package test

import (
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// amuxBin is the path to the built amux binary, set in TestMain.
var amuxBin string

// gocoverDir is the directory for integration test coverage data.
var gocoverDir string

// gocoverOwned is true when TestMain created gocoverDir (vs. inheriting it).
var gocoverOwned bool

// buildAmux builds the amux binary at binPath. When GOCOVERDIR is set,
// the binary is built with -cover so it writes coverage data on exit.
// Set AMUX_TEST_RACE=1 to build with -race (enables race detection in
// the server binary itself, not just the test code).
func buildAmux(binPath string) error {
	return buildAmuxWithCommit(binPath, "")
}

func buildAmuxWithCommit(binPath, buildCommit string) error {
	args := []string{"build"}
	if os.Getenv("AMUX_TEST_RACE") == "1" {
		args = append(args, "-race")
	}
	if os.Getenv("GOCOVERDIR") != "" {
		args = append(args, "-cover", "-covermode=atomic")
	}
	if buildCommit != "" {
		args = append(args, "-ldflags", "-X main.BuildCommit="+buildCommit)
	}
	args = append(args, "-o", binPath, "..")
	out, err := exec.Command("go", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("building amux: %v\n%s", err, out)
	}
	return nil
}

func privateAmuxBin(tb testing.TB) string {
	tb.Helper()

	src, err := os.Open(amuxBin)
	if err != nil {
		tb.Fatalf("opening shared amux binary: %v", err)
	}
	defer src.Close()

	info, err := src.Stat()
	if err != nil {
		tb.Fatalf("stat shared amux binary: %v", err)
	}

	dstPath := filepath.Join(tb.TempDir(), "amux")
	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		tb.Fatalf("creating private amux binary: %v", err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		tb.Fatalf("copying private amux binary: %v", err)
	}
	if err := dst.Close(); err != nil {
		tb.Fatalf("closing private amux binary: %v", err)
	}
	return dstPath
}

func TestMain(m *testing.M) {
	// Clean up orphaned test sessions from previous runs that may have
	// been killed by a timeout panic (t.Cleanup doesn't run on panic).
	cleanupStaleTestSessions()

	// Use GOCOVERDIR if explicitly set (e.g. by CI). When set, the amux
	// binary is built with -cover and writes coverage data on exit.
	// When not set, build without -cover for faster tests and no metadata races.
	gocoverDir = os.Getenv("GOCOVERDIR")

	// Build amux binary for testing
	tmp, err := os.MkdirTemp("", "amux-test-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating temp dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmp)

	amuxBin = tmp + "/amux"
	if err := buildAmux(amuxBin); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	code := m.Run()
	cleanupStaleTestSessions()

	// Convert coverage data to text profile
	if gocoverDir != "" {
		entries, _ := os.ReadDir(gocoverDir)
		if len(entries) > 0 {
			exec.Command("go", "tool", "covdata", "textfmt",
				"-i="+gocoverDir, "-o=integration-coverage.txt").Run()
		}
		if gocoverOwned {
			os.RemoveAll(gocoverDir)
		}
	}

	os.Exit(code)
}

func upsertEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func newTestHome(tb testing.TB) string {
	tb.Helper()
	home := filepath.Join(tb.TempDir(), "home")
	for _, dir := range []string{
		home,
		filepath.Join(home, ".local", "state", "amux"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			tb.Fatalf("creating test home dir %s: %v", dir, err)
		}
	}
	return home
}

// cleanupStaleTestSessions removes orphaned amux server processes, sockets,
// and log files left behind by previous test runs that were killed by a
// timeout panic.
//
// Only kills servers whose sockets are unresponsive (stale). Live servers
// that accept connections are left alone, making this safe even if another
// `go test` invocation is running concurrently.
func cleanupStaleTestSessions() {
	socketDir := fmt.Sprintf("/tmp/amux-%d", os.Getuid())

	// Kill orphaned amux server processes, but only if their socket is stale
	out, _ := exec.Command("pgrep", "-fl", "amux _server t-").Output()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && isTestSession(fields[len(fields)-1]) {
			session := fields[len(fields)-1]
			if isSocketAlive(filepath.Join(socketDir, session)) {
				continue // server is live, don't kill
			}
			exec.Command("kill", fields[0]).Run()
		}
	}

	// Also kill orphaned benchmark amux servers (same liveness check)
	out, _ = exec.Command("pgrep", "-fl", "amux _server bench-").Output()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && isBenchSession(fields[len(fields)-1]) {
			session := fields[len(fields)-1]
			if isSocketAlive(filepath.Join(socketDir, session)) {
				continue
			}
			exec.Command("kill", fields[0]).Run()
		}
	}

	// Kill orphaned tmux benchmark sessions
	if _, err := exec.LookPath("tmux"); err == nil {
		out, _ = exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
		for _, name := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if isBenchSession(name) {
				exec.Command("tmux", "kill-session", "-t", name).Run()
			}
		}
	}

	// Kill orphaned client processes still connected to dead test sockets.
	// These survive after their server is killed because they hold open
	// Unix socket connections. Use a single lsof call for efficiency.
	killOrphanedTestClients(socketDir)

	// Clean up stale sockets, log files, and lock files
	entries, _ := os.ReadDir(socketDir)
	for _, e := range entries {
		name := e.Name()

		// Clean up .start.lock files for test/bench sessions and
		// client startup locks (c<digits>.start.lock)
		if strings.HasSuffix(name, ".start.lock") {
			base := strings.TrimSuffix(name, ".start.lock")
			if isTestSession(base) || isBenchSession(base) || isClientLock(base) {
				os.Remove(filepath.Join(socketDir, name))
				continue
			}
		}

		base := strings.TrimSuffix(name, ".log")
		if isTestSession(base) || isBenchSession(base) {
			sockPath := filepath.Join(socketDir, base)
			if !isSocketAlive(sockPath) {
				os.Remove(filepath.Join(socketDir, name))
			}
		}
	}
}

// killOrphanedTestClients kills amux client processes connected to dead test
// session sockets. Uses a single lsof call to find all amux Unix socket
// connections, then kills those connected to stale test session paths.
func killOrphanedTestClients(socketDir string) {
	out, err := exec.Command("lsof", "-U", "-c", "amux", "-F", "pn").Output()
	if err != nil {
		return
	}

	// Parse lsof -F output: "p<pid>\n" followed by "n<path>\n" lines
	var currentPid string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "p") {
			currentPid = line[1:]
		} else if strings.HasPrefix(line, "n") && currentPid != "" {
			sockPath := line[1:]
			if !strings.HasPrefix(sockPath, socketDir+"/") {
				continue
			}
			session := filepath.Base(sockPath)
			// Strip @hostname suffix for remote session sockets (e.g., t-abc12345@mbp)
			if at := strings.Index(session, "@"); at >= 0 {
				session = session[:at]
			}
			if !isTestSession(session) && !isBenchSession(session) {
				continue
			}
			// The server is already dead (we killed it above), so kill the client
			exec.Command("kill", currentPid).Run()
		}
	}
}

// isClientLock returns true if name matches the client startup lock pattern: c<digits>
func isClientLock(name string) bool {
	if len(name) < 2 || name[0] != 'c' {
		return false
	}
	for _, ch := range name[1:] {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

// isSocketAlive checks if a Unix socket is accepting connections.
func isSocketAlive(sockPath string) bool {
	conn, err := net.DialTimeout("unix", sockPath, 100*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// isTestSession returns true if the name matches the test session convention: t- followed by 8 hex chars.
func isTestSession(name string) bool {
	if len(name) != 10 || name[:2] != "t-" {
		return false
	}
	_, err := hex.DecodeString(name[2:])
	return err == nil
}

// isBenchSession returns true if the name matches the bench session convention: bench- followed by 8 hex chars.
func isBenchSession(name string) bool {
	if len(name) != 14 || !strings.HasPrefix(name, "bench-") {
		return false
	}
	_, err := hex.DecodeString(name[6:])
	return err == nil
}

// ---------------------------------------------------------------------------
// Shared ANSI / color helpers (used by border, hotreload, and mouse tests)
// ---------------------------------------------------------------------------

// pickContentLine returns a middle content line from ANSI-escaped screen output,
// skipping status lines and empty lines.
func pickContentLine(screen string) string {
	lines := strings.Split(screen, "\n")
	for i := len(lines) / 2; i < len(lines); i++ {
		if strings.Contains(lines[i], "│") && !strings.Contains(lines[i], "amux") {
			return lines[i]
		}
	}
	for _, line := range lines {
		if strings.Contains(line, "│") && !strings.Contains(lines[0], "[pane-") {
			return line
		}
	}
	return ""
}

// extractBorderColors finds each │ in an ANSI-escaped line and returns
// the most recent \033[...m escape sequence before each one.
func extractBorderColors(line string) []string {
	var colors []string
	lastEscape := ""
	i := 0
	for i < len(line) {
		if line[i] == '\033' && i+1 < len(line) && line[i+1] == '[' {
			j := i + 2
			for j < len(line) && line[j] != 'm' {
				j++
			}
			if j < len(line) {
				lastEscape = line[i : j+1]
				i = j + 1
				continue
			}
		}
		if i+2 < len(line) && line[i] == '\xe2' && line[i+1] == '\x94' && line[i+2] == '\x82' {
			colors = append(colors, lastEscape)
			i += 3
			continue
		}
		i++
	}
	return colors
}

// ---------------------------------------------------------------------------
// Layout-aware screen helpers
// ---------------------------------------------------------------------------

// isGlobalBar returns true if the line looks like the global status bar.
// Matches the structural pattern: " amux │ ... panes │ HH:MM "
func isGlobalBar(line string) bool {
	return strings.Contains(line, " amux ") && strings.Contains(line, "panes │")
}

// hasWindowTab returns true if the global bar contains a tab for the given
// 1-based window index (e.g., "1:window-" or "[2:window-").
func hasWindowTab(bar string, index int) bool {
	prefix := fmt.Sprintf("%d:window-", index)
	return strings.Contains(bar, prefix)
}

// isBorderRune returns true for any box-drawing character used in borders.
func isBorderRune(r rune) bool {
	switch r {
	case '│', '─', '┼', '├', '┤', '┬', '┴', '┌', '┐', '└', '┘':
		return true
	}
	return false
}

// isVerticalBorderRune returns true for box-drawing characters with a vertical component.
func isVerticalBorderRune(r rune) bool {
	switch r {
	case '│', '┼', '├', '┤', '┬', '┴', '┌', '┐', '└', '┘':
		return true
	}
	return false
}

// findVerticalBorderCol finds a consistent vertical border column in lines.
func findVerticalBorderCol(lines []string) int {
	if len(lines) == 0 {
		return -1
	}

	// Find all columns that have a vertical border char on the first content line
	candidates := map[int]bool{}
	for i, r := range []rune(lines[0]) {
		if isVerticalBorderRune(r) {
			candidates[i] = true
		}
	}

	// Keep only columns where a vertical border char appears on most lines (>50%)
	for col := range candidates {
		count := 0
		for _, line := range lines {
			runes := []rune(line)
			if col < len(runes) && isVerticalBorderRune(runes[col]) {
				count++
			}
		}
		if count < len(lines)/2 {
			delete(candidates, col)
		}
	}

	for col := range candidates {
		return col
	}
	return -1
}

// isHorizontalBorderRune returns true for box-drawing characters with a horizontal component.
func isHorizontalBorderRune(r rune) bool {
	switch r {
	case '─', '┼', '├', '┤', '┬', '┴', '┌', '┐', '└', '┘':
		return true
	}
	return false
}

// findHorizontalBorderRow finds a consistent horizontal border row in lines.
func findHorizontalBorderRow(lines []string) int {
	for row, line := range lines {
		count := 0
		for _, r := range line {
			if isHorizontalBorderRune(r) {
				count++
			}
		}
		if count > 0 && count >= len([]rune(line))/2 {
			return row
		}
	}
	return -1
}
