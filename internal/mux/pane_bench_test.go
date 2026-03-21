package mux

import (
	"fmt"
	"testing"
)

func BenchmarkPaneApplyOutput(b *testing.B) {
	for _, size := range []int{256, 4096, 32768} {
		b.Run(fmt.Sprintf("bytes_%d", size), func(b *testing.B) {
			payload := realisticTerminalPayload(size)
			pane := NewProxyPaneWithScrollback(1, PaneMeta{
				Name: "pane-1",
				Host: DefaultHost,
			}, 80, 24, DefaultScrollbackLines, nil, nil, func(data []byte) (int, error) {
				return len(data), nil
			})

			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				pane.applyOutput(payload)
			}
		})
	}
}
