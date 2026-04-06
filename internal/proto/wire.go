package proto

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"io"
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
	MsgTypeCopyMode     MsgType = 18 // enter copy mode for a pane (server → client)
	MsgTypeClipboard    MsgType = 19 // OSC 52 clipboard data from a pane
	MsgTypeInputPane    MsgType = 20 // input targeted at a specific pane ID (remote proxying)

	// Bidirectional — capture routed through attached client
	MsgTypeCaptureRequest  MsgType = 21 // server → client: render capture from client emulators
	MsgTypeCaptureResponse MsgType = 22 // client → server: captured output

	// Server → Client — inject keystrokes into client input pipeline
	MsgTypeTypeKeys MsgType = 23

	// Client → Server — transient client-local UI state transitions
	MsgTypeUIEvent MsgType = 24

	// Server → Client — retained pane history bootstrap during attach.
	MsgTypePaneHistory MsgType = 25
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
	// AttachMode is optional. The zero value preserves the legacy attach
	// semantics: attached clients participate in size negotiation unless they
	// explicitly opt out.
	AttachMode AttachMode
	// AttachColorProfile is optional. Empty means the client did not advertise
	// an explicit terminal color profile.
	AttachColorProfile string
	// AttachCapabilities is optional. Nil means the client used the legacy
	// attach path and did not advertise explicit capability support.
	AttachCapabilities *ClientCapabilities

	// MsgTypeCommand
	CmdName     string
	CmdArgs     []string
	ActorPaneID uint32

	// MsgTypeRender
	RenderData []byte

	// MsgTypeCmdResult
	CmdOutput string
	CmdErr    string

	// MsgTypeNotify
	Text string

	// MsgTypePaneOutput, MsgTypeInputPane, MsgTypeTypeKeys (optional target pane)
	PaneID   uint32
	PaneData []byte

	// MsgTypePaneHistory
	History       []string
	StyledHistory []StyledLine

	// MsgTypeLayout
	Layout *LayoutSnapshot

	// MsgTypeCaptureRequest — server-gathered agent status for JSON capture.
	// Keyed by pane ID. Only populated when capture args include --format json.
	AgentStatus map[uint32]PaneAgentStatus

	// MsgTypeUIEvent
	UIEvent string
}

const maxMessageSize = 16 * 1024 * 1024 // 16 MB

// Wire format discriminators. The first byte on the wire identifies the
// encoding used for the rest of the message:
//   - wireFormatGob:    [0x00][length:4 BE][gob payload]
//   - wireFormatBinary: [0x01][paneID:4 BE][length:4 BE][pane data]
const (
	wireFormatGob    byte = 0x00
	wireFormatBinary byte = 0x01
)

// WriteMsg encodes and writes a message to w.
//
// MsgTypePaneOutput uses a compact binary encoding (no gob overhead).
// All other message types use the original length-prefixed gob encoding.
func WriteMsg(w io.Writer, msg *Message) error {
	if msg.Type == MsgTypePaneOutput {
		return writePaneOutputBinary(w, msg)
	}
	return writeMsgGob(w, msg)
}

// writePaneOutputBinary writes a PaneOutput message using compact binary
// framing: [0x01][paneID:4 BE][length:4 BE][data].
func writePaneOutputBinary(w io.Writer, msg *Message) error {
	dataLen := len(msg.PaneData)
	// Header: 1 (discriminator) + 4 (paneID) + 4 (length) = 9 bytes
	var hdr [9]byte
	hdr[0] = wireFormatBinary
	binary.BigEndian.PutUint32(hdr[1:5], msg.PaneID)
	binary.BigEndian.PutUint32(hdr[5:9], uint32(dataLen))
	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("writing binary header: %w", err)
	}
	if dataLen > 0 {
		if _, err := w.Write(msg.PaneData); err != nil {
			return fmt.Errorf("writing pane data: %w", err)
		}
	}
	return nil
}

// writeMsgGob writes a gob-encoded message with format:
// [0x00][length:4 BE][gob payload].
func writeMsgGob(w io.Writer, msg *Message) error {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(msg); err != nil {
		return fmt.Errorf("encoding message: %w", err)
	}

	// Header: 1 (discriminator) + 4 (length) = 5 bytes
	var hdr [5]byte
	hdr[0] = wireFormatGob
	binary.BigEndian.PutUint32(hdr[1:5], uint32(buf.Len()))
	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("writing gob header: %w", err)
	}

	_, err := w.Write(buf.Bytes())
	return err
}

// ReadMsg reads a message from r. It inspects the first byte to determine
// whether the message uses binary PaneOutput encoding or gob encoding.
func ReadMsg(r io.Reader) (*Message, error) {
	var disc [1]byte
	if _, err := io.ReadFull(r, disc[:]); err != nil {
		return nil, err
	}

	if disc[0] == wireFormatBinary {
		return readPaneOutputBinary(r)
	}
	return readMsgGob(r)
}

// readPaneOutputBinary reads the remainder of a binary PaneOutput message
// after the discriminator byte has been consumed.
// Format: [paneID:4 BE][length:4 BE][data].
func readPaneOutputBinary(r io.Reader) (*Message, error) {
	var hdr [8]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}

	paneID := binary.BigEndian.Uint32(hdr[0:4])
	dataLen := binary.BigEndian.Uint32(hdr[4:8])

	if dataLen > maxMessageSize {
		return nil, fmt.Errorf("message too large: %d bytes", dataLen)
	}

	data := make([]byte, dataLen)
	if dataLen > 0 {
		if _, err := io.ReadFull(r, data); err != nil {
			return nil, err
		}
	}

	return &Message{
		Type:     MsgTypePaneOutput,
		PaneID:   paneID,
		PaneData: data,
	}, nil
}

// readMsgGob reads the remainder of a gob-encoded message after the
// discriminator byte has been consumed. Format: [length:4 BE][gob payload].
func readMsgGob(r io.Reader) (*Message, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(lenBuf[:])

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
