package proto

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image/color"
	"io"
	"reflect"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
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

	if !equalMessages(got, msg) {
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
	writer := NewWriter(&wire)
	if err := writer.WriteMsg(msgs[0]); err != nil {
		t.Fatalf("WriteMsg layout: %v", err)
	}
	if _, err := wire.Write(encodeTestPaneHistoryFrame(t, msgs[1])); err != nil {
		t.Fatalf("write pane history frame: %v", err)
	}
	if err := writer.WriteMsg(msgs[2]); err != nil {
		t.Fatalf("WriteMsg command: %v", err)
	}

	reader := NewReader(bytes.NewReader(wire.Bytes()))
	for i, want := range msgs {
		got, err := reader.ReadMsg()
		if err != nil {
			t.Fatalf("ReadMsg(%d): %v", i, err)
		}
		if !equalMessages(got, want) {
			t.Fatalf("message %d mismatch:\n got: %#v\nwant: %#v", i, got, want)
		}
	}
}

func TestReadMsgPaneHistoryBinaryFrameWithExplicitHistory(t *testing.T) {
	t.Parallel()

	msg := &Message{
		Type:   MsgTypePaneHistory,
		PaneID: 19,
		History: []string{
			"plain one",
			"plain two",
		},
		StyledHistory: []StyledLine{
			{
				Text: "styled one",
				Cells: []Cell{
					{Char: "s", Width: 1, Style: uv.Style{Fg: ansi.BasicColor(2)}},
					{Char: "1", Width: 1, Style: uv.Style{Fg: ansi.BasicColor(2)}},
				},
			},
			{Text: "styled two"},
		},
	}

	got, err := ReadMsg(bytes.NewReader(encodeTestPaneHistoryFrame(t, msg)))
	if err != nil {
		t.Fatalf("ReadMsg: %v", err)
	}

	if !equalMessages(got, msg) {
		t.Fatalf("round-trip mismatch:\n got: %#v\nwant: %#v", got, msg)
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
	if raw[0] != wireFormatPaneHistory {
		t.Fatalf("discriminator = %#x, want %#x", raw[0], wireFormatPaneHistory)
	}

	got, err := ReadMsg(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadMsg: %v", err)
	}
	if !equalMessages(got, msg) {
		t.Fatalf("round-trip mismatch:\n got: %#v\nwant: %#v", got, msg)
	}
}

func TestPaneHistoryCodecHelpers(t *testing.T) {
	t.Parallel()

	t.Run("matches styled text", func(t *testing.T) {
		t.Parallel()

		styled := []StyledLine{{Text: "one"}, {Text: "two"}}
		if paneHistoryMatchesStyledText(nil, styled) {
			t.Fatal("paneHistoryMatchesStyledText(nil, styled) = true, want false")
		}
		if paneHistoryMatchesStyledText([]string{"one"}, styled) {
			t.Fatal("paneHistoryMatchesStyledText(len mismatch) = true, want false")
		}
		if paneHistoryMatchesStyledText([]string{"one", "mismatch"}, styled) {
			t.Fatal("paneHistoryMatchesStyledText(text mismatch) = true, want false")
		}
		if !paneHistoryMatchesStyledText([]string{"one", "two"}, styled) {
			t.Fatal("paneHistoryMatchesStyledText(match) = false, want true")
		}
	})

	t.Run("style table and cell runs normalize equivalent colors", func(t *testing.T) {
		t.Parallel()

		base := uv.Style{
			Fg:             color.RGBA64{R: 0x0102, G: 0x0304, B: 0x0506, A: 0x0708},
			Bg:             ansi.BasicColor(4),
			UnderlineColor: color.RGBA{R: 0x11, G: 0x22, B: 0x33, A: 0x44},
			Underline:      uv.Underline(2),
			Attrs:          uv.AttrBold,
		}
		equivalent := uv.Style{
			Fg:             testCustomColor{r: 0x0102, g: 0x0304, b: 0x0506, a: 0x0708},
			Bg:             ansi.BasicColor(4),
			UnderlineColor: color.RGBA{R: 0x11, G: 0x22, B: 0x33, A: 0x44},
			Underline:      uv.Underline(2),
			Attrs:          uv.AttrBold,
		}
		other := uv.Style{Fg: ansi.IndexedColor(9)}

		keyBase := paneHistoryComparableStyle(base)
		if keyBase != paneHistoryComparableStyle(equivalent) {
			t.Fatal("paneHistoryComparableStyle should normalize equivalent colors")
		}
		if keyBase == paneHistoryComparableStyle(other) {
			t.Fatal("paneHistoryComparableStyle collapsed distinct styles")
		}

		line := StyledLine{
			Text: "xxy",
			Cells: []Cell{
				{Char: "x", Width: 1, Style: base},
				{Char: "x", Width: 1, Style: equivalent},
				{Char: "y", Width: 1, Style: other},
			},
		}
		styles, index := paneHistoryStyleTable([]StyledLine{line})
		if len(styles) != 2 {
			t.Fatalf("style table length = %d, want 2", len(styles))
		}
		if index[keyBase] != index[paneHistoryComparableStyle(equivalent)] {
			t.Fatal("equivalent styles should share one style index")
		}

		runs := paneHistoryCellRuns(line.Cells)
		if len(runs) != 2 {
			t.Fatalf("run count = %d, want 2", len(runs))
		}
		if runs[0].count != 2 || runs[0].cell.Char != "x" {
			t.Fatalf("first run = %+v, want x repeated twice", runs[0])
		}
		if runs[1].count != 1 || runs[1].cell.Char != "y" {
			t.Fatalf("second run = %+v, want single y", runs[1])
		}
	})

	t.Run("cells text helpers", func(t *testing.T) {
		t.Parallel()

		cells := []Cell{
			{Char: "a", Width: 1},
			{Char: "bc", Width: 1},
		}
		if got := paneHistoryCellsText(cells); got != "abc" {
			t.Fatalf("paneHistoryCellsText() = %q, want abc", got)
		}
		if !paneHistoryCellsTextEqual("abc", cells) {
			t.Fatal("paneHistoryCellsTextEqual(match) = false, want true")
		}
		if paneHistoryCellsTextEqual("ab", cells) {
			t.Fatal("paneHistoryCellsTextEqual(short text) = true, want false")
		}
		if paneHistoryCellsTextEqual("axc", cells) {
			t.Fatal("paneHistoryCellsTextEqual(prefix mismatch) = true, want false")
		}
		if got := paneHistoryCellRuns(nil); got != nil {
			t.Fatalf("paneHistoryCellRuns(nil) = %#v, want nil", got)
		}
	})
}

func TestPaneHistoryStyleRoundTripCoversColorKinds(t *testing.T) {
	t.Parallel()

	styles := []uv.Style{
		{},
		{
			Fg:             ansi.BasicColor(3),
			Bg:             ansi.IndexedColor(42),
			UnderlineColor: ansi.RGBColor{R: 0xaa, G: 0xbb, B: 0xcc},
			Underline:      uv.Underline(1),
			Attrs:          uv.AttrBold,
		},
		{
			Fg:             color.RGBA{R: 0x11, G: 0x22, B: 0x33, A: 0x44},
			Bg:             color.RGBA64{R: 0x0102, G: 0x0304, B: 0x0506, A: 0x0708},
			UnderlineColor: testCustomColor{r: 0x1111, g: 0x2222, b: 0x3333, a: 0x4444},
			Underline:      uv.Underline(2),
			Attrs:          uv.AttrItalic,
		},
	}

	for i, want := range styles {
		var buf bytes.Buffer
		if err := writePaneHistoryStyle(&buf, want); err != nil {
			t.Fatalf("writePaneHistoryStyle(%d): %v", i, err)
		}
		reader := newPaneHistoryReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		got, err := readPaneHistoryStyle(reader)
		if err != nil {
			t.Fatalf("readPaneHistoryStyle(%d): %v", i, err)
		}
		if !equalStylesByRGBA(got, want) {
			t.Fatalf("style %d mismatch:\n got: %#v\nwant: %#v", i, got, want)
		}
	}
}

func TestPaneHistoryBinaryErrorPaths(t *testing.T) {
	t.Parallel()

	t.Run("encode rejects nil message", func(t *testing.T) {
		t.Parallel()

		if _, err := encodePaneHistoryPayload(nil); err == nil || !strings.Contains(err.Error(), "nil message") {
			t.Fatalf("encodePaneHistoryPayload(nil) error = %v, want nil message", err)
		}
	})

	t.Run("encode rejects negative cell width", func(t *testing.T) {
		t.Parallel()

		msg := &Message{
			Type:          MsgTypePaneHistory,
			PaneID:        1,
			History:       []string{"x"},
			StyledHistory: []StyledLine{{Text: "x", Cells: []Cell{{Char: "x", Width: -1}}}},
		}
		if _, err := encodePaneHistoryPayload(msg); err == nil || !strings.Contains(err.Error(), "negative cell width") {
			t.Fatalf("encodePaneHistoryPayload(negative width) error = %v, want negative cell width", err)
		}
	})

	t.Run("writer surfaces header and payload write errors", func(t *testing.T) {
		t.Parallel()

		msg := &Message{
			Type:          MsgTypePaneHistory,
			PaneID:        1,
			History:       []string{"x"},
			StyledHistory: []StyledLine{{Text: "x"}},
		}
		if err := writePaneHistoryBinary(&testFailWriter{failOnCall: 1}, msg); err == nil || !strings.Contains(err.Error(), "writing pane history header") {
			t.Fatalf("header write error = %v, want header failure", err)
		}
		if err := writePaneHistoryBinary(&testFailWriter{failOnCall: 2}, msg); err == nil || !strings.Contains(err.Error(), "writing pane history payload") {
			t.Fatalf("payload write error = %v, want payload failure", err)
		}
	})

	t.Run("binary reader rejects invalid payloads", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			raw  []byte
			want string
		}{
			{
				name: "oversized payload",
				raw:  appendPaneHistoryHeader(nil, 1, maxMessageSize+1),
				want: "message too large",
			},
			{
				name: "trailing bytes",
				raw: func() []byte {
					payload := append(encodeTestPaneHistoryPayload(t, &Message{
						Type:          MsgTypePaneHistory,
						PaneID:        2,
						History:       []string{"x"},
						StyledHistory: []StyledLine{{Text: "x"}},
					}), 0xff)
					return appendPaneHistoryHeader(payload, 2, uint32(len(payload)))
				}(),
				want: "trailing bytes",
			},
		}

		for _, tt := range tests {
			tt := tt
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				if _, err := readPaneHistoryBinary(bytes.NewReader(tt.raw)); err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("readPaneHistoryBinary() error = %v, want substring %q", err, tt.want)
				}
			})
		}
	})

	t.Run("decoder validates history and style references", func(t *testing.T) {
		t.Parallel()

		var mismatch bytes.Buffer
		mismatch.WriteByte(paneHistoryFlagHistoryFromStyledText)
		writePaneHistoryUvarint(&mismatch, 1)
		writePaneHistoryUvarint(&mismatch, 0)
		writePaneHistoryUvarint(&mismatch, 0)
		if _, err := decodePaneHistoryPayload(1, newPaneHistoryReader(bytes.NewReader(mismatch.Bytes()), int64(mismatch.Len())), mismatch.Len()); err == nil || !strings.Contains(err.Error(), "count mismatch") {
			t.Fatalf("decodePaneHistoryPayload(history mismatch) error = %v, want count mismatch", err)
		}

		var badStyleIndex bytes.Buffer
		badStyleIndex.WriteByte(0)
		writePaneHistoryUvarint(&badStyleIndex, 1)
		writePaneHistoryUvarint(&badStyleIndex, 1)
		writePaneHistoryString(&badStyleIndex, "plain")
		writePaneHistoryUvarint(&badStyleIndex, 0)
		badStyleIndex.WriteByte(paneHistoryLineFlagHasCells)
		writePaneHistoryString(&badStyleIndex, "styled")
		writePaneHistoryUvarint(&badStyleIndex, 1)
		writePaneHistoryUvarint(&badStyleIndex, 1)
		writePaneHistoryString(&badStyleIndex, "x")
		writePaneHistoryUvarint(&badStyleIndex, 1)
		writePaneHistoryUvarint(&badStyleIndex, 0)
		if _, err := decodePaneHistoryPayload(1, newPaneHistoryReader(bytes.NewReader(badStyleIndex.Bytes()), int64(badStyleIndex.Len())), badStyleIndex.Len()); err == nil || !strings.Contains(err.Error(), "out of range") {
			t.Fatalf("decodePaneHistoryPayload(style index) error = %v, want out of range", err)
		}
	})

	t.Run("low level readers reject invalid encodings", func(t *testing.T) {
		t.Parallel()

		if _, err := readPaneHistoryColor(newPaneHistoryReader(bytes.NewReader([]byte{0xff}), 1)); err == nil || !strings.Contains(err.Error(), "unknown color encoding") {
			t.Fatalf("readPaneHistoryColor() error = %v, want unknown color encoding", err)
		}

		var countBuf bytes.Buffer
		writePaneHistoryUvarint(&countBuf, 5)
		if _, err := readPaneHistoryCount(newPaneHistoryReader(bytes.NewReader(countBuf.Bytes()), int64(countBuf.Len())), 4, "test count"); err == nil || !strings.Contains(err.Error(), "too large") {
			t.Fatalf("readPaneHistoryCount() error = %v, want too large", err)
		}

		var stringBuf bytes.Buffer
		writePaneHistoryUvarint(&stringBuf, 5)
		stringBuf.WriteByte('x')
		if _, err := readPaneHistoryString(newPaneHistoryReader(bytes.NewReader(stringBuf.Bytes()), int64(stringBuf.Len()))); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("readPaneHistoryString() error = %v, want unexpected EOF", err)
		}
	})
}

func enableWriterPaneHistoryBinary(t *testing.T, writer *Writer) {
	t.Helper()

	if writer == nil {
		t.Fatal("writer is nil")
	}
	writer.SetBinaryPaneHistory(true)
}

func encodeTestPaneHistoryFrame(t *testing.T, msg *Message) []byte {
	t.Helper()

	payload := encodeTestPaneHistoryPayload(t, msg)
	var hdr [9]byte
	hdr[0] = wireFormatPaneHistory
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
		flags |= paneHistoryFlagHistoryFromStyledText
	}
	payload.WriteByte(flags)
	writeTestUvarint(&payload, uint64(len(msg.History)))
	writeTestUvarint(&payload, uint64(len(msg.StyledHistory)))

	if flags&paneHistoryFlagHistoryFromStyledText == 0 {
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
			lineFlags |= paneHistoryLineFlagHasCells
			if testCellsText(line.Cells) == line.Text {
				lineFlags |= paneHistoryLineFlagTextFromCells
			}
		}
		payload.WriteByte(lineFlags)
		if lineFlags&paneHistoryLineFlagTextFromCells == 0 {
			writeTestString(&payload, line.Text)
		}
		if lineFlags&paneHistoryLineFlagHasCells == 0 {
			continue
		}

		runs := testCellRuns(line.Cells)
		writeTestUvarint(&payload, uint64(len(runs)))
		for _, run := range runs {
			writeTestUvarint(&payload, uint64(run.count))
			writeTestString(&payload, run.cell.Char)
			writeTestUvarint(&payload, uint64(run.cell.Width))
			writeTestUvarint(&payload, uint64(styleIndex[paneHistoryComparableStyle(run.cell.Style)]))
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
		writeTestUint16(dst, v.R)
		writeTestUint16(dst, v.G)
		writeTestUint16(dst, v.B)
		writeTestUint16(dst, v.A)
	default:
		r, g, b, a := v.RGBA()
		dst.WriteByte(paneHistoryColorRGBA64)
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
	currentStyleKey := paneHistoryComparableStyle(cells[0].Style)
	for _, cell := range cells[1:] {
		cellStyleKey := paneHistoryComparableStyle(cell.Style)
		if cell.Char == current.cell.Char && cell.Width == current.cell.Width && cellStyleKey == currentStyleKey {
			current.count++
			continue
		}
		runs = append(runs, current)
		current = testCellRun{count: 1, cell: cell}
		currentStyleKey = cellStyleKey
	}
	runs = append(runs, current)
	return runs
}

func buildTestStyleTable(lines []StyledLine) ([]uv.Style, map[paneHistoryStyleKey]int) {
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

func BenchmarkPaneHistoryMessageWire(b *testing.B) {
	msg := benchmarkPaneHistoryMessage(1024, 120)

	b.Run("write/gob", func(b *testing.B) {
		var wire bytes.Buffer
		b.ReportAllocs()
		for b.Loop() {
			wire.Reset()
			if err := WriteMsg(&wire, msg); err != nil {
				b.Fatalf("WriteMsg: %v", err)
			}
		}
	})

	b.Run("write/binary", func(b *testing.B) {
		var wire bytes.Buffer
		writer := NewWriter(&wire)
		writer.SetBinaryPaneHistory(true)
		b.ReportAllocs()
		for b.Loop() {
			wire.Reset()
			if err := writer.WriteMsg(msg); err != nil {
				b.Fatalf("WriteMsg: %v", err)
			}
		}
	})

	b.Run("read/gob", func(b *testing.B) {
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

	b.Run("read/binary", func(b *testing.B) {
		var wire bytes.Buffer
		writer := NewWriter(&wire)
		writer.SetBinaryPaneHistory(true)
		if err := writer.WriteMsg(msg); err != nil {
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
}

func benchmarkPaneHistoryMessage(lineCount, width int) *Message {
	styles := []uv.Style{
		{Fg: ansi.BasicColor(2)},
		{Fg: ansi.BasicColor(6), Attrs: uv.AttrBold},
		{Fg: ansi.RGBColor{R: 0xff, G: 0x88}, Bg: ansi.BasicColor(0)},
	}

	history := make([]string, lineCount)
	styled := make([]StyledLine, lineCount)
	contentWidth := width - 24
	if contentWidth < 1 {
		contentWidth = width
	}
	alphabet := "abcdefghijklmnopqrstuvwxyz0123456789"

	for i := 0; i < lineCount; i++ {
		cells := make([]Cell, width)
		var text strings.Builder
		for x := 0; x < width; x++ {
			style := styles[(i/8+x/24)%len(styles)]
			if x < contentWidth {
				ch := string(alphabet[(i+x)%len(alphabet)])
				cells[x] = Cell{Char: ch, Width: 1, Style: style}
				text.WriteString(ch)
				continue
			}
			cells[x] = Cell{Char: " ", Width: 1, Style: style}
		}
		history[i] = text.String()
		styled[i] = StyledLine{
			Text:  history[i],
			Cells: cells,
		}
	}

	return &Message{
		Type:          MsgTypePaneHistory,
		PaneID:        9,
		History:       history,
		StyledHistory: styled,
	}
}

type testCustomColor struct {
	r uint16
	g uint16
	b uint16
	a uint16
}

func (c testCustomColor) RGBA() (uint32, uint32, uint32, uint32) {
	return uint32(c.r), uint32(c.g), uint32(c.b), uint32(c.a)
}

type testFailWriter struct {
	failOnCall int
	calls      int
}

func (w *testFailWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.calls == w.failOnCall {
		return 0, errors.New("boom")
	}
	return len(p), nil
}

func appendPaneHistoryHeader(payload []byte, paneID uint32, payloadLen uint32) []byte {
	var hdr [8]byte
	binary.BigEndian.PutUint32(hdr[0:4], paneID)
	binary.BigEndian.PutUint32(hdr[4:8], payloadLen)
	return append(hdr[:], payload...)
}

func equalPaneHistoryMessage(got, want *Message) bool {
	if got == nil || want == nil {
		return got == want
	}
	if got.Type != want.Type || got.PaneID != want.PaneID || !equalStringSlices(got.History, want.History) {
		return false
	}
	if len(got.StyledHistory) != len(want.StyledHistory) {
		return false
	}
	for i := range got.StyledHistory {
		if got.StyledHistory[i].Text != want.StyledHistory[i].Text || !equalCells(got.StyledHistory[i].Cells, want.StyledHistory[i].Cells) {
			return false
		}
	}
	return true
}

func equalMessages(got, want *Message) bool {
	if got == nil || want == nil {
		return got == want
	}
	if got.Type != MsgTypePaneHistory || want.Type != MsgTypePaneHistory {
		return reflect.DeepEqual(got, want)
	}
	return equalPaneHistoryMessage(got, want)
}

func equalStringSlices(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func equalCells(got, want []Cell) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i].Char != want[i].Char || got[i].Width != want[i].Width || !equalStylesByRGBA(got[i].Style, want[i].Style) {
			return false
		}
	}
	return true
}

func equalStylesByRGBA(got, want uv.Style) bool {
	return equalColorsByRGBA(got.Fg, want.Fg) &&
		equalColorsByRGBA(got.Bg, want.Bg) &&
		equalColorsByRGBA(got.UnderlineColor, want.UnderlineColor) &&
		got.Underline == want.Underline &&
		got.Attrs == want.Attrs
}

func equalColorsByRGBA(got, want color.Color) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	gr, gg, gb, ga := got.RGBA()
	wr, wg, wb, wa := want.RGBA()
	return gr == wr && gg == wg && gb == wb && ga == wa
}
