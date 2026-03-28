package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func normalizeCaptureScreen(capture, sessionName string) string {
	lines := strings.Split(strings.TrimSuffix(capture, "\n"), "\n")
	for i, line := range lines {
		line = normalizeIdleIcon(line)
		if isGlobalBar(line) {
			line = normalizeGlobalBar(line, sessionName)
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

func writeCursorAssembledScript(t *testing.T, body string) string {
	t.Helper()
	scriptPath := filepath.Join(t.TempDir(), "cursor-assembled.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nset -eu\n"+body), 0o755); err != nil {
		t.Fatalf("writing script: %v", err)
	}
	return scriptPath
}

func TestCaptureDisplayMatchesCaptureForCursorAssembledGraphemes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		scriptBody string
		golden     string
	}{
		{
			name: "emoji modifier applied after wide emoji",
			scriptBody: strings.Join([]string{
				"clear",
				"printf '👍'",
				"sleep 0.2",
				"printf '🏻2\\n'",
				"sleep 0.2",
				"printf 'DONE\\n'",
				"sleep 30",
			}, "\n"),
			golden: "renderdiff_grapheme_modifier.golden",
		},
		{
			name: "zwj suffix written after emoji base",
			scriptBody: strings.Join([]string{
				"clear",
				"printf '🤷'",
				"sleep 0.2",
				"printf '‍♂️3\\n'",
				"sleep 0.2",
				"printf 'DONE\\n'",
				"sleep 30",
			}, "\n"),
			golden: "renderdiff_grapheme_zwj.golden",
		},
		{
			name: "regional indicator repair via backspace",
			scriptBody: strings.Join([]string{
				"clear",
				"printf '🇸4'",
				"sleep 0.2",
				"printf '\\b🇪4\\n'",
				"sleep 0.2",
				"printf 'DONE\\n'",
				"sleep 30",
			}, "\n"),
			golden: "renderdiff_grapheme_flag_repair.golden",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newAmuxHarness(t)
			scriptPath := writeCursorAssembledScript(t, tt.scriptBody)

			h.sendKeys("sh "+scriptPath, "Enter")
			if !h.waitFor("DONE", 10*time.Second) {
				t.Fatalf("timed out waiting for DONE\nouter:\n%s", h.captureOuter())
			}

			outer := normalizeCaptureScreen(h.captureOuter(), h.session)
			assertGolden(t, tt.golden, outer+"\n")
		})
	}
}
