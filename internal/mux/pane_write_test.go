package mux

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestWriteAllRetriesShortWrites(t *testing.T) {
	t.Parallel()

	payload := []byte("\U0001f642\u6f22\u5b57 boundary paste payload")
	var got []byte
	var calls int

	n, err := writeAll(payload, func(data []byte) (int, error) {
		calls++
		if len(data) == 0 {
			t.Fatal("writeAll writer called with empty payload")
		}
		wrote := 1 + (calls % 5)
		if wrote > len(data) {
			wrote = len(data)
		}
		got = append(got, data[:wrote]...)
		return wrote, nil
	})
	if err != nil {
		t.Fatalf("writeAll() error = %v", err)
	}
	if n != len(payload) {
		t.Fatalf("writeAll() wrote %d bytes, want %d", n, len(payload))
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("writeAll() payload = %q, want %q", got, payload)
	}
	if calls < 2 {
		t.Fatalf("writeAll() calls = %d, want multiple writes", calls)
	}
}

func TestWriteAllReturnsShortWriteOnZeroProgress(t *testing.T) {
	t.Parallel()

	n, err := writeAll([]byte("abc"), func([]byte) (int, error) {
		return 0, nil
	})
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("writeAll() error = %v, want %v", err, io.ErrShortWrite)
	}
	if n != 0 {
		t.Fatalf("writeAll() wrote %d bytes, want 0", n)
	}
}

func TestWriteAllRejectsInvalidWriteCount(t *testing.T) {
	t.Parallel()

	n, err := writeAll([]byte("abc"), func(data []byte) (int, error) {
		return len(data) + 1, nil
	})
	if err == nil || !strings.Contains(err.Error(), "invalid write count") {
		t.Fatalf("writeAll() error = %v, want invalid write count", err)
	}
	if n != 0 {
		t.Fatalf("writeAll() wrote %d bytes, want 0", n)
	}
}
