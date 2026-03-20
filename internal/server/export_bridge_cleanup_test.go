package server

import (
	"errors"
	"io/fs"
	"os"
	"testing"
)

func TestExportTestBridgeRemoved(t *testing.T) {
	t.Parallel()

	_, err := os.Stat("export_test.go")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("export_test.go still exists; move bridge-dependent tests into package server and delete the bridge")
	}
}
