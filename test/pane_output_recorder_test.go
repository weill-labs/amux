package test

import (
	"testing"
	"time"
)

// paneOutputRecorder attaches a second client to a test session and records
// raw MsgTypePaneOutput bytes by pane ID.
type paneOutputRecorder struct{}

func newPaneOutputRecorder(tb testing.TB, sockPath, session string, cols, rows int) *paneOutputRecorder {
	tb.Helper()
	return &paneOutputRecorder{}
}

func (r *paneOutputRecorder) clearPane(paneID uint32) {}

func (r *paneOutputRecorder) waitForBytes(paneID uint32, want []byte, timeout time.Duration) bool {
	return false
}

func (r *paneOutputRecorder) close() {}
