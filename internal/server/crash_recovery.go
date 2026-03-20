package server

import (
	"fmt"
	"strings"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/mux"
)

const archivedCrashScreenMarker = "amux: archived pre-crash visible screen"

func restoreCrashHistory(ps checkpoint.CrashPaneState) []string {
	history := append([]string{}, ps.History...)
	if ps.IsProxy || ps.WasIdle {
		return history
	}

	lines := archivedCrashScreenLines(ps.Screen, ps.Cols, ps.Rows)
	if len(lines) == 0 {
		return history
	}
	history = append(history, archivedCrashScreenMarker)
	history = append(history, lines...)
	return history
}

func replayCrashRecoveredScreen(pane *mux.Pane, ps checkpoint.CrashPaneState) {
	if ps.IsProxy {
		if ps.Screen != "" {
			pane.ReplayScreen(ps.Screen)
		}
		return
	}
	if ps.WasIdle {
		if ps.Screen != "" {
			pane.ReplayScreen(ps.Screen)
		}
		return
	}
	pane.ReplayScreen(recoveryNoticeScreen(ps.Command))
}

func archivedCrashScreenLines(screen string, cols, rows int) []string {
	if screen == "" {
		return nil
	}
	if cols <= 0 {
		cols = DefaultTermCols
	}
	if rows <= 0 {
		rows = DefaultTermRows
	}

	emu := mux.NewVTEmulatorWithDrain(cols, rows)
	_, _ = emu.Write([]byte(screen))

	var out []string
	for _, line := range mux.EmulatorContentLines(emu) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

func recoveryNoticeScreen(command string) string {
	lines := []string{"amux: previous process lost during crash recovery"}
	if command != "" {
		lines = append(lines, fmt.Sprintf("was: %s", command))
	}
	return strings.Join(lines, "\r\n") + "\r\n"
}
