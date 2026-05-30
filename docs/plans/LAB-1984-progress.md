# LAB-1984 — Progress

## Status: PLANNING (awaiting user sign-off on scope + CLI shape)

## Done
- [x] Research: mapped single-pane mirror end-to-end (findings.md).
- [x] Confirmed `mux.RebuildWindowFromSnapshot` enables window reconstruction.
- [x] Confirmed per-pane checkpoint/restore covers reload for free in MVP.
- [x] Drafted phased plan (plan.md).

## Next (pending approval)
- [ ] Decision A: CLI disambiguation (`--window` flag vs `attach-window` verb).
- [ ] Decision B: confirm MVP scope (static snapshot, defer dynamic resync).
- [ ] Step 1: `remote windows` listing (TDD).
- [ ] Step 2: remote window resolver (TDD).
- [ ] Step 3: `remote attach --window` core (TDD).
- [ ] Step 4: docs + help.

## Notes / breadcrumbs
- Branch: `LAB-1984-remote-window-mirror` off `origin/main@bcfab03`.
- Each remote window pane → independent proxy pane; window is a creation-time
  arrangement via RebuildWindowFromSnapshot. Reuses all per-pane lifecycle.
