package test

import (
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// amuxBin is the path to the built amux binary, set in TestMain.
var amuxBin string

// gocoverDir is the directory for integration test coverage data.
var gocoverDir string

// gocoverOwned is true when TestMain created gocoverDir (vs. inheriting it).
var gocoverOwned bool

// buildAmux builds the amux binary at binPath. When GOCOVERDIR is set,
// the binary is built with -cover so it writes coverage data on exit.
func buildAmux(binPath string) error {
	args := []string{"build"}
	if os.Getenv("GOCOVERDIR") != "" {
		args = append(args, "-cover", "-covermode=atomic")
	}
	args = append(args, "-o", binPath, "..")
	out, err := exec.Command("go", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("building amux: %v\n%s", err, out)
	}
	return nil
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

// cleanupStaleTestSessions removes orphaned amux server processes, sockets,
// and log files left behind by previous test runs that were killed by a
// timeout panic.
//
// Not safe if multiple `go test` invocations run concurrently — it may
// kill sessions belonging to the other run.
func cleanupStaleTestSessions() {
	// Kill orphaned amux server processes, validating session name
	out, _ := exec.Command("pgrep", "-fl", "amux _server t-").Output()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && isTestSession(fields[len(fields)-1]) {
			exec.Command("kill", fields[0]).Run()
		}
	}

	// Also kill orphaned benchmark amux servers
	out, _ = exec.Command("pgrep", "-fl", "amux _server bench-").Output()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && isBenchSession(fields[len(fields)-1]) {
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

	// Clean up stale sockets and log files
	socketDir := fmt.Sprintf("/tmp/amux-%d", os.Getuid())
	entries, _ := os.ReadDir(socketDir)
	for _, e := range entries {
		name := e.Name()
		base := strings.TrimSuffix(name, ".log")
		if isTestSession(base) || isBenchSession(base) {
			os.Remove(filepath.Join(socketDir, name))
		}
	}
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

// isPaneActive returns true if the captured screen shows the named pane
// with the active indicator (● [name]).
func isPaneActive(screen, paneName string) bool {
	target := "[" + paneName + "]"
	for _, line := range strings.Split(screen, "\n") {
		idx := strings.Index(line, target)
		if idx < 0 {
			continue
		}
		if strings.Contains(line[:idx], "●") {
			return true
		}
	}
	return false
}

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

