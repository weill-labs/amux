//go:build !race

package server

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
)

func setupWaitReadyTestPane(t *testing.T, writeOverride func([]byte) (int, error)) (*Server, *Session, *mux.Pane, func()) {
	t.Helper()

	srv, sess, cleanup := newCommandTestSession(t)
	if writeOverride == nil {
		writeOverride = func(data []byte) (int, error) { return len(data), nil }
	}
	pane := newProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: config.CatppuccinMocha[0],
	}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), writeOverride)
	w := newTestWindowWithPanes(t, sess, 1, "main", pane)
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{pane}
	return srv, sess, pane, cleanup
}

func codexReadyScreen(placeholder string) string {
	return "\x1b[?25h\x1b[2J\x1b[H" + strings.Repeat("\r\n", 19) + "› " + placeholder
}

func codexTrustDialogScreen(path string) string {
	return "\x1b[2J\x1b[H> You are in " + path + "\r\n\r\n" +
		"  Do you trust the contents of this directory? Working with untrusted contents\r\n" +
		"  comes with higher risk of prompt injection.\r\n\r\n" +
		"› 1. Yes, continue\r\n" +
		"  2. No, quit\r\n\r\n" +
		"  Press enter to continue\x1b[?25l"
}

func claudePromptScreen() string {
	return "\x1b[2J\x1b[H" +
		strings.Repeat("\r\n", 6) +
		"❯ \x1b[7m \x1b[m" +
		"\x1b[?25l" +
		"\x1b[11;1H"
}

func numberedMenuScreen() string {
	return "\x1b[2J\x1b[H" + strings.Repeat("\r\n", 5) + "› 1. Yes, continue"
}

func TestWaitReadyUsage(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	res := runTestCommand(t, srv, sess, "wait-ready")
	if got := res.cmdErr; got != "usage: wait-ready <pane> [--timeout <duration>] [--continue-known-dialogs]" {
		t.Fatalf("wait-ready usage error = %q", got)
	}
}

func TestParseWaitReadyArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		wantPane string
		wantOpts waitReadyOptions
		wantErr  string
	}{
		{
			name:     "defaults",
			args:     []string{"pane-1"},
			wantPane: "pane-1",
			wantOpts: waitReadyOptions{timeout: 10 * time.Second},
		},
		{
			name:     "custom timeout and continue",
			args:     []string{"pane-2", "--continue-known-dialogs", "--timeout", "25ms"},
			wantPane: "pane-2",
			wantOpts: waitReadyOptions{timeout: 25 * time.Millisecond, continueKnownDialogs: true},
		},
		{
			name:    "missing timeout value",
			args:    []string{"pane-1", "--timeout"},
			wantErr: "missing value for --timeout",
		},
		{
			name:    "invalid timeout",
			args:    []string{"pane-1", "--timeout", "later"},
			wantErr: "invalid timeout: later",
		},
		{
			name:    "unknown flag",
			args:    []string{"pane-1", "--bogus"},
			wantErr: "unknown flag: --bogus",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotPane, gotOpts, err := parseWaitReadyArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("parseWaitReadyArgs(%v) error = %v, want %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseWaitReadyArgs(%v) error = %v", tt.args, err)
			}
			if gotPane != tt.wantPane {
				t.Fatalf("pane = %q, want %q", gotPane, tt.wantPane)
			}
			if gotOpts != tt.wantOpts {
				t.Fatalf("opts = %#v, want %#v", gotOpts, tt.wantOpts)
			}
		})
	}
}

func TestParseSendKeysArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		want    sendKeysOptions
		wantErr string
	}{
		{
			name: "wait ready and keys",
			args: []string{"--wait-ready", "--continue-known-dialogs", "--hex", "6869", "Enter"},
			want: sendKeysOptions{
				waitReady:            true,
				continueKnownDialogs: true,
				hexMode:              true,
				keys:                 []string{"6869", "Enter"},
			},
		},
		{
			name: "literal args after first key",
			args: []string{"task", "--wait-ready"},
			want: sendKeysOptions{
				keys: []string{"task", "--wait-ready"},
			},
		},
		{
			name:    "continue requires wait ready",
			args:    []string{"--continue-known-dialogs", "task"},
			wantErr: "send-keys: --continue-known-dialogs requires --wait-ready",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseSendKeysArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("parseSendKeysArgs(%v) error = %v, want %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSendKeysArgs(%v) error = %v", tt.args, err)
			}
			if got.waitReady != tt.want.waitReady ||
				got.continueKnownDialogs != tt.want.continueKnownDialogs ||
				got.hexMode != tt.want.hexMode ||
				strings.Join(got.keys, "|") != strings.Join(tt.want.keys, "|") {
				t.Fatalf("opts = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestWaitReadyMatchesCodexPrompt(t *testing.T) {
	t.Parallel()

	srv, sess, pane, cleanup := setupWaitReadyTestPane(t, nil)
	defer cleanup()

	pane.FeedOutput([]byte(codexReadyScreen("Implement {feature}")))

	res := runTestCommand(t, srv, sess, "wait-ready", "pane-1", "--timeout", "10ms")
	if res.cmdErr != "" || strings.TrimSpace(res.output) != "ready" {
		t.Fatalf("wait-ready result = %#v", res)
	}
}

func TestWaitReadyBlocksOnCodexTrustDialog(t *testing.T) {
	t.Parallel()

	srv, sess, pane, cleanup := setupWaitReadyTestPane(t, nil)
	defer cleanup()

	pane.FeedOutput([]byte(codexTrustDialogScreen("/tmp/untrusted")))

	res := runTestCommand(t, srv, sess, "wait-ready", "pane-1", "--timeout", "10ms")
	if !strings.Contains(res.cmdErr, "Codex trust dialog is blocking input in pane-1") {
		t.Fatalf("wait-ready dialog error = %#v", res)
	}
}

func TestWaitReadyInspectMissingPane(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	if _, err := inspectPaneReadiness(sess, 999); err == nil || err.Error() != "pane missing" {
		t.Fatalf("inspectPaneReadiness missing pane error = %v, want pane missing", err)
	}
}

func TestWaitForPaneReadyReturnsSessionShuttingDown(t *testing.T) {
	t.Parallel()

	sess := &Session{
		sessionEventStop: make(chan struct{}),
		sessionEventDone: make(chan struct{}),
	}
	close(sess.sessionEventStop)

	err := waitForPaneReady(sess, "pane-1", resolvedPaneRef{}, waitReadyOptions{timeout: time.Millisecond})
	if err == nil || err.Error() != "session shutting down" {
		t.Fatalf("waitForPaneReady session shutdown error = %v, want session shutting down", err)
	}
}

func TestWaitReadyCanContinueKnownCodexDialog(t *testing.T) {
	t.Parallel()

	var (
		pane   *mux.Pane
		mu     sync.Mutex
		writes []string
	)

	writeOverride := func(data []byte) (int, error) {
		mu.Lock()
		writes = append(writes, string(data))
		mu.Unlock()
		if string(data) == "\r" {
			pane.FeedOutput([]byte(codexReadyScreen("Implement {feature}")))
		}
		return len(data), nil
	}

	srv, sess, createdPane, cleanup := setupWaitReadyTestPane(t, writeOverride)
	defer cleanup()
	pane = createdPane
	pane.FeedOutput([]byte(codexTrustDialogScreen("/tmp/untrusted")))

	res := runTestCommand(t, srv, sess, "wait-ready", "pane-1", "--continue-known-dialogs", "--timeout", "50ms")
	if res.cmdErr != "" || strings.TrimSpace(res.output) != "ready" {
		t.Fatalf("wait-ready result = %#v", res)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(writes) != 1 || writes[0] != "\r" {
		t.Fatalf("dialog writes = %#v, want single Enter", writes)
	}
}

func TestWaitReadyRejectsUnknownContinueFlagCombination(t *testing.T) {
	t.Parallel()

	srv, sess, pane, cleanup := setupWaitReadyTestPane(t, nil)
	defer cleanup()

	pane.FeedOutput([]byte(codexReadyScreen("Summarize recent commits")))

	res := runTestCommand(t, srv, sess, "send-keys", "pane-1", "--continue-known-dialogs", "ship it")
	if got := res.cmdErr; got != "send-keys: --continue-known-dialogs requires --wait-ready" {
		t.Fatalf("send-keys flag error = %q", got)
	}
}

func TestSendKeysWaitReadyUsage(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	res := runTestCommand(t, srv, sess, "send-keys", "pane-1")
	if got := res.cmdErr; got != "usage: send-keys <pane> [--wait-ready] [--continue-known-dialogs] [--hex] <keys>..." {
		t.Fatalf("send-keys usage error = %q", got)
	}
}

func TestSendKeysWaitReadyMissingPane(t *testing.T) {
	t.Parallel()

	srv, sess, _, cleanup := setupWaitReadyTestPane(t, nil)
	defer cleanup()

	res := runTestCommand(t, srv, sess, "send-keys", "missing", "--wait-ready", "ship it")
	if !strings.Contains(res.cmdErr, "not found") {
		t.Fatalf("send-keys missing pane error = %q", res.cmdErr)
	}
}

func TestWaitReadyUsesClaudeCursorBlockFallback(t *testing.T) {
	t.Parallel()

	srv, sess, pane, cleanup := setupWaitReadyTestPane(t, nil)
	defer cleanup()

	pane.FeedOutput([]byte(claudePromptScreen()))

	res := runTestCommand(t, srv, sess, "wait-ready", "pane-1", "--timeout", "10ms")
	if res.cmdErr != "" || strings.TrimSpace(res.output) != "ready" {
		t.Fatalf("wait-ready Claude result = %#v", res)
	}
}

func TestClassifyPaneReadinessPendingCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		snap mux.CaptureSnapshot
	}{
		{
			name: "cursor hidden with no visible prompt cursor",
			snap: mux.CaptureSnapshot{
				Content:      []string{"", "> ready"},
				CursorHidden: true,
			},
		},
		{
			name: "cursor row outside screen",
			snap: mux.CaptureSnapshot{
				Content:      []string{"> ready"},
				CursorRow:    5,
				CursorHidden: false,
			},
		},
		{
			name: "cursor row is not a prompt",
			snap: mux.CaptureSnapshot{
				Content:      []string{"working"},
				CursorRow:    0,
				CursorHidden: false,
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := classifyPaneReadiness(tt.snap); got.state != paneReadinessPending {
				t.Fatalf("classifyPaneReadiness() = %#v, want pending", got)
			}
		})
	}
}

func TestCodexTrustDialogClassifier(t *testing.T) {
	t.Parallel()

	if !isCodexTrustDialog([]string{codexTrustDialogQuestion, codexTrustDialogWarning}) {
		t.Fatal("isCodexTrustDialog should match known question + warning")
	}
	if isCodexTrustDialog([]string{codexTrustDialogQuestion}) {
		t.Fatal("isCodexTrustDialog should reject partial matches")
	}
}

func TestReadinessCursorRow(t *testing.T) {
	t.Parallel()

	if row, ok := readinessCursorRow(mux.CaptureSnapshot{CursorHidden: false, CursorRow: 7}); !ok || row != 7 {
		t.Fatalf("visible cursor row = (%d,%t), want (7,true)", row, ok)
	}
	if row, ok := readinessCursorRow(mux.CaptureSnapshot{CursorHidden: true, HasCursorBlock: true, CursorBlockRow: 3}); !ok || row != 3 {
		t.Fatalf("cursor block row = (%d,%t), want (3,true)", row, ok)
	}
	if _, ok := readinessCursorRow(mux.CaptureSnapshot{CursorHidden: true}); ok {
		t.Fatal("readinessCursorRow should reject hidden cursor without cursor block")
	}
}

func TestReadyPromptHelpers(t *testing.T) {
	t.Parallel()

	for _, line := range []string{">", "› task", "❯"} {
		if !isReadyPromptLine(line) {
			t.Fatalf("isReadyPromptLine(%q) = false, want true", line)
		}
	}
	for _, line := range []string{"", "› 1. Yes, continue", "> 2. No"} {
		if isReadyPromptLine(line) {
			t.Fatalf("isReadyPromptLine(%q) = true, want false", line)
		}
	}
	if !isNumberedPromptOption("❯ 3. Continue") {
		t.Fatal("isNumberedPromptOption should match numbered prompt options")
	}
	if isNumberedPromptOption("❯ continue") {
		t.Fatal("isNumberedPromptOption should reject non-numbered prompt text")
	}
}

func TestWaitReadyRejectsNumberedMenuRows(t *testing.T) {
	t.Parallel()

	srv, sess, pane, cleanup := setupWaitReadyTestPane(t, nil)
	defer cleanup()

	pane.FeedOutput([]byte(numberedMenuScreen()))

	res := runTestCommand(t, srv, sess, "wait-ready", "pane-1", "--timeout", "1ms")
	if !strings.Contains(res.cmdErr, "timeout waiting for pane-1 to become ready") {
		t.Fatalf("wait-ready timeout = %#v", res)
	}
}

func TestSendKeysWaitReadyContinuesKnownDialogBeforeSendingInput(t *testing.T) {
	t.Parallel()

	var (
		pane   *mux.Pane
		mu     sync.Mutex
		writes []string
	)

	writeOverride := func(data []byte) (int, error) {
		mu.Lock()
		writes = append(writes, string(data))
		mu.Unlock()
		if string(data) == "\r" && len(writes) == 1 {
			pane.FeedOutput([]byte(codexReadyScreen("Summarize recent commits")))
		}
		return len(data), nil
	}

	srv, sess, createdPane, cleanup := setupWaitReadyTestPane(t, writeOverride)
	defer cleanup()
	pane = createdPane
	pane.FeedOutput([]byte(codexTrustDialogScreen("/tmp/untrusted")))

	res := runTestCommand(t, srv, sess, "send-keys", "pane-1", "--wait-ready", "--continue-known-dialogs", "ship it", "Enter")
	if res.cmdErr != "" || strings.TrimSpace(res.output) != "Sent 8 bytes to pane-1" {
		t.Fatalf("send-keys result = %#v", res)
	}

	mu.Lock()
	defer mu.Unlock()
	if got := strings.Join(writes, "|"); got != "\r|ship it|\r" {
		t.Fatalf("send-keys writes = %q, want dialog Enter + task + Enter", got)
	}
}
