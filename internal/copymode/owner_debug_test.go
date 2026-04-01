//go:build debug

package copymode

import (
	"strings"
	"testing"
)

func TestCopyModePanicsOnCrossGoroutineMutation(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(80, 24)
	cm := New(emu, 80, 24, 0)

	cm.SetScrollExit(true)

	panicCh := make(chan any, 1)
	go func() {
		defer func() {
			panicCh <- recover()
		}()
		cm.SetScrollExit(false)
	}()

	panicValue := <-panicCh
	if panicValue == nil {
		t.Fatal("expected panic from cross-goroutine mutation")
	}
	if got := panicValue.(string); !strings.Contains(got, "copymode.CopyMode.SetScrollExit") {
		t.Fatalf("panic = %q, want method name", got)
	}
}
