package auditlog

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestRotatingFileWriterBoundsTotalLogBytes(t *testing.T) {
	t.Parallel()

	logPath := filepath.Join(t.TempDir(), "main.log")
	const maxBytes int64 = 12
	w, err := NewRotatingFileWriter(logPath, RotationOptions{
		MaxBytes:   maxBytes,
		MaxBackups: 2,
	})
	if err != nil {
		t.Fatalf("NewRotatingFileWriter: %v", err)
	}

	for i := 0; i < 8; i++ {
		if _, err := fmt.Fprintf(w, "entry-%02d\n", i); err != nil {
			t.Fatalf("Write entry %d: %v", i, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	total := totalRegularFileSize(t, logPath, logPath+".1", logPath+".2")
	if total > maxBytes*3 {
		t.Fatalf("total rotated log size = %d, want <= %d", total, maxBytes*3)
	}
	if _, err := os.Stat(logPath + ".3"); !os.IsNotExist(err) {
		t.Fatalf("unexpected third backup stat error = %v", err)
	}
}

func totalRegularFileSize(t *testing.T, paths ...string) int64 {
	t.Helper()

	var total int64
	for _, path := range paths {
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatalf("Stat(%q): %v", path, err)
		}
		if !info.Mode().IsRegular() {
			t.Fatalf("%q mode = %s, want regular file", path, info.Mode())
		}
		total += info.Size()
	}
	return total
}
