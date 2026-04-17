package proto

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"io"
)

// Writer caches gob encoder state for one connection and can opt specific
// high-volume message types into compact binary frames. It is safe for
// sequential use only; callers serialize writes at a higher level.
type Writer struct {
	dst               io.Writer
	gobBuf            bytes.Buffer
	gobEnc            *gob.Encoder
	binaryPaneHistory bool
}

// NewWriter binds a stateful gob encoder to one output stream.
func NewWriter(w io.Writer) *Writer {
	if w == nil {
		return nil
	}
	pw := &Writer{dst: w}
	pw.gobEnc = gob.NewEncoder(&pw.gobBuf)
	return pw
}

// WriteMsg writes one protocol message while preserving gob type state across
// calls on the same stream.
func (w *Writer) WriteMsg(msg *Message) error {
	if w == nil {
		return io.ErrClosedPipe
	}
	if msg.Type == MsgTypePaneOutput && msg.SourceEpoch == 0 {
		return writePaneOutputBinary(w.dst, msg)
	}
	if msg.Type == MsgTypePaneHistory && w.binaryPaneHistory {
		return writePaneHistoryBinary(w.dst, msg)
	}
	w.gobBuf.Reset()
	if err := w.gobEnc.Encode(msg); err != nil {
		return fmt.Errorf("encoding message: %w", err)
	}
	return writeGobFrame(w.dst, w.gobBuf.Bytes())
}

// Reader caches gob decoder state for one connection. It is safe for
// sequential use only; callers serialize reads at a higher level.
type Reader struct {
	src    io.Reader
	gobBuf bytes.Buffer
	gobDec *gob.Decoder
}

// NewReader binds a stateful gob decoder to one input stream.
func NewReader(r io.Reader) *Reader {
	if r == nil {
		return nil
	}
	pr := &Reader{src: r}
	pr.gobDec = gob.NewDecoder(&pr.gobBuf)
	return pr
}

// ReadMsg reads one protocol message while preserving gob type state across
// calls on the same stream.
func (r *Reader) ReadMsg() (*Message, error) {
	if r == nil {
		return nil, io.ErrClosedPipe
	}
	var disc [1]byte
	if _, err := io.ReadFull(r.src, disc[:]); err != nil {
		return nil, err
	}

	if disc[0] == wireFormatBinary {
		return readPaneOutputBinary(r.src)
	}
	if disc[0] == wireFormatPaneHistory {
		return readPaneHistoryBinary(r.src)
	}
	return r.readMsgGob()
}

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
	// InputEpoch optionally tags input forwarded from a predicting client.
	// Zero means the input is not associated with local echo tracking.
	InputEpoch uint32

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
	// SourceEpoch optionally tags output with the originating input epoch for
	// reconciliation on the receiving client. Zero means no epoch is attached.
	SourceEpoch uint32

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
//   - wireFormatGob:         [0x00][length:4 BE][gob payload]
//   - wireFormatBinary:      [0x01][paneID:4 BE][length:4 BE][pane data]
//   - wireFormatPaneHistory: [0x02][paneID:4 BE][length:4 BE][pane history payload]
//
// PaneOutput falls back to gob when SourceEpoch is non-zero because the
// compact binary frame does not carry reconciliation metadata.
const (
	wireFormatGob    byte = 0x00
	wireFormatBinary byte = 0x01
)

// WriteMsg encodes and writes a message to w.
//
// The stateless helper only uses the always-on compact PaneOutput frame.
// Connection-scoped writers can also opt PaneHistory into compact binary via
// SetBinaryPaneHistory when both peers negotiated support.
func WriteMsg(w io.Writer, msg *Message) error {
	if msg.Type == MsgTypePaneOutput && msg.SourceEpoch == 0 {
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
	return writeGobFrame(w, buf.Bytes())
}

func writeGobFrame(w io.Writer, payload []byte) error {
	// Header: 1 (discriminator) + 4 (length) = 5 bytes
	var hdr [5]byte
	hdr[0] = wireFormatGob
	binary.BigEndian.PutUint32(hdr[1:5], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("writing gob header: %w", err)
	}

	_, err := w.Write(payload)
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
	if disc[0] == wireFormatPaneHistory {
		return readPaneHistoryBinary(r)
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

	msg := &Message{}
	lr := &io.LimitedReader{R: r, N: int64(length)}
	// Stream gob directly from the connection so large bootstrap history
	// messages do not require a second full-message buffer on the client.
	if err := gob.NewDecoder(lr).Decode(msg); err != nil {
		return nil, fmt.Errorf("decoding message: %w", err)
	}
	if lr.N > 0 {
		if _, err := io.Copy(io.Discard, lr); err != nil {
			return nil, fmt.Errorf("discarding message tail: %w", err)
		}
	}

	return msg, nil
}

func (r *Reader) readMsgGob() (*Message, error) {
	data, err := readGobPayload(r.src)
	if err != nil {
		return nil, err
	}
	if _, err := r.gobBuf.Write(data); err != nil {
		return nil, fmt.Errorf("buffering gob message: %w", err)
	}
	msg := &Message{}
	if err := r.gobDec.Decode(msg); err != nil {
		return nil, fmt.Errorf("decoding message: %w", err)
	}
	return msg, nil
}

func readGobPayload(r io.Reader) ([]byte, error) {
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
	return data, nil
}
