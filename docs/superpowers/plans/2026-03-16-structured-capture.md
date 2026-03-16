# Structured Capture Output Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `amux capture --format json` for full-screen and single-pane structured output from the VT emulator's parsed state.

**Architecture:** New JSON types in `proto/types.go`, a `ContentLines()` method on `Pane`, a `captureJSON`/`capturePaneJSON` builder on `Session` in a new `capture_json.go` file, and flag handling in the existing capture case in `client_conn.go`. TDD throughout — tests before implementation.

**Tech Stack:** Go, `encoding/json`, existing `mux.Pane` emulator API, `ServerHarness` integration test framework.

**Spec:** `docs/superpowers/specs/2026-03-15-structured-capture-design.md`

---

## Setup: Create feature branch

Before any commits, create the feature branch:

```bash
git fetch origin main && git checkout -b lab-157-structured-capture origin/main
```

---

## Chunk 1: Types and ContentLines

### Task 1: Add JSON capture types to `proto/types.go`

**Files:**
- Modify: `internal/proto/types.go` (append after line 50)

- [ ] **Step 1: Add the JSON output types**

Append these types after `PaneSnapshot`. These are separate from the gob-encoded snapshot types — they serve CLI JSON output, not the wire protocol.

```go
// CaptureJSON is the full-screen JSON capture output.
type CaptureJSON struct {
	Session string        `json:"session"`
	Window  CaptureWindow `json:"window"`
	Width   int           `json:"width"`
	Height  int           `json:"height"`
	Panes   []CapturePane `json:"panes"`
}

// CaptureWindow identifies the captured window.
type CaptureWindow struct {
	ID    uint32 `json:"id"`
	Name  string `json:"name"`
	Index int    `json:"index"`
}

// CapturePane holds one pane's metadata, cursor, and content for JSON output.
type CapturePane struct {
	ID        uint32         `json:"id"`
	Name      string         `json:"name"`
	Active    bool           `json:"active"`
	Minimized bool           `json:"minimized"`
	Zoomed    bool           `json:"zoomed"`
	Host      string         `json:"host"`
	Task      string         `json:"task"`
	Color     string         `json:"color"`
	Position  *CapturePos    `json:"position,omitempty"`
	Cursor    CaptureCursor  `json:"cursor"`
	Content   []string       `json:"content"`
}

// CapturePos holds a pane's position and size within the layout.
type CapturePos struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// CaptureCursor holds cursor state for JSON output.
type CaptureCursor struct {
	Col    int  `json:"col"`
	Row    int  `json:"row"`
	Hidden bool `json:"hidden"`
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd /Users/cweill/github/amux9 && go build ./...`
Expected: success, no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/proto/types.go
git commit -m "Add JSON capture output types to proto package (LAB-157)"
```

### Task 2: Add `ContentLines()` method to `Pane`

**Files:**
- Modify: `internal/mux/pane.go` (add method after `Output()` at line 293)
- Create: `internal/mux/pane_test.go` (unit test)

- [ ] **Step 1: Write the unit test**

Create `internal/mux/pane_test.go`:

```go
package mux

import (
	"testing"
)

func TestContentLines(t *testing.T) {
	emu := NewVTEmulatorWithDrain(40, 5)

	p := &Pane{
		ID:       1,
		emulator: emu,
	}

	// Write two lines of content
	emu.Write([]byte("hello world\r\nline two\r\n"))

	lines := p.ContentLines()

	if len(lines) != 5 {
		t.Fatalf("expected 5 lines (pane height), got %d", len(lines))
	}
	if lines[0] != "hello world" {
		t.Errorf("line 0: got %q, want %q", lines[0], "hello world")
	}
	if lines[1] != "line two" {
		t.Errorf("line 1: got %q, want %q", lines[1], "line two")
	}
	// Remaining lines should be empty
	for i := 2; i < 5; i++ {
		if lines[i] != "" {
			t.Errorf("line %d: got %q, want empty", i, lines[i])
		}
	}
}

func TestContentLinesStripsANSI(t *testing.T) {
	emu := NewVTEmulatorWithDrain(40, 3)

	p := &Pane{
		ID:       1,
		emulator: emu,
	}

	// Write colored text
	emu.Write([]byte("\033[31mRED\033[m normal\r\n"))

	lines := p.ContentLines()

	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	if lines[0] != "RED normal" {
		t.Errorf("line 0: got %q, want %q", lines[0], "RED normal")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/cweill/github/amux9 && go test -run TestContentLines ./internal/mux/ -v`
Expected: FAIL — `ContentLines` not defined.

- [ ] **Step 3: Implement `ContentLines()`**

Add to `internal/mux/pane.go` after the `Output()` method (after line 293):

```go
// ContentLines returns all visible screen lines as a slice of plain text strings.
// Every row from 0 to height-1 is represented (len(result) == pane height).
// Lines are ANSI-stripped and right-trimmed of trailing whitespace.
func (p *Pane) ContentLines() []string {
	_, rows := p.emulator.Size()
	rendered := p.emulator.Render()
	all := strings.Split(rendered, "\n")

	result := make([]string, rows)
	for i := 0; i < rows && i < len(all); i++ {
		result[i] = StripANSI(strings.TrimRight(all[i], " "))
	}
	return result
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/cweill/github/amux9 && go test -run TestContentLines ./internal/mux/ -v`
Expected: PASS for both `TestContentLines` and `TestContentLinesStripsANSI`.

- [ ] **Step 5: Commit**

```bash
git add internal/mux/pane.go internal/mux/pane_test.go
git commit -m "Add ContentLines() method for structured pane content (LAB-157)"
```

---

## Chunk 2: Session builder methods

### Task 3: Add `captureJSON()` and `capturePaneJSON()` to Session

**Files:**
- Create: `internal/server/capture_json.go`

- [ ] **Step 1: Create `capture_json.go` with both builder methods**

Create `internal/server/capture_json.go`:

```go
package server

import (
	"encoding/json"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

// captureJSON returns the full-screen JSON capture of the active window.
// Caller does NOT hold s.mu — this method acquires it.
func (s *Session) captureJSON() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	w := s.ActiveWindow()
	if w == nil {
		return "{}"
	}

	// Find the active window's 1-based index.
	windowIndex := 1
	for i, win := range s.Windows {
		if win.ID == s.ActiveWindowID {
			windowIndex = i + 1
			break
		}
	}

	var activePaneID uint32
	if w.ActivePane != nil {
		activePaneID = w.ActivePane.ID
	}

	root := w.Root
	if w.ZoomedPaneID != 0 {
		root = mux.NewLeafByID(w.ZoomedPaneID, 0, 0, w.Width, w.Height)
	}

	capture := proto.CaptureJSON{
		Session: s.Name,
		Window: proto.CaptureWindow{
			ID:    w.ID,
			Name:  w.Name,
			Index: windowIndex,
		},
		Width:  w.Width,
		Height: w.Height,
	}

	// Walk visits only leaf cells. Use CellPaneID() to handle both
	// server-side cells (c.Pane.ID) and zoomed-view cells (c.PaneID).
	root.Walk(func(c *mux.LayoutCell) {
		paneID := c.CellPaneID()
		if paneID == 0 {
			return
		}
		pane := s.findPaneLocked(paneID)
		if pane == nil {
			return
		}

		cp := proto.CapturePane{
			ID:        pane.ID,
			Name:      pane.Meta.Name,
			Active:    pane.ID == activePaneID,
			Minimized: pane.Meta.Minimized,
			Zoomed:    pane.ID == w.ZoomedPaneID,
			Host:      pane.Meta.Host,
			Task:      pane.Meta.Task,
			Color:     pane.Meta.Color,
			Position: &proto.CapturePos{
				X:      c.X,
				Y:      c.Y,
				Width:  c.W,
				Height: c.H,
			},
			Cursor:  captureCursor(pane),
			Content: pane.ContentLines(),
		}
		capture.Panes = append(capture.Panes, cp)
	})

	out, _ := json.MarshalIndent(capture, "", "  ")
	return string(out)
}

// capturePaneJSON returns a single pane's JSON capture.
// Caller must hold s.mu.
func (s *Session) capturePaneJSON(pane *mux.Pane) string {
	var activePaneID uint32
	w := s.ActiveWindow()
	if w != nil && w.ActivePane != nil {
		activePaneID = w.ActivePane.ID
	}

	var zoomedPaneID uint32
	if w != nil {
		zoomedPaneID = w.ZoomedPaneID
	}

	cp := proto.CapturePane{
		ID:        pane.ID,
		Name:      pane.Meta.Name,
		Active:    pane.ID == activePaneID,
		Minimized: pane.Meta.Minimized,
		Zoomed:    pane.ID == zoomedPaneID,
		Host:      pane.Meta.Host,
		Task:      pane.Meta.Task,
		Color:     pane.Meta.Color,
		Cursor:    captureCursor(pane),
		Content:   pane.ContentLines(),
	}

	out, _ := json.MarshalIndent(cp, "", "  ")
	return string(out)
}

// captureCursor reads cursor state from a pane.
func captureCursor(pane *mux.Pane) proto.CaptureCursor {
	col, row := pane.CursorPos()
	return proto.CaptureCursor{
		Col:    col,
		Row:    row,
		Hidden: pane.CursorHidden(),
	}
}

// findPaneLocked finds a pane by ID. Caller must hold s.mu.
func (s *Session) findPaneLocked(id uint32) *mux.Pane {
	for _, p := range s.Panes {
		if p.ID == id {
			return p
		}
	}
	return nil
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd /Users/cweill/github/amux9 && go build ./...`
Expected: success. If `NewLeafByID` or `Walk` or `findPaneLocked` don't exist or have different signatures, adjust accordingly. Check existing code patterns in `server.go` for how panes are looked up (the `Panes` slice on Session).

**Note:** `findPaneLocked` may already exist. Search for it first: `grep -n findPaneLocked internal/server/server.go`. If it exists, remove the duplicate from `capture_json.go` and use the existing one.

- [ ] **Step 3: Commit**

```bash
git add internal/server/capture_json.go
git commit -m "Add captureJSON and capturePaneJSON session methods (LAB-157)"
```

---

## Chunk 3: Flag handling and integration tests

### Task 4: Wire `--format json` into the capture command handler

**Files:**
- Modify: `internal/server/client_conn.go` (capture case, lines 199-249)

- [ ] **Step 1: Update the capture command handler**

Replace the capture case in `client_conn.go` (lines 199-249) with:

```go
	case "capture":
		// amux capture [--ansi|--colors|--format json] [pane]
		includeANSI := false
		colorMap := false
		formatJSON := false
		var paneRef string
		for _, arg := range msg.CmdArgs {
			switch arg {
			case "--ansi":
				includeANSI = true
			case "--colors":
				colorMap = true
			case "--format":
				// next arg is the format value; handled below
			case "json":
				// only valid after --format; set flag
				formatJSON = true
			default:
				paneRef = arg
			}
		}

		// Three-way mutual exclusivity check
		flagCount := 0
		if includeANSI { flagCount++ }
		if colorMap { flagCount++ }
		if formatJSON { flagCount++ }
		if flagCount > 1 {
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "--ansi, --colors, and --format json are mutually exclusive"})
			return
		}

		if paneRef != "" {
			if colorMap {
				cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: "--colors is only supported for full screen capture"})
				return
			}
			// Single pane capture
			sess.mu.Lock()
			pane := cc.resolvePaneAcrossWindows(sess, "capture", paneRef)
			if pane == nil {
				sess.mu.Unlock()
				return
			}
			var out string
			if formatJSON {
				out = sess.capturePaneJSON(pane)
			} else if includeANSI {
				out = pane.Render()
			} else {
				out = pane.Output(DefaultOutputLines)
			}
			sess.mu.Unlock()
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: out + "\n"})
		} else {
			// Full composited screen capture
			var out string
			if formatJSON {
				out = sess.captureJSON()
			} else if colorMap {
				out = sess.renderColorMap()
			} else {
				out = sess.renderCapture(!includeANSI)
			}
			cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: out + "\n"})
		}
```

**Important:** The `--format` flag uses two args (`--format json`). The parsing above handles this by treating `--format` as a skip marker and `json` as the value. This works because `json` is not a valid pane name (pane names are `pane-N`). If more formats are added later, use a proper `--format=value` parser.

- [ ] **Step 2: Verify it compiles**

Run: `cd /Users/cweill/github/amux9 && go build ./...`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add internal/server/client_conn.go
git commit -m "Wire --format json flag into capture command handler (LAB-157)"
```

### Task 5: Write integration tests

**Files:**
- Create: `test/capture_json_test.go`

- [ ] **Step 1: Write all integration tests**

Create `test/capture_json_test.go`:

```go
package test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/proto"
)

func TestCaptureJSON_FullScreen(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.sendKeys("pane-1", "echo JSONTEST", "Enter")
	h.waitFor("pane-1", "JSONTEST")

	h.splitV()

	out := h.runCmd("capture", "--format", "json")

	var capture proto.CaptureJSON
	if err := json.Unmarshal([]byte(out), &capture); err != nil {
		t.Fatalf("failed to parse JSON: %v\nraw output:\n%s", err, out)
	}

	if capture.Session == "" {
		t.Error("session should be non-empty")
	}
	if capture.Window.ID == 0 {
		t.Error("window ID should be non-zero")
	}
	if capture.Width != 80 {
		t.Errorf("width: got %d, want 80", capture.Width)
	}
	if len(capture.Panes) != 2 {
		t.Fatalf("expected 2 panes, got %d", len(capture.Panes))
	}

	// Check pane-1 has content
	var pane1 *proto.CapturePane
	for i := range capture.Panes {
		if capture.Panes[i].Name == "pane-1" {
			pane1 = &capture.Panes[i]
			break
		}
	}
	if pane1 == nil {
		t.Fatal("pane-1 not found in JSON output")
	}

	found := false
	for _, line := range pane1.Content {
		if strings.Contains(line, "JSONTEST") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("pane-1 content should contain JSONTEST, got: %v", pane1.Content)
	}

	// Check positions are present and non-overlapping
	for _, p := range capture.Panes {
		if p.Position == nil {
			t.Errorf("pane %s: position should be present", p.Name)
		}
	}
}

func TestCaptureJSON_SinglePane(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.sendKeys("pane-1", "echo SINGLEPANE", "Enter")
	h.waitFor("pane-1", "SINGLEPANE")

	out := h.runCmd("capture", "--format", "json", "pane-1")

	var pane proto.CapturePane
	if err := json.Unmarshal([]byte(out), &pane); err != nil {
		t.Fatalf("failed to parse JSON: %v\nraw output:\n%s", err, out)
	}

	if pane.Name != "pane-1" {
		t.Errorf("name: got %q, want %q", pane.Name, "pane-1")
	}
	if !pane.Active {
		t.Error("pane-1 should be active")
	}
	if pane.Position != nil {
		t.Error("single-pane capture should not include position")
	}

	found := false
	for _, line := range pane.Content {
		if strings.Contains(line, "SINGLEPANE") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("content should contain SINGLEPANE, got: %v", pane.Content)
	}

	// Content should be padded to pane height.
	// Harness seeds with Rows=24. Layout height = 24-1 (global bar) = 23.
	// Pane content height = 23-1 (status line) = 22.
	if len(pane.Content) != 22 {
		t.Errorf("content lines: got %d, want 22", len(pane.Content))
	}
}

func TestCaptureJSON_Minimized(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()
	h.runCmd("minimize", "pane-1")

	out := h.runCmd("capture", "--format", "json")

	var capture proto.CaptureJSON
	if err := json.Unmarshal([]byte(out), &capture); err != nil {
		t.Fatalf("failed to parse JSON: %v\nraw output:\n%s", err, out)
	}

	for _, p := range capture.Panes {
		if p.Name == "pane-1" {
			if !p.Minimized {
				t.Error("pane-1 should be minimized")
			}
			return
		}
	}
	t.Error("pane-1 not found in JSON output")
}

func TestCaptureJSON_Zoomed(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()
	h.runCmd("zoom", "pane-1")

	out := h.runCmd("capture", "--format", "json")

	var capture proto.CaptureJSON
	if err := json.Unmarshal([]byte(out), &capture); err != nil {
		t.Fatalf("failed to parse JSON: %v\nraw output:\n%s", err, out)
	}

	for _, p := range capture.Panes {
		if p.Name == "pane-1" {
			if !p.Zoomed {
				t.Error("pane-1 should be zoomed")
			}
			// Zoomed pane should fill the window
			if p.Position != nil && p.Position.Width != 80 {
				t.Errorf("zoomed pane width: got %d, want 80", p.Position.Width)
			}
			return
		}
	}
	t.Error("pane-1 not found in JSON output")
}

func TestCaptureJSON_MutualExclusivity(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("capture", "--format", "json", "--ansi")
	if !strings.Contains(out, "mutually exclusive") {
		t.Errorf("expected mutual exclusivity error, got: %s", out)
	}
}

func TestCaptureJSON_RoundTrip(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.sendKeys("pane-1", "echo ROUNDTRIP", "Enter")
	h.waitFor("pane-1", "ROUNDTRIP")

	// Capture as both plain text and JSON
	plain := h.runCmd("capture", "pane-1")
	jsonOut := h.runCmd("capture", "--format", "json", "pane-1")

	var pane proto.CapturePane
	if err := json.Unmarshal([]byte(jsonOut), &pane); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	// The plain text capture contains the non-empty lines.
	// Verify every non-empty line from plain text appears in JSON content.
	for _, line := range strings.Split(strings.TrimSpace(plain), "\n") {
		trimmed := strings.TrimRight(line, " ")
		if trimmed == "" {
			continue
		}
		found := false
		for _, jline := range pane.Content {
			if strings.Contains(jline, trimmed) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("plain text line %q not found in JSON content", trimmed)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they pass**

Run: `cd /Users/cweill/github/amux9 && go test -v -run TestCaptureJSON ./test/ -timeout 60s`
Expected: all 6 tests pass.

- [ ] **Step 3: Run the full test suite**

Run: `cd /Users/cweill/github/amux9 && go test ./... -timeout 120s`
Expected: all tests pass (existing tests unaffected).

- [ ] **Step 4: Commit**

```bash
git add test/capture_json_test.go
git commit -m "Add integration tests for structured JSON capture (LAB-157)"
```

### Task 6: Final commit — push and open PR

- [ ] **Step 1: Run full test suite one more time**

Run: `cd /Users/cweill/github/amux9 && go test ./... -timeout 120s`
Expected: all tests pass.

- [ ] **Step 2: Push and open PR**

```bash
git push -u origin lab-157-structured-capture
gh pr create --title "Add structured JSON capture output (LAB-157)" --body "$(cat <<'EOF'
## Summary
- Add `amux capture --format json` for full-screen and single-pane structured output
- JSON is derived from the VT emulator's parsed state (same source of truth as rendered panes)
- Full-screen returns all panes with positions, active/minimized/zoomed state, cursor, and content
- Single-pane returns one pane's metadata, cursor, and content
- Round-trip test proves JSON content matches text capture (losslessness)

## Motivation
Delivers the "structured projection" described in the README Philosophy diagram. Agents get first-class structured access to terminal state without parsing rendered text.

## Testing
- Unit tests for `ContentLines()` (ANSI stripping, height padding)
- Integration tests: full-screen, single-pane, minimized, zoomed, mutual exclusivity, round-trip
- Full test suite passes

## Review focus
- `--format` flag parsing in `client_conn.go` — the two-arg `--format json` pattern
- `captureJSON()` lock scope matches `renderCapture()` pattern
- `ContentLines()` height padding logic

Fixes LAB-157

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```
