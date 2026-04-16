package client

import (
	"encoding/json"
	"testing"
)

func mustUnmarshalJSON(tb testing.TB, data []byte, target any) {
	tb.Helper()
	if err := json.Unmarshal(data, target); err != nil {
		tb.Fatalf("Unmarshal() error = %v", err)
	}
}

func mustWrite(tb testing.TB, writer interface{ Write([]byte) (int, error) }, data []byte) {
	tb.Helper()
	if _, err := writer.Write(data); err != nil {
		tb.Fatalf("Write() error = %v", err)
	}
}
