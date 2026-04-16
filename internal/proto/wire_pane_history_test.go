package proto

import (
	"bytes"
	"encoding/binary"
	"image/color"
	"reflect"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

const testWireFormatPaneHistory byte = 0x02

const (
	testPaneHistoryFlagHistoryFromStyledText byte = 1 << iota
)

const (
	testPaneHistoryLineFlagTextFromCells byte = 1 << iota
	testPaneHistoryLineFlagHasCells
)

const (
	testPaneHistoryColorNil byte = iota
	testPaneHistoryColorBasic
	testPaneHistoryColorIndexed
	testPaneHistoryColorRGB
	testPaneHistoryColorRGBA
	testPaneHistoryColorRGBA64
)

func TestReadMsgPaneHistoryBinaryFrameWithStyledCells(t *testing.T) {
	t.Parallel()

	msg := &Message{
		Type:   MsgTypePaneHistory,
		PaneID: 42,
		History: []string{
			"plain line",
			"styled line",
		},
		StyledHistory: []StyledLine{
			{Text: "plain line"},
			{
				Text: "styled line",
				Cells: []Cell{
					{Char: "s", Width: 1, Style: uv.Style{Fg: ansi.Red}},
					{Char: "t", Width: 1, Style: uv.Style{Fg: ansi.Red}},
					{Char: "y", Width: 1, Style: uv.Style{Fg: ansi.IndexedColor(42), Bg: ansi.RGBColor{R: 0xff, G: 0x88}}},
				},
			},
		},
	}

	got, err := ReadMsg(bytes.NewReader(encodeTestPaneHistoryFrame(t, msg)))
	if err != nil {
		t.Fatalf("ReadMsg: %v", err)
	}

	if !reflect.DeepEqual(got, msg) {
		t.Fatalf("round-trip mismatch:\n got: %#v\nwant: %#v", got, msg)
	}
}

func TestReaderReadMsgMixedStreamWithPaneHistoryBinaryFrame(t *testing.T) {
	t.Parallel()

	msgs := []*Message{
		{Type: MsgTypeLayout, Layout: sampleLayoutSnapshot()},
		{
			Type:   MsgTypePaneHistory,
			PaneID: 7,
			History: []string{
				"bootstrap",
				"history",
			},
			StyledHistory: []StyledLine{
				{Text: "bootstrap"},
				{
					Text: "history",
					Cells: []Cell{
						{Char: "h", Width: 1, Style: uv.Style{Fg: ansi.BasicColor(3)}},
						{Char: "i", Width: 1, Style: uv.Style{Fg: ansi.BasicColor(3)}},
						{Char: "s", Width: 1, Style: uv.Style{Fg: ansi.BasicColor(3)}},
						{Char: "t", Width: 1, Style: uv.Style{Fg: ansi.BasicColor(3)}},
						{Char: "o", Width: 1, Style: uv.Style{Fg: ansi.BasicColor(3)}},
						{Char: "r", Width: 1, Style: uv.Style{Fg: ansi.BasicColor(3)}},
						{Char: "y", Width: 1, Style: uv.Style{Fg: ansi.BasicColor(3)}},
					},
				},
			},
		},
		{Type: MsgTypeCommand, CmdName: "list", CmdArgs: []string{"--json"}},
	}

	var wire bytes.Buffer
	if err := WriteMsg(&wire, msgs[0]); err != nil {
		t.Fatalf("WriteMsg layout: %v", err)
	}
	if _, err := wire.Write(encodeTestPaneHistoryFrame(t, msgs[1])); err != nil {
		t.Fatalf("write pane history frame: %v", err)
	}
	if err := WriteMsg(&wire, msgs[2]); err != nil {
		t.Fatalf("WriteMsg command: %v", err)
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

func TestWriterWriteMsgPaneHistoryUsesBinaryFrameWhenEnabled(t *testing.T) {
	t.Parallel()

	msg := &Message{
		Type:   MsgTypePaneHistory,
		PaneID: 5,
		History: []string{
			"one",
			"two",
		},
		StyledHistory: []StyledLine{
			{Text: "one"},
			{Text: "two"},
		},
	}

	var buf bytes.Buffer
	writer := NewWriter(&buf)
	enableWriterPaneHistoryBinary(t, writer)
	if err := writer.WriteMsg(msg); err != nil {
		t.Fatalf("WriteMsg: %v", err)
	}

	raw := buf.Bytes()
	if len(raw) < 9 {
		t.Fatalf("encoded length = %d, want at least 9", len(raw))
	}
	if raw[0] != testWireFormatPaneHistory {
		t.Fatalf("discriminator = %#x, want %#x", raw[0], testWireFormatPaneHistory)
	}

	got, err := ReadMsg(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadMsg: %v", err)
	}
	if !reflect.DeepEqual(got, msg) {
		t.Fatalf("round-trip mismatch:\n got: %#v\nwant: %#v", got, msg)
	}
}

func enableWriterPaneHistoryBinary(t *testing.T, writer *Writer) {
	t.Helper()

	field := reflect.ValueOf(writer).Elem().FieldByName("binaryPaneHistory")
	if !field.IsValid() {
		t.Fatal("Writer.binaryPaneHistory field not found")
	}
	if !field.CanSet() || field.Kind() != reflect.Bool {
		t.Fatal("Writer.binaryPaneHistory field is not a settable bool")
	}
	field.SetBool(true)
}

func encodeTestPaneHistoryFrame(t *testing.T, msg *Message) []byte {
	t.Helper()

	payload := encodeTestPaneHistoryPayload(t, msg)
	var hdr [9]byte
	hdr[0] = testWireFormatPaneHistory
	binary.BigEndian.PutUint32(hdr[1:5], msg.PaneID)
	binary.BigEndian.PutUint32(hdr[5:9], uint32(len(payload)))

	var frame bytes.Buffer
	frame.Write(hdr[:])
	frame.Write(payload)
	return frame.Bytes()
}

func encodeTestPaneHistoryPayload(t *testing.T, msg *Message) []byte {
	t.Helper()

	var payload bytes.Buffer
	flags := byte(0)
	if testHistoryMatchesStyledText(msg.History, msg.StyledHistory) {
		flags |= testPaneHistoryFlagHistoryFromStyledText
	}
	payload.WriteByte(flags)
	writeTestUvarint(&payload, uint64(len(msg.History)))
	writeTestUvarint(&payload, uint64(len(msg.StyledHistory)))

	if flags&testPaneHistoryFlagHistoryFromStyledText == 0 {
		for _, line := range msg.History {
			writeTestString(&payload, line)
		}
	}

	styleTable, styleIndex := buildTestStyleTable(msg.StyledHistory)
	writeTestUvarint(&payload, uint64(len(styleTable)))
	for _, style := range styleTable {
		writeTestStyle(t, &payload, style)
	}

	for _, line := range msg.StyledHistory {
		lineFlags := byte(0)
		if len(line.Cells) > 0 {
			lineFlags |= testPaneHistoryLineFlagHasCells
			if testCellsText(line.Cells) == line.Text {
				lineFlags |= testPaneHistoryLineFlagTextFromCells
			}
		}
		payload.WriteByte(lineFlags)
		if lineFlags&testPaneHistoryLineFlagTextFromCells == 0 {
			writeTestString(&payload, line.Text)
		}
		if lineFlags&testPaneHistoryLineFlagHasCells == 0 {
			continue
		}

		runs := testCellRuns(line.Cells)
		writeTestUvarint(&payload, uint64(len(runs)))
		for _, run := range runs {
			writeTestUvarint(&payload, uint64(run.count))
			writeTestString(&payload, run.cell.Char)
			writeTestUvarint(&payload, uint64(run.cell.Width))
			writeTestUvarint(&payload, uint64(styleIndex[testStyleKey(run.cell.Style)]))
		}
	}

	return payload.Bytes()
}

func writeTestStyle(t *testing.T, dst *bytes.Buffer, style uv.Style) {
	t.Helper()

	writeTestColor(t, dst, style.Fg)
	writeTestColor(t, dst, style.Bg)
	writeTestColor(t, dst, style.UnderlineColor)
	dst.WriteByte(byte(style.Underline))
	dst.WriteByte(style.Attrs)
}

func writeTestColor(t *testing.T, dst *bytes.Buffer, c color.Color) {
	t.Helper()

	switch v := c.(type) {
	case nil:
		dst.WriteByte(testPaneHistoryColorNil)
	case ansi.BasicColor:
		dst.WriteByte(testPaneHistoryColorBasic)
		dst.WriteByte(byte(v))
	case ansi.IndexedColor:
		dst.WriteByte(testPaneHistoryColorIndexed)
		dst.WriteByte(byte(v))
	case ansi.RGBColor:
		dst.WriteByte(testPaneHistoryColorRGB)
		dst.WriteByte(v.R)
		dst.WriteByte(v.G)
		dst.WriteByte(v.B)
	case color.RGBA:
		dst.WriteByte(testPaneHistoryColorRGBA)
		dst.WriteByte(v.R)
		dst.WriteByte(v.G)
		dst.WriteByte(v.B)
		dst.WriteByte(v.A)
	case color.RGBA64:
		dst.WriteByte(testPaneHistoryColorRGBA64)
		writeTestUint16(dst, v.R)
		writeTestUint16(dst, v.G)
		writeTestUint16(dst, v.B)
		writeTestUint16(dst, v.A)
	default:
		r, g, b, a := v.RGBA()
		dst.WriteByte(testPaneHistoryColorRGBA64)
		writeTestUint16(dst, uint16(r))
		writeTestUint16(dst, uint16(g))
		writeTestUint16(dst, uint16(b))
		writeTestUint16(dst, uint16(a))
	}
}

func writeTestUint16(dst *bytes.Buffer, v uint16) {
	var buf [2]byte
	binary.BigEndian.PutUint16(buf[:], v)
	dst.Write(buf[:])
}

func writeTestString(dst *bytes.Buffer, s string) {
	writeTestUvarint(dst, uint64(len(s)))
	dst.WriteString(s)
}

func writeTestUvarint(dst *bytes.Buffer, v uint64) {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], v)
	dst.Write(buf[:n])
}

type testCellRun struct {
	count int
	cell  Cell
}

func testCellRuns(cells []Cell) []testCellRun {
	if len(cells) == 0 {
		return nil
	}

	runs := make([]testCellRun, 0, len(cells))
	current := testCellRun{count: 1, cell: cells[0]}
	for _, cell := range cells[1:] {
		if reflect.DeepEqual(cell, current.cell) {
			current.count++
			continue
		}
		runs = append(runs, current)
		current = testCellRun{count: 1, cell: cell}
	}
	runs = append(runs, current)
	return runs
}

func buildTestStyleTable(lines []StyledLine) ([]uv.Style, map[string]int) {
	styles := make([]uv.Style, 0)
	index := make(map[string]int)
	for _, line := range lines {
		for _, cell := range line.Cells {
			key := testStyleKey(cell.Style)
			if _, ok := index[key]; ok {
				continue
			}
			index[key] = len(styles)
			styles = append(styles, cell.Style)
		}
	}
	return styles, index
}

func testStyleKey(style uv.Style) string {
	var b strings.Builder
	b.WriteString(testColorKey(style.Fg))
	b.WriteByte('|')
	b.WriteString(testColorKey(style.Bg))
	b.WriteByte('|')
	b.WriteString(testColorKey(style.UnderlineColor))
	b.WriteByte('|')
	b.WriteByte(byte(style.Underline))
	b.WriteByte('|')
	b.WriteByte(style.Attrs)
	return b.String()
}

func testColorKey(c color.Color) string {
	switch v := c.(type) {
	case nil:
		return "nil"
	case ansi.BasicColor:
		return "basic:" + string([]byte{byte(v)})
	case ansi.IndexedColor:
		return "indexed:" + string([]byte{byte(v)})
	case ansi.RGBColor:
		return "rgb:" + string([]byte{v.R, v.G, v.B})
	case color.RGBA:
		return "rgba:" + string([]byte{v.R, v.G, v.B, v.A})
	case color.RGBA64:
		var buf [8]byte
		binary.BigEndian.PutUint16(buf[0:2], v.R)
		binary.BigEndian.PutUint16(buf[2:4], v.G)
		binary.BigEndian.PutUint16(buf[4:6], v.B)
		binary.BigEndian.PutUint16(buf[6:8], v.A)
		return "rgba64:" + string(buf[:])
	default:
		r, g, b, a := v.RGBA()
		var buf [8]byte
		binary.BigEndian.PutUint16(buf[0:2], uint16(r))
		binary.BigEndian.PutUint16(buf[2:4], uint16(g))
		binary.BigEndian.PutUint16(buf[4:6], uint16(b))
		binary.BigEndian.PutUint16(buf[6:8], uint16(a))
		return "generic:" + string(buf[:])
	}
}

func testHistoryMatchesStyledText(history []string, styled []StyledLine) bool {
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

func testCellsText(cells []Cell) string {
	var b strings.Builder
	for _, cell := range cells {
		b.WriteString(cell.Char)
	}
	return b.String()
}
