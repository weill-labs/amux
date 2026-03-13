package swap

import (
	"testing"

	"github.com/weill-labs/amux/internal/tmux"
)

type mockTmux struct {
	options map[string]map[string]string
	swapped bool
}

func newMock() *mockTmux {
	return &mockTmux{
		options: make(map[string]map[string]string),
	}
}

func (m *mockTmux) ListPanes() (map[string]tmux.PaneFields, error) { return nil, nil }
func (m *mockTmux) PaneOutput(paneID string, lines int) (string, error) {
	return "", nil
}
func (m *mockTmux) ResizePane(paneID string, height int) error   { return nil }
func (m *mockTmux) PaneHeight(paneID string) (int, error)        { return 20, nil }
func (m *mockTmux) SetPaneTitle(paneID, title string) error      { return nil }
func (m *mockTmux) SelectPane(paneID string) error               { return nil }
func (m *mockTmux) KillPane(paneID string) error                 { return nil }
func (m *mockTmux) SplitWindow(cmd string) (string, error)       { return "%99", nil }
func (m *mockTmux) SendKeys(paneID string, keys ...string) error { return nil }
func (m *mockTmux) CurrentSession() string                       { return "main" }
func (m *mockTmux) RemoteSessionAlive(user, host, session string) bool {
	return false
}
func (m *mockTmux) WindowPanes(paneID string) ([]string, error) {
	return []string{paneID}, nil
}

func (m *mockTmux) SwapPane(src, dst string) error {
	m.swapped = true
	return nil
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

func TestSwapWithMeta(t *testing.T) {
	mt := newMock()
	mt.options["%1"] = map[string]string{
		"@amux_name":  "agent-a",
		"@amux_host":  "local",
		"@amux_task":  "CHA-1",
		"@amux_color": "a6e3a1",
	}
	mt.options["%2"] = map[string]string{
		"@amux_name":  "agent-b",
		"@amux_host":  "remote-1",
		"@amux_task":  "CHA-2",
		"@amux_color": "f38ba8",
	}

	if err := SwapWithMeta(mt, "%1", "%2"); err != nil {
		t.Fatalf("SwapWithMeta: %v", err)
	}

	if !mt.swapped {
		t.Error("expected SwapPane to be called")
	}

	// After swap, pane A should have B's original metadata (and vice versa)
	if mt.options["%1"]["@amux_name"] != "agent-b" {
		t.Errorf("expected pane-A name=agent-b, got %s", mt.options["%1"]["@amux_name"])
	}
	if mt.options["%2"]["@amux_name"] != "agent-a" {
		t.Errorf("expected pane-B name=agent-a, got %s", mt.options["%2"]["@amux_name"])
	}
	if mt.options["%1"]["@amux_color"] != "f38ba8" {
		t.Errorf("expected pane-A color=f38ba8, got %s", mt.options["%1"]["@amux_color"])
	}
	if mt.options["%2"]["@amux_host"] != "local" {
		t.Errorf("expected pane-B host=local, got %s", mt.options["%2"]["@amux_host"])
	}
}
