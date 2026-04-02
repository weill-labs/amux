package config

import "time"

// Shared default timing values. Tests can still override per-struct fields in
// the consuming packages; these constants are only the built-in defaults.
const (
	VTIdleSettle              = 2 * time.Second
	VTIdleTimeout             = 60 * time.Second
	UndoGracePeriod           = 30 * time.Second
	BootstrapPaneReplayWait   = 50 * time.Millisecond
	BootstrapCorrectionWindow = 50 * time.Millisecond
	RenderFrameInterval       = time.Second / 30
	RenderPriorityWindow      = 40 * time.Millisecond
)
