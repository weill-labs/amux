package merge

import (
	"fmt"
	"testing"

	"github.com/weill-labs/amux/internal/tmux"
)

type mockTmux struct {
	session string
	windows map[string][]string // "session:window" -> pane IDs
	joined  []joinCall
}

type joinCall struct{ src, dst string }

func newMock() *mockTmux {
	return &mockTmux{
		session: "amux",
		windows: make(map[string][]string),
	}
}

func (m *mockTmux) ListPanes() (map[string]tmux.PaneFields, error)      { return nil, nil }
func (m *mockTmux) PaneOutput(paneID string, lines int) (string, error) { return "", nil }
func (m *mockTmux) ResizePane(paneID string, height int) error          { return nil }
func (m *mockTmux) SwapPane(src, dst string) error                      { return nil }
func (m *mockTmux) PaneHeight(paneID string) (int, error)               { return 20, nil }
func (m *mockTmux) GetOption(paneID, key string) (string, error)        { return "", nil }
func (m *mockTmux) SetOption(paneID, key, value string) error           { return nil }
func (m *mockTmux) SetPaneTitle(paneID, title string) error             { return nil }
func (m *mockTmux) SelectPane(paneID string) error                      { return nil }
func (m *mockTmux) KillPane(paneID string) error                        { return nil }
func (m *mockTmux) SplitWindow(cmd string) (string, error)              { return "%99", nil }
func (m *mockTmux) SendKeys(paneID string, keys ...string) error        { return nil }
func (m *mockTmux) CurrentSession() string                              { return m.session }
func (m *mockTmux) RemoteSessionAlive(user, host, session string) bool  { return false }
func (m *mockTmux) WindowPanes(paneID string) ([]string, error)         { return []string{paneID}, nil }

func (m *mockTmux) JoinPane(src, dst string) error {
	m.joined = append(m.joined, joinCall{src, dst})
	return nil
}

func (m *mockTmux) SessionWindowPanes(sessionWindow string) ([]string, error) {
	if panes, ok := m.windows[sessionWindow]; ok {
		return panes, nil
	}
	return nil, fmt.Errorf("window not found: %s", sessionWindow)
}

func TestMerge(t *testing.T) {
	t.Parallel()
	mt := newMock()
	mt.windows["amux:0"] = []string{"%1"}
	mt.windows["amux:1"] = []string{"%2", "%3"}

	count, err := Merge(mt, "1", "0")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	if count != 2 {
		t.Errorf("expected 2 panes merged, got %d", count)
	}

	if len(mt.joined) != 2 {
		t.Fatalf("expected 2 JoinPane calls, got %d", len(mt.joined))
	}

	for _, j := range mt.joined {
		if j.dst != "%1" {
			t.Errorf("expected join target %%1, got %s", j.dst)
		}
	}
}

func TestMergeSrcNotFound(t *testing.T) {
	t.Parallel()
	mt := newMock()
	mt.windows["amux:0"] = []string{"%1"}

	_, err := Merge(mt, "5", "0")
	if err == nil {
		t.Fatal("expected error for missing source window")
	}
}

func TestMergeDstNotFound(t *testing.T) {
	t.Parallel()
	mt := newMock()
	mt.windows["amux:0"] = []string{"%1"}

	_, err := Merge(mt, "0", "5")
	if err == nil {
		t.Fatal("expected error for missing destination window")
	}
}

func TestMergeNotInTmux(t *testing.T) {
	t.Parallel()
	mt := newMock()
	mt.session = ""

	_, err := Merge(mt, "1", "0")
	if err == nil {
		t.Fatal("expected error when not in tmux")
	}
	if err.Error() != "not inside a tmux session" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestMergeJoinError(t *testing.T) {
	t.Parallel()
	mt := &errorMock{
		mockTmux: mockTmux{
			session: "amux",
			windows: map[string][]string{
				"amux:0": {"%1"},
				"amux:1": {"%2"},
			},
		},
	}

	_, err := Merge(mt, "1", "0")
	if err == nil {
		t.Fatal("expected error from JoinPane failure")
	}
}

type errorMock struct {
	mockTmux
}

func (m *errorMock) JoinPane(src, dst string) error {
	return fmt.Errorf("tmux error")
}
