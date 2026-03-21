package client

import (
	"testing"

	"github.com/weill-labs/amux/internal/proto"
)

func TestDetectAttachCapabilitiesFromEnv(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		want proto.ClientCapabilities
	}{
		{
			name: "unknown terminal defaults to legacy",
			env: map[string]string{
				"TERM":         "screen-256color",
				"TERM_PROGRAM": "tmux",
			},
			want: proto.ClientCapabilities{},
		},
		{
			name: "kitty",
			env: map[string]string{
				"TERM": "xterm-kitty",
			},
			want: proto.ClientCapabilities{
				KittyKeyboard:       true,
				Hyperlinks:          true,
				RichUnderline:       true,
				PromptMarkers:       true,
				GraphicsPlaceholder: true,
			},
		},
		{
			name: "ghostty",
			env: map[string]string{
				"TERM_PROGRAM": "ghostty",
			},
			want: proto.ClientCapabilities{
				KittyKeyboard:       true,
				Hyperlinks:          true,
				RichUnderline:       true,
				CursorMetadata:      true,
				PromptMarkers:       true,
				GraphicsPlaceholder: true,
			},
		},
		{
			name: "wezterm",
			env: map[string]string{
				"WEZTERM_EXECUTABLE": "/Applications/WezTerm.app/Contents/MacOS/wezterm",
			},
			want: proto.ClientCapabilities{
				Hyperlinks:          true,
				RichUnderline:       true,
				CursorMetadata:      true,
				PromptMarkers:       true,
				GraphicsPlaceholder: true,
			},
		},
		{
			name: "iterm2",
			env: map[string]string{
				"ITERM_SESSION_ID": "w0t0p0:ABC123",
			},
			want: proto.ClientCapabilities{
				KittyKeyboard:  true,
				Hyperlinks:     true,
				CursorMetadata: true,
				PromptMarkers:  true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := detectAttachCapabilitiesFromEnv(mapLookup(tt.env)); got != tt.want {
				t.Fatalf("detectAttachCapabilitiesFromEnv() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDetectAttachCapabilitiesFromEnvOverride(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		want proto.ClientCapabilities
	}{
		{
			name: "override unknown terminal",
			env: map[string]string{
				"TERM_PROGRAM":             "tmux",
				"AMUX_CLIENT_CAPABILITIES": "hyperlinks,prompt_markers",
			},
			want: proto.ClientCapabilities{
				Hyperlinks:    true,
				PromptMarkers: true,
			},
		},
		{
			name: "disable detected capability",
			env: map[string]string{
				"TERM":                     "xterm-kitty",
				"AMUX_CLIENT_CAPABILITIES": "-kitty_keyboard,-graphics_placeholder",
			},
			want: proto.ClientCapabilities{
				Hyperlinks:    true,
				RichUnderline: true,
				PromptMarkers: true,
			},
		},
		{
			name: "legacy then selective enable",
			env: map[string]string{
				"TERM_PROGRAM":             "ghostty",
				"AMUX_CLIENT_CAPABILITIES": "legacy,hyperlinks,cursor_metadata",
			},
			want: proto.ClientCapabilities{
				Hyperlinks:     true,
				CursorMetadata: true,
			},
		},
		{
			name: "all then disable one",
			env: map[string]string{
				"TERM_PROGRAM":             "tmux",
				"AMUX_CLIENT_CAPABILITIES": "all,-graphics_placeholder",
			},
			want: proto.ClientCapabilities{
				KittyKeyboard:  true,
				Hyperlinks:     true,
				RichUnderline:  true,
				CursorMetadata: true,
				PromptMarkers:  true,
			},
		},
		{
			name: "ignore unknown token",
			env: map[string]string{
				"TERM_PROGRAM":             "tmux",
				"AMUX_CLIENT_CAPABILITIES": "hyperlinks,not_real,prompt_markers",
			},
			want: proto.ClientCapabilities{
				Hyperlinks:    true,
				PromptMarkers: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := detectAttachCapabilitiesFromEnv(mapLookup(tt.env)); got != tt.want {
				t.Fatalf("detectAttachCapabilitiesFromEnv() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func mapLookup(values map[string]string) envLookup {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}
