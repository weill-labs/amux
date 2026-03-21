package checkpoint

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

// CrashVersion is the current crash checkpoint format version.
// Increment when the format changes in a backward-incompatible way.
const CrashVersion = 1

// CrashCheckpoint captures the full server state for crash recovery.
// Unlike ServerCheckpoint (gob, ephemeral, includes FDs/PIDs), this is
// JSON-encoded and persists across crashes. FDs and PIDs are omitted
// because they cannot survive a process crash.
type CrashCheckpoint struct {
	Version       int                  `json:"version"`
	SessionName   string               `json:"session_name"`
	Counter       uint32               `json:"counter"`
	WindowCounter uint32               `json:"window_counter"`
	Generation    uint64               `json:"generation"`
	Layout        proto.LayoutSnapshot `json:"layout"`
	PaneStates    []CrashPaneState     `json:"pane_states"`
	Timestamp     time.Time            `json:"timestamp"`
}

// CrashPaneState captures one pane's state for crash recovery.
type CrashPaneState struct {
	ID        uint32       `json:"id"`
	Meta      mux.PaneMeta `json:"meta"`
	Cols      int          `json:"cols"`
	Rows      int          `json:"rows"`
	History   []string     `json:"history,omitempty"`
	Screen    string       `json:"screen"`
	WasIdle   bool         `json:"was_idle,omitempty"`
	Command   string       `json:"current_command,omitempty"`
	CreatedAt time.Time    `json:"created_at"`
	IsProxy   bool         `json:"is_proxy"`
	Cwd       string       `json:"cwd,omitempty"`
}

// CrashCheckpointDir returns the directory for crash checkpoint files.
// Defaults to ~/.local/state/amux/, respects $XDG_STATE_HOME.
func CrashCheckpointDir() string {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "amux")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "amux")
}

// CrashCheckpointPathTimestamped returns the full path for a session's crash
// checkpoint, namespaced by the session start time.
func CrashCheckpointPathTimestamped(session string, startTime time.Time) string {
	return filepath.Join(
		CrashCheckpointDir(),
		fmt.Sprintf("%s_%s.json", startTime.Format("20060102-150405"), session),
	)
}

// WriteCrash atomically writes a crash checkpoint to disk.
// Uses temp file + rename for atomicity (no partial writes on crash).
func WriteCrash(cp *CrashCheckpoint, session string, startTime time.Time) error {
	dir := CrashCheckpointDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating checkpoint dir: %w", err)
	}

	data, err := json.Marshal(cp)
	if err != nil {
		return fmt.Errorf("encoding crash checkpoint: %w", err)
	}

	// Write to temp file in the same directory (same filesystem for atomic rename)
	tmp, err := os.CreateTemp(dir, ".crash-*.json.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("writing crash checkpoint: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("closing temp file: %w", err)
	}

	// Atomic rename
	dest := CrashCheckpointPathTimestamped(session, startTime)
	if err := os.Rename(tmpPath, dest); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming crash checkpoint: %w", err)
	}

	return nil
}

// ReadCrash reads and validates a crash checkpoint from the given path.
// Unlike the hot-reload Read(), this does NOT delete the file — the caller
// is responsible for calling RemoveCrashFile() after successful recovery.
func ReadCrash(path string) (*CrashCheckpoint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading crash checkpoint: %w", err)
	}

	var cp CrashCheckpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("decoding crash checkpoint: %w", err)
	}

	if cp.Version != CrashVersion {
		return nil, fmt.Errorf("unsupported crash checkpoint version %d (want %d)", cp.Version, CrashVersion)
	}

	return &cp, nil
}

// FindCrashCheckpoints returns all crash checkpoint files for the given
// session, sorted newest-first by timestamped filename.
func FindCrashCheckpoints(session string) []string {
	dir := CrashCheckpointDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	suffix := "_" + session + ".json"
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), suffix) {
			continue
		}
		paths = append(paths, filepath.Join(dir, entry.Name()))
	}
	sort.Sort(sort.Reverse(sort.StringSlice(paths)))
	return paths
}

// RemoveCrashFile deletes the crash checkpoint file at the given path.
func RemoveCrashFile(path string) error {
	return os.Remove(path)
}
