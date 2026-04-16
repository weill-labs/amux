package server

import (
	"encoding/json"
	"net"
	"os"
	"testing"
	"time"
)

func mustWriteFile(tb testing.TB, path string, data []byte, perm os.FileMode) {
	tb.Helper()
	if err := os.WriteFile(path, data, perm); err != nil {
		tb.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func mustUnmarshalJSON(tb testing.TB, data []byte, target any) {
	tb.Helper()
	if err := json.Unmarshal(data, target); err != nil {
		tb.Fatalf("Unmarshal() error = %v", err)
	}
}

func mustSetReadDeadline(tb testing.TB, conn net.Conn, deadline time.Time) {
	tb.Helper()
	if err := conn.SetReadDeadline(deadline); err != nil {
		tb.Fatalf("SetReadDeadline() error = %v", err)
	}
}
