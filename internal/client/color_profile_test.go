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
		{
			name: "nested amux inherits truecolor profile",
			env:  stubEnviron{"TERM": "amux", "AMUX_COLOR_PROFILE": "TrueColor"},
			want: termenv.TrueColor,
		},
		{
			name: "nested amux inherits ansi256 profile",
			env:  stubEnviron{"TERM": "amux", "AMUX_COLOR_PROFILE": "ANSI256"},
			want: termenv.ANSI256,
		},
		{
			name: "nested amux defaults to ansi256",
			env:  stubEnviron{"TERM": "amux"},
			want: termenv.ANSI256,
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

func TestDetectTerminalColorProfileUsesProcessEnvironment(t *testing.T) {
	t.Parallel()

	env := processEnviron{}
	if got := env.Getenv("__AMUX_MISSING_ENV__"); got != "" {
		t.Fatalf("processEnviron.Getenv() = %q, want empty string", got)
	}
	if got := env.Environ(); len(got) == 0 {
		t.Fatal("processEnviron.Environ() returned no environment entries")
	}

	switch got := detectTerminalColorProfile(nil, nil, termenv.WithTTY(true)); got {
	case termenv.TrueColor, termenv.ANSI256, termenv.ANSI, termenv.Ascii:
	default:
		t.Fatalf("detectTerminalColorProfile(nil, nil) = %v, want known termenv profile", got)
	}
}

func TestClientRendererColorProfileNilGuards(t *testing.T) {
	t.Parallel()

	var nilRenderer *ClientRenderer
	nilRenderer.SetColorProfile(termenv.ANSI256)
	if got := nilRenderer.ColorProfile(); got != termenv.TrueColor {
		t.Fatalf("(*ClientRenderer)(nil).ColorProfile() = %v, want %v", got, termenv.TrueColor)
	}

	empty := &ClientRenderer{}
	empty.SetColorProfile(termenv.ANSI)
	if got := empty.ColorProfile(); got != termenv.TrueColor {
		t.Fatalf("(&ClientRenderer{}).ColorProfile() = %v, want %v", got, termenv.TrueColor)
	}
}
