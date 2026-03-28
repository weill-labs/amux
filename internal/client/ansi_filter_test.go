package client

import (
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/proto"
)

func TestFilterRenderedANIOSC8Hyperlinks(t *testing.T) {
	t.Parallel()

	rendered := "\033]8;;https://example.com\alink\033]8;;\a plain"

	t.Run("preserves hyperlinks when supported", func(t *testing.T) {
		t.Parallel()
		got := filterRenderedANSI(rendered, proto.ClientCapabilities{Hyperlinks: true})
		if got != rendered {
			t.Fatalf("filterRenderedANSI() = %q, want unchanged", got)
		}
	})

	t.Run("strips hyperlinks when unsupported", func(t *testing.T) {
		t.Parallel()
		got := filterRenderedANSI(rendered, proto.ClientCapabilities{})
		if strings.Contains(got, "\033]8;") {
			t.Fatalf("filtered output should not contain OSC 8 sequences, got %q", got)
		}
		if !strings.Contains(got, "link plain") {
			t.Fatalf("filtered output should preserve visible text, got %q", got)
		}
	})
}
