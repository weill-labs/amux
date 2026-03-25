//go:build debug

package mux

import (
	"strings"
	"testing"
)

func TestWindowPanicsOnCrossGoroutineMutation(t *testing.T) {
	w := NewWindow(fakePaneID(1), 80, 24)

	w.Resize(81, 24)

	panicCh := make(chan any, 1)
	go func() {
		defer func() {
			panicCh <- recover()
		}()
		w.Resize(82, 24)
	}()

	panicValue := <-panicCh
	if panicValue == nil {
		t.Fatal("expected panic from cross-goroutine mutation")
	}
	if got := panicValue.(string); !strings.Contains(got, "mux.Window.Resize") {
		t.Fatalf("panic = %q, want method name", got)
	}
}
