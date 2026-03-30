package test

import (
	"encoding/json"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/checkpoint"
)

func decodeJSONMap(t *testing.T, raw string) map[string]any {
	t.Helper()

	var got map[string]any
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("json.Unmarshal: %v\nraw:\n%s", err, raw)
	}
	return got
}

func waitForJSONMap(t *testing.T, timeout time.Duration, capture func() string) map[string]any {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		last = capture()
		var got map[string]any
		if err := json.Unmarshal([]byte(last), &got); err == nil {
			return got
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for JSON capture within %v\nlast output:\n%s", timeout, last)
	return nil
}

func jsonStringValue(t *testing.T, m map[string]any, key string) string {
	t.Helper()

	value, ok := m[key]
	if !ok {
		t.Fatalf("missing %q in %#v", key, m)
	}
	s, ok := value.(string)
	if !ok {
		t.Fatalf("%q = %#v, want string", key, value)
	}
	return s
}

func jsonPaneByName(t *testing.T, capture map[string]any, name string) map[string]any {
	t.Helper()

	panes, ok := capture["panes"].([]any)
	if !ok {
		t.Fatalf("capture panes = %#v, want []any", capture["panes"])
	}
	for _, paneValue := range panes {
		pane, ok := paneValue.(map[string]any)
		if !ok {
			t.Fatalf("pane = %#v, want map", paneValue)
		}
		if jsonStringValue(t, pane, "name") == name {
			return pane
		}
	}
	t.Fatalf("pane %q not found in capture: %#v", name, capture)
	return nil
}

func paneMetaJSON(t *testing.T, pane map[string]any) map[string]any {
	t.Helper()

	value, ok := pane["meta"]
	if !ok {
		t.Fatalf("pane %q missing meta field: %#v", jsonStringValue(t, pane, "name"), pane)
	}
	meta, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("pane meta = %#v, want map", value)
	}
	return meta
}

func metaKVJSON(t *testing.T, meta map[string]any) map[string]any {
	t.Helper()

	value, ok := meta["kv"]
	if !ok {
		t.Fatalf("meta missing kv field: %#v", meta)
	}
	kv, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("meta.kv = %#v, want map", value)
	}
	return kv
}

func assertPaneMetaValues(t *testing.T, pane map[string]any) {
	t.Helper()

	meta := paneMetaJSON(t, pane)
	if got := jsonStringValue(t, meta, "task"); got != "ship" {
		t.Fatalf("meta.task = %q, want ship", got)
	}
	if got := jsonStringValue(t, meta, "git_branch"); got != "main" {
		t.Fatalf("meta.git_branch = %q, want main", got)
	}
	if got := jsonStringValue(t, meta, "pr"); got != "99" {
		t.Fatalf("meta.pr = %q, want 99", got)
	}

	kv := metaKVJSON(t, meta)
	if got := jsonStringValue(t, kv, "issue"); got != "LAB-338" {
		t.Fatalf("meta.kv.issue = %q, want LAB-338", got)
	}
	if got := jsonStringValue(t, kv, "owner"); got != "codex" {
		t.Fatalf("meta.kv.owner = %q, want codex", got)
	}
}

func TestCaptureJSONIncludesPaneMetaKV(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	if out := h.runCmd("meta", "set", "pane-1", "task=build", "branch=feat/meta", "pr=99", "issue=LAB-338", "owner=codex"); strings.TrimSpace(out) != "" {
		t.Fatalf("meta set output = %q, want empty", out)
	}

	fullCapture := decodeJSONMap(t, h.runCmd("capture", "--format", "json"))
	pane := jsonPaneByName(t, fullCapture, "pane-1")
	meta := paneMetaJSON(t, pane)

	if got := jsonStringValue(t, pane, "task"); got != "build" {
		t.Fatalf("top-level task = %q, want build", got)
	}
	if got := jsonStringValue(t, pane, "git_branch"); got != "feat/meta" {
		t.Fatalf("top-level git_branch = %q, want feat/meta", got)
	}
	if got := jsonStringValue(t, pane, "pr"); got != "99" {
		t.Fatalf("top-level pr = %q, want 99", got)
	}
	if got := jsonStringValue(t, meta, "task"); got != "build" {
		t.Fatalf("meta.task = %q, want build", got)
	}
	if got := jsonStringValue(t, meta, "git_branch"); got != "feat/meta" {
		t.Fatalf("meta.git_branch = %q, want feat/meta", got)
	}
	if got := jsonStringValue(t, meta, "pr"); got != "99" {
		t.Fatalf("meta.pr = %q, want 99", got)
	}

	kv := metaKVJSON(t, meta)
	if got := jsonStringValue(t, kv, "issue"); got != "LAB-338" {
		t.Fatalf("meta.kv.issue = %q, want LAB-338", got)
	}
	if got := jsonStringValue(t, kv, "owner"); got != "codex" {
		t.Fatalf("meta.kv.owner = %q, want codex", got)
	}
}

func TestPaneMetaSurvivesReloadServer(t *testing.T) {
	h := newAmuxHarness(t)

	h.runCmd("meta", "set", "pane-1", "task=ship", "branch=main", "pr=99", "issue=LAB-338", "owner=codex")
	h.runCmd("reload-server")
	h.waitForCaptureJSONReady(15 * time.Second)

	pane := waitForJSONMap(t, 5*time.Second, func() string {
		return h.runCmd("capture", "--format", "json", "pane-1")
	})
	assertPaneMetaValues(t, pane)
}

func TestPaneMetaSurvivesCrashRecovery(t *testing.T) {
	h := newServerHarnessPersistent(t)

	if out := strings.TrimSpace(h.runCmd("meta", "set", "pane-1", "task=ship", "branch=main", "pr=99", "issue=LAB-338", "owner=codex")); out != "" {
		t.Fatalf("meta set output = %q, want empty", out)
	}

	_, _ = waitForCrashCheckpointMatch(t, h, 0, crashCheckpointTestTimeout, "checkpoint with pane metadata", func(cp checkpoint.CrashCheckpoint) bool {
		ps, ok := findCrashCheckpointPane(cp, "pane-1")
		if !ok {
			return false
		}
		if ps.Meta.Task != "ship" || ps.Meta.GitBranch != "main" || ps.Meta.PR != "99" {
			return false
		}
		return ps.Meta.KV["issue"] == "LAB-338" && ps.Meta.KV["owner"] == "codex"
	})

	if h.client != nil {
		h.client.close()
		h.client = nil
	}
	if err := h.signalServer(syscall.SIGKILL); err != nil {
		t.Fatalf("SIGKILL server: %v", err)
	}
	_, _ = h.cmd.Process.Wait()
	h.cmd = nil

	h2 := startServerForSession(t, h.session, h.home)
	pane := waitForJSONMap(t, crashCheckpointTestTimeout, func() string {
		return h2.runControlCmd("capture", "--history", "--format", "json", "pane-1")
	})
	assertPaneMetaValues(t, pane)
}
