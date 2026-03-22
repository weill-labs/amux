package capture

import (
	"reflect"
	"testing"

	"github.com/weill-labs/amux/internal/proto"
)

func TestRewrapHistoryBuffer(t *testing.T) {
	t.Parallel()

	t.Run("rewraps live history and content but keeps base history raw", func(t *testing.T) {
		t.Parallel()

		base := []string{"persisted-narrow-fragment"}
		live := []HistoryLine{
			{Text: "01234567890123456789", SourceWidth: 20, Filled: true},
			{Text: "abcdefghij", SourceWidth: 20},
			{Text: "short hard break", SourceWidth: 20},
		}
		content := []HistoryLine{
			{Text: "01234567890123456789", SourceWidth: 20, Filled: true},
			{Text: "abcdefghij", SourceWidth: 20},
			{Text: "", SourceWidth: 20},
		}

		got := RewrapHistoryBuffer(base, live, content, proto.CaptureCursor{Row: 1, Col: 6}, 80)

		if want := []string{
			"persisted-narrow-fragment",
			"01234567890123456789abcdefghij",
			"short hard break",
		}; !reflect.DeepEqual(got.History, want) {
			t.Fatalf("History = %#v, want %#v", got.History, want)
		}
		if want := []string{"01234567890123456789abcdefghij", ""}; !reflect.DeepEqual(got.Content, want) {
			t.Fatalf("Content = %#v, want %#v", got.Content, want)
		}
		if got.Cursor.Row != 0 || got.Cursor.Col != 26 {
			t.Fatalf("Cursor = (%d,%d), want (0,26)", got.Cursor.Row, got.Cursor.Col)
		}
	})

	t.Run("respects mixed source widths across live history segments", func(t *testing.T) {
		t.Parallel()

		live := []HistoryLine{
			{Text: "01234567890123456789", SourceWidth: 20, Filled: true},
			{Text: "abcdefghij", SourceWidth: 20},
			{Text: "ABCDEFGHIJ", SourceWidth: 10, Filled: true},
			{Text: "KLMNOPQRST", SourceWidth: 10, Filled: true},
			{Text: "tail", SourceWidth: 10},
		}

		got := RewrapHistoryBuffer(nil, live, nil, proto.CaptureCursor{}, 80)
		want := []string{
			"01234567890123456789abcdefghij",
			"ABCDEFGHIJKLMNOPQRSTtail",
		}
		if !reflect.DeepEqual(got.History, want) {
			t.Fatalf("History = %#v, want %#v", got.History, want)
		}
	})

	t.Run("rewraps unicode content with grapheme widths", func(t *testing.T) {
		t.Parallel()

		content := []HistoryLine{
			{Text: "界界界界界", SourceWidth: 10, Filled: true},
			{Text: "界界界界界", SourceWidth: 10},
		}

		got := RewrapHistoryBuffer(nil, nil, content, proto.CaptureCursor{Row: 1, Col: 10}, 12)
		want := []string{"界界界界界界", "界界界界"}
		if !reflect.DeepEqual(got.Content, want) {
			t.Fatalf("Content = %#v, want %#v", got.Content, want)
		}
		if got.Cursor.Row != 1 || got.Cursor.Col != 8 {
			t.Fatalf("Cursor = (%d,%d), want (1,8)", got.Cursor.Row, got.Cursor.Col)
		}
	})

	t.Run("returns zero cursor for empty buffers", func(t *testing.T) {
		t.Parallel()

		got := RewrapHistoryBuffer(nil, nil, nil, proto.CaptureCursor{Row: 9, Col: 9}, 80)
		if got.Cursor.Row != 0 || got.Cursor.Col != 0 {
			t.Fatalf("Cursor = (%d,%d), want (0,0)", got.Cursor.Row, got.Cursor.Col)
		}
		if len(got.History) != 0 || len(got.Content) != 0 {
			t.Fatalf("History/Content = %#v/%#v, want empty", got.History, got.Content)
		}
	})

	t.Run("keeps logical lines unwrapped when target width is disabled", func(t *testing.T) {
		t.Parallel()

		content := []HistoryLine{
			{Text: "alpha", SourceWidth: 5},
			{Text: "beta", SourceWidth: 4},
		}

		got := RewrapHistoryBuffer(nil, nil, content, proto.CaptureCursor{Row: 1, Col: 2}, 0)
		if want := []string{"alpha", "beta"}; !reflect.DeepEqual(got.Content, want) {
			t.Fatalf("Content = %#v, want %#v", got.Content, want)
		}
		if got.Cursor.Row != 1 || got.Cursor.Col != 2 {
			t.Fatalf("Cursor = (%d,%d), want (1,2)", got.Cursor.Row, got.Cursor.Col)
		}
	})

	t.Run("counts prior wrapped content rows when remapping the cursor", func(t *testing.T) {
		t.Parallel()

		content := []HistoryLine{
			{Text: "abcdef", SourceWidth: 6},
			{Text: "gh", SourceWidth: 2},
		}

		got := RewrapHistoryBuffer(nil, nil, content, proto.CaptureCursor{Row: 1, Col: 1}, 3)
		if want := []string{"abc", "def", "gh"}; !reflect.DeepEqual(got.Content, want) {
			t.Fatalf("Content = %#v, want %#v", got.Content, want)
		}
		if got.Cursor.Row != 2 || got.Cursor.Col != 1 {
			t.Fatalf("Cursor = (%d,%d), want (2,1)", got.Cursor.Row, got.Cursor.Col)
		}
	})

	t.Run("counts empty prior content rows as one line when remapping the cursor", func(t *testing.T) {
		t.Parallel()

		content := []HistoryLine{
			{Text: "", SourceWidth: 4},
			{Text: "next", SourceWidth: 4},
		}

		got := RewrapHistoryBuffer(nil, nil, content, proto.CaptureCursor{Row: 1, Col: 0}, 3)
		if want := []string{"", "nex", "t"}; !reflect.DeepEqual(got.Content, want) {
			t.Fatalf("Content = %#v, want %#v", got.Content, want)
		}
		if got.Cursor.Row != 1 || got.Cursor.Col != 0 {
			t.Fatalf("Cursor = (%d,%d), want (1,0)", got.Cursor.Row, got.Cursor.Col)
		}
	})
}
