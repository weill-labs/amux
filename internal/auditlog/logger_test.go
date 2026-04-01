package auditlog

import (
	"bytes"
	"strings"
	"testing"

	charmlog "github.com/charmbracelet/log"
)

func TestParseLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		raw      string
		fallback charmlog.Level
		want     charmlog.Level
	}{
		{name: "empty uses fallback", raw: "", fallback: charmlog.WarnLevel, want: charmlog.WarnLevel},
		{name: "valid level", raw: "error", fallback: charmlog.InfoLevel, want: charmlog.ErrorLevel},
		{name: "invalid uses fallback", raw: "nope", fallback: charmlog.DebugLevel, want: charmlog.DebugLevel},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := ParseLevel(tt.raw, tt.fallback); got != tt.want {
				t.Fatalf("ParseLevel(%q, %v) = %v, want %v", tt.raw, tt.fallback, got, tt.want)
			}
		})
	}
}

func TestNewFormats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		format string
		check  func(t *testing.T, output string)
	}{
		{
			name:   "auto defaults to json for buffers",
			format: FormatAuto,
			check: func(t *testing.T, output string) {
				t.Helper()
				if !strings.Contains(output, `"msg":"hello"`) {
					t.Fatalf("output %q missing json message", output)
				}
			},
		},
		{
			name:   "json format",
			format: FormatJSON,
			check: func(t *testing.T, output string) {
				t.Helper()
				if !strings.Contains(output, `"event":"audit_event"`) {
					t.Fatalf("output %q missing json event", output)
				}
			},
		},
		{
			name:   "logfmt format",
			format: FormatLogfmt,
			check: func(t *testing.T, output string) {
				t.Helper()
				if !strings.Contains(output, "msg=hello") {
					t.Fatalf("output %q missing logfmt message", output)
				}
			},
		},
		{
			name:   "text format",
			format: FormatText,
			check: func(t *testing.T, output string) {
				t.Helper()
				if strings.HasPrefix(strings.TrimSpace(output), "{") {
					t.Fatalf("output %q unexpectedly looks like json", output)
				}
				if !strings.Contains(output, "hello") {
					t.Fatalf("output %q missing text message", output)
				}
			},
		},
		{
			name:   "invalid format falls back to auto",
			format: "bogus",
			check: func(t *testing.T, output string) {
				t.Helper()
				if !strings.Contains(output, `"msg":"hello"`) {
					t.Fatalf("output %q missing fallback json message", output)
				}
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			logger := New(&buf, Options{Format: tt.format, Level: charmlog.InfoLevel})
			logger.Info("hello", "event", "audit_event")
			tt.check(t, buf.String())
		})
	}
}

func TestNewNilWriterAndDiscard(t *testing.T) {
	t.Parallel()

	New(nil, Options{Format: FormatJSON, Level: charmlog.InfoLevel}).Info("hello")
	Discard().Info("discarded")
}
