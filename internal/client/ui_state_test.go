package client

import (
	"reflect"
	"sort"
	"testing"

	"github.com/weill-labs/amux/internal/bubblesutil"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/copymode"
	"github.com/weill-labs/amux/internal/proto"
)

type clientUIStateSnapshot struct {
	dirty           bool
	message         string
	displayPanes    bool
	chooser         string
	prompt          string
	copyModePaneIDs []uint32
	inputIdle       bool
}

func snapshotClientUIState(st clientUIState) clientUIStateSnapshot {
	paneIDs := make([]uint32, 0, len(st.copyModes))
	for paneID := range st.copyModes {
		paneIDs = append(paneIDs, paneID)
	}
	sort.Slice(paneIDs, func(i, j int) bool { return paneIDs[i] < paneIDs[j] })

	chooser := ""
	if st.chooser != nil {
		chooser = string(st.chooser.mode)
	}
	prompt := ""
	if st.windowRenamePrompt != nil {
		prompt = st.windowRenamePrompt.title()
	} else if st.helpOverlay != nil {
		prompt = st.helpOverlay.title()
	}

	return clientUIStateSnapshot{
		dirty:           st.dirty,
		message:         st.message,
		displayPanes:    st.displayPanes != nil,
		chooser:         chooser,
		prompt:          prompt,
		copyModePaneIDs: paneIDs,
		inputIdle:       st.inputIdle,
	}
}

func assertClientUIState(t *testing.T, st clientUIState, want clientUIStateSnapshot) {
	t.Helper()

	got := snapshotClientUIState(st)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("state = %+v, want %+v", got, want)
	}
}

func TestClientUIStateReduceTransitions(t *testing.T) {
	t.Parallel()

	existingCopyMode := new(copymode.CopyMode)

	tests := []struct {
		name       string
		setup      func(*clientUIState)
		action     any
		wantState  clientUIStateSnapshot
		wantEvents []string
		assert     func(*testing.T, *clientUIState)
	}{
		{
			name: "structural layout change clears transient UI",
			setup: func(st *clientUIState) {
				st.displayPanes = &displayPanesState{}
				st.chooser = &chooserState{mode: chooserModeWindow}
				st.windowRenamePrompt = &windowRenamePromptState{input: bubblesutil.TextInputState{Value: "logs", Cursor: 4}}
				st.helpOverlay = buildHelpOverlay(config.DefaultKeybindings())
				st.message = "command failed"
			},
			action: uiActionHandleLayout{structureChanged: true},
			wantState: clientUIStateSnapshot{
				dirty:           true,
				copyModePaneIDs: []uint32{},
				inputIdle:       true,
			},
			wantEvents: []string{
				proto.UIEventDisplayPanesHidden,
				proto.UIEventChooseWindowHidden,
				proto.UIEventPrefixMessageHidden,
			},
		},
		{
			name: "non-structural layout change preserves overlays and message",
			setup: func(st *clientUIState) {
				st.displayPanes = &displayPanesState{}
				st.message = "command failed"
			},
			action: uiActionHandleLayout{structureChanged: false},
			wantState: clientUIStateSnapshot{
				dirty:           true,
				displayPanes:    true,
				message:         "command failed",
				copyModePaneIDs: []uint32{},
				inputIdle:       true,
			},
		},
		{
			name: "non-structural layout change preserves chooser and input state",
			setup: func(st *clientUIState) {
				st.chooser = &chooserState{mode: chooserModeTree}
				st.message = "command failed"
				st.inputIdle = false
			},
			action: uiActionHandleLayout{structureChanged: false},
			wantState: clientUIStateSnapshot{
				dirty:           true,
				chooser:         string(chooserModeTree),
				message:         "command failed",
				copyModePaneIDs: []uint32{},
				inputIdle:       false,
			},
		},
		{
			name: "layout update without transient UI only marks dirty",
			setup: func(st *clientUIState) {
				st.inputIdle = false
			},
			action: uiActionHandleLayout{structureChanged: false},
			wantState: clientUIStateSnapshot{
				dirty:           true,
				copyModePaneIDs: []uint32{},
				inputIdle:       false,
			},
		},
		{
			name: "pane output preserves message and marks dirty",
			setup: func(st *clientUIState) {
				st.message = "No binding for C-a f"
			},
			action: uiActionPaneOutput{},
			wantState: clientUIStateSnapshot{
				dirty:           true,
				message:         "No binding for C-a f",
				copyModePaneIDs: []uint32{},
				inputIdle:       true,
			},
		},
		{
			name:   "set message stores text and marks dirty",
			action: uiActionSetMessage{message: "command failed"},
			wantState: clientUIStateSnapshot{
				dirty:           true,
				message:         "command failed",
				copyModePaneIDs: []uint32{},
				inputIdle:       true,
			},
			wantEvents: []string{proto.UIEventPrefixMessageShown},
		},
		{
			name: "setting a new visible message replaces text without re-emitting shown",
			setup: func(st *clientUIState) {
				st.message = "No binding for C-a f"
			},
			action: uiActionSetMessage{message: "No binding for C-a g"},
			wantState: clientUIStateSnapshot{
				dirty:           true,
				message:         "No binding for C-a g",
				copyModePaneIDs: []uint32{},
				inputIdle:       true,
			},
		},
		{
			name: "setting message empty hides prefix state",
			setup: func(st *clientUIState) {
				st.message = "command failed"
			},
			action: uiActionSetMessage{message: ""},
			wantState: clientUIStateSnapshot{
				dirty:           true,
				copyModePaneIDs: []uint32{},
				inputIdle:       true,
			},
			wantEvents: []string{proto.UIEventPrefixMessageHidden},
		},
		{
			name: "clear message removes text and marks dirty",
			setup: func(st *clientUIState) {
				st.message = "command failed"
			},
			action: uiActionClearMessage{},
			wantState: clientUIStateSnapshot{
				dirty:           true,
				copyModePaneIDs: []uint32{},
				inputIdle:       true,
			},
			wantEvents: []string{proto.UIEventPrefixMessageHidden},
		},
		{
			name:   "clear message is a no-op when already empty",
			action: uiActionClearMessage{},
			wantState: clientUIStateSnapshot{
				copyModePaneIDs: []uint32{},
				inputIdle:       true,
			},
		},
		{
			name:   "show display panes emits shown once",
			action: uiActionShowDisplayPanes{displayPanes: &displayPanesState{}},
			wantState: clientUIStateSnapshot{
				dirty:           true,
				displayPanes:    true,
				copyModePaneIDs: []uint32{},
				inputIdle:       true,
			},
			wantEvents: []string{proto.UIEventDisplayPanesShown},
		},
		{
			name: "hide display panes emits hidden",
			setup: func(st *clientUIState) {
				st.displayPanes = &displayPanesState{}
			},
			action: uiActionHideDisplayPanes{},
			wantState: clientUIStateSnapshot{
				dirty:           true,
				copyModePaneIDs: []uint32{},
				inputIdle:       true,
			},
			wantEvents: []string{proto.UIEventDisplayPanesHidden},
		},
		{
			name:   "hide display panes is a no-op when already hidden",
			action: uiActionHideDisplayPanes{},
			wantState: clientUIStateSnapshot{
				copyModePaneIDs: []uint32{},
				inputIdle:       true,
			},
		},
		{
			name: "show chooser hides display panes and emits transitions",
			setup: func(st *clientUIState) {
				st.displayPanes = &displayPanesState{}
			},
			action: uiActionShowChooser{chooser: &chooserState{mode: chooserModeWindow}},
			wantState: clientUIStateSnapshot{
				dirty:           true,
				chooser:         string(chooserModeWindow),
				copyModePaneIDs: []uint32{},
				inputIdle:       true,
			},
			wantEvents: []string{
				proto.UIEventDisplayPanesHidden,
				proto.UIEventChooseWindowShown,
			},
		},
		{
			name: "show window rename prompt hides chooser and display panes",
			setup: func(st *clientUIState) {
				st.displayPanes = &displayPanesState{}
				st.chooser = &chooserState{mode: chooserModeWindow}
			},
			action: uiActionShowWindowRenamePrompt{
				prompt: &windowRenamePromptState{input: bubblesutil.TextInputState{Value: "logs", Cursor: 4}},
			},
			wantState: clientUIStateSnapshot{
				dirty:           true,
				prompt:          "rename-window",
				copyModePaneIDs: []uint32{},
				inputIdle:       true,
			},
			wantEvents: []string{
				proto.UIEventDisplayPanesHidden,
				proto.UIEventChooseWindowHidden,
			},
		},
		{
			name: "show help overlay hides chooser display panes and prompt",
			setup: func(st *clientUIState) {
				st.displayPanes = &displayPanesState{}
				st.chooser = &chooserState{mode: chooserModeWindow}
				st.windowRenamePrompt = &windowRenamePromptState{input: bubblesutil.TextInputState{Value: "logs", Cursor: 4}}
			},
			action: uiActionShowHelpOverlay{
				overlay: buildHelpOverlay(config.DefaultKeybindings()),
			},
			wantState: clientUIStateSnapshot{
				dirty:           true,
				prompt:          helpOverlayTitle,
				copyModePaneIDs: []uint32{},
				inputIdle:       true,
			},
			wantEvents: []string{
				proto.UIEventDisplayPanesHidden,
				proto.UIEventChooseWindowHidden,
			},
		},
		{
			name: "hide help overlay clears prompt state",
			setup: func(st *clientUIState) {
				st.helpOverlay = buildHelpOverlay(config.DefaultKeybindings())
			},
			action: uiActionHideHelpOverlay{},
			wantState: clientUIStateSnapshot{
				dirty:           true,
				copyModePaneIDs: []uint32{},
				inputIdle:       true,
			},
		},
		{
			name: "hide window rename prompt clears prompt state",
			setup: func(st *clientUIState) {
				st.windowRenamePrompt = &windowRenamePromptState{input: bubblesutil.TextInputState{Value: "logs", Cursor: 4}}
			},
			action: uiActionHideWindowRenamePrompt{},
			wantState: clientUIStateSnapshot{
				dirty:           true,
				copyModePaneIDs: []uint32{},
				inputIdle:       true,
			},
		},
		{
			name: "switch chooser modes emits hide then show",
			setup: func(st *clientUIState) {
				st.chooser = &chooserState{mode: chooserModeWindow}
			},
			action: uiActionShowChooser{chooser: &chooserState{mode: chooserModeTree}},
			wantState: clientUIStateSnapshot{
				dirty:           true,
				chooser:         string(chooserModeTree),
				copyModePaneIDs: []uint32{},
				inputIdle:       true,
			},
			wantEvents: []string{
				proto.UIEventChooseWindowHidden,
				proto.UIEventChooseTreeShown,
			},
		},
		{
			name: "re-showing same chooser mode stays silent but dirty",
			setup: func(st *clientUIState) {
				st.chooser = &chooserState{mode: chooserModeTree}
			},
			action: uiActionShowChooser{chooser: &chooserState{mode: chooserModeTree}},
			wantState: clientUIStateSnapshot{
				dirty:           true,
				chooser:         string(chooserModeTree),
				copyModePaneIDs: []uint32{},
				inputIdle:       true,
			},
		},
		{
			name: "hide chooser emits hidden event",
			setup: func(st *clientUIState) {
				st.chooser = &chooserState{mode: chooserModeWindow}
			},
			action: uiActionHideChooser{},
			wantState: clientUIStateSnapshot{
				dirty:           true,
				copyModePaneIDs: []uint32{},
				inputIdle:       true,
			},
			wantEvents: []string{proto.UIEventChooseWindowHidden},
		},
		{
			name:   "hide chooser is a no-op when already hidden",
			action: uiActionHideChooser{},
			wantState: clientUIStateSnapshot{
				copyModePaneIDs: []uint32{},
				inputIdle:       true,
			},
		},
		{
			name: "first copy mode enters active state",
			action: uiActionEnterCopyMode{
				paneID: 1,
				mode:   new(copymode.CopyMode),
			},
			wantState: clientUIStateSnapshot{
				dirty:           true,
				copyModePaneIDs: []uint32{1},
				inputIdle:       true,
			},
			wantEvents: []string{proto.UIEventCopyModeShown},
		},
		{
			name: "additional copy-mode pane keeps visibility active without event",
			setup: func(st *clientUIState) {
				st.copyModes[1] = new(copymode.CopyMode)
			},
			action: uiActionEnterCopyMode{
				paneID: 2,
				mode:   new(copymode.CopyMode),
			},
			wantState: clientUIStateSnapshot{
				dirty:           true,
				copyModePaneIDs: []uint32{1, 2},
				inputIdle:       true,
			},
		},
		{
			name: "re-entering copy mode for the same pane is a no-op",
			setup: func(st *clientUIState) {
				st.copyModes[1] = existingCopyMode
			},
			action: uiActionEnterCopyMode{
				paneID: 1,
				mode:   new(copymode.CopyMode),
			},
			wantState: clientUIStateSnapshot{
				copyModePaneIDs: []uint32{1},
				inputIdle:       true,
			},
			assert: func(t *testing.T, st *clientUIState) {
				t.Helper()
				if st.copyModes[1] != existingCopyMode {
					t.Fatal("re-entering copy mode should keep the existing copy-mode state")
				}
			},
		},
		{
			name: "exiting one of multiple copy-mode panes stays visible",
			setup: func(st *clientUIState) {
				st.copyModes[1] = new(copymode.CopyMode)
				st.copyModes[2] = new(copymode.CopyMode)
			},
			action: uiActionExitCopyMode{paneID: 1},
			wantState: clientUIStateSnapshot{
				dirty:           true,
				copyModePaneIDs: []uint32{2},
				inputIdle:       true,
			},
		},
		{
			name: "exiting last copy-mode pane hides active state",
			setup: func(st *clientUIState) {
				st.copyModes[2] = new(copymode.CopyMode)
			},
			action: uiActionExitCopyMode{paneID: 2},
			wantState: clientUIStateSnapshot{
				dirty:           true,
				copyModePaneIDs: []uint32{},
				inputIdle:       true,
			},
			wantEvents: []string{proto.UIEventCopyModeHidden},
		},
		{
			name:   "input busy emits event only on change",
			action: uiActionSetInputIdle{idle: false},
			wantState: clientUIStateSnapshot{
				copyModePaneIDs: []uint32{},
				inputIdle:       false,
			},
			wantEvents: []string{proto.UIEventInputBusy},
		},
		{
			name: "setting input busy again is a no-op",
			setup: func(st *clientUIState) {
				st.inputIdle = false
			},
			action: uiActionSetInputIdle{idle: false},
			wantState: clientUIStateSnapshot{
				copyModePaneIDs: []uint32{},
				inputIdle:       false,
			},
		},
		{
			name: "returning to input idle emits idle event",
			setup: func(st *clientUIState) {
				st.inputIdle = false
			},
			action: uiActionSetInputIdle{idle: true},
			wantState: clientUIStateSnapshot{
				copyModePaneIDs: []uint32{},
				inputIdle:       true,
			},
			wantEvents: []string{proto.UIEventInputIdle},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			st := newClientUIState()
			if tt.setup != nil {
				tt.setup(&st)
			}

			result := st.reduce(tt.action)

			assertClientUIState(t, st, tt.wantState)
			assertUIEvents(t, result.uiEvents, tt.wantEvents)
			if tt.assert != nil {
				tt.assert(t, &st)
			}
		})
	}
}

func TestClientUIStateDirtyLifecycle(t *testing.T) {
	t.Parallel()

	st := newClientUIState()
	assertClientUIState(t, st, clientUIStateSnapshot{
		copyModePaneIDs: []uint32{},
		inputIdle:       true,
	})

	st.reduce(uiActionSetMessage{message: "command failed"})
	if !st.dirty {
		t.Fatal("set message should mark state dirty")
	}

	st.markRendered()
	if st.dirty {
		t.Fatal("markRendered should clear dirty state")
	}

	st.reduce(uiActionPaneOutput{})
	if !st.dirty {
		t.Fatal("pane output should mark state dirty")
	}

	st.markRendered()
	if st.dirty {
		t.Fatal("markRendered should clear dirty state after pane output")
	}
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
