package proto

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"
)

func TestReaderWriterRoundTripMixedStream(t *testing.T) {
	t.Parallel()

	msgs := []*Message{
		{Type: MsgTypeLayout, Layout: sampleLayoutSnapshot()},
		{Type: MsgTypePaneOutput, PaneID: 7, PaneData: []byte("stdout")},
		{Type: MsgTypeCommand, CmdName: "list", CmdArgs: []string{"--json"}},
		{Type: MsgTypePaneOutput, PaneID: 7, PaneData: []byte("stderr")},
		{Type: MsgTypeLayout, Layout: sampleLayoutSnapshot()},
	}

	var wire bytes.Buffer
	writer := NewWriter(&wire)
	for _, msg := range msgs {
		if err := writer.WriteMsg(msg); err != nil {
			t.Fatalf("WriteMsg(%v): %v", msg.Type, err)
		}
	}

	reader := NewReader(bytes.NewReader(wire.Bytes()))
	for i, want := range msgs {
		got, err := reader.ReadMsg()
		if err != nil {
			t.Fatalf("ReadMsg(%d): %v", i, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("message %d mismatch:\n got: %#v\nwant: %#v", i, got, want)
		}
	}
}

func TestWriterReusesGobTypeMetadataAcrossMessages(t *testing.T) {
	t.Parallel()

	msg := &Message{Type: MsgTypeLayout, Layout: sampleLayoutSnapshot()}

	var wire bytes.Buffer
	writer := NewWriter(&wire)

	if err := writer.WriteMsg(msg); err != nil {
		t.Fatalf("first WriteMsg: %v", err)
	}
	first := append([]byte(nil), wire.Bytes()...)

	wire.Reset()
	if err := writer.WriteMsg(msg); err != nil {
		t.Fatalf("second WriteMsg: %v", err)
	}
	second := append([]byte(nil), wire.Bytes()...)

	firstPayloadLen := gobFramePayloadLen(t, first)
	secondPayloadLen := gobFramePayloadLen(t, second)
	if secondPayloadLen >= firstPayloadLen {
		t.Fatalf("second gob payload len = %d, want less than first payload len %d", secondPayloadLen, firstPayloadLen)
	}
}

func gobFramePayloadLen(t *testing.T, frame []byte) uint32 {
	t.Helper()

	if len(frame) < 5 {
		t.Fatalf("frame length = %d, want at least 5", len(frame))
	}
	if frame[0] != wireFormatGob {
		t.Fatalf("frame discriminator = %#x, want %#x", frame[0], wireFormatGob)
	}
	return binary.BigEndian.Uint32(frame[1:5])
}

func BenchmarkLayoutMessageWire(b *testing.B) {
	msg := &Message{Type: MsgTypeLayout, Layout: sampleLayoutSnapshot()}

	b.Run("write/stateless", func(b *testing.B) {
		var wire bytes.Buffer
		b.ReportAllocs()
		for b.Loop() {
			wire.Reset()
			if err := WriteMsg(&wire, msg); err != nil {
				b.Fatalf("WriteMsg: %v", err)
			}
		}
	})

	b.Run("write/stateful", func(b *testing.B) {
		var wire bytes.Buffer
		writer := NewWriter(&wire)
		b.ReportAllocs()
		for b.Loop() {
			wire.Reset()
			if err := writer.WriteMsg(msg); err != nil {
				b.Fatalf("WriteMsg: %v", err)
			}
		}
	})

	b.Run("read/stateless", func(b *testing.B) {
		var wire bytes.Buffer
		if err := WriteMsg(&wire, msg); err != nil {
			b.Fatalf("WriteMsg: %v", err)
		}
		raw := append([]byte(nil), wire.Bytes()...)

		b.ReportAllocs()
		for b.Loop() {
			if _, err := ReadMsg(bytes.NewReader(raw)); err != nil {
				b.Fatalf("ReadMsg: %v", err)
			}
		}
	})

	b.Run("read/stateful", func(b *testing.B) {
		var wire bytes.Buffer
		writer := NewWriter(&wire)
		for i := 0; i < b.N+1; i++ {
			if err := writer.WriteMsg(msg); err != nil {
				b.Fatalf("WriteMsg: %v", err)
			}
		}

		reader := NewReader(bytes.NewReader(wire.Bytes()))
		if _, err := reader.ReadMsg(); err != nil {
			b.Fatalf("warmup ReadMsg: %v", err)
		}

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := reader.ReadMsg(); err != nil {
				b.Fatalf("ReadMsg: %v", err)
			}
		}
	})
}
