package mux

import "github.com/weill-labs/amux/internal/proto"

// DefaultScrollbackLines aliases the shared scrollback default.
const DefaultScrollbackLines = proto.DefaultScrollbackLines

type (
	PaneMeta           = proto.PaneMeta
	CaptureHistoryLine = proto.CaptureHistoryLine
	CaptureSnapshot    = proto.CaptureSnapshot
	MouseTrackingMode  = proto.MouseTrackingMode
	MouseProtocol      = proto.MouseProtocol
	TerminalState      = proto.TerminalState
)

const (
	MouseTrackingNone     = proto.MouseTrackingNone
	MouseTrackingStandard = proto.MouseTrackingStandard
	MouseTrackingButton   = proto.MouseTrackingButton
	MouseTrackingAny      = proto.MouseTrackingAny
)
