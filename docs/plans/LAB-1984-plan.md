# LAB-1984 — Implementation plan: window-level remote mirroring

## Locked decisions (user-confirmed)
- **Delivery:** single big PR with structured TDD commits on one branch.
- **CLI:** separate verbs — `remote windows`, `remote attach-window`, `remote detach-window`.
- **Dimension matching:** resize the REMOTE to match the LOCAL window geometry
  (extend `planRemoteResize` per pane). Fallbacks required for lead panes and
  single-axis windows (skip + log; cannot resize those).
- **Architecture:** reuse the existing per-pane output/input mirror path untouched
  (one Link per pane). Add only a separate per-window LAYOUT-subscription link
  (`MsgTypeAttachWindow`) for dynamic resync. Server change is minimal: one accept
  case + relax `allowsServerMessage` to pass `MsgTypeLayout` for a window-scoped conn.

## Scope (FULL feature, single PR) — confirmed with user
Mirror a remote window's panes + split layout into a new local window. Includes:
1. Static window mirror (reuse per-pane machinery + RebuildWindowFromSnapshot).
2. **Dynamic resync** — remote window layout changes (pane add/remove/resize)
   propagate to the local mirror via a new layout-subscription channel.
3. **Dimension matching** — the local mirror window tracks the remote window's
   geometry (and/or reconciles size like the per-pane `remote resize`).
4. **Whole-window detach** — `remote detach-window <local-window>` one-shot teardown.

## CLI surface — confirmed: separate verbs
- `amux remote windows <name>` — list the remote host's windows
  (index, name, pane count, dimensions, active marker). Parallels `remote panes`.
- `amux remote attach-window <name>:<window>` — mirror a whole remote window
  (window name or index). Distinct verb from pane `attach`.
- `amux remote detach-window <local-window>` — tear down a mirrored window.

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

## Dynamic resync — design (the hard part)
The per-pane mirror link carries pane output only. For window mirroring we need the
remote to tell us when the window's split structure changes. Approach:
- Add `MsgTypeAttachWindow{Session, WindowName}` on the remote: subscribes the
  connection to (a) layout snapshots for that window on every change, and (b) the
  output streams of all panes in it. Server reuses its existing per-client layout
  broadcast path, scoped to one window.
- Local mirror manager gains a window-mirror state: on each inbound layout snapshot,
  reconcile — add proxy panes for new remote pane IDs, soft-close removed ones,
  re-run RebuildWindowFromSnapshot to update the split tree, rebroadcast locally.
- Falls back to a single multiplexed link per window (not N) so add/remove of remote
  panes does not require opening/closing sockets mid-stream.
- TO VERIFY before coding: server-side handling of MsgTypeAttachPane (is there a
  scoped subscriber model to extend?), the layout broadcast path, and whether output
  for multiple panes can share one connection. See LAB-1984-findings.md "resync".

## Dimension matching — design
- On attach and on local-window resize, send the remote a window-level resize
  (reuse planRemoteResize per pane, or a new window-resize remote command) so the
  remote window geometry tracks the local mirror window. Confirm RebuildWindowFromSnapshot
  scales cell proportions to the local width/height.
