package wrap

import (
	"bytes"
	"strings"
	"testing"
)

func TestSetScrollRegion(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	setScrollRegion(&buf, 24)
	expected := "\x1b[1;24r"
	if buf.String() != expected {
		t.Errorf("setScrollRegion = %q, want %q", buf.String(), expected)
	}
}

func TestClearScrollRegion(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	clearScrollRegion(&buf)
	expected := "\x1b[r"
	if buf.String() != expected {
		t.Errorf("clearScrollRegion = %q, want %q", buf.String(), expected)
	}
}

func TestConfigBasic(t *testing.T) {
	t.Parallel()
	cfg := Config{
		PaneID: "%5",
		Cmd:    "echo",
		Args:   []string{"hello"},
	}
	if cfg.PaneID != "%5" {
		t.Errorf("PaneID = %q, want %%5", cfg.PaneID)
	}
	if cfg.Cmd != "echo" {
		t.Errorf("Cmd = %q, want echo", cfg.Cmd)
	}
}

func TestParserAltScreenIntegration(t *testing.T) {
	t.Parallel()
	// Simulate a sequence of child output with alt-screen transitions.
	p := &Parser{}

	// Normal output
	p.Feed([]byte("$ vim file.txt\r\n"))
	if p.InAltScreen() {
		t.Error("should not be in alt screen yet")
	}

	// Vim enters alt screen
	p.Feed([]byte("\x1b[?1049h\x1b[H\x1b[2J"))
	if !p.InAltScreen() {
		t.Error("should be in alt screen after vim starts")
	}

	// Vim draws some content
	p.Feed([]byte("~\r\n~\r\n~\r\n"))
	if !p.InAltScreen() {
		t.Error("should still be in alt screen")
	}

	// User exits vim
	p.Feed([]byte("\x1b[?1049l"))
	if p.InAltScreen() {
		t.Error("should not be in alt screen after vim exits")
	}
}

func TestStatusBarIntegrationWithMock(t *testing.T) {
	t.Parallel()
	mt := newMock()
	mt.options["%5"] = map[string]string{
		"@amux_name":  "worker-1",
		"@amux_host":  "gpu-box",
		"@amux_task":  "PROJ-99",
		"@amux_color": "a6e3a1",
	}

	sb := &StatusBar{PaneID: "%5", T: mt}
	meta := sb.Fetch()

	// Render with cursor positioning
	rendered := sb.Render(80, 25, meta)

	if !strings.HasPrefix(rendered, "\x1b[25H") {
		t.Error("expected cursor positioning to row 25")
	}
	if !strings.Contains(rendered, "worker-1") {
		t.Error("expected agent name in rendered bar")
	}
}
