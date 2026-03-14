package wrap

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/weill-labs/amux/internal/tmux"
)

type mockTmux struct {
	panes   map[string]tmux.PaneFields
	options map[string]map[string]string
	heights map[string]int
	window  map[string][]string
}

func newMock() *mockTmux {
	return &mockTmux{
		panes:   make(map[string]tmux.PaneFields),
		options: make(map[string]map[string]string),
		heights: make(map[string]int),
		window:  make(map[string][]string),
	}
}

func (m *mockTmux) ListPanes() (map[string]tmux.PaneFields, error) { return m.panes, nil }
func (m *mockTmux) PaneOutput(string, int) (string, error)         { return "", nil }
func (m *mockTmux) SwapPane(string, string) error                  { return nil }
func (m *mockTmux) SetPaneTitle(string, string) error              { return nil }
func (m *mockTmux) SelectPane(string) error                        { return nil }
func (m *mockTmux) KillPane(string) error                          { return nil }
func (m *mockTmux) SplitWindow(string) (string, error)             { return "%99", nil }
func (m *mockTmux) SendKeys(string, ...string) error               { return nil }
func (m *mockTmux) CurrentSession() string                         { return "main" }
func (m *mockTmux) RemoteSessionAlive(string, string, string) bool { return false }
func (m *mockTmux) WindowPanes(string) ([]string, error)           { return nil, nil }
func (m *mockTmux) JoinPane(string, string) error                  { return nil }
func (m *mockTmux) SessionWindowPanes(string) ([]string, error)    { return nil, nil }

func (m *mockTmux) ResizePane(paneID string, height int) error {
	m.heights[paneID] = height
	return nil
}

func (m *mockTmux) PaneHeight(paneID string) (int, error) {
	if h, ok := m.heights[paneID]; ok {
		return h, nil
	}
	return 20, nil
}

func (m *mockTmux) GetOption(paneID, key string) (string, error) {
	if opts, ok := m.options[paneID]; ok {
		return opts[key], nil
	}
	return "", nil
}

func (m *mockTmux) SetOption(paneID, key, value string) error {
	if m.options[paneID] == nil {
		m.options[paneID] = make(map[string]string)
	}
	m.options[paneID][key] = value
	return nil
}

func TestStatusBarFetch(t *testing.T) {
	t.Parallel()
	mt := newMock()
	mt.options["%1"] = map[string]string{
		"@amux_name":  "auth-agent",
		"@amux_host":  "lambda-a100",
		"@amux_task":  "CHA-42",
		"@amux_color": "f38ba8",
	}

	sb := &StatusBar{PaneID: "%1", T: mt}
	meta := sb.Fetch()

	if meta.Name != "auth-agent" {
		t.Errorf("Name = %q, want auth-agent", meta.Name)
	}
	if meta.Host != "lambda-a100" {
		t.Errorf("Host = %q, want lambda-a100", meta.Host)
	}
	if meta.Task != "CHA-42" {
		t.Errorf("Task = %q, want CHA-42", meta.Task)
	}
	if meta.Color != "f38ba8" {
		t.Errorf("Color = %q, want f38ba8", meta.Color)
	}
}

func TestStatusBarRenderLine(t *testing.T) {
	t.Parallel()
	mt := newMock()
	sb := &StatusBar{PaneID: "%1", T: mt}

	meta := PaneMeta{
		Name:  "test-agent",
		Host:  "remote-gpu",
		Task:  "TASK-1",
		Color: "a6e3a1",
	}

	line := sb.RenderLine(80, meta)
	width := lipgloss.Width(line)

	if width != 80 {
		t.Errorf("rendered width = %d, want 80", width)
	}
}

func TestStatusBarRenderLineLocalHost(t *testing.T) {
	t.Parallel()
	mt := newMock()
	sb := &StatusBar{PaneID: "%1", T: mt}

	meta := PaneMeta{
		Name: "pane-1",
		Host: "local",
	}

	line := sb.RenderLine(60, meta)
	// "local" host should not appear in output
	if strings.Contains(line, "@local") {
		t.Error("local host should not be shown in status bar")
	}
}

func TestStatusBarRenderCursorPosition(t *testing.T) {
	t.Parallel()
	mt := newMock()
	sb := &StatusBar{PaneID: "%1", T: mt}
	meta := PaneMeta{Name: "test"}

	rendered := sb.Render(80, 25, meta)
	if !strings.HasPrefix(rendered, "\x1b[25H") {
		t.Errorf("expected cursor position ESC[25H, got prefix %q", rendered[:10])
	}
}

func TestStatusBarRenderNarrow(t *testing.T) {
	t.Parallel()
	mt := newMock()
	sb := &StatusBar{PaneID: "%1", T: mt}

	meta := PaneMeta{
		Name:  "very-long-agent-name",
		Host:  "very-long-hostname",
		Task:  "VERY-LONG-TASK-ID",
		Color: "f38ba8",
	}

	// Should not panic on narrow widths
	line := sb.RenderLine(20, meta)
	if lipgloss.Width(line) < 20 {
		t.Errorf("rendered width = %d, want at least 20", lipgloss.Width(line))
	}
}
