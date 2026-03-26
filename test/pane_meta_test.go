package test

import (
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"strconv"
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

func decodeJSONList(t *testing.T, value any, name string) []any {
	t.Helper()

	items, ok := value.([]any)
	if !ok {
		t.Fatalf("%s = %#v, want []any", name, value)
	}
	return items
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

	for _, paneValue := range decodeJSONList(t, capture["panes"], "panes") {
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

func jsonIntList(t *testing.T, m map[string]any, key string) []int {
	t.Helper()

	items := decodeJSONList(t, m[key], key)
	out := make([]int, 0, len(items))
	for _, item := range items {
		n, ok := item.(float64)
		if !ok {
			t.Fatalf("%q item = %#v, want float64", key, item)
		}
		out = append(out, int(n))
	}
	return out
}

func jsonStringList(t *testing.T, m map[string]any, key string) []string {
	t.Helper()

	items := decodeJSONList(t, m[key], key)
	out := make([]string, 0, len(items))
	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			t.Fatalf("%q item = %#v, want string", key, item)
		}
		out = append(out, s)
	}
	return out
}

func paneMetaCollections(t *testing.T, meta any) ([]int, []string) {
	t.Helper()

	value := reflect.ValueOf(meta)
	prsField := value.FieldByName("PRs")
	if !prsField.IsValid() {
		t.Fatal("PaneMeta.PRs field missing")
	}
	issuesField := value.FieldByName("Issues")
	if !issuesField.IsValid() {
		t.Fatal("PaneMeta.Issues field missing")
	}

	prs := make([]int, prsField.Len())
	for i := 0; i < prsField.Len(); i++ {
		prs[i] = int(prsField.Index(i).Int())
	}
	issues := make([]string, issuesField.Len())
	for i := 0; i < issuesField.Len(); i++ {
		issues[i] = issuesField.Index(i).String()
	}
	return prs, issues
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
	if got := jsonIntList(t, meta, "prs"); !reflect.DeepEqual(got, []int{42}) {
		t.Fatalf("meta.prs = %v, want [42]", got)
	}
	if got := jsonStringList(t, meta, "issues"); !reflect.DeepEqual(got, []string{"LAB-338"}) {
		t.Fatalf("meta.issues = %v, want [LAB-338]", got)
	}
}

func tryPaneCaptureJSON(runCmd func(...string) string, pane string) (map[string]any, bool) {
	raw := runCmd("capture", "--format", "json", pane)
	var got map[string]any
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		return nil, false
	}
	return got, true
}

func waitForPaneCaptureJSON(t *testing.T, pane string, timeout time.Duration, runCmd func(...string) string) map[string]any {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if got, ok := tryPaneCaptureJSON(runCmd, pane); ok {
			return got
		}
	}
	t.Fatalf("timed out waiting for pane %s JSON capture within %v", pane, timeout)
	return nil
}

func tryGeneration(runCmd func(...string) string) (uint64, bool) {
	out := strings.TrimSpace(runCmd("cursor", "layout"))
	n, err := strconv.ParseUint(out, 10, 64)
	return n, err == nil
}

func waitForGeneration(t *testing.T, pane string, runCmd func(...string) string) uint64 {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		waitForPaneCaptureJSON(t, pane, time.Second, runCmd)
		if gen, ok := tryGeneration(runCmd); ok {
			return gen
		}
	}
	t.Fatalf("generation command did not recover for %s within 5s", pane)
	return 0
}

func waitForPaneIdle(t *testing.T, pane string, runCmd func(...string) string) {
	t.Helper()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		out := runCmd("wait", "idle", pane, "--timeout", "10s")
		if strings.Contains(out, "server not running") {
			continue
		}
		if strings.Contains(out, "timeout") || strings.Contains(out, "not found") {
			t.Fatalf("wait-idle %s: %s", pane, strings.TrimSpace(out))
		}
		return
	}
	t.Fatalf("wait-idle %s did not recover within 15s", pane)
}

func waitForPostIdleRefresh(t *testing.T, pane string, runCmd func(...string) string, generation func() uint64, waitLayoutOrTimeout func(uint64, string) bool) {
	t.Helper()

	before := waitForGeneration(t, pane, runCmd)
	waitForPaneIdle(t, pane, runCmd)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		gen := generation()
		if gen >= before+2 {
			return
		}
		_ = waitLayoutOrTimeout(gen, "250ms")
	}
	t.Fatalf("pane %s did not reach post-idle metadata refresh (generation before=%d after=%d)", pane, before, generation())
}

func runAmuxUsageCmd(t *testing.T, args ...string) string {
	t.Helper()

	cmd := exec.Command(amuxBin, args...)
	env := os.Environ()
	for _, key := range []string{"AMUX_PANE", "AMUX_SESSION", "TMUX"} {
		env = removeEnv(env, key)
	}
	env = upsertEnv(env, "HOME", newTestHome(t))
	if gocoverDir != "" {
		env = upsertEnv(env, "GOCOVERDIR", gocoverDir)
	}
	cmd.Env = env
	out, _ := cmd.CombinedOutput()
	return string(out)
}

func TestAddMetaTracksPanePRsAndIssues(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	if out := h.runCmd("add-meta", "pane-1", "pr=42", "issue=LAB-338", "pr=73", "issue=LAB-412"); out != "" {
		t.Fatalf("add-meta returned unexpected output: %q", out)
	}
	if out := h.runCmd("add-meta", "pane-1", "pr=42", "issue=LAB-338"); out != "" {
		t.Fatalf("idempotent add-meta returned unexpected output: %q", out)
	}

	list := h.runCmd("list")
	if !strings.Contains(list, "META") {
		t.Fatalf("list header should contain META column, got:\n%s", list)
	}
	if !strings.Contains(list, "prs=[42,73]") {
		t.Fatalf("list should show PR collection, got:\n%s", list)
	}
	if !strings.Contains(list, "issues=[LAB-338,LAB-412]") {
		t.Fatalf("list should show issue collection, got:\n%s", list)
	}
}

func TestPaneMetaCLIUsageErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "add-meta usage",
			args: []string{"add-meta"},
			want: "usage: amux add-meta <pane> key=value [key=value...]",
		},
		{
			name: "rm-meta usage",
			args: []string{"rm-meta"},
			want: "usage: amux rm-meta <pane> key=value [key=value...]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if out := runAmuxUsageCmd(t, tt.args...); !strings.Contains(out, tt.want) {
				t.Fatalf("%s output = %q, want substring %q", tt.name, out, tt.want)
			}
		})
	}
}

func TestRmMetaRemovesPanePRsAndIssues(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	h.runCmd("add-meta", "pane-1", "pr=42", "pr=73", "issue=LAB-338", "issue=LAB-412")

	if out := h.runCmd("rm-meta", "pane-1", "pr=42", "issue=LAB-338"); out != "" {
		t.Fatalf("rm-meta returned unexpected output: %q", out)
	}

	list := h.runCmd("list")
	if strings.Contains(list, "prs=[42,73]") {
		t.Fatalf("removed PR should not remain in list output:\n%s", list)
	}
	if strings.Contains(list, "issues=[LAB-338,LAB-412]") {
		t.Fatalf("removed issue should not remain in list output:\n%s", list)
	}
	if !strings.Contains(list, "prs=[73]") {
		t.Fatalf("remaining PR should stay visible, got:\n%s", list)
	}
	if !strings.Contains(list, "issues=[LAB-412]") {
		t.Fatalf("remaining issue should stay visible, got:\n%s", list)
	}
}

func TestCaptureJSONIncludesNestedPaneMeta(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	h.runCmd("set-meta", "pane-1", "task=build", "branch=feat/meta", "pr=99")
	h.runCmd("add-meta", "pane-1", "pr=42", "issue=LAB-338")

	fullCapture := decodeJSONMap(t, h.runCmd("capture", "--format", "json"))
	pane := jsonPaneByName(t, fullCapture, "pane-1")
	meta := paneMetaJSON(t, pane)

	if got := jsonStringValue(t, pane, "task"); got != "build" {
		t.Fatalf("legacy top-level task = %q, want build", got)
	}
	if got := jsonStringValue(t, pane, "git_branch"); got != "feat/meta" {
		t.Fatalf("legacy top-level git_branch = %q, want feat/meta", got)
	}
	if got := jsonStringValue(t, pane, "pr"); got != "99" {
		t.Fatalf("legacy top-level pr = %q, want 99", got)
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
	if got := jsonIntList(t, meta, "prs"); !reflect.DeepEqual(got, []int{42}) {
		t.Fatalf("meta.prs = %v, want [42]", got)
	}
	if got := jsonStringList(t, meta, "issues"); !reflect.DeepEqual(got, []string{"LAB-338"}) {
		t.Fatalf("meta.issues = %v, want [LAB-338]", got)
	}

	historyPane := decodeJSONMap(t, h.runCmd("capture", "--history", "--format", "json", "pane-1"))
	historyMeta := paneMetaJSON(t, historyPane)
	if got := jsonIntList(t, historyMeta, "prs"); !reflect.DeepEqual(got, []int{42}) {
		t.Fatalf("history meta.prs = %v, want [42]", got)
	}
	if got := jsonStringList(t, historyMeta, "issues"); !reflect.DeepEqual(got, []string{"LAB-338"}) {
		t.Fatalf("history meta.issues = %v, want [LAB-338]", got)
	}
}

func TestPaneMetaSurvivesReloadServer(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)

	h.runCmd("set-meta", "pane-1", "task=ship", "branch=main", "pr=99")
	h.runCmd("add-meta", "pane-1", "pr=42", "issue=LAB-338")

	h.runCmd("reload-server")
	if !h.waitFor("[pane-", 5*time.Second) {
		t.Fatalf("session did not recover after reload-server\nScreen:\n%s", h.captureOuter())
	}

	waitForPostIdleRefresh(t, "pane-1", h.runCmd, h.generation, h.waitLayoutOrTimeout)

	pane := waitForJSONMap(t, 5*time.Second, func() string {
		return h.runCmd("capture", "--format", "json", "pane-1")
	})
	assertPaneMetaValues(t, pane)
}

func TestPaneMetaSurvivesCrashRecovery(t *testing.T) {
	t.Parallel()

	h := newServerHarnessPersistent(t)

	h.runCmd("set-meta", "pane-1", "task=ship", "branch=main", "pr=99")
	h.runCmd("add-meta", "pane-1", "pr=42", "issue=LAB-338")

	cpPath := waitForCrashCheckpointPath(t, h.home, h.session, 5*time.Second)
	_ = waitForCrashCheckpointMatch(t, cpPath, 5*time.Second, "checkpoint with pane metadata", func(cp checkpoint.CrashCheckpoint) bool {
		ps, ok := findCrashCheckpointPane(cp, "pane-1")
		if !ok {
			return false
		}
		if ps.Meta.Task != "ship" || ps.Meta.GitBranch != "main" || ps.Meta.PR != "99" {
			return false
		}
		prs, issues := paneMetaCollections(t, ps.Meta)
		return reflect.DeepEqual(prs, []int{42}) && reflect.DeepEqual(issues, []string{"LAB-338"})
	})

	if h.client != nil {
		h.client.close()
		h.client = nil
	}
	if err := h.cmd.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("SIGKILL server: %v", err)
	}
	_, _ = h.cmd.Process.Wait()
	h.cmd = nil

	h2 := startServerForSession(t, h.session, h.home)
	waitForPostIdleRefresh(t, "pane-1", h2.runCmd, h2.generation, h2.waitLayoutOrTimeout)
	pane := decodeJSONMap(t, h2.runCmd("capture", "--history", "--format", "json", "pane-1"))
	assertPaneMetaValues(t, pane)
}
