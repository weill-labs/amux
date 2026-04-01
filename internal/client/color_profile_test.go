package client

import (
	"io"
	"testing"

	"github.com/muesli/termenv"
	"github.com/weill-labs/amux/internal/mux"
)

type stubEnviron map[string]string

func (e stubEnviron) Environ() []string {
	return nil
}

func (e stubEnviron) Getenv(key string) string {
	return e[key]
}

func TestNewAttachClientRendererDetectsColorProfile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  stubEnviron
		want termenv.Profile
	}{
		{
			name: "truecolor terminal",
			env:  stubEnviron{"TERM": "xterm-ghostty"},
			want: termenv.TrueColor,
		},
		{
			name: "256 color terminal",
			env:  stubEnviron{"TERM": "xterm-256color"},
			want: termenv.ANSI256,
		},
		{
			name: "16 color terminal",
			env:  stubEnviron{"TERM": "xterm-color"},
			want: termenv.ANSI,
		},
		{
			name: "no color override",
			env:  stubEnviron{"TERM": "xterm-256color", "NO_COLOR": "1"},
			want: termenv.Ascii,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cr := newAttachClientRenderer(80, 24, mux.DefaultScrollbackLines, io.Discard, tt.env, termenv.WithTTY(true))
			if got := cr.ColorProfile(); got != tt.want {
				t.Fatalf("ColorProfile() = %v, want %v", got, tt.want)
			}
		})
	}
}
