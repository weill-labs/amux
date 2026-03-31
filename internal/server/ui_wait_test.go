package server

import (
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
)

func TestClientConnApplyUIEventPrefixMessageCopyModeAndInputIdle(t *testing.T) {
	t.Parallel()

	cc := newClientConn(nil)

	changed, err := cc.applyUIEvent(proto.UIEventPrefixMessageShown)
	if err != nil || !changed {
		t.Fatalf("apply prefix-message-shown = (%v, %v), want (true, nil)", changed, err)
	}
	changed, err = cc.applyUIEvent(proto.UIEventPrefixMessageShown)
	if err != nil || changed {
		t.Fatalf("repeat prefix-message-shown = (%v, %v), want (false, nil)", changed, err)
	}
	if !cc.matchesUIEvent(proto.UIEventPrefixMessageShown) {
		t.Fatal("prefix-message-shown should match after apply")
	}

	changed, err = cc.applyUIEvent(proto.UIEventCopyModeShown)
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

	changed, err = cc.applyUIEvent(proto.UIEventPrefixMessageHidden)
	if err != nil || !changed {
		t.Fatalf("apply prefix-message-hidden = (%v, %v), want (true, nil)", changed, err)
	}
	changed, err = cc.applyUIEvent(proto.UIEventCopyModeHidden)
	if err != nil || !changed {
		t.Fatalf("apply copy-mode-hidden = (%v, %v), want (true, nil)", changed, err)
	}
	changed, err = cc.applyUIEvent(proto.UIEventInputIdle)
	if err != nil || !changed {
		t.Fatalf("apply input-idle = (%v, %v), want (true, nil)", changed, err)
	}
	if !cc.matchesUIEvent(proto.UIEventPrefixMessageHidden) {
		t.Fatal("prefix-message-hidden should match after hide")
	}
	if !cc.matchesUIEvent(proto.UIEventCopyModeHidden) {
		t.Fatal("copy-mode-hidden should match after hide")
	}
	if !cc.matchesUIEvent(proto.UIEventInputIdle) {
		t.Fatal("input-idle should match after idle")
	}
}

func TestClientConnCurrentUIEventsIncludesPrefixMessageBusyAndCopyModeShown(t *testing.T) {
	t.Parallel()

	cc := &clientConn{
		ID:                 "client-1",
		displayPanesShown:  true,
		prefixMessageShown: true,
		copyModeShown:      true,
		inputIdle:          false,
		chooserMode:        chooserWindow,
	}

	got := cc.currentUIEvents()
	want := []string{
		proto.UIEventDisplayPanesShown,
		proto.UIEventPrefixMessageShown,
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

func TestParseUIGenArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		args       []string
		wantClient string
		wantErr    string
	}{
		{name: "no args"},
		{name: "client", args: []string{"--client", "client-2"}, wantClient: "client-2"},
		{name: "missing client value", args: []string{"--client"}, wantErr: "missing value for --client"},
		{name: "unknown flag", args: []string{"--wat"}, wantErr: "unknown flag: --wat"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseUIGenArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseUIGenArgs(%v) error = %v, want substring %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseUIGenArgs(%v): %v", tt.args, err)
			}
			if got != tt.wantClient {
				t.Fatalf("client = %q, want %q", got, tt.wantClient)
			}
		})
	}
}

func TestParseWaitUIArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		args      []string
		wantEvent string
		wantID    string
		wantAfter uint64
		wantSet   bool
		wantDur   time.Duration
		wantErr   string
	}{
		{name: "missing event", wantErr: "usage: wait ui"},
		{name: "missing client value", args: []string{"input-idle", "--client"}, wantErr: "missing value for --client"},
		{name: "missing after value", args: []string{"input-idle", "--after"}, wantErr: "missing value for --after"},
		{name: "invalid after value", args: []string{"input-idle", "--after", "abc"}, wantErr: "invalid value for --after: abc"},
		{name: "missing timeout value", args: []string{"input-idle", "--timeout"}, wantErr: "missing value for --timeout"},
		{name: "invalid timeout", args: []string{"input-idle", "--timeout", "later"}, wantErr: "invalid value for --timeout: later"},
		{name: "unknown flag", args: []string{"input-idle", "--wat"}, wantErr: "unknown flag: --wat"},
		{name: "all flags", args: []string{"input-idle", "--client", "client-3", "--after", "9", "--timeout", "250ms"}, wantEvent: "input-idle", wantID: "client-3", wantAfter: 9, wantSet: true, wantDur: 250 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			event, clientID, afterGen, afterSet, timeout, err := parseWaitUIArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseWaitUIArgs(%v) error = %v, want substring %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseWaitUIArgs(%v): %v", tt.args, err)
			}
			if event != tt.wantEvent || clientID != tt.wantID || afterGen != tt.wantAfter || afterSet != tt.wantSet || timeout != tt.wantDur {
				t.Fatalf("parsed = (%q, %q, %d, %t, %v), want (%q, %q, %d, %t, %v)", event, clientID, afterGen, afterSet, timeout, tt.wantEvent, tt.wantID, tt.wantAfter, tt.wantSet, tt.wantDur)
			}
		})
	}
}
