package server

import (
	"io"

	"github.com/weill-labs/amux/internal/proto"
)

// Re-export wire protocol types from proto to maintain backwards compatibility.
// All callsites using server.WriteMsg, server.ReadMsg, server.Message, etc.
// continue to work unchanged.
type MsgType = proto.MsgType
type Message = proto.Message

const (
	MsgTypeInput        = proto.MsgTypeInput
	MsgTypeResize       = proto.MsgTypeResize
	MsgTypeAttach       = proto.MsgTypeAttach
	MsgTypeDetach       = proto.MsgTypeDetach
	MsgTypeCommand      = proto.MsgTypeCommand
	MsgTypeRender       = proto.MsgTypeRender
	MsgTypeCmdResult    = proto.MsgTypeCmdResult
	MsgTypeExit         = proto.MsgTypeExit
	MsgTypeNotify       = proto.MsgTypeNotify
	MsgTypeBell         = proto.MsgTypeBell
	MsgTypePaneOutput   = proto.MsgTypePaneOutput
	MsgTypeLayout       = proto.MsgTypeLayout
	MsgTypeServerReload = proto.MsgTypeServerReload
	MsgTypeCopyMode     = proto.MsgTypeCopyMode
	MsgTypeClipboard    = proto.MsgTypeClipboard
	MsgTypeInputPane    = proto.MsgTypeInputPane

	// Bidirectional — capture routed through attached client
	MsgTypeCaptureRequest  = proto.MsgTypeCaptureRequest
	MsgTypeCaptureResponse = proto.MsgTypeCaptureResponse

	// Server → Client — inject keystrokes into client input pipeline
	MsgTypeTypeKeys = proto.MsgTypeTypeKeys
)

// WriteMsg encodes and writes a message to w.
func WriteMsg(w io.Writer, msg *Message) error {
	return proto.WriteMsg(w, msg)
}

// ReadMsg reads a message from r.
func ReadMsg(r io.Reader) (*Message, error) {
	return proto.ReadMsg(r)
}
