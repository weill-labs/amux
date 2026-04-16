package server

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/weill-labs/amux/internal/proto"
)

// benchLayoutSnapshot constructs a LayoutSnapshot with n panes.
func benchLayoutSnapshot(n int) *proto.LayoutSnapshot {
	snap := &proto.LayoutSnapshot{
		SessionName:  "bench-session",
		ActivePaneID: 1,
		Width:        200,
		Height:       60,
	}

	// Build a flat CellSnapshot tree (root with n leaf children)
	snap.Root = proto.CellSnapshot{
		X: 0, Y: 0, W: 200, H: 60,
		IsLeaf: false,
		Dir:    0, // SplitHorizontal
	}

	paneW := 200 / n
	for i := 0; i < n; i++ {
		child := proto.CellSnapshot{
			X: i * (paneW + 1), Y: 0, W: paneW, H: 60,
			IsLeaf: true,
			Dir:    -1,
			PaneID: uint32(i + 1),
		}
		snap.Root.Children = append(snap.Root.Children, child)
		snap.Panes = append(snap.Panes, proto.PaneSnapshot{
			ID:   uint32(i + 1),
			Name: fmt.Sprintf("pane-%d", i+1),
			Host: "local",
		})
	}

	return snap
}

// benchPaneOutputMsg creates a MsgTypePaneOutput message with n bytes of payload.
func benchPaneOutputMsg(n int) *Message {
	data := make([]byte, n)
	for i := range data {
		data[i] = byte('A' + i%26)
	}
	return &Message{
		Type:     MsgTypePaneOutput,
		PaneID:   1,
		PaneData: data,
	}
}

func BenchmarkWriteMsg_PaneOutput(b *testing.B) {
	for _, size := range []int{256, 4096, 32768} {
		b.Run(fmt.Sprintf("bytes_%d", size), func(b *testing.B) {
			msg := benchPaneOutputMsg(size)
			var buf bytes.Buffer
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				buf.Reset()
				if err := WriteMsg(&buf, msg); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkReadMsg_PaneOutput(b *testing.B) {
	for _, size := range []int{256, 4096, 32768} {
		b.Run(fmt.Sprintf("bytes_%d", size), func(b *testing.B) {
			msg := benchPaneOutputMsg(size)
			// Pre-encode
			var encoded bytes.Buffer
			if err := WriteMsg(&encoded, msg); err != nil {
				b.Fatal(err)
			}
			raw := encoded.Bytes()

			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := ReadMsg(bytes.NewReader(raw)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkWriteMsg_Layout(b *testing.B) {
	for _, n := range []int{1, 10, 20} {
		b.Run(fmt.Sprintf("panes_%d", n), func(b *testing.B) {
			snap := benchLayoutSnapshot(n)
			msg := &Message{
				Type:   MsgTypeLayout,
				Layout: snap,
			}
			var buf bytes.Buffer
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				buf.Reset()
				if err := WriteMsg(&buf, msg); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkReadMsg_Layout(b *testing.B) {
	for _, n := range []int{1, 10, 20} {
		b.Run(fmt.Sprintf("panes_%d", n), func(b *testing.B) {
			snap := benchLayoutSnapshot(n)
			msg := &Message{
				Type:   MsgTypeLayout,
				Layout: snap,
			}
			var encoded bytes.Buffer
			if err := WriteMsg(&encoded, msg); err != nil {
				b.Fatal(err)
			}
			raw := encoded.Bytes()

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := ReadMsg(bytes.NewReader(raw)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
