package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/render"
)

func TestMainDoctorFontsPrintsIconDiagnostic(t *testing.T) {
	t.Parallel()

	output, exitCode := runHermeticMain(t, "doctor", "fonts")
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0\noutput:\n%s", exitCode, output)
	}
	if strings.Contains(output, "connecting to server") {
		t.Fatalf("doctor fonts should be a local diagnostic, got server connection output:\n%s", output)
	}

	for _, want := range []string{
		"amux font diagnostics",
		"terminal font lacks",
		"not an amux bug",
		"does not install fonts or change config",
		`[theme]`,
		`icons = "ascii"`,
		`icons = "unicode"`,
		`icons = "nerd"`,
		`status_style = "powerline"`,
		"powerline:",
		"right=\ue0b4",
		"left=\ue0b6",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("doctor fonts output missing %q:\n%s", want, output)
		}
	}

	presets := []struct {
		name  string
		icons render.IconSet
	}{
		{name: config.ThemeIconsASCII, icons: render.ASCIIIconSet()},
		{name: config.ThemeIconsUnicode, icons: render.UnicodeIconSet()},
		{name: config.ThemeIconsNerd, icons: render.NerdFontIconSet()},
	}
	for _, preset := range presets {
		if !strings.Contains(output, preset.name+":") {
			t.Fatalf("doctor fonts output missing preset %q:\n%s", preset.name, output)
		}
		for label, glyph := range representativeDiagnosticGlyphs(preset.icons) {
			if glyph == "" {
				continue
			}
			want := fmt.Sprintf("%s=%s", label, glyph)
			if !strings.Contains(output, want) {
				t.Fatalf("doctor fonts output missing %s sample for %s:\n%s", want, preset.name, output)
			}
		}
	}
}

func representativeDiagnosticGlyphs(icons render.IconSet) map[string]string {
	return map[string]string{
		"idle":      icons.PaneIdle,
		"active":    icons.PaneActive,
		"busy":      icons.PaneBusy,
		"lead":      icons.PaneLead,
		"escalated": icons.PaneEscalated,
		"stuck":     icons.PaneStuck,
		"remote":    icons.RemoteHost,
		"pr":        icons.PR,
		"issue":     icons.Issue,
		"task":      icons.Task,
		"copy":      icons.CopyMode,
	}
}
