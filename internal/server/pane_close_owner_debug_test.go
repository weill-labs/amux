//go:build debug

package server

import (
	"strings"
	"testing"
)

func TestPaneClosePanicsFromSessionEventLoop(t *testing.T) {
	sess := newSession("test-pane-close-owner")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	pane := newTestPane(sess, 1, "pane-1")
	t.Cleanup(func() {
		if err := pane.Close(); err != nil {
			t.Errorf("Close() = %v, want nil", err)
		}
		_ = pane.WaitClosed()
	})

	result := mustSessionQuery(t, sess, func(sess *Session) string {
		var panicText string
		func() {
			defer func() {
				if v := recover(); v != nil {
					panicText = v.(string)
				}
			}()
			_ = pane.Close()
		}()
		return panicText
	})

	if result == "" {
		t.Fatal("expected panic from event-loop Close()")
	}
	if !strings.Contains(result, "mux.Pane.Close") {
		t.Fatalf("panic = %q, want method name", result)
	}
}
