package client

import "testing"

func queueBlockingLocalAction(msgCh chan<- *RenderMsg) (started <-chan struct{}, release chan<- struct{}) {
	startedCh := make(chan struct{})
	releaseCh := make(chan struct{})
	msgCh <- &RenderMsg{
		Typ: RenderMsgLocalAction,
		Local: func(*ClientRenderer) localRenderResult {
			close(startedCh)
			<-releaseCh
			return localRenderResult{}
		},
	}
	return startedCh, releaseCh
}

func TestToggleDisplayPanesOnRenderLoopWaitsForQueuedLayout(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(80, 24)
	msgCh := startTestRenderLoop(t, cr)

	started, release := queueBlockingLocalAction(msgCh)
	<-started

	msgCh <- &RenderMsg{Typ: RenderMsgLayout, Layout: twoPane80x23()}

	resultCh := make(chan bool, 1)
	go func() {
		resultCh <- toggleDisplayPanesOnRenderLoop(cr, msgCh)
	}()

	close(release)

	if ok := <-resultCh; !ok {
		t.Fatal("toggleDisplayPanesOnRenderLoop should succeed after queued layout")
	}
	if !cr.DisplayPanesActive() {
		t.Fatal("display-panes overlay should be active after queued layout")
	}
}

func TestToggleDisplayPanesOnRenderLoopHidesActiveOverlay(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	msgCh := startTestRenderLoop(t, cr)

	if !toggleDisplayPanesOnRenderLoop(cr, msgCh) {
		t.Fatal("toggleDisplayPanesOnRenderLoop should show display panes")
	}
	if !cr.DisplayPanesActive() {
		t.Fatal("display-panes overlay should be active after first toggle")
	}

	if !toggleDisplayPanesOnRenderLoop(cr, msgCh) {
		t.Fatal("toggleDisplayPanesOnRenderLoop should hide an active overlay")
	}
	if cr.DisplayPanesActive() {
		t.Fatal("display-panes overlay should be inactive after second toggle")
	}
}

func TestToggleDisplayPanesOnRenderLoopReturnsFalseWithoutLayout(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(80, 24)
	msgCh := startTestRenderLoop(t, cr)

	if toggleDisplayPanesOnRenderLoop(cr, msgCh) {
		t.Fatal("toggleDisplayPanesOnRenderLoop should fail before layout is ready")
	}
	if cr.DisplayPanesActive() {
		t.Fatal("display-panes overlay should stay inactive without layout")
	}
}

func TestShowChooserOnRenderLoopWaitsForQueuedLayout(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(80, 24)
	msgCh := startTestRenderLoop(t, cr)

	started, release := queueBlockingLocalAction(msgCh)
	<-started

	msgCh <- &RenderMsg{Typ: RenderMsgLayout, Layout: multiWindow80x23()}

	resultCh := make(chan bool, 1)
	go func() {
		resultCh <- showChooserOnRenderLoop(cr, msgCh, chooserModeWindow)
	}()

	close(release)

	if ok := <-resultCh; !ok {
		t.Fatal("showChooserOnRenderLoop should succeed after queued layout")
	}
	if !cr.ChooserActive() {
		t.Fatal("chooser overlay should be active after queued layout")
	}
}

func TestShowChooserOnRenderLoopReturnsFalseWithoutLayout(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(80, 24)
	msgCh := startTestRenderLoop(t, cr)

	if showChooserOnRenderLoop(cr, msgCh, chooserModeWindow) {
		t.Fatal("showChooserOnRenderLoop should fail before layout is ready")
	}
	if cr.ChooserActive() {
		t.Fatal("chooser overlay should stay inactive without layout")
	}
}

func TestHandleChooserInputOnRenderLoopSelectsFilteredWindow(t *testing.T) {
	t.Parallel()

	cr := buildMultiWindowRendererAt(t, 1)
	msgCh := startTestRenderLoop(t, cr)

	if !showChooserOnRenderLoop(cr, msgCh, chooserModeWindow) {
		t.Fatal("showChooserOnRenderLoop should succeed")
	}

	result := handleChooserInputOnRenderLoop(cr, msgCh, []byte("logs\r"))
	if !result.handled {
		t.Fatal("chooser input should be handled while chooser is active")
	}
	if result.action.command != "select-window" {
		t.Fatalf("chooser command = %q, want select-window", result.action.command)
	}
	// buildMultiWindowRendererAt(t, 1) uses window 2 for the "logs" fixture.
	if len(result.action.args) != 1 || result.action.args[0] != "2" {
		t.Fatalf("chooser args = %v, want [2]", result.action.args)
	}
	if cr.ChooserActive() {
		t.Fatal("chooser should hide after selecting a row")
	}
}

func TestHandleChooserInputOnRenderLoopReturnsUnhandledWhenChooserInactive(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	msgCh := startTestRenderLoop(t, cr)

	result := handleChooserInputOnRenderLoop(cr, msgCh, []byte("logs\r"))
	if result.handled {
		t.Fatal("chooser input should not be handled when chooser is inactive")
	}
	if result.action.command != "" || result.action.bell || len(result.action.args) != 0 {
		t.Fatalf("chooser action = %+v, want zero value", result.action)
	}
}
