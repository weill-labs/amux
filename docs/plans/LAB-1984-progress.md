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
- [x] Step 1a: `ResolveWindowFromLayout` resolver (remote pkg) + tests. [9354311]
- [x] Step 1b: `remote windows` listing (formatRemoteWindows + dispatch) + tests. [9354311]
- [x] Step 2: `remote attach-window` static mirror — planRemoteWindowLeaves,
      RebuildWindowFromSnapshot, uniqueLocalWindowName, e2e test. [7d816d1]

## Next (protocol-heavy, higher risk)
- [x] Step 3: `MsgTypeAttachWindow` (=30) server handler + `scopedWindowID` on
      clientConn + relax `allowsServerMessage` to pass MsgTypeLayout. [6630270]
- [x] Step 4a: mirror.Manager window-layout subscription (TrackWindow/DetachWindow,
      OnWindowLayout callback, WindowRef) + tests. [8ab1888]
- [ ] Step 4b: session-side reconcile — wire OnWindowLayout to a mutation that
      diffs the remote window snapshot vs the local mirror window (match panes by
      remote name), create/soft-close proxies, rebuild the split tree; register
      the window mirror in `attach-window`. (NEXT — largest/riskiest sub-step.)
- [ ] Step 5: dimension matching — resize REMOTE to match LOCAL on attach + on
      local window resize (extend planRemoteResize; skip lead/single-axis panes).
- [ ] Step 6: `remote detach-window <local-window>` teardown.
- [ ] Step 7: checkpoint/restore for window mirrors (resume layout subscription).
- [ ] Step 8: docs + `amux --help` + README CLI reference.

## Live validation (hetzner-1, 2026-05-30)
- `remote windows hetzner-1` listed all 11 real windows.
- `remote attach-window hetzner-1:orca` rebuilt the 3-pane window locally; all 3
  mirrors connected and streamed real orca output; split structure + per-pane
  `@hetzner-1` headers correct; appeared in session bar as `hetzner-1:orca`.
- Local width (~265) ≈ remote orca (265) → near-zero wrapping, confirming the
  Step-5 thesis (wrapping is purely a width-delta problem).
- Cleanup (detach x3 + rm) left remote orca workers alive → detach non-destructive.

## Verified working now
- `amux remote windows <host>` lists remote windows.
- `amux remote attach-window <host>:<window>` mirrors a remote window into a new
  local window; each pane streams independently. Static snapshot (no live resync
  yet); content wraps if local/remote geometry differ until Step 5.

## Notes / breadcrumbs
- Branch: `LAB-1984-remote-window-mirror` off `origin/main@bcfab03`.
- Each remote window pane → independent proxy pane; window is a creation-time
  arrangement via RebuildWindowFromSnapshot. Reuses all per-pane lifecycle.
- GOTCHA: never name a Go file `*_windows*.go` / `*_linux*.go` etc — Go's filename
  GOOS/GOARCH constraint silently excludes it. Window-listing test lives in
  `commands_remote_winlist_test.go` for this reason.
