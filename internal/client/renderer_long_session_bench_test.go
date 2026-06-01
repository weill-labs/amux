package client

import "testing"

func TestLongLivedSessionRendererBenchmarkWorkload(t *testing.T) {
	t.Parallel()

	workload := newLongLivedSessionRendererBench(t, longLivedSessionRendererBenchConfig{
		VisiblePanes: 4,
		HiddenPanes:  3,
		HistoryLines: 32,
		LineWidth:    80,
		LayoutHeight: 18,
	})
	defer workload.Close()

	if got := workload.VisiblePanes(); got != 4 {
		t.Fatalf("visible panes = %d, want 4", got)
	}
	if got := workload.HiddenPanes(); got != 3 {
		t.Fatalf("hidden panes = %d, want 3", got)
	}
	if got := workload.BaseHistoryLines(); got != 7*32 {
		t.Fatalf("base history lines = %d, want %d", got, 7*32)
	}

	dirty := workload.RenderDirtyFrame()
	if dirty.ANSIBytes == 0 {
		t.Fatal("dirty frame emitted no ANSI bytes")
	}
	if dirty.RenderStats.PanesComposited == 0 {
		t.Fatal("dirty frame composited no panes")
	}
	if dirty.VisibleOutputPanes == 0 {
		t.Fatal("dirty frame did not update visible panes")
	}
	if dirty.HiddenOutputPanes == 0 {
		t.Fatal("dirty frame did not update hidden panes")
	}
	if dirty.CaptureJSONBytes == 0 {
		t.Fatal("dirty frame did not build capture JSON with history")
	}

	full := workload.RenderFullFrame()
	if full.ANSIBytes == 0 {
		t.Fatal("full frame emitted no ANSI bytes")
	}
	if full.RenderStats.PanesComposited < dirty.RenderStats.PanesComposited {
		t.Fatalf("full frame panes composited = %d, want >= dirty frame %d", full.RenderStats.PanesComposited, dirty.RenderStats.PanesComposited)
	}
	if retained := workload.RetainedHeapAfterGC(); retained == 0 {
		t.Fatal("retained heap after GC should be observable")
	}
}
