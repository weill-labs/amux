package test

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var updateGoldens = flag.Bool("update", false, "update golden files")

// assertGolden compares actual against a golden file in testdata/.
// With -update flag, writes actual as the new golden.
func assertGolden(t *testing.T, name string, actual string) {
	t.Helper()
	path := filepath.Join("testdata", name)

	if *updateGoldens {
		os.MkdirAll("testdata", 0755)
		if err := os.WriteFile(path, []byte(actual), 0644); err != nil {
			t.Fatalf("writing golden %s: %v", path, err)
		}
		t.Logf("updated golden: %s", path)
		return
	}

	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden %s: %v\nRun with -update to create it.\nActual:\n%s", path, err, actual)
	}

	if actual != string(expected) {
		t.Errorf("golden mismatch: %s\n\n--- expected ---\n%s\n--- actual ---\n%s\n--- diff ---\n%s",
			path, string(expected), actual, goldenDiff(string(expected), actual))
	}
}

// goldenDiff produces a simple line-by-line diff between expected and actual.
func goldenDiff(expected, actual string) string {
	expLines := strings.Split(expected, "\n")
	actLines := strings.Split(actual, "\n")

	maxLen := len(expLines)
	if len(actLines) > maxLen {
		maxLen = len(actLines)
	}

	var diffs []string
	for i := 0; i < maxLen; i++ {
		var exp, act string
		if i < len(expLines) {
			exp = expLines[i]
		}
		if i < len(actLines) {
			act = actLines[i]
		}
		if exp != act {
			diffs = append(diffs, fmt.Sprintf("  line %2d exp: %q", i, exp))
			diffs = append(diffs, fmt.Sprintf("  line %2d act: %q", i, act))
		}
	}
	return strings.Join(diffs, "\n")
}

// ---------------------------------------------------------------------------
// Frame extraction — structural skeleton from plain-text capture
// ---------------------------------------------------------------------------

// extractFrame takes a plain-text capture (from `amux capture`) and returns
// only the structural elements: status lines, borders, and global bar.
// Pane content cells are replaced with spaces. Session name and timestamp
// in the global bar are normalized for deterministic comparison.
func extractFrame(capture string, sessionName string) string {
	lines := strings.Split(capture, "\n")
	var result []string

	for _, line := range lines {
		switch {
		case isStatusLine(line):
			result = append(result, line)
		case isGlobalBar(line):
			result = append(result, normalizeGlobalBar(line, sessionName))
		default:
			// Keep only border characters, replace content with spaces
			result = append(result, extractBorderLine(line))
		}
	}

	return strings.Join(result, "\n")
}

// isStatusLine returns true if the line contains a pane status indicator.
func isStatusLine(line string) bool {
	return strings.Contains(line, "[pane-")
}

var timeRe = regexp.MustCompile(`\d{2}:\d{2}`)

// normalizeGlobalBar replaces the random session name with SESSION and
// the timestamp with 00:00.
func normalizeGlobalBar(line string, sessionName string) string {
	line = strings.ReplaceAll(line, sessionName, "SESSION")
	return timeRe.ReplaceAllString(line, "00:00")
}

// extractBorderLine keeps only box-drawing border characters in a line,
// replacing everything else with spaces. Trailing spaces are trimmed.
func extractBorderLine(line string) string {
	runes := []rune(line)
	out := make([]rune, len(runes))
	for i, r := range runes {
		if isBorderRune(r) {
			out[i] = r
		} else {
			out[i] = ' '
		}
	}
	return strings.TrimRight(string(out), " ")
}


