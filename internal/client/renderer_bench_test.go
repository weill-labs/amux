package client

import (
	"fmt"
	"testing"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

func benchTerminalPayload(n int) []byte {
	fragments := []string{
		"\033[32m$ \033[0mls -la\r\n",
		"\033[1;34mdrwxr-xr-x\033[0m  5 user staff  160 Mar 15 10:00 \033[1;34m.\033[0m\r\n",
		"\033[1;34mdrwxr-xr-x\033[0m  3 user staff   96 Mar 14 09:00 \033[1;34m..\033[0m\r\n",
		"\033[0;32m-rw-r--r--\033[0m  1 user staff 1234 Mar 15 10:00 \033[0mmain.go\033[0m\r\n",
		"\033[0;32m-rw-r--r--\033[0m  1 user staff  567 Mar 14 09:30 \033[0;33mREADME.md\033[0m\r\n",
		"\033[0;32m-rwxr-xr-x\033[0m  1 user staff 8901 Mar 15 09:50 \033[0;31mamux\033[0m\r\n",
	}

	buf := make([]byte, 0, n)
	for len(buf) < n {
		for _, f := range fragments {
			buf = append(buf, f...)
			if len(buf) >= n {
				break
			}
		}
	}
	return buf[:n]
}

func benchLayoutSnapshot(n, width, height int) *proto.LayoutSnapshot {
	panes := make([]proto.PaneSnapshot, 0, n)
	for i := 1; i <= n; i++ {
		panes = append(panes, proto.PaneSnapshot{
			ID:    uint32(i),
			Name:  fmt.Sprintf("pane-%d", i),
			Host:  "local",
			Color: config.AccentColor(uint32(i - 1)),
		})
	}

	var root proto.CellSnapshot
	if n == 1 {
		root = proto.CellSnapshot{
			X: 0, Y: 0, W: width, H: height,
			IsLeaf: true, Dir: -1, PaneID: 1,
		}
	} else {
		root = proto.CellSnapshot{
			X: 0, Y: 0, W: width, H: height,
			Dir: int(mux.SplitVertical),
		}
		x := 0
		cellW := (width - (n - 1)) / n
		for i := 1; i <= n; i++ {
			w := cellW
			if i == n {
				w = width - x
			}
			root.Children = append(root.Children, proto.CellSnapshot{
				X: x, Y: 0, W: w, H: height,
				IsLeaf: true, Dir: -1, PaneID: uint32(i),
			})
			x += w + 1
		}
	}

	window := proto.WindowSnapshot{
		ID:           1,
		Name:         "window-1",
		Index:        1,
		ActivePaneID: 1,
		Root:         root,
		Panes:        panes,
	}

	return &proto.LayoutSnapshot{
		SessionName:    "bench",
		ActivePaneID:   1,
		Width:          width,
		Height:         height,
		Root:           root,
		Panes:          panes,
		Windows:        []proto.WindowSnapshot{window},
		ActiveWindowID: 1,
	}
}

func benchRendererWithContent(n int) (*Renderer, map[uint32]proto.PaneAgentStatus) {
	const width = 200
	const layoutHeight = 23

	r := NewWithScrollback(width, layoutHeight+1, mux.DefaultScrollbackLines)
	snap := benchLayoutSnapshot(n, width, layoutHeight)
	r.HandleLayout(snap)

	payload := benchTerminalPayload(80 * 24)
	status := make(map[uint32]proto.PaneAgentStatus, n)
	for _, pane := range snap.Panes {
		r.HandlePaneOutput(pane.ID, payload)
		status[pane.ID] = proto.PaneAgentStatus{
			Idle:           true,
			CurrentCommand: "bash",
		}
	}

	return r, status
}

func BenchmarkRendererHandlePaneOutput(b *testing.B) {
	for _, size := range []int{256, 4096, 32768} {
		b.Run(fmt.Sprintf("bytes_%d", size), func(b *testing.B) {
			r, _ := benchRendererWithContent(1)
			defer r.Close()
			payload := benchTerminalPayload(size)

			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				r.HandlePaneOutput(1, payload)
			}
		})
	}
}

func benchMultiWindowLayoutSnapshot(visiblePanes, hiddenPanes, width, height int) *proto.LayoutSnapshot {
	windowRoot := func(panes int) proto.CellSnapshot {
		root := proto.CellSnapshot{X: 0, Y: 0, W: width, H: height}
		if panes == 1 {
			root.IsLeaf = true
			root.Dir = -1
		} else {
			root.Dir = int(mux.SplitVertical)
		}
		return root
	}

	window1Root := windowRoot(visiblePanes)
	window2Root := windowRoot(hiddenPanes)

	buildWindow := func(root *proto.CellSnapshot, startID, panes int) []proto.PaneSnapshot {
		snaps := make([]proto.PaneSnapshot, 0, panes)
		if panes == 0 {
			return snaps
		}
		if panes == 1 {
			id := uint32(startID)
			root.PaneID = id
			return append(snaps, proto.PaneSnapshot{
				ID:    id,
				Name:  fmt.Sprintf("pane-%d", id),
				Host:  "local",
				Color: config.AccentColor(0),
			})
		}
		x := 0
		cellW := (width - (panes - 1)) / panes
		for i := 0; i < panes; i++ {
			id := uint32(startID + i)
			w := cellW
			if i == panes-1 {
				w = width - x
			}
			root.Children = append(root.Children, proto.CellSnapshot{
				X: x, Y: 0, W: w, H: height,
				IsLeaf: true, Dir: -1, PaneID: id,
			})
			snaps = append(snaps, proto.PaneSnapshot{
				ID:    id,
				Name:  fmt.Sprintf("pane-%d", id),
				Host:  "local",
				Color: config.AccentColor(uint32(i)),
			})
			x += w + 1
		}
		return snaps
	}

	window1Panes := buildWindow(&window1Root, 1, visiblePanes)
	window2Panes := buildWindow(&window2Root, visiblePanes+1, hiddenPanes)

	return &proto.LayoutSnapshot{
		SessionName:  "bench",
		ActivePaneID: 1,
		Width:        width,
		Height:       height,
		Root:         window1Root,
		Windows: []proto.WindowSnapshot{
			{ID: 1, Name: "window-1", Index: 1, ActivePaneID: 1, Root: window1Root, Panes: window1Panes},
			{ID: 2, Name: "window-2", Index: 2, ActivePaneID: uint32(visiblePanes + 1), Root: window2Root, Panes: window2Panes},
		},
		ActiveWindowID: 1,
	}
}

func BenchmarkRendererHandlePaneOutputVisibility(b *testing.B) {
	const (
		width        = 200
		layoutHeight = 23
		payloadSize  = 4096
	)

	payload := benchTerminalPayload(payloadSize)
	layout := benchMultiWindowLayoutSnapshot(10, 10, width, layoutHeight)

	b.Run("visible", func(b *testing.B) {
		r := NewWithScrollback(width, layoutHeight+1, mux.DefaultScrollbackLines)
		defer r.Close()
		r.HandleLayout(layout)

		b.SetBytes(int64(payloadSize))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; b.Loop(); i++ {
			paneID := uint32((i % 10) + 1)
			r.HandlePaneOutput(paneID, payload)
		}
	})

	b.Run("hidden", func(b *testing.B) {
		r := NewWithScrollback(width, layoutHeight+1, mux.DefaultScrollbackLines)
		defer r.Close()
		r.HandleLayout(layout)

		b.SetBytes(int64(payloadSize))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; b.Loop(); i++ {
			paneID := uint32((i % 10) + 11)
			r.HandlePaneOutput(paneID, payload)
		}
	})
}

func BenchmarkRendererCaptureJSON(b *testing.B) {
	for _, panes := range []int{1, 20} {
		b.Run(fmt.Sprintf("panes_%d/build", panes), func(b *testing.B) {
			r, status := benchRendererWithContent(panes)
			defer r.Close()

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, ok := r.captureJSONValue(status); !ok {
					b.Fatal("captureJSONValue returned no layout")
				}
			}
		})

		b.Run(fmt.Sprintf("panes_%d/marshal", panes), func(b *testing.B) {
			r, status := benchRendererWithContent(panes)
			defer r.Close()
			capture, ok := r.captureJSONValue(status)
			if !ok {
				b.Fatal("captureJSONValue returned no layout")
			}

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				marshalIndented(capture)
			}
		})
	}
}

func BenchmarkRendererHandleLayout(b *testing.B) {
	const width = 200
	const layoutHeight = 23

	base := benchLayoutSnapshot(1, width, layoutHeight)
	for _, panes := range []int{4, 10, 20} {
		target := benchLayoutSnapshot(panes, width, layoutHeight)
		b.Run(fmt.Sprintf("panes_%d", panes), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				r := NewWithScrollback(width, layoutHeight+1, mux.DefaultScrollbackLines)
				r.HandleLayout(base)
				b.StartTimer()
				r.HandleLayout(target)
				b.StopTimer()
				r.Close()
			}
		})
	}
}
