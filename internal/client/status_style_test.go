package client

import (
	"testing"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
)

func TestClientRendererSetStatusStyleHandlesNilRenderer(t *testing.T) {
	t.Parallel()

	var nilClient *ClientRenderer
	nilClient.SetStatusStyle(config.StatusStylePowerline)

	emptyClient := &ClientRenderer{}
	emptyClient.SetStatusStyle(config.StatusStylePowerline)
}

func TestRendererSetStatusStyleUpdatesCompositor(t *testing.T) {
	t.Parallel()

	r := NewWithScrollback(20, 4, mux.DefaultScrollbackLines)
	t.Cleanup(r.Close)

	r.SetStatusStyle(config.StatusStylePowerline)

	got := withRendererActorValue(r, func(st *rendererActorState) string {
		return st.compositor.StatusStyle()
	})
	if got != config.StatusStylePowerline {
		t.Fatalf("StatusStyle() = %q, want %q", got, config.StatusStylePowerline)
	}
}

func TestClientRendererSetStatusStyleUpdatesCompositor(t *testing.T) {
	t.Parallel()

	cr := NewClientRendererWithScrollback(20, 4, mux.DefaultScrollbackLines)
	t.Cleanup(cr.renderer.Close)

	cr.SetStatusStyle(config.StatusStylePowerline)

	got := withRendererActorValue(cr.renderer, func(st *rendererActorState) string {
		return st.compositor.StatusStyle()
	})
	if got != config.StatusStylePowerline {
		t.Fatalf("StatusStyle() = %q, want %q", got, config.StatusStylePowerline)
	}
}
