package client

import "testing"

func TestBusyMultiPaneClientRendererBenchmarkWorkload(t *testing.T) {
	t.Parallel()

	workload := newBusyMultiPaneClientRenderBench(t)
	defer workload.Close()

	result := workload.Step()
	if result.VisiblePanes != 10 {
		t.Fatalf("visible panes = %d, want 10", result.VisiblePanes)
	}
	if result.PaneOutputs != 4 {
		t.Fatalf("pane outputs = %d, want 4", result.PaneOutputs)
	}
	if result.ScreenChangedOutputs != result.PaneOutputs {
		t.Fatalf("screen changed outputs = %d, want %d", result.ScreenChangedOutputs, result.PaneOutputs)
	}
	if result.RenderStats.PanesComposited == 0 {
		t.Fatal("render composed no panes")
	}
	if result.ANSIBytes == 0 {
		t.Fatal("render emitted no ANSI bytes")
	}
}
