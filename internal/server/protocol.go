package server

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"io"

	"github.com/weill-labs/amux/internal/proto"
)

// MsgType identifies the kind of protocol message.
type MsgType uint8

const (
	// Client → Server
	MsgTypeInput   MsgType = 1
	MsgTypeResize  MsgType = 2
	MsgTypeAttach  MsgType = 3
	MsgTypeDetach  MsgType = 4
	MsgTypeCommand MsgType = 5

	// Server → Client
	MsgTypeRender       MsgType = 10
	MsgTypeCmdResult    MsgType = 11
	MsgTypeExit         MsgType = 12
	MsgTypeNotify       MsgType = 13
	MsgTypeBell         MsgType = 14
	MsgTypePaneOutput   MsgType = 15 // raw PTY output for one pane
	MsgTypeLayout       MsgType = 16 // serialized layout tree + metadata
	MsgTypeServerReload MsgType = 17 // server is about to exec — clients should reconnect
)

// Message is the wire protocol envelope. Only the fields relevant to
// the message Type are populated; the rest are zero-valued.
type Message struct {
	Type MsgType

	// MsgTypeInput
	Input []byte

	// MsgTypeResize, MsgTypeAttach
	Cols int
	Rows int

	// MsgTypeAttach
	Session string

	// MsgTypeCommand
	CmdName string
	CmdArgs []string

	// MsgTypeRender
	RenderData []byte

	// MsgTypeCmdResult
	CmdOutput string
	CmdErr    string

	// MsgTypeNotify
	Text string

	// MsgTypePaneOutput
	PaneID   uint32
	PaneData []byte

	// MsgTypeLayout
	Layout *proto.LayoutSnapshot
}

const maxMessageSize = 16 * 1024 * 1024 // 16 MB

// WriteMsg encodes and writes a length-prefixed message to w.
func WriteMsg(w io.Writer, msg *Message) error {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(msg); err != nil {
		return fmt.Errorf("encoding message: %w", err)
	}

	length := uint32(buf.Len())
	if err := binary.Write(w, binary.BigEndian, length); err != nil {
		return fmt.Errorf("writing length: %w", err)
	}

	_, err := w.Write(buf.Bytes())
	return err
}

// ReadMsg reads a length-prefixed message from r.
func ReadMsg(r io.Reader) (*Message, error) {
	var length uint32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return nil, err
	}

	if length > maxMessageSize {
		return nil, fmt.Errorf("message too large: %d bytes", length)
	}

	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}

	msg := &Message{}
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(msg); err != nil {
		return nil, fmt.Errorf("decoding message: %w", err)
	}

	return msg, nil
}
