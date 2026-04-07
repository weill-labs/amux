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
