package checkpoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteFailsWhenTempDirUnavailable(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	t.Setenv("TMPDIR", missing)

	if _, err := Write(&ServerCheckpoint{SessionName: "broken"}); err == nil {
		t.Fatal("Write() succeeded with an unavailable temp dir")
	} else if !strings.Contains(err.Error(), "creating checkpoint file") {
		t.Fatalf("Write() error = %q, want create-temp context", err)
	}
}

func TestReadRejectsCorruptGobAndDeletesFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "corrupt.gob")
	if err := os.WriteFile(path, []byte("not a gob"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := Read(path); err == nil {
		t.Fatal("Read() succeeded for corrupt gob")
	} else if !strings.Contains(err.Error(), "decoding checkpoint") {
		t.Fatalf("Read() error = %q, want decode context", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("checkpoint file should be removed after failed read, stat err = %v", err)
	}
}

func TestWriteCrashFailsWhenStateHomeIsAFile(t *testing.T) {
	stateHome := filepath.Join(t.TempDir(), "state-home-file")
	if err := os.WriteFile(stateHome, []byte("not a directory"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("XDG_STATE_HOME", stateHome)

	err := WriteCrash(&CrashCheckpoint{
		Version:     CrashVersion,
		SessionName: "broken",
		Timestamp:   time.Now(),
	}, "broken", time.Unix(1, 0))
	if err == nil {
		t.Fatal("WriteCrash() succeeded when XDG_STATE_HOME points at a file")
	}
	if !strings.Contains(err.Error(), "creating checkpoint dir") {
		t.Fatalf("WriteCrash() error = %q, want mkdir context", err)
	}
}

func TestWriteCrashFailsWhenCheckpointDirIsNotWritable(t *testing.T) {
	stateHome := filepath.Join(t.TempDir(), "state-home")
	checkpointDir := filepath.Join(stateHome, "amux")
	if err := os.MkdirAll(checkpointDir, 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Chmod(checkpointDir, 0500); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(checkpointDir, 0700)
	})
	t.Setenv("XDG_STATE_HOME", stateHome)

	err := WriteCrash(&CrashCheckpoint{
		Version:     CrashVersion,
		SessionName: "broken",
		Timestamp:   time.Now(),
	}, "broken", time.Unix(2, 0))
	if err == nil {
		t.Fatal("WriteCrash() succeeded with a non-writable checkpoint dir")
	}
	if !strings.Contains(err.Error(), "creating temp file") {
		t.Fatalf("WriteCrash() error = %q, want create-temp context", err)
	}
}

func TestWriteCrashFailsWhenDestinationPathNeedsMissingParent(t *testing.T) {
	stateHome := filepath.Join(t.TempDir(), "state-home")
	t.Setenv("XDG_STATE_HOME", stateHome)

	err := WriteCrash(&CrashCheckpoint{
		Version:     CrashVersion,
		SessionName: "nested/session",
		Timestamp:   time.Now(),
	}, "nested/session", time.Unix(3, 0))
	if err == nil {
		t.Fatal("WriteCrash() succeeded with a session name that needs a missing parent dir")
	}
	if !strings.Contains(err.Error(), "renaming crash checkpoint") {
		t.Fatalf("WriteCrash() error = %q, want rename context", err)
	}

	matches, globErr := filepath.Glob(filepath.Join(CrashCheckpointDir(), ".crash-*.json.tmp"))
	if globErr != nil {
		t.Fatalf("Glob: %v", globErr)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary crash files should be cleaned up after rename failure, got %v", matches)
	}
}

func TestReadCrashRejectsCorruptJSON(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "corrupt.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"session_name":`), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := ReadCrash(path); err == nil {
		t.Fatal("ReadCrash() succeeded for corrupt json")
	} else if !strings.Contains(err.Error(), "decoding crash checkpoint") {
		t.Fatalf("ReadCrash() error = %q, want decode context", err)
	}
}

func TestFindCrashCheckpointsMissingDirectory(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "missing-state-home"))

	if got := FindCrashCheckpoints("main"); got != nil {
		t.Fatalf("FindCrashCheckpoints() = %v, want nil when state dir is absent", got)
	}
}
