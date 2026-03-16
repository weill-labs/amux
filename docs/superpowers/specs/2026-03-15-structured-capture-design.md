# Structured Capture Output (`amux capture --format json`)

**Date:** 2026-03-15
**Linear:** LAB-157

## Motivation

amux's Philosophy states that rendered panes (for humans) and structured output (for agents) are both projections of the same parsed terminal state. Today, `amux capture` only returns rendered text — agents and tests must reverse-engineer structure by parsing ANSI escape sequences, scanning for border characters, and matching status indicators. This feature delivers the structured projection.

## CLI Surface

```bash
amux capture --format json              # full-screen: layout + all pane content
amux capture --format json pane-1       # single-pane: content + cursor + metadata
```

`--format json` is mutually exclusive with `--ansi` and `--colors`. The existing two-way exclusivity check (`--ansi` vs `--colors`) is replaced with a three-way check covering all combinations.

## Output Shapes

### Full-screen

Returns the active window's layout state with all pane content embedded. Multi-window sessions capture only the active window (matching existing `renderCapture` behavior). The `window.index` field is derived from the window's position in `Session.Windows` (1-based).

At most one pane can be zoomed per window. When a pane is zoomed, its `position` reflects the full window dimensions (the zoomed viewport), not its original layout position. Other panes retain their layout positions but are not visible.

```json
{
  "session": "my-session",
  "window": {"id": 1, "name": "default", "index": 1},
  "width": 160,
  "height": 48,
  "panes": [
    {
      "id": 1,
      "name": "pane-1",
      "active": true,
      "minimized": false,
      "zoomed": false,
      "host": "local",
      "task": "",
      "color": "f38ba8",
      "position": {"x": 0, "y": 0, "width": 80, "height": 24},
      "cursor": {"col": 12, "row": 5, "hidden": false},
      "content": ["$ go test ./...", "ok  amux 0.3s", ""]
    }
  ]
}
```

### Single-pane

Returns one pane's metadata, cursor, and content. Omits session, window, position (caller already knows which pane they asked for; position is only meaningful in layout context).

```json
{
  "id": 1,
  "name": "pane-1",
  "active": true,
  "minimized": false,
  "zoomed": false,
  "host": "local",
  "task": "",
  "color": "f38ba8",
  "cursor": {"col": 12, "row": 5, "hidden": false},
  "content": ["$ go test ./...", "ok  amux 0.3s", ""]
}
```

## Content Format

The `content` field is an array of strings, one per visible screen line. Lines are:
- Right-trimmed of trailing whitespace
- ANSI-stripped (plain text only)
- All lines preserved (including empty ones) to maintain positional accuracy — `content[n]` is screen row `n`

The array is padded to the pane's height: `len(content) == pane_height` is guaranteed. This matches the VT emulator's screen buffer: every row from 0 to height-1 is represented.

## Implementation

### New types (`proto/types.go`)

JSON output structs with `json:` tags. Separate from the existing gob-encoded snapshot types (which serve the wire protocol, not CLI output).

```go
type CaptureJSON struct {
    Session string            `json:"session"`
    Window  CaptureWindow     `json:"window"`
    Width   int               `json:"width"`
    Height  int               `json:"height"`
    Panes   []CapturePane     `json:"panes"`
}

type CaptureWindow struct {
    ID    uint32 `json:"id"`
    Name  string `json:"name"`
    Index int    `json:"index"`
}

type CapturePane struct {
    ID        uint32        `json:"id"`
    Name      string        `json:"name"`
    Active    bool          `json:"active"`
    Minimized bool          `json:"minimized"`
    Zoomed    bool          `json:"zoomed"`
    Host      string        `json:"host"`
    Task      string        `json:"task"`
    Color     string        `json:"color"`
    Position  *CapturePos   `json:"position,omitempty"`
    Cursor    CaptureCursor `json:"cursor"`
    Content   []string      `json:"content"`
}

type CapturePos struct {
    X      int `json:"x"`
    Y      int `json:"y"`
    Width  int `json:"width"`
    Height int `json:"height"`
}

type CaptureCursor struct {
    Col    int  `json:"col"`
    Row    int  `json:"row"`
    Hidden bool `json:"hidden"`
}
```

### Capture builder (`server/`)

A new method on `Session` (next to `renderCapture` and `renderColorMap`) that:

1. Locks the session mutex
2. Reads the active window's layout snapshot (reuses `SnapshotLayout()` infrastructure)
3. For each pane, reads emulator state: cursor position, cursor hidden, and screen content (render + strip ANSI + split into lines + pad to height)
4. Builds the `CaptureJSON` struct
5. Serializes with `encoding/json.Marshal`

Note: Pane emulator reads race with concurrent PTY writes (same best-effort pattern as existing `renderCapture`). The session lock prevents layout changes but does not synchronize emulator content. This is acceptable — the capture is a point-in-time snapshot, not a transaction.

For single-pane: builds a single `CapturePane` without the `Position` field.

### Flag handling (`client_conn.go`)

Add `--format` flag parsing in the capture case alongside `--ansi` and `--colors`. When `format == "json"`, call the new builder instead of `renderCapture`.

### Content extraction

New method on `Pane` that returns all visible lines as `[]string`:

```go
func (p *Pane) ContentLines() []string
```

Renders the emulator, strips ANSI, splits by newline, right-trims each line, and pads the result to the pane's height with empty strings. Unlike `Output()` which skips empty lines and limits count, this preserves all rows for positional accuracy (`len(result) == pane_height`).

## Testing

### Round-trip test

Captures the same state as both text and JSON. Verifies that the JSON content lines match the text capture after normalization: strip ANSI, right-trim each line, match line-for-line. This proves the JSON is lossless and keeps it honest as rendering evolves.

### Integration tests

- `TestCaptureJSON_FullScreen`: split panes, verify JSON contains all panes with correct positions, active state, and content
- `TestCaptureJSON_SinglePane`: type text in a pane, capture as JSON, verify content and cursor
- `TestCaptureJSON_Minimized`: minimize a pane, verify `minimized: true` in JSON
- `TestCaptureJSON_Zoomed`: zoom a pane, verify `zoomed: true` in JSON

### Test migration

After the feature lands, existing tests that parse text for layout assertions (border scanning, active pane detection) can be incrementally migrated to use JSON capture. This is follow-up work, not part of this PR.

## What This Does Not Include

- Scrollback content (only visible screen lines)
- Cell-level attributes (bold, color per character)
- Border geometry as a separate field (borders are a rendering concern; pane positions implicitly define where borders go)
- Changes to the wire protocol (this is CLI output only)

These can be added later if needed without breaking the JSON shape (additive fields).
