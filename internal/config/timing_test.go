package config

import (
	"testing"
	"time"
)

func TestRenderFrameIntervalDefault(t *testing.T) {
	t.Parallel()

	if got := RenderFrameInterval; got != 16*time.Millisecond {
		t.Fatalf("RenderFrameInterval = %v, want %v", got, 16*time.Millisecond)
	}
}
