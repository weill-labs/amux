package mux

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/weill-labs/amux/internal/mouse"
)

const (
	mouseModeStandard uint32 = 1 << iota
	mouseModeButton
	mouseModeAny
	mouseModeSGR
)

func (v *vtEmulator) setMouseMode(mode ansi.Mode, enabled bool) {
	bit := uint32(0)
	switch mode {
	case ansi.ModeMouseNormal:
		bit = mouseModeStandard
	case ansi.ModeMouseButtonEvent:
		bit = mouseModeButton
	case ansi.ModeMouseAnyEvent:
		bit = mouseModeAny
	case ansi.ModeMouseExtSgr:
		bit = mouseModeSGR
	default:
		return
	}
	for {
		current := v.mouseFlags.Load()
		next := current
		if enabled {
			next |= bit
		} else {
			next &^= bit
		}
		if v.mouseFlags.CompareAndSwap(current, next) {
			return
		}
	}
}

func (v *vtEmulator) MouseProtocol() MouseProtocol {
	flags := v.mouseFlags.Load()
	mouseProto := MouseProtocol{SGR: flags&mouseModeSGR != 0}
	switch {
	case flags&mouseModeAny != 0:
		mouseProto.Tracking = MouseTrackingAny
	case flags&mouseModeButton != 0:
		mouseProto.Tracking = MouseTrackingButton
	case flags&mouseModeStandard != 0:
		mouseProto.Tracking = MouseTrackingStandard
	default:
		mouseProto.Tracking = MouseTrackingNone
	}
	return mouseProto
}

func (v *vtEmulator) EncodeMouse(ev mouse.Event, x, y int) []byte {
	mouseProto := v.MouseProtocol()
	if !mouseProto.Enabled() {
		return nil
	}
	if x < 0 || y < 0 {
		return nil
	}

	switch ev.Action {
	case mouse.Motion:
		if mouseProto.Tracking != MouseTrackingButton && mouseProto.Tracking != MouseTrackingAny {
			return nil
		}
	case mouse.Release:
		if mouseProto.Tracking == MouseTrackingNone {
			return nil
		}
	}

	btn, ok := encodeMouseButton(ev.Button)
	if !ok {
		return nil
	}
	code := ansi.EncodeMouseButton(btn, ev.Action == mouse.Motion, ev.Shift, ev.Alt, ev.Ctrl)
	if code == 0xff {
		return nil
	}

	if mouseProto.SGR {
		return []byte(ansi.MouseSgr(code, x, y, ev.Action == mouse.Release))
	}
	return []byte(ansi.MouseX10(code, x, y))
}

func encodeMouseButton(btn mouse.Button) (ansi.MouseButton, bool) {
	switch btn {
	case mouse.ButtonLeft:
		return ansi.MouseLeft, true
	case mouse.ButtonMiddle:
		return ansi.MouseMiddle, true
	case mouse.ButtonRight:
		return ansi.MouseRight, true
	case mouse.ButtonNone:
		return ansi.MouseNone, true
	case mouse.ScrollUp:
		return ansi.MouseWheelUp, true
	case mouse.ScrollDown:
		return ansi.MouseWheelDown, true
	case mouse.ScrollLeft:
		return ansi.MouseWheelLeft, true
	case mouse.ScrollRight:
		return ansi.MouseWheelRight, true
	default:
		return 0, false
	}
}

func renderMouseProtocol(mouseProto MouseProtocol) string {
	var buf strings.Builder
	switch mouseProto.Tracking {
	case MouseTrackingStandard:
		buf.WriteString("\x1b[?1000h")
		buf.WriteString("\x1b[?1002l")
		buf.WriteString("\x1b[?1003l")
	case MouseTrackingButton:
		buf.WriteString("\x1b[?1000l")
		buf.WriteString("\x1b[?1002h")
		buf.WriteString("\x1b[?1003l")
	case MouseTrackingAny:
		buf.WriteString("\x1b[?1000l")
		buf.WriteString("\x1b[?1002l")
		buf.WriteString("\x1b[?1003h")
	default:
		buf.WriteString("\x1b[?1000l")
		buf.WriteString("\x1b[?1002l")
		buf.WriteString("\x1b[?1003l")
	}
	if mouseProto.SGR {
		buf.WriteString("\x1b[?1006h")
	} else {
		buf.WriteString("\x1b[?1006l")
	}
	return buf.String()
}
