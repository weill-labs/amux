package pane

import (
	"fmt"
	"testing"

	"github.com/weill-labs/amux/internal/tmux"
)

// mockTmux implements tmux.Tmux for testing.
type mockTmux struct {
	panes   map[string]tmux.PaneFields
	outputs map[string]string
	options map[string]map[string]string
}

func newMockTmux() *mockTmux {
	return &mockTmux{
		panes:   make(map[string]tmux.PaneFields),
		outputs: make(map[string]string),
		options: make(map[string]map[string]string),
	}
}

func (m *mockTmux) ListPanes() (map[string]tmux.PaneFields, error) {
	return m.panes, nil
}

func (m *mockTmux) PaneOutput(paneID string, lines int) (string, error) {
	if out, ok := m.outputs[paneID]; ok {
		return out, nil
	}
	return "", fmt.Errorf("pane not found")
}

func (m *mockTmux) ResizePane(paneID string, height int) error { return nil }
func (m *mockTmux) SwapPane(src, dst string) error             { return nil }
func (m *mockTmux) PaneHeight(paneID string) (int, error)      { return 20, nil }

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

func (m *mockTmux) SetPaneTitle(paneID, title string) error { return nil }
func (m *mockTmux) SelectPane(paneID string) error          { return nil }
func (m *mockTmux) KillPane(paneID string) error            { return nil }
func (m *mockTmux) SplitWindow(cmd string) (string, error)  { return "%99", nil }
func (m *mockTmux) SendKeys(paneID string, keys ...string) error {
	return nil
}
func (m *mockTmux) CurrentSession() string                                { return "main" }
func (m *mockTmux) RemoteSessionAlive(user, host, session string) bool    { return false }
func (m *mockTmux) WindowPanes(paneID string) ([]string, error)          { return []string{paneID}, nil }

func TestDiscover(t *testing.T) {
	mt := newMockTmux()
	mt.panes = map[string]tmux.PaneFields{
		"%1": {ID: "%1", Name: "auth-agent", Host: "local", Task: "CHA-16"},
		"%2": {ID: "%2", Name: "trainer", Host: "lambda-a100", Task: "CHA-15", Color: "f38ba8"},
		"%3": {ID: "%3"}, // regular pane, no amux metadata
	}
	mt.outputs["%1"] = "Running tests...\n"
	mt.outputs["%2"] = "Epoch 3/10...\n"

	panes, err := Discover(mt)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	if len(panes) != 2 {
		t.Fatalf("expected 2 amux panes, got %d", len(panes))
	}

	// Should be sorted by ID
	if panes[0].ID != "%1" || panes[1].ID != "%2" {
		t.Errorf("unexpected order: %s, %s", panes[0].ID, panes[1].ID)
	}

	if panes[0].Name != "auth-agent" {
		t.Errorf("expected name auth-agent, got %s", panes[0].Name)
	}

	if panes[0].Status != StatusActive {
		t.Errorf("expected status active, got %s", panes[0].Status)
	}
}

func TestDiscoverMinimized(t *testing.T) {
	mt := newMockTmux()
	mt.panes = map[string]tmux.PaneFields{
		"%1": {ID: "%1", Name: "agent", Minimized: "1"},
	}

	panes, err := Discover(mt)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	if len(panes) != 1 {
		t.Fatalf("expected 1 pane, got %d", len(panes))
	}
	if panes[0].Status != StatusMinimized {
		t.Errorf("expected minimized, got %s", panes[0].Status)
	}
}

func TestIsIdle(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   bool
	}{
		{"shell prompt $", "some output\nuser@host:~$ ", true},
		{"shell prompt suffix", "some output\nuser@host:~$", true},
		{"starship prompt", "some output\n❯ ", true},
		{"active agent", "Running tests...\nProcessing file 42/100", false},
		{"exited", "some output\nexited with code 0", true},
		{"empty", "", false},
		{"blank lines", "\n\n\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsIdle(tt.output); got != tt.want {
				t.Errorf("IsIdle(%q) = %v, want %v", tt.output, got, tt.want)
			}
		})
	}
}

func TestLastNonEmptyLine(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello\nworld\n", "world"},
		{"hello\n\n\n", "hello"},
		{"", ""},
		{"\n\n\n", ""},
	}
	for _, tt := range tests {
		got := lastNonEmptyLine(tt.input)
		if got != tt.want {
			t.Errorf("lastNonEmptyLine(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
