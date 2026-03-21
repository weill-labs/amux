package proto

import (
	"reflect"
	"testing"
)

func TestNegotiateClientCapabilities(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		advertised *ClientCapabilities
		want       ClientCapabilities
	}{
		{
			name:       "legacy attach omits capabilities",
			advertised: nil,
			want:       ClientCapabilities{},
		},
		{
			name: "partial modern attach",
			advertised: &ClientCapabilities{
				Hyperlinks:     true,
				PromptMarkers:  true,
				CursorMetadata: true,
			},
			want: ClientCapabilities{
				Hyperlinks:     true,
				PromptMarkers:  true,
				CursorMetadata: true,
			},
		},
		{
			name:       "all known capabilities",
			advertised: ptrClientCaps(KnownClientCapabilities()),
			want:       KnownClientCapabilities(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := NegotiateClientCapabilities(tt.advertised); got != tt.want {
				t.Fatalf("NegotiateClientCapabilities() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestClientCapabilitiesEnabledNamesAndSummary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		caps ClientCapabilities
		want []string
		text string
	}{
		{
			name: "legacy",
			caps: ClientCapabilities{},
			want: []string{},
			text: "legacy",
		},
		{
			name: "stable ordering",
			caps: ClientCapabilities{
				GraphicsPlaceholder: true,
				Hyperlinks:          true,
				KittyKeyboard:       true,
			},
			want: []string{"kitty_keyboard", "hyperlinks", "graphics_placeholder"},
			text: "kitty_keyboard,hyperlinks,graphics_placeholder",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.caps.EnabledNames(); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("EnabledNames() = %v, want %v", got, tt.want)
			}
			if got := tt.caps.Summary(); got != tt.text {
				t.Fatalf("Summary() = %q, want %q", got, tt.text)
			}
		})
	}
}

func ptrClientCaps(c ClientCapabilities) *ClientCapabilities {
	return &c
}
