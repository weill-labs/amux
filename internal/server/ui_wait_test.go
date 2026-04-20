package server

import (
	"testing"

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
