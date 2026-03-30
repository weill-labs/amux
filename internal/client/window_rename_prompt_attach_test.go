package client

import "testing"

func TestShowWindowRenamePromptOnRenderLoop(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	msgCh := startTestRenderLoop(t, cr)
	if !showWindowRenamePromptOnRenderLoop(cr, msgCh) {
		t.Fatal("showWindowRenamePromptOnRenderLoop should succeed with an active window")
	}

	cr = NewClientRenderer(80, 24)
	if showWindowRenamePromptOnRenderLoop(cr, nil) {
		t.Fatal("showWindowRenamePromptOnRenderLoop should fail without a layout")
	}
}
