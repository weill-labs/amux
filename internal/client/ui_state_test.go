package client

import (
	"testing"

	"github.com/weill-labs/amux/internal/copymode"
	"github.com/weill-labs/amux/internal/proto"
)

func TestClientUIStateReduceLayoutStructureChangeClearsTransientUI(t *testing.T) {
	t.Parallel()

	st := newClientUIState()
	st.displayPanes = &displayPanesState{}
	st.chooser = &chooserState{mode: chooserModeWindow}
	st.message = "cannot minimize"

	effects := st.reduce(uiActionHandleLayout{structureChanged: true})

	if st.displayPanes != nil {
		t.Fatal("display panes should be cleared on structural layout change")
	}
	if st.chooser != nil {
		t.Fatal("chooser should be cleared on structural layout change")
	}
	if st.message != "" {
		t.Fatalf("message = %q, want empty", st.message)
	}
	if !st.dirty {
		t.Fatal("layout change should mark state dirty")
	}
	assertUIEvents(t, effects.uiEvents, []string{
		proto.UIEventDisplayPanesHidden,
		proto.UIEventChooseWindowHidden,
		proto.UIEventPrefixMessageHidden,
	})
}

func TestClientUIStateReduceSetInputIdleEmitsOnlyOnChange(t *testing.T) {
	t.Parallel()

	st := newClientUIState()

	effects := st.reduce(uiActionSetInputIdle{idle: false})
	assertUIEvents(t, effects.uiEvents, []string{proto.UIEventInputBusy})

	effects = st.reduce(uiActionSetInputIdle{idle: false})
	assertUIEvents(t, effects.uiEvents, nil)

	effects = st.reduce(uiActionSetInputIdle{idle: true})
	assertUIEvents(t, effects.uiEvents, []string{proto.UIEventInputIdle})
}

func TestClientUIStateReduceShowChooserHidesDisplayPanesAndEmitsTransitions(t *testing.T) {
	t.Parallel()

	st := newClientUIState()
	st.displayPanes = &displayPanesState{}

	effects := st.reduce(uiActionShowChooser{
		chooser: &chooserState{mode: chooserModeWindow},
	})
	assertUIEvents(t, effects.uiEvents, []string{
		proto.UIEventDisplayPanesHidden,
		proto.UIEventChooseWindowShown,
	})

	effects = st.reduce(uiActionShowChooser{
		chooser: &chooserState{mode: chooserModeTree},
	})
	assertUIEvents(t, effects.uiEvents, []string{
		proto.UIEventChooseWindowHidden,
		proto.UIEventChooseTreeShown,
	})

	effects = st.reduce(uiActionShowChooser{
		chooser: &chooserState{mode: chooserModeTree},
	})
	assertUIEvents(t, effects.uiEvents, nil)
}

func TestClientUIStateReduceCopyModeVisibilityTransitions(t *testing.T) {
	t.Parallel()

	st := newClientUIState()

	effects := st.reduce(uiActionEnterCopyMode{
		paneID: 1,
		mode:   new(copymode.CopyMode),
	})
	assertUIEvents(t, effects.uiEvents, []string{proto.UIEventCopyModeShown})

	effects = st.reduce(uiActionEnterCopyMode{
		paneID: 2,
		mode:   new(copymode.CopyMode),
	})
	assertUIEvents(t, effects.uiEvents, nil)

	effects = st.reduce(uiActionExitCopyMode{paneID: 1})
	assertUIEvents(t, effects.uiEvents, nil)

	effects = st.reduce(uiActionExitCopyMode{paneID: 2})
	assertUIEvents(t, effects.uiEvents, []string{proto.UIEventCopyModeHidden})
}

func assertUIEvents(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("ui events = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ui events[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
