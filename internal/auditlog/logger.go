package auditlog

import (
	"io"
	"log/slog"
	"os"
	"strings"

	charmlog "github.com/charmbracelet/log"
	"golang.org/x/term"
)

const (
	FormatAuto   = "auto"
	FormatText   = "text"
	FormatJSON   = "json"
	FormatLogfmt = "logfmt"
)

type Options struct {
	Format          string
	Level           charmlog.Level
	Prefix          string
	ReportTimestamp bool
}

func New(w io.Writer, opts Options) *charmlog.Logger {
	if w == nil {
		w = io.Discard
	}
	format := normalizeFormat(opts.Format)
	return charmlog.NewWithOptions(w, charmlog.Options{
		Formatter:       formatterFor(format, w),
		Level:           opts.Level,
		Prefix:          opts.Prefix,
		ReportTimestamp: opts.ReportTimestamp,
	})
}

func Discard() *charmlog.Logger {
	return New(io.Discard, Options{Format: FormatJSON})
}

func ParseLevel(raw string, fallback charmlog.Level) charmlog.Level {
	if raw == "" {
		return fallback
	}
	level, err := charmlog.ParseLevel(raw)
	if err != nil {
		return fallback
	}
	return level
}

func normalizeFormat(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", FormatAuto:
		return FormatAuto
	case FormatText:
		return FormatText
	case FormatJSON:
		return FormatJSON
	case FormatLogfmt:
		return FormatLogfmt
	default:
		return FormatAuto
	}
}

func formatterFor(format string, w io.Writer) charmlog.Formatter {
	switch format {
	case FormatText:
		return charmlog.TextFormatter
	case FormatJSON:
		return charmlog.JSONFormatter
	case FormatLogfmt:
		return charmlog.LogfmtFormatter
	default:
		if file, ok := w.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
			return charmlog.TextFormatter
		}
		return charmlog.JSONFormatter
	}
}

var _ slog.Handler = (*charmlog.Logger)(nil)
