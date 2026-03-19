package server

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

func TestClientConnApplyUIEventCopyModeAndInputIdle(t *testing.T) {
	t.Parallel()

	cc := NewClientConn(nil)

	changed, err := cc.applyUIEvent(proto.UIEventCopyModeShown)
	if err != nil || !changed {
		t.Fatalf("apply copy-mode-shown = (%v, %v), want (true, nil)", changed, err)
	}
	changed, err = cc.applyUIEvent(proto.UIEventCopyModeShown)
	if err != nil || changed {
		t.Fatalf("repeat copy-mode-shown = (%v, %v), want (false, nil)", changed, err)
	}
	if !cc.matchesUIEvent(proto.UIEventCopyModeShown) {
		t.Fatal("copy-mode-shown should match after apply")
	}

	changed, err = cc.applyUIEvent(proto.UIEventInputBusy)
	if err != nil || !changed {
		t.Fatalf("apply input-busy = (%v, %v), want (true, nil)", changed, err)
	}
	changed, err = cc.applyUIEvent(proto.UIEventInputBusy)
	if err != nil || changed {
		t.Fatalf("repeat input-busy = (%v, %v), want (false, nil)", changed, err)
	}
	if !cc.matchesUIEvent(proto.UIEventInputBusy) {
		t.Fatal("input-busy should match after apply")
	}

	changed, err = cc.applyUIEvent(proto.UIEventCopyModeHidden)
	if err != nil || !changed {
		t.Fatalf("apply copy-mode-hidden = (%v, %v), want (true, nil)", changed, err)
	}
	changed, err = cc.applyUIEvent(proto.UIEventInputIdle)
	if err != nil || !changed {
		t.Fatalf("apply input-idle = (%v, %v), want (true, nil)", changed, err)
	}
	if !cc.matchesUIEvent(proto.UIEventCopyModeHidden) {
		t.Fatal("copy-mode-hidden should match after hide")
	}
	if !cc.matchesUIEvent(proto.UIEventInputIdle) {
		t.Fatal("input-idle should match after idle")
	}
}

func TestClientConnCurrentUIEventsIncludesBusyAndCopyModeShown(t *testing.T) {
	t.Parallel()

	cc := &ClientConn{
		ID:                "client-1",
		displayPanesShown: true,
		copyModeShown:     true,
		inputIdle:         false,
		chooserMode:       chooserWindow,
	}

	got := cc.currentUIEvents()
	want := []string{
		proto.UIEventDisplayPanesShown,
		proto.UIEventCopyModeShown,
		proto.UIEventInputBusy,
		proto.UIEventChooseTreeHidden,
		proto.UIEventChooseWindowShown,
	}
	if len(got) != len(want) {
		t.Fatalf("len(currentUIEvents) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Type != want[i] {
			t.Fatalf("events[%d] = %q, want %q", i, got[i].Type, want[i])
		}
	}
}

func TestParseWaitHookArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "missing event", wantErr: "usage: wait-hook"},
		{name: "missing pane value", args: []string{"on-idle", "--pane"}, wantErr: "missing value for --pane"},
		{name: "missing after value", args: []string{"on-idle", "--after"}, wantErr: "missing value for --after"},
		{name: "invalid after value", args: []string{"on-idle", "--after", "abc"}, wantErr: "invalid --after generation: abc"},
		{name: "missing timeout value", args: []string{"on-idle", "--timeout"}, wantErr: "missing value for --timeout"},
		{name: "invalid timeout value", args: []string{"on-idle", "--timeout", "later"}, wantErr: "time: invalid duration"},
		{name: "unknown flag", args: []string{"on-idle", "--wat"}, wantErr: "unknown flag: --wat"},
		{name: "invalid event", args: []string{"not-an-event"}, wantErr: `unknown hook event: "not-an-event"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, _, _, err := parseWaitHookArgs(tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("parseWaitHookArgs(%v) error = %v, want substring %q", tt.args, err, tt.wantErr)
			}
		})
	}

	eventName, paneName, afterGen, timeout, err := parseWaitHookArgs([]string{"on-idle", "--pane", "1", "--after", "7", "--timeout", "250ms"})
	if err != nil {
		t.Fatalf("parseWaitHookArgs success case: %v", err)
	}
	if eventName != "on-idle" || paneName != "1" || afterGen != 7 || timeout != 250*time.Millisecond {
		t.Fatalf("parsed = (%q, %q, %d, %v), want (%q, %q, %d, %v)", eventName, paneName, afterGen, timeout, "on-idle", "1", 7, 250*time.Millisecond)
	}
}

func TestResolveWaitHookPaneName(t *testing.T) {
	t.Parallel()

	sess := newSession("test-resolve-wait-hook-pane")
	stopCrashCheckpointLoop(t, sess)

	pane := mux.NewProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: "f5e0dc",
	}, 80, 23, nil, nil, func(data []byte) (int, error) { return len(data), nil })
	w := mux.NewWindow(pane, 80, 23)
	w.ID = 1
	w.Name = "window-1"
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{pane}

	ctx := &CommandContext{CC: NewClientConn(nil), Sess: sess}

	name, err := resolveWaitHookPaneName(ctx, "")
	if err != nil || name != "" {
		t.Fatalf("resolve empty ref = (%q, %v), want (\"\", nil)", name, err)
	}

	name, err = resolveWaitHookPaneName(ctx, "1")
	if err != nil {
		t.Fatalf("resolve numeric ref: %v", err)
	}
	if name != "pane-1" {
		t.Fatalf("resolve numeric ref = %q, want %q", name, "pane-1")
	}
}

func TestWaitHookTimeout(t *testing.T) {
	t.Parallel()

	sess := &Session{}
	sess.hookCond = sync.NewCond(&sess.hookMu)

	if _, ok := sess.waitHook(0, "on-idle", "pane-1", 20*time.Millisecond); ok {
		t.Fatal("waitHook should time out when no matching hook arrives")
	}
}

func TestHookResultEventTrimsHistoryAndEmitsHookEvent(t *testing.T) {
	t.Parallel()

	sess := newSession("test-hook-result-event")
	stopSessionBackgroundLoops(t, sess)
	sess.hookCond = sync.NewCond(&sess.hookMu)

	for i := 0; i < 128; i++ {
		sess.hookResults = append(sess.hookResults, hookResultRecord{Generation: uint64(i + 1)})
	}
	sub := &eventSub{ch: make(chan []byte, 1)}
	sess.eventSubs = []*eventSub{sub}

	hookResultEvent{
		record: hookResultRecord{
			Event:    "on-idle",
			PaneID:   1,
			PaneName: "pane-1",
			Host:     "local",
			Command:  "true",
			Success:  true,
		},
	}.handle(sess)

	if len(sess.hookResults) != 128 {
		t.Fatalf("len(hookResults) = %d, want 128", len(sess.hookResults))
	}
	if sess.hookResults[0].Generation != 2 {
		t.Fatalf("first retained generation = %d, want 2", sess.hookResults[0].Generation)
	}
	select {
	case data := <-sub.ch:
		var ev Event
		if err := json.Unmarshal(data, &ev); err != nil {
			t.Fatalf("json.Unmarshal hook event: %v", err)
		}
		if ev.Type != EventHook || !ev.Success || ev.HookEvent != "on-idle" {
			t.Fatalf("event = %+v, want hook success for on-idle", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for emitted hook event")
	}
}
