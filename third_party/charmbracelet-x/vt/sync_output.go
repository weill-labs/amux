package vt

import (
	"bytes"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/parser"
)

var synchronizedOutputResetSequence = []byte(ansi.ResetModeSynchronizedOutput)

func (e *Emulator) beginSynchronizedOutput() {
	e.syncOutputActive = true
	e.syncOutputBuffer = nil
	if e.now == nil {
		e.now = time.Now
	}
	e.syncOutputDeadline = e.now().Add(e.syncOutputTimeout)
}

func (e *Emulator) endSynchronizedOutput() {
	e.syncOutputActive = false
	e.syncOutputBuffer = nil
	e.syncOutputDeadline = time.Time{}
}

func (e *Emulator) flushExpiredSynchronizedOutput() {
	if !e.syncOutputActive || e.syncOutputDeadline.IsZero() {
		return
	}
	if e.now == nil {
		e.now = time.Now
	}
	if e.now().Before(e.syncOutputDeadline) {
		return
	}
	e.flushSynchronizedOutputBuffer(true)
}

func (e *Emulator) flushSynchronizedOutputBuffer(implicitReset bool) {
	buffered := append([]byte(nil), e.syncOutputBuffer...)
	e.syncOutputActive = false
	e.syncOutputBuffer = nil
	e.syncOutputDeadline = time.Time{}
	if len(buffered) > 0 {
		e.parseBytes(buffered)
	}
	if implicitReset {
		e.resetSynchronizedOutputMode()
	}
}

func (e *Emulator) resetSynchronizedOutputMode() {
	if !e.modes[ansi.ModeSynchronizedOutput].IsSet() {
		return
	}
	e.modes[ansi.ModeSynchronizedOutput] = ansi.ModeReset
	if e.cb.DisableMode != nil {
		e.cb.DisableMode(ansi.ModeSynchronizedOutput)
	}
}

func (e *Emulator) bufferSynchronizedOutput(p []byte) {
	if len(p) == 0 {
		return
	}
	e.syncOutputBuffer = append(e.syncOutputBuffer, p...)
	idx := bytes.Index(e.syncOutputBuffer, synchronizedOutputResetSequence)
	if idx < 0 {
		return
	}
	end := idx + len(synchronizedOutputResetSequence)
	buffered := append([]byte(nil), e.syncOutputBuffer[:end]...)
	suffix := append([]byte(nil), e.syncOutputBuffer[end:]...)
	e.syncOutputBuffer = nil
	e.syncOutputActive = false
	e.syncOutputDeadline = time.Time{}
	e.parseBytes(buffered)
	if len(suffix) == 0 {
		return
	}
	if e.syncOutputActive {
		e.bufferSynchronizedOutput(suffix)
		return
	}
	e.parseBytes(suffix)
}

func (e *Emulator) parseBytes(p []byte) {
	for i := range p {
		e.parser.Advance(p[i])
		state := e.parser.State()
		// flush grapheme if we transitioned to a non-utf8 state or we have
		// written the whole byte slice.
		if len(e.grapheme) > 0 {
			if (e.lastState == parser.GroundState && state != parser.Utf8State) || i == len(p)-1 {
				e.flushGrapheme()
			}
		}
		e.lastState = state
		if e.syncOutputActive {
			e.bufferSynchronizedOutput(p[i+1:])
			return
		}
	}
}
