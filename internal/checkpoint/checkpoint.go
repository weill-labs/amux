// Package checkpoint handles serialization and deserialization of server state
// for hot-reload via syscall.Exec. The checkpoint captures layout, pane metadata,
// FD numbers, and screen state so the new binary can reconstruct the session.
package checkpoint

import (
	"encoding/gob"
	"fmt"
	"os"
	"time"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

// PaneCheckpoint captures the state of a single pane for reload.
type PaneCheckpoint struct {
	ID           uint32
	Meta         mux.PaneMeta
	ManualBranch bool // preserve whether GitBranch was set manually
	PtmxFd       int  // PTY master FD number (inherited across exec); -1 for proxy panes
	PID          int  // Shell process PID (for waitLoop); 0 for proxy panes
	Cols         int
	Rows         int
	History      []string  // retained scrollback lines (oldest first)
	Screen       string    // RenderScreen() ANSI output for replay
	CreatedAt    time.Time // Pane creation time (preserved across reloads)
	IsProxy      bool      // true for remote proxy panes (no PTY/process)
}

// ServerCheckpoint captures the full server state for reload.
type ServerCheckpoint struct {
	SessionName   string
	Counter       uint32
	WindowCounter uint32
	Generation    uint64 // layout generation counter (survives reload)
	ListenerFd    int
	Layout        proto.LayoutSnapshot
	Panes         []PaneCheckpoint
}

// Write gob-encodes the checkpoint to a temp file and returns the path.
func Write(cp *ServerCheckpoint) (string, error) {
	f, err := os.CreateTemp("", "amux-checkpoint-*.gob")
	if err != nil {
		return "", fmt.Errorf("creating checkpoint file: %w", err)
	}
	defer f.Close()

	if err := gob.NewEncoder(f).Encode(cp); err != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("encoding checkpoint: %w", err)
	}

	return f.Name(), nil
}

// Read decodes a checkpoint from the given path and deletes the file.
func Read(path string) (*ServerCheckpoint, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening checkpoint: %w", err)
	}
	defer f.Close()
	defer os.Remove(path)

	var cp ServerCheckpoint
	if err := gob.NewDecoder(f).Decode(&cp); err != nil {
		return nil, fmt.Errorf("decoding checkpoint: %w", err)
	}

	return &cp, nil
}
