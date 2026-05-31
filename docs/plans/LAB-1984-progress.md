# LAB-1984 — Progress

## Status: PLANNING (awaiting user sign-off on scope + CLI shape)

## Done
- [x] Research: mapped single-pane mirror end-to-end (findings.md).
- [x] Confirmed `mux.RebuildWindowFromSnapshot` enables window reconstruction.
- [x] Confirmed per-pane checkpoint/restore covers reload for free in MVP.
- [x] Drafted phased plan (plan.md).

## Decisions (locked)
- CLI: separate verbs `remote windows` / `attach-window` / `detach-window`.
- Scope: FULL feature in one PR (static + dynamic resync + dimension matching).
- Dimension matching: resize REMOTE to match LOCAL (extend planRemoteResize).
- Architecture: reuse per-pane output/input links untouched; add one per-window
  layout-subscription link (MsgTypeAttachWindow) for dynamic resync.

## Done
- [x] Step 1a: `ResolveWindowFromLayout` resolver (remote pkg) + tests.
- [x] Step 1b: `remote windows` listing (formatRemoteWindows + dispatch) + tests.

## Next
- [ ] Step 2: `remote attach-window` core — build N proxy panes, RebuildWindowFromSnapshot,
      new local window, track each pane (static mirror).
- [ ] Step 3: `MsgTypeAttachWindow` server handler + allowsServerMessage relax.
- [ ] Step 4: window-mirror coordinator in mirror.Manager + reconcile loop.
- [ ] Step 5: dimension matching (resize remote to local on attach + local resize).
- [ ] Step 6: `remote detach-window` teardown.
- [ ] Step 7: checkpoint/restore for window mirrors (resume layout subscription).
- [ ] Step 8: docs + `amux --help` + README CLI reference.

## Notes / breadcrumbs
- Branch: `LAB-1984-remote-window-mirror` off `origin/main@bcfab03`.
- Each remote window pane → independent proxy pane; window is a creation-time
  arrangement via RebuildWindowFromSnapshot. Reuses all per-pane lifecycle.
- GOTCHA: never name a Go file `*_windows*.go` / `*_linux*.go` etc — Go's filename
  GOOS/GOARCH constraint silently excludes it. Window-listing test lives in
  `commands_remote_winlist_test.go` for this reason.
