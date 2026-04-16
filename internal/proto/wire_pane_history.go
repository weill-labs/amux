package proto

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"image/color"
	"io"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

const wireFormatPaneHistory byte = 0x02

const (
	paneHistoryFlagHistoryFromStyledText byte = 1 << iota
)

const (
	paneHistoryLineFlagTextFromCells byte = 1 << iota
	paneHistoryLineFlagHasCells
)

const (
	paneHistoryColorNil byte = iota
	paneHistoryColorBasic
	paneHistoryColorIndexed
	paneHistoryColorRGB
	paneHistoryColorRGBA
	paneHistoryColorRGBA64
)

type paneHistoryCellRun struct {
	count int
	cell  Cell
}

type paneHistoryColorKey struct {
	kind byte
	data [8]byte
}

type paneHistoryStyleKey struct {
	fg             paneHistoryColorKey
	bg             paneHistoryColorKey
	underlineColor paneHistoryColorKey
	underline      byte
	attrs          uint8
}

type paneHistoryReader struct {
	br *bufio.Reader
	lr *io.LimitedReader
}

func newPaneHistoryReader(r io.Reader, limit int64) *paneHistoryReader {
	lr := &io.LimitedReader{R: r, N: limit}
	return &paneHistoryReader{
		br: bufio.NewReader(lr),
		lr: lr,
	}
}

func (r *paneHistoryReader) Read(p []byte) (int, error) {
	return r.br.Read(p)
}

func (r *paneHistoryReader) ReadByte() (byte, error) {
	return r.br.ReadByte()
}

func (r *paneHistoryReader) remaining() int64 {
	return int64(r.br.Buffered()) + r.lr.N
}

// SetBinaryPaneHistory toggles compact binary encoding for MsgTypePaneHistory.
// Callers should configure this before they begin writing on the stream.
func (w *Writer) SetBinaryPaneHistory(enabled bool) {
	if w == nil {
		return
	}
	w.binaryPaneHistory = enabled
}

func writePaneHistoryBinary(w io.Writer, msg *Message) error {
	payload, err := encodePaneHistoryPayload(msg)
	if err != nil {
		return fmt.Errorf("encoding pane history payload: %w", err)
	}

	var hdr [9]byte
	hdr[0] = wireFormatPaneHistory
	binary.BigEndian.PutUint32(hdr[1:5], msg.PaneID)
	binary.BigEndian.PutUint32(hdr[5:9], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("writing pane history header: %w", err)
	}
	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return fmt.Errorf("writing pane history payload: %w", err)
		}
	}
	return nil
}

func readPaneHistoryBinary(r io.Reader) (*Message, error) {
	var hdr [8]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}

	paneID := binary.BigEndian.Uint32(hdr[0:4])
	payloadLen := binary.BigEndian.Uint32(hdr[4:8])
	if payloadLen > maxMessageSize {
		return nil, fmt.Errorf("message too large: %d bytes", payloadLen)
	}

	pr := newPaneHistoryReader(r, int64(payloadLen))
	msg, err := decodePaneHistoryPayload(paneID, pr, int(payloadLen))
	if err != nil {
		return nil, err
	}
	if pr.remaining() != 0 {
		remaining := pr.remaining()
		if _, err := io.Copy(io.Discard, pr); err != nil {
			return nil, fmt.Errorf("discarding pane history payload tail: %w", err)
		}
		return nil, fmt.Errorf("pane history payload has %d trailing bytes", remaining)
	}
	return msg, nil
}

func encodePaneHistoryPayload(msg *Message) ([]byte, error) {
	if msg == nil {
		return nil, fmt.Errorf("nil message")
	}

	var payload bytes.Buffer
	flags := byte(0)
	if paneHistoryMatchesStyledText(msg.History, msg.StyledHistory) {
		flags |= paneHistoryFlagHistoryFromStyledText
	}
	payload.WriteByte(flags)
	writePaneHistoryUvarint(&payload, uint64(len(msg.History)))
	writePaneHistoryUvarint(&payload, uint64(len(msg.StyledHistory)))

	if flags&paneHistoryFlagHistoryFromStyledText == 0 {
		for _, line := range msg.History {
			writePaneHistoryString(&payload, line)
		}
	}

	styles, styleIndex := paneHistoryStyleTable(msg.StyledHistory)
	writePaneHistoryUvarint(&payload, uint64(len(styles)))
	for _, style := range styles {
		if err := writePaneHistoryStyle(&payload, style); err != nil {
			return nil, err
		}
	}

	for _, line := range msg.StyledHistory {
		lineFlags := byte(0)
		if len(line.Cells) > 0 {
			lineFlags |= paneHistoryLineFlagHasCells
			if paneHistoryCellsTextEqual(line.Text, line.Cells) {
				lineFlags |= paneHistoryLineFlagTextFromCells
			}
		}
		payload.WriteByte(lineFlags)

		if lineFlags&paneHistoryLineFlagTextFromCells == 0 {
			writePaneHistoryString(&payload, line.Text)
		}
		if lineFlags&paneHistoryLineFlagHasCells == 0 {
			continue
		}

		runs := paneHistoryCellRuns(line.Cells)
		writePaneHistoryUvarint(&payload, uint64(len(runs)))
		for _, run := range runs {
			writePaneHistoryUvarint(&payload, uint64(run.count))
			writePaneHistoryString(&payload, run.cell.Char)
			if run.cell.Width < 0 {
				return nil, fmt.Errorf("negative cell width: %d", run.cell.Width)
			}
			writePaneHistoryUvarint(&payload, uint64(run.cell.Width))
			writePaneHistoryUvarint(&payload, uint64(styleIndex[paneHistoryComparableStyle(run.cell.Style)]))
		}
	}

	return payload.Bytes(), nil
}

func decodePaneHistoryPayload(paneID uint32, r *paneHistoryReader, payloadLen int) (*Message, error) {
	flags, err := r.ReadByte()
	if err != nil {
		return nil, fmt.Errorf("reading pane history flags: %w", err)
	}

	historyCount, err := readPaneHistoryCount(r, payloadLen, "history count")
	if err != nil {
		return nil, err
	}
	styledCount, err := readPaneHistoryCount(r, payloadLen, "styled history count")
	if err != nil {
		return nil, err
	}

	history := make([]string, historyCount)
	if flags&paneHistoryFlagHistoryFromStyledText == 0 {
		for i := 0; i < historyCount; i++ {
			history[i], err = readPaneHistoryString(r)
			if err != nil {
				return nil, fmt.Errorf("reading history line %d: %w", i, err)
			}
		}
	} else if historyCount != styledCount {
		return nil, fmt.Errorf("history/styled history count mismatch: %d != %d", historyCount, styledCount)
	}

	styleCount, err := readPaneHistoryCount(r, payloadLen, "style count")
	if err != nil {
		return nil, err
	}
	styles := make([]uv.Style, styleCount)
	for i := 0; i < styleCount; i++ {
		styles[i], err = readPaneHistoryStyle(r)
		if err != nil {
			return nil, fmt.Errorf("reading style %d: %w", i, err)
		}
	}

	styledHistory := make([]StyledLine, styledCount)
	for i := 0; i < styledCount; i++ {
		lineFlags, err := r.ReadByte()
		if err != nil {
			return nil, fmt.Errorf("reading styled line %d flags: %w", i, err)
		}

		line := StyledLine{}
		if lineFlags&paneHistoryLineFlagTextFromCells == 0 {
			line.Text, err = readPaneHistoryString(r)
			if err != nil {
				return nil, fmt.Errorf("reading styled line %d text: %w", i, err)
			}
		}
		if lineFlags&paneHistoryLineFlagHasCells != 0 {
			runCount, err := readPaneHistoryCount(r, payloadLen, "cell run count")
			if err != nil {
				return nil, fmt.Errorf("reading styled line %d run count: %w", i, err)
			}
			line.Cells = make([]Cell, 0, runCount)
			for runIndex := 0; runIndex < runCount; runIndex++ {
				runLen, err := readPaneHistoryCount(r, payloadLen, "cell run length")
				if err != nil {
					return nil, fmt.Errorf("reading styled line %d run %d length: %w", i, runIndex, err)
				}
				char, err := readPaneHistoryString(r)
				if err != nil {
					return nil, fmt.Errorf("reading styled line %d run %d char: %w", i, runIndex, err)
				}
				width, err := readPaneHistoryCount(r, payloadLen, "cell width")
				if err != nil {
					return nil, fmt.Errorf("reading styled line %d run %d width: %w", i, runIndex, err)
				}
				styleIndex, err := readPaneHistoryCount(r, payloadLen, "style index")
				if err != nil {
					return nil, fmt.Errorf("reading styled line %d run %d style index: %w", i, runIndex, err)
				}
				if styleIndex >= len(styles) {
					return nil, fmt.Errorf("style index %d out of range %d", styleIndex, len(styles))
				}
				cell := Cell{
					Char:  char,
					Width: width,
					Style: styles[styleIndex],
				}
				for j := 0; j < runLen; j++ {
					line.Cells = append(line.Cells, cell)
				}
			}
			if lineFlags&paneHistoryLineFlagTextFromCells != 0 {
				line.Text = paneHistoryCellsText(line.Cells)
			}
		}
		styledHistory[i] = line
	}

	if flags&paneHistoryFlagHistoryFromStyledText != 0 {
		history = StyledLineText(styledHistory)
	}

	return &Message{
		Type:          MsgTypePaneHistory,
		PaneID:        paneID,
		History:       history,
		StyledHistory: styledHistory,
	}, nil
}

func paneHistoryMatchesStyledText(history []string, styled []StyledLine) bool {
	if len(history) == 0 || len(history) != len(styled) {
		return false
	}
	for i, line := range styled {
		if history[i] != line.Text {
			return false
		}
	}
	return true
}

func paneHistoryStyleTable(lines []StyledLine) ([]uv.Style, map[paneHistoryStyleKey]int) {
	styles := make([]uv.Style, 0)
	index := make(map[paneHistoryStyleKey]int)
	for _, line := range lines {
		for _, cell := range line.Cells {
			key := paneHistoryComparableStyle(cell.Style)
			if _, ok := index[key]; ok {
				continue
			}
			index[key] = len(styles)
			styles = append(styles, cell.Style)
		}
	}
	return styles, index
}

func paneHistoryComparableStyle(style uv.Style) paneHistoryStyleKey {
	return paneHistoryStyleKey{
		fg:             paneHistoryComparableColor(style.Fg),
		bg:             paneHistoryComparableColor(style.Bg),
		underlineColor: paneHistoryComparableColor(style.UnderlineColor),
		underline:      byte(style.Underline),
		attrs:          style.Attrs,
	}
}

func paneHistoryComparableColor(c color.Color) paneHistoryColorKey {
	switch v := c.(type) {
	case nil:
		return paneHistoryColorKey{kind: paneHistoryColorNil}
	case ansi.BasicColor:
		var key paneHistoryColorKey
		key.kind = paneHistoryColorBasic
		key.data[0] = byte(v)
		return key
	case ansi.IndexedColor:
		var key paneHistoryColorKey
		key.kind = paneHistoryColorIndexed
		key.data[0] = byte(v)
		return key
	case ansi.RGBColor:
		var key paneHistoryColorKey
		key.kind = paneHistoryColorRGB
		key.data[0] = v.R
		key.data[1] = v.G
		key.data[2] = v.B
		return key
	case color.RGBA:
		var key paneHistoryColorKey
		key.kind = paneHistoryColorRGBA
		key.data[0] = v.R
		key.data[1] = v.G
		key.data[2] = v.B
		key.data[3] = v.A
		return key
	case color.RGBA64:
		return paneHistoryComparableRGBA64(v.R, v.G, v.B, v.A)
	default:
		r, g, b, a := v.RGBA()
		return paneHistoryComparableRGBA64(uint16(r), uint16(g), uint16(b), uint16(a))
	}
}

func paneHistoryCellRuns(cells []Cell) []paneHistoryCellRun {
	if len(cells) == 0 {
		return nil
	}
	runs := make([]paneHistoryCellRun, 0, len(cells))
	current := paneHistoryCellRun{count: 1, cell: cells[0]}
	currentStyleKey := paneHistoryComparableStyle(cells[0].Style)
	for _, cell := range cells[1:] {
		cellStyleKey := paneHistoryComparableStyle(cell.Style)
		if cell.Char == current.cell.Char && cell.Width == current.cell.Width && cellStyleKey == currentStyleKey {
			current.count++
			continue
		}
		runs = append(runs, current)
		current = paneHistoryCellRun{count: 1, cell: cell}
		currentStyleKey = cellStyleKey
	}
	runs = append(runs, current)
	return runs
}

func paneHistoryCellsTextEqual(text string, cells []Cell) bool {
	offset := 0
	for _, cell := range cells {
		if offset > len(text) {
			return false
		}
		if !strings.HasPrefix(text[offset:], cell.Char) {
			return false
		}
		offset += len(cell.Char)
	}
	return offset == len(text)
}

func paneHistoryCellsText(cells []Cell) string {
	var b strings.Builder
	for _, cell := range cells {
		b.WriteString(cell.Char)
	}
	return b.String()
}

func writePaneHistoryStyle(dst *bytes.Buffer, style uv.Style) error {
	if err := writePaneHistoryColor(dst, style.Fg); err != nil {
		return err
	}
	if err := writePaneHistoryColor(dst, style.Bg); err != nil {
		return err
	}
	if err := writePaneHistoryColor(dst, style.UnderlineColor); err != nil {
		return err
	}
	dst.WriteByte(byte(style.Underline))
	dst.WriteByte(style.Attrs)
	return nil
}

func readPaneHistoryStyle(r *paneHistoryReader) (uv.Style, error) {
	fg, err := readPaneHistoryColor(r)
	if err != nil {
		return uv.Style{}, err
	}
	bg, err := readPaneHistoryColor(r)
	if err != nil {
		return uv.Style{}, err
	}
	underlineColor, err := readPaneHistoryColor(r)
	if err != nil {
		return uv.Style{}, err
	}
	underline, err := r.ReadByte()
	if err != nil {
		return uv.Style{}, err
	}
	attrs, err := r.ReadByte()
	if err != nil {
		return uv.Style{}, err
	}
	return uv.Style{
		Fg:             fg,
		Bg:             bg,
		UnderlineColor: underlineColor,
		Underline:      uv.Underline(underline),
		Attrs:          attrs,
	}, nil
}

func writePaneHistoryColor(dst *bytes.Buffer, c color.Color) error {
	switch v := c.(type) {
	case nil:
		dst.WriteByte(paneHistoryColorNil)
	case ansi.BasicColor:
		dst.WriteByte(paneHistoryColorBasic)
		dst.WriteByte(byte(v))
	case ansi.IndexedColor:
		dst.WriteByte(paneHistoryColorIndexed)
		dst.WriteByte(byte(v))
	case ansi.RGBColor:
		dst.WriteByte(paneHistoryColorRGB)
		dst.WriteByte(v.R)
		dst.WriteByte(v.G)
		dst.WriteByte(v.B)
	case color.RGBA:
		dst.WriteByte(paneHistoryColorRGBA)
		dst.WriteByte(v.R)
		dst.WriteByte(v.G)
		dst.WriteByte(v.B)
		dst.WriteByte(v.A)
	case color.RGBA64:
		dst.WriteByte(paneHistoryColorRGBA64)
		writePaneHistoryUint16(dst, v.R)
		writePaneHistoryUint16(dst, v.G)
		writePaneHistoryUint16(dst, v.B)
		writePaneHistoryUint16(dst, v.A)
	default:
		r, g, b, a := v.RGBA()
		dst.WriteByte(paneHistoryColorRGBA64)
		writePaneHistoryUint16(dst, uint16(r))
		writePaneHistoryUint16(dst, uint16(g))
		writePaneHistoryUint16(dst, uint16(b))
		writePaneHistoryUint16(dst, uint16(a))
	}
	return nil
}

func readPaneHistoryColor(r *paneHistoryReader) (color.Color, error) {
	kind, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	switch kind {
	case paneHistoryColorNil:
		return nil, nil
	case paneHistoryColorBasic:
		v, err := r.ReadByte()
		return ansi.BasicColor(v), err
	case paneHistoryColorIndexed:
		v, err := r.ReadByte()
		return ansi.IndexedColor(v), err
	case paneHistoryColorRGB:
		var buf [3]byte
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return nil, err
		}
		return ansi.RGBColor{R: buf[0], G: buf[1], B: buf[2]}, nil
	case paneHistoryColorRGBA:
		var buf [4]byte
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return nil, err
		}
		return color.RGBA{R: buf[0], G: buf[1], B: buf[2], A: buf[3]}, nil
	case paneHistoryColorRGBA64:
		var buf [8]byte
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return nil, err
		}
		return color.RGBA64{
			R: binary.BigEndian.Uint16(buf[0:2]),
			G: binary.BigEndian.Uint16(buf[2:4]),
			B: binary.BigEndian.Uint16(buf[4:6]),
			A: binary.BigEndian.Uint16(buf[6:8]),
		}, nil
	default:
		return nil, fmt.Errorf("unknown color encoding: %d", kind)
	}
}

func readPaneHistoryCount(r *paneHistoryReader, payloadLen int, name string) (int, error) {
	n, err := binary.ReadUvarint(r)
	if err != nil {
		return 0, fmt.Errorf("reading %s: %w", name, err)
	}
	if n > uint64(payloadLen) {
		return 0, fmt.Errorf("%s too large: %d", name, n)
	}
	return int(n), nil
}

func readPaneHistoryString(r *paneHistoryReader) (string, error) {
	n, err := binary.ReadUvarint(r)
	if err != nil {
		return "", err
	}
	if n > uint64(r.remaining()) {
		return "", io.ErrUnexpectedEOF
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(r, data); err != nil {
		return "", err
	}
	return string(data), nil
}

func paneHistoryComparableRGBA64(r, g, b, a uint16) paneHistoryColorKey {
	var key paneHistoryColorKey
	key.kind = paneHistoryColorRGBA64
	binary.BigEndian.PutUint16(key.data[0:2], r)
	binary.BigEndian.PutUint16(key.data[2:4], g)
	binary.BigEndian.PutUint16(key.data[4:6], b)
	binary.BigEndian.PutUint16(key.data[6:8], a)
	return key
}

func writePaneHistoryUint16(dst *bytes.Buffer, v uint16) {
	var buf [2]byte
	binary.BigEndian.PutUint16(buf[:], v)
	dst.Write(buf[:])
}

func writePaneHistoryString(dst *bytes.Buffer, s string) {
	writePaneHistoryUvarint(dst, uint64(len(s)))
	dst.WriteString(s)
}

func writePaneHistoryUvarint(dst *bytes.Buffer, v uint64) {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], v)
	dst.Write(buf[:n])
}
