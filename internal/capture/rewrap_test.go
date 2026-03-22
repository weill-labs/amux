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
}
