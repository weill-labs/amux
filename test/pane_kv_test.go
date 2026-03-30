package test

import (
	"strings"
	"testing"
)

func TestMetaSetStoresArbitraryMetadataAndProjectsReservedKeys(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	if out := h.runCmd("meta", "set", "pane-1", "foo=bar", "task=build", "branch=feat/kv", "pr=42"); out != "" {
		t.Fatalf("meta set returned unexpected output: %q", out)
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

	got := h.runCmd("meta", "get", "pane-1")
	for _, want := range []string{"branch=feat/kv", "foo=bar", "pr=42", "task=build"} {
		if !strings.Contains(got, want) {
			t.Fatalf("meta get output missing %q:\n%s", want, got)
		}
	}
}

func TestMetaSetSupportsTrackedCollections(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	if out := h.runCmd(
		"meta", "set", "pane-1",
		"task=ship",
		"branch=main",
		"pr=99",
		`tracked_prs=[{"number":42}]`,
		`tracked_issues=[{"id":"LAB-338"}]`,
	); out != "" {
		t.Fatalf("meta set returned unexpected output: %q", out)
	}

	got := h.runCmd("meta", "get", "pane-1")
	for _, want := range []string{"task=ship", "branch=main", "pr=99", `tracked_prs=[{"number":42`, `tracked_issues=[{"id":"LAB-338"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("meta get output missing %q:\n%s", want, got)
		}
	}
}

func TestMetaRmRemovesArbitraryMetadataAndClearsReservedProjection(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	h.runCmd("meta", "set", "pane-1", "foo=bar", "branch=feat/remove")

	if out := h.runCmd("meta", "rm", "pane-1", "foo", "branch"); out != "" {
		t.Fatalf("meta rm returned unexpected output: %q", out)
	}

	got := h.runCmd("meta", "get", "pane-1")
	if strings.Contains(got, "foo=bar") {
		t.Fatalf("removed kv should not remain in meta get output:\n%s", got)
	}
	if strings.Contains(got, "branch=feat/remove") {
		t.Fatalf("removed branch should not remain in meta get output:\n%s", got)
	}

	list := h.runCmd("list")
	if strings.Contains(list, "feat/remove") {
		t.Fatalf("list should not show removed branch projection:\n%s", list)
	}
}

func TestMetaSetRejectsInvalidTrackedPRJSONWithoutMutation(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	h.runCmd("meta", "set", "pane-1", "foo=bar")

	out := h.runCmd("meta", "set", "pane-1", "tracked_prs=not-json")
	if !strings.Contains(out, "invalid tracked_prs value") {
		t.Fatalf("meta set should reject malformed tracked_prs JSON, got:\n%s", out)
	}

	got := h.runCmd("meta", "get", "pane-1")
	if !strings.Contains(got, "foo=bar") {
		t.Fatalf("existing kv should remain after failed meta set:\n%s", got)
	}
	if strings.Contains(got, "tracked_prs=") {
		t.Fatalf("failed meta set should not leave tracked_prs behind:\n%s", got)
	}
}

func TestMetaGetMissingKeyReturnsEmptyOutput(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	h.runCmd("meta", "set", "pane-1", "foo=bar")

	if out := h.runCmd("meta", "get", "pane-1", "missing"); strings.TrimSpace(out) != "" {
		t.Fatalf("meta get missing key should return empty output, got:\n%s", out)
	}
}

func TestMetaRmMissingKeyIsNoOp(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	h.runCmd("meta", "set", "pane-1", "foo=bar")

	if out := h.runCmd("meta", "rm", "pane-1", "missing"); out != "" {
		t.Fatalf("meta rm missing key should succeed, got:\n%s", out)
	}

	got := h.runCmd("meta", "get", "pane-1")
	if !strings.Contains(got, "foo=bar") {
		t.Fatalf("meta rm missing key should leave existing kv unchanged:\n%s", got)
	}
}

func TestCaptureJSONIncludesPaneKV(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	h.runCmd("meta", "set", "pane-1", "foo=bar", "task=build", "branch=feat/meta-kv", "pr=77")

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
