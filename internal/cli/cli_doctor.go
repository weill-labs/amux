package cli

import (
	"fmt"
	"io"

	"github.com/weill-labs/amux/internal/render"
)

const doctorUsage = "usage: amux doctor [--json] [--all-sessions] [--quiet|-q] [--verbose|-v] [check]"

func doctorCLICommands() map[string]commandHandler {
	return map[string]commandHandler{
		"doctor": func(inv invocation, args []string) int {
			if hasHelpFlag(args) {
				fmt.Fprintln(inv.runtime.Stdout, doctorUsage)
				return 0
			}
			return runDoctorCommand(inv, args)
		},
	}
}

func writeFontDiagnostics(w io.Writer) {
	fmt.Fprintln(w, "amux font diagnostics")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Icon presets ([theme] icons):")
	for _, preset := range render.IconSetPresets() {
		writeIconPresetDiagnostic(w, preset.Name, preset.Set)
	}

	right, left := render.PowerlineSeparators()
	fmt.Fprintln(w, "powerline:")
	fmt.Fprintf(w, "  separators: right=%s left=%s\n", right, left)
	fmt.Fprintln(w)

	fmt.Fprintln(w, "If any sample appears as a box, question mark, or missing/blank glyph,")
	fmt.Fprintln(w, "your terminal font lacks that glyph; this is not an amux bug.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Fallback config:")
	fmt.Fprintln(w, "Choose one icons value:")
	fmt.Fprintln(w, "[theme]")
	fmt.Fprintln(w, `icons = "ascii"   # safest fallback`)
	fmt.Fprintln(w, `# icons = "unicode" # default`)
	fmt.Fprintln(w, `# icons = "nerd"    # requires Nerd Font glyphs`)
	fmt.Fprintln(w, `# status_style = "powerline" # requires Powerline separator glyphs`)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "This command only prints samples; it does not install fonts or change config.")
}

func writeIconPresetDiagnostic(w io.Writer, name string, icons render.IconSet) {
	fmt.Fprintf(w, "%s:\n", name)
	fmt.Fprintf(w, "  pane: idle=%s active=%s busy=%s lead=%s escalated=%s stuck=%s\n",
		diagnosticGlyph(icons.PaneIdle),
		diagnosticGlyph(icons.PaneActive),
		diagnosticGlyph(icons.PaneBusy),
		diagnosticGlyph(icons.PaneLead),
		diagnosticGlyph(icons.PaneEscalated),
		diagnosticGlyph(icons.PaneStuck),
	)
	fmt.Fprintf(w, "  metadata: remote=%s pr=%s issue=%s task=%s copy=%s\n",
		diagnosticGlyph(icons.RemoteHost),
		diagnosticGlyph(icons.PR),
		diagnosticGlyph(icons.Issue),
		diagnosticGlyph(icons.Task),
		diagnosticGlyph(icons.CopyMode),
	)
	fmt.Fprintln(w)
}

func diagnosticGlyph(glyph string) string {
	if glyph == "" {
		return "(empty)"
	}
	return glyph
}
