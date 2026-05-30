# LAB-1984 — Implementation plan: window-level remote mirroring

## Scope (MVP / tracer bullet)
Mirror a remote window's panes + split layout into a new local window, reusing the
existing per-pane mirror machinery. Static snapshot at attach time. Defer dynamic
resync, window-level dimension matching, and whole-window detach to follow-ups.

## CLI surface
- `amux remote windows <name>` — list the remote host's windows
  (index, name, pane count, dimensions, active marker). Parallels `remote panes`.
- `amux remote attach --window <name>:<window>` — mirror a whole remote window.
  `--window` disambiguates from the pane form (matches the existing
  `send-keys --window <index|name>` convention). Accepts window name or index.

## Build sequence (TDD, separate commits per red/green/refactor)

### Step 1 — `remote windows` listing
- Add `windows` case to `runRemoteCommand` dispatch + usage string.
- `runRemoteWindows`: `listRemotePanes(host)` → iterate `layout.Windows` →
  format a table (reuse/extend listing helpers).
- Tests: unit test the formatter from a synthetic `LayoutSnapshot`;
  hermetic CLI test for arg validation/usage.

### Step 2 — resolve a remote window ref
- Add `WindowName` (or a `RemoteWindowRef`) + a resolver:
  `resolveRemoteWindow(layout, ref) -> WindowSnapshot` (by name, then index).
- Pure function, table-driven tests (found / not-found / ambiguous / by-index).

### Step 3 — `remote attach --window` (the core)
- Parse `--window` flag in attach; split `<name>:<window>`.
- Mutation:
  1. Fetch remote layout, resolve target `WindowSnapshot`.
  2. For each remote **leaf** pane: `prepareMirrorPane(meta, RemoteRef{Host,Session,PaneName}, cols, rows)`;
     record `paneMap[remotePaneID] = pane`.
  3. `win := mux.RebuildWindowFromSnapshot(ws, w.Width, w.Height, paneMap)`.
  4. Assign `win.ID = nextWindowID()`, name it (e.g. `<host>:<window>`),
     append to `sess.Windows`, `activateWindow(win)`.
  5. `trackMirrorPane(pane, ref)` for each proxy pane.
  6. Broadcast layout.
- Integration test via the server harness: drive a second in-process amux as the
  "remote", attach its multi-pane window, assert the local session gains a window
  with matching pane count/structure and that output flows.

### Step 4 — docs + polish
- Update `amux --help` usage block and README CLI reference.
- Update `docs/capture-architecture.md` / remote docs if they enumerate subcommands.
- Note the static-snapshot limitation in help text or docs.

## Test commands
```
go test ./internal/server/... ./internal/mux/... ./test/... -timeout 120s
cd test && go test -run TestRemote -count=100   # repeat new tests for flakes
make coverage
```

## Risks / open questions
- **Disambiguation choice** (flag vs separate verb vs auto-detect) — see Decision A.
- **Partial connect** — panes connect independently; the window appears immediately
  with panes filling in as each subscription bootstraps. Acceptable for MVP; note it.
- **Window naming collisions** — local window name `<host>:<window>` must not clash
  with `nextWindowID`/name uniqueness rules; verify in `runNewWindow` path.
- **RebuildWindowFromSnapshot pane sizing** — confirm it sizes proxy panes from the
  local width/height + snapshot proportions (not the remote's absolute dims).

## Out of scope (follow-up tickets)
- Dynamic remote-split resync (needs a layout-subscription channel on the mirror link).
- Window-level dimension matching / `remote resize --window`.
- `remote detach --window` single-command teardown.
