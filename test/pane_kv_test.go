package test

import (
	"strings"
	"testing"
)

func TestSetKVStoresArbitraryMetadataAndProjectsReservedKeys(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	if out := h.runCmd("set-kv", "pane-1", "foo=bar", "task=build", "branch=feat/kv", "pr=42"); out != "" {
		t.Fatalf("set-kv returned unexpected output: %q", out)
	}

	list := h.runCmd("list")
	if !strings.Contains(list, "feat/kv") {
		t.Fatalf("list should show branch projected from kv, got:\n%s", list)
	}
	if !strings.Contains(list, "build") {
		t.Fatalf("list should show task projected from kv, got:\n%s", list)
	}
	if !strings.Contains(list, "#42") {
		t.Fatalf("list should show PR projected from kv, got:\n%s", list)
	}

	got := h.runCmd("get-kv", "pane-1")
	for _, want := range []string{"branch=feat/kv", "foo=bar", "pr=42", "task=build"} {
		if !strings.Contains(got, want) {
			t.Fatalf("get-kv output missing %q:\n%s", want, got)
		}
	}
}

func TestMetaWrappersPopulateKVStore(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	if out := h.runCmd("set-meta", "pane-1", "task=ship", "branch=main", "pr=99"); out != "" {
		t.Fatalf("set-meta returned unexpected output: %q", out)
	}
	if out := h.runCmd("add-meta", "pane-1", "pr=42", "issue=LAB-338"); out != "" {
		t.Fatalf("add-meta returned unexpected output: %q", out)
	}

	got := h.runCmd("get-kv", "pane-1")
	for _, want := range []string{"task=ship", "branch=main", "pr=99", `tracked_prs=[{"number":42`, `tracked_issues=[{"id":"LAB-338"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("get-kv output missing %q:\n%s", want, got)
		}
	}
}

func TestRmKVRemovesArbitraryMetadataAndClearsReservedProjection(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	h.runCmd("set-kv", "pane-1", "foo=bar", "branch=feat/remove")

	if out := h.runCmd("rm-kv", "pane-1", "foo", "branch"); out != "" {
		t.Fatalf("rm-kv returned unexpected output: %q", out)
	}

	got := h.runCmd("get-kv", "pane-1")
	if strings.Contains(got, "foo=bar") {
		t.Fatalf("removed kv should not remain in get-kv output:\n%s", got)
	}
	if strings.Contains(got, "branch=feat/remove") {
		t.Fatalf("removed branch should not remain in get-kv output:\n%s", got)
	}

	list := h.runCmd("list")
	if strings.Contains(list, "feat/remove") {
		t.Fatalf("list should not show removed branch projection:\n%s", list)
	}
}

func TestCaptureJSONIncludesPaneKV(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	h.runCmd("set-kv", "pane-1", "foo=bar", "task=build", "branch=feat/meta-kv", "pr=77")

	fullCapture := decodeJSONMap(t, h.runCmd("capture", "--format", "json"))
	pane := jsonPaneByName(t, fullCapture, "pane-1")
	meta := paneMetaJSON(t, pane)

	kvValue, ok := meta["kv"].(map[string]any)
	if !ok {
		t.Fatalf("meta.kv = %#v, want object", meta["kv"])
	}
	if got := jsonStringValue(t, kvValue, "foo"); got != "bar" {
		t.Fatalf("meta.kv.foo = %q, want bar", got)
	}
	if got := jsonStringValue(t, kvValue, "task"); got != "build" {
		t.Fatalf("meta.kv.task = %q, want build", got)
	}
	if got := jsonStringValue(t, kvValue, "branch"); got != "feat/meta-kv" {
		t.Fatalf("meta.kv.branch = %q, want feat/meta-kv", got)
	}
	if got := jsonStringValue(t, kvValue, "pr"); got != "77" {
		t.Fatalf("meta.kv.pr = %q, want 77", got)
	}
}
