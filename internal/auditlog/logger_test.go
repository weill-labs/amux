package auditlog

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
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

func TestLogWithLevelLivesInAuditlog(t *testing.T) {
	t.Parallel()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}

	internalDir := filepath.Dir(filepath.Dir(thisFile))
	auditlogDir := filepath.Join(internalDir, "auditlog")
	serverDir := filepath.Join(internalDir, "server")
	remoteDir := filepath.Join(internalDir, "remote")

	if !packageDeclaresFunc(t, auditlogDir, "LogWithLevel") {
		t.Fatal("internal/auditlog must declare LogWithLevel")
	}
	for _, dir := range []string{serverDir, remoteDir} {
		if packageDeclaresFunc(t, dir, "logWithLevel") || packageDeclaresFunc(t, dir, "LogWithLevel") {
			t.Fatalf("%s still declares its own logWithLevel helper", dir)
		}
	}
}

func packageDeclaresFunc(t *testing.T, dir, name string) bool {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatalf("filepath.Glob(%q): %v", dir, err)
	}

	fset := token.NewFileSet()
	for _, path := range matches {
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parser.ParseFile(%q): %v", path, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Name == nil {
				continue
			}
			if fn.Name.Name == name {
				return true
			}
		}
	}
	return false
}
