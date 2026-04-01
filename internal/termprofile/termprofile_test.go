package termprofile

import (
	"io"
	"testing"

	"github.com/muesli/termenv"
)

type stubEnviron map[string]string

func (e stubEnviron) Environ() []string {
	return nil
}

func (e stubEnviron) Getenv(key string) string {
	return e[key]
}

func TestParseAndFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		value     string
		want      termenv.Profile
		wantParse bool
	}{
		{name: "truecolor", value: "TrueColor", want: termenv.TrueColor, wantParse: true},
		{name: "ansi256", value: "ansi256", want: termenv.ANSI256, wantParse: true},
		{name: "ansi", value: " ANSI ", want: termenv.ANSI, wantParse: true},
		{name: "ascii", value: "ascii", want: termenv.Ascii, wantParse: true},
		{name: "unknown", value: "bogus", want: termenv.Ascii, wantParse: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := Parse(tt.value)
			if got != tt.want || ok != tt.wantParse {
				t.Fatalf("Parse(%q) = (%v, %t), want (%v, %t)", tt.value, got, ok, tt.want, tt.wantParse)
			}
			if ok {
				if name := Format(got); name == "" {
					t.Fatalf("Format(%v) returned empty string", got)
				}
			}
		})
	}
}

func TestDetect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  stubEnviron
		want termenv.Profile
	}{
		{name: "standard termenv detection", env: stubEnviron{"TERM": "xterm-256color"}, want: termenv.ANSI256},
		{name: "inherited amux profile", env: stubEnviron{"TERM": "amux", EnvKey: "TrueColor"}, want: termenv.TrueColor},
		{name: "amux defaults to ansi256", env: stubEnviron{"TERM": "amux"}, want: termenv.ANSI256},
		{name: "no color override wins", env: stubEnviron{"TERM": "amux", EnvKey: "TrueColor", "NO_COLOR": "1"}, want: termenv.Ascii},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := Detect(io.Discard, tt.env, termenv.WithTTY(true)); got != tt.want {
				t.Fatalf("Detect(%v) = %v, want %v", tt.env, got, tt.want)
			}
		})
	}
}

func TestDetectFromEnvironmentAndEnvEntry(t *testing.T) {
	t.Parallel()

	env := stubEnviron{"TERM": "xterm-color"}
	if got := DetectFromEnvironment(env); got != termenv.ANSI {
		t.Fatalf("DetectFromEnvironment() = %v, want %v", got, termenv.ANSI)
	}
	if got := EnvEntry(env); got != EnvKey+"=ANSI" {
		t.Fatalf("EnvEntry() = %q, want %q", got, EnvKey+"=ANSI")
	}
}

func TestDetectHandlesNilEnvironment(t *testing.T) {
	t.Parallel()

	if got := Detect(io.Discard, nil, termenv.WithTTY(true)); got != termenv.Ascii {
		t.Fatalf("Detect(nil environ) = %v, want %v", got, termenv.Ascii)
	}

	env := emptyEnviron{}
	if got := env.Environ(); got != nil {
		t.Fatalf("emptyEnviron.Environ() = %v, want nil", got)
	}
	if got := env.Getenv("TERM"); got != "" {
		t.Fatalf("emptyEnviron.Getenv() = %q, want empty string", got)
	}
}
