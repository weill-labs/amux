package minimize

import (
	"testing"

	"github.com/weill-labs/amux/internal/tmux"
)

type mockTmux struct {
	panes   map[string]tmux.PaneFields
	options map[string]map[string]string
	heights map[string]int
	window  map[string][]string // paneID -> window pane IDs
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
func (m *mockTmux) PaneOutput(paneID string, lines int) (string, error) {
	return "", nil
}
func (m *mockTmux) SwapPane(src, dst string) error                     { return nil }
func (m *mockTmux) SetPaneTitle(paneID, title string) error            { return nil }
func (m *mockTmux) SelectPane(paneID string) error                     { return nil }
func (m *mockTmux) KillPane(paneID string) error                       { return nil }
func (m *mockTmux) SplitWindow(cmd string) (string, error)             { return "%99", nil }
func (m *mockTmux) SendKeys(paneID string, keys ...string) error       { return nil }
func (m *mockTmux) CurrentSession() string                             { return "main" }
func (m *mockTmux) RemoteSessionAlive(user, host, session string) bool { return false }

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

func (m *mockTmux) WindowPanes(paneID string) ([]string, error) {
	if panes, ok := m.window[paneID]; ok {
		return panes, nil
	}
	return []string{paneID}, nil
}
func (m *mockTmux) JoinPane(src, dst string) error                            { return nil }
func (m *mockTmux) SessionWindowPanes(sessionWindow string) ([]string, error) { return nil, nil }

func TestMinimize(t *testing.T) {
	t.Parallel()
	mt := newMock()
	mt.heights["%1"] = 30
	mt.window["%1"] = []string{"%1", "%2"} // two panes in window

	if err := Minimize(mt, "%1"); err != nil {
		t.Fatalf("Minimize: %v", err)
	}

	// Should resize to 1
	if mt.heights["%1"] != 1 {
		t.Errorf("expected height 1, got %d", mt.heights["%1"])
	}

	// Should save restore height
	restoreH := mt.options["%1"]["@amux_restore_h"]
	if restoreH != "30" {
		t.Errorf("expected restore_h 30, got %s", restoreH)
	}

	// Should set minimized flag
	if mt.options["%1"]["@amux_minimized"] != "1" {
		t.Error("expected @amux_minimized to be 1")
	}
}

func TestMinimizeAlreadyMinimized(t *testing.T) {
	t.Parallel()
	mt := newMock()
	mt.options["%1"] = map[string]string{"@amux_minimized": "1"}

	err := Minimize(mt, "%1")
	if err == nil {
		t.Fatal("expected error for already minimized pane")
	}
}

func TestMinimizeLastPane(t *testing.T) {
	t.Parallel()
	mt := newMock()
	mt.heights["%1"] = 30
	mt.window["%1"] = []string{"%1"} // only pane in window

	err := Minimize(mt, "%1")
	if err == nil {
		t.Fatal("expected error when minimizing last pane")
	}
}

func TestRestore(t *testing.T) {
	t.Parallel()
	mt := newMock()
	mt.options["%1"] = map[string]string{
		"@amux_minimized": "1",
		"@amux_restore_h": "30",
	}
	mt.heights["%1"] = 1

	if err := Restore(mt, "%1"); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if mt.heights["%1"] != 30 {
		t.Errorf("expected height 30, got %d", mt.heights["%1"])
	}

	// Should clear minimized state
	if mt.options["%1"]["@amux_minimized"] != "" {
		t.Error("expected @amux_minimized to be cleared")
	}
}

func TestRestoreNotMinimized(t *testing.T) {
	t.Parallel()
	mt := newMock()
	err := Restore(mt, "%1")
	if err == nil {
		t.Fatal("expected error for non-minimized pane")
	}
}

func TestMinimizeRestore_RoundTrip(t *testing.T) {
	t.Parallel()
	mt := newMock()
	mt.heights["%1"] = 25
	mt.window["%1"] = []string{"%1", "%2"}

	if err := Minimize(mt, "%1"); err != nil {
		t.Fatalf("Minimize: %v", err)
	}
	if mt.heights["%1"] != 1 {
		t.Fatalf("after minimize: height=%d, want 1", mt.heights["%1"])
	}

	if err := Restore(mt, "%1"); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if mt.heights["%1"] != 25 {
		t.Errorf("after restore: height=%d, want 25", mt.heights["%1"])
	}
}
