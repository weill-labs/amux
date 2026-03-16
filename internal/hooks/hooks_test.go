package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRegistryAddAndList(t *testing.T) {
	r := NewRegistry()
	r.Add(OnIdle, "echo idle")
	r.Add(OnActivity, "echo active")
	r.Add(OnIdle, "echo idle2")

	hooks := r.List(OnIdle)
	if len(hooks) != 2 {
		t.Fatalf("expected 2 on-idle hooks, got %d", len(hooks))
	}
	if hooks[0].Command != "echo idle" {
		t.Errorf("expected 'echo idle', got %q", hooks[0].Command)
	}
	if hooks[1].Command != "echo idle2" {
		t.Errorf("expected 'echo idle2', got %q", hooks[1].Command)
	}

	hooks = r.List(OnActivity)
	if len(hooks) != 1 {
		t.Fatalf("expected 1 on-activity hook, got %d", len(hooks))
	}
}

func TestRegistryRemove(t *testing.T) {
	r := NewRegistry()
	r.Add(OnIdle, "echo a")
	r.Add(OnIdle, "echo b")
	r.Add(OnIdle, "echo c")

	r.Remove(OnIdle, 1) // remove "echo b" (0-indexed)

	hooks := r.List(OnIdle)
	if len(hooks) != 2 {
		t.Fatalf("expected 2 hooks after remove, got %d", len(hooks))
	}
	if hooks[0].Command != "echo a" || hooks[1].Command != "echo c" {
		t.Errorf("unexpected hooks: %v", hooks)
	}
}

func TestRegistryRemoveAll(t *testing.T) {
	r := NewRegistry()
	r.Add(OnIdle, "echo a")
	r.Add(OnIdle, "echo b")
	r.Add(OnActivity, "echo c")

	r.RemoveAll(OnIdle)

	if len(r.List(OnIdle)) != 0 {
		t.Error("expected 0 on-idle hooks after RemoveAll")
	}
	if len(r.List(OnActivity)) != 1 {
		t.Error("on-activity hooks should be unaffected")
	}
}

func TestRegistryListAll(t *testing.T) {
	r := NewRegistry()
	r.Add(OnIdle, "echo idle")
	r.Add(OnActivity, "echo active")

	all := r.ListAll()
	if len(all) != 2 {
		t.Fatalf("expected 2 total hooks, got %d", len(all))
	}
}

func TestFireExecutesCommand(t *testing.T) {
	r := NewRegistry()
	tmp := t.TempDir()
	marker := filepath.Join(tmp, "fired")

	r.Add(OnIdle, "touch "+marker)

	env := map[string]string{
		"AMUX_PANE_ID":   "1",
		"AMUX_PANE_NAME": "pane-1",
	}
	r.Fire(OnIdle, env)

	// Wait for async execution
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			return // success
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("hook command did not execute within timeout")
}

func TestFirePassesEnvVars(t *testing.T) {
	r := NewRegistry()
	tmp := t.TempDir()
	outFile := filepath.Join(tmp, "env")

	r.Add(OnIdle, "env > "+outFile)

	env := map[string]string{
		"AMUX_PANE_ID":   "42",
		"AMUX_PANE_NAME": "test-pane",
	}
	r.Fire(OnIdle, env)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(outFile)
		if err == nil && len(data) > 0 {
			content := string(data)
			if !strings.Contains(content, "AMUX_PANE_ID=42") {
				t.Errorf("missing AMUX_PANE_ID=42 in env output")
			}
			if !strings.Contains(content, "AMUX_PANE_NAME=test-pane") {
				t.Errorf("missing AMUX_PANE_NAME=test-pane in env output")
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("hook env output not written within timeout")
}

func TestFireNoHooks(t *testing.T) {
	r := NewRegistry()
	// Should not panic
	r.Fire(OnIdle, nil)
}

func TestRemoveOutOfBounds(t *testing.T) {
	r := NewRegistry()
	r.Add(OnIdle, "echo a")

	// These should not panic or corrupt state
	r.Remove(OnIdle, -1)
	r.Remove(OnIdle, 99)
	r.Remove(OnActivity, 0) // no hooks for this event

	hooks := r.List(OnIdle)
	if len(hooks) != 1 || hooks[0].Command != "echo a" {
		t.Errorf("hooks should be unchanged after out-of-bounds removes, got %v", hooks)
	}
}

func TestFireFailingCommandLogsError(t *testing.T) {
	// Capture stderr by temporarily replacing os.Stderr with a pipe
	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating pipe: %v", err)
	}
	os.Stderr = w

	reg := NewRegistry()
	reg.Add(OnIdle, "/nonexistent/binary/that/does/not/exist")
	reg.Fire(OnIdle, nil)

	// Wait for async goroutine to write to stderr
	deadline := time.Now().Add(3 * time.Second)
	done := make(chan string, 1)
	go func() {
		buf := make([]byte, 4096)
		n, _ := r.Read(buf)
		done <- string(buf[:n])
	}()

	var output string
	select {
	case output = <-done:
	case <-time.After(time.Until(deadline)):
		t.Fatal("timed out waiting for stderr output")
	}

	// Restore stderr before any assertions (t.Error writes to stderr)
	w.Close()
	os.Stderr = origStderr
	r.Close()

	if !strings.Contains(output, "hook") || !strings.Contains(output, "failed") {
		t.Errorf("expected error log containing 'hook' and 'failed', got: %q", output)
	}
}

func TestParseEvent(t *testing.T) {
	tests := []struct {
		input string
		want  Event
		ok    bool
	}{
		{"on-idle", OnIdle, true},
		{"on-activity", OnActivity, true},
		{"invalid", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, err := ParseEvent(tt.input)
		if tt.ok && err != nil {
			t.Errorf("ParseEvent(%q): unexpected error: %v", tt.input, err)
		}
		if !tt.ok && err == nil {
			t.Errorf("ParseEvent(%q): expected error", tt.input)
		}
		if got != tt.want {
			t.Errorf("ParseEvent(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
