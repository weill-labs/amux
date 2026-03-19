# Agent API Reference

API version: **0.1**

The `api_version` field in `capture --format json` output identifies the JSON schema version. Changes to stable fields will increment this version.

## Stability labels

- **Stable** — covered by semantic versioning. Breaking changes require a major version bump.
- **Experimental** — may change in minor releases. Use with caution in production tooling.

## Commands

| Command | Stability | Description |
|---------|-----------|-------------|
| `capture --format json` | Stable | Structured JSON capture of session state |
| `capture --format json <pane>` | Stable | Single-pane JSON capture with position |
| `send-keys <pane> <keys>...` | Stable | Send keystrokes to a pane |
| `wait-idle <pane>` | Stable | Block until pane has no foreground process |
| `wait-busy <pane>` | Stable | Block until pane has a child process |
| `wait-for <pane> <substring>` | Stable | Block until substring appears in pane content |
| `wait-layout [--after N]` | Stable | Block until layout generation exceeds N |
| `wait-clipboard [--after N]` | Experimental | Block until clipboard content changes |
| `wait-ui <event>` | Experimental | Block until a client-local UI state is reached |
| `events` | Stable | Subscribe to real-time session events (NDJSON) |
| `list-clients` | Stable | Discover attached clients |
| `list` | Stable | List panes with metadata |
| `generation` | Stable | Current layout generation counter |
| `set-hook` / `unset-hook` | Experimental | Register shell commands on session events |

## Capture JSON fields

### Top-level (`capture --format json`)

| Field | Type | Stability |
|-------|------|-----------|
| `api_version` | string | Stable |
| `session` | string | Stable |
| `window` | object | Stable |
| `width` | int | Stable |
| `height` | int | Stable |
| `panes` | array | Stable |

### Per-pane

| Field | Type | Stability | Notes |
|-------|------|-----------|-------|
| `id` | int | Stable | |
| `name` | string | Stable | |
| `active` | bool | Stable | |
| `minimized` | bool | Stable | |
| `zoomed` | bool | Stable | |
| `host` | string | Stable | |
| `task` | string | Stable | |
| `color` | string | Stable | Hex color without `#` |
| `position` | object | Stable | `{x, y, width, height}` |
| `cursor` | object | Stable | `{col, row, hidden}` |
| `content` | string[] | Stable | Lines of visible text |
| `idle` | bool | Stable | |
| `idle_since` | string | Stable | RFC 3339, omitted when busy |
| `current_command` | string | Stable | |
| `child_pids` | int[] | Stable | Empty array when idle |
| `conn_status` | string | Experimental | Remote panes only |

## Event types

| Type | Stability | Payload |
|------|-----------|---------|
| `layout` | Stable | `generation`, `active_pane` |
| `idle` | Stable | `pane_id`, `pane_name`, `host` |
| `busy` | Stable | `pane_id`, `pane_name`, `host` |
| `output` | Experimental | `pane_id`, `pane_name` |

## Versioning policy

The agent API follows semantic versioning independently of the amux binary version:

- **Patch** (0.1.x): bug fixes, no schema changes.
- **Minor** (0.x.0): new fields or event types added. Existing fields remain unchanged.
- **Major** (x.0.0): breaking changes to existing fields, removed fields, or changed semantics.

The `api_version` field reflects the agent API version, not the binary version.
