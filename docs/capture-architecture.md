# Capture Architecture

Status: **implemented.** Design parent for [LAB-1643](https://linear.app/weill-labs/issue/LAB-1643/design-split-amux-capture-into-server-side-default-client-side-opt-in). The architecture below has shipped: server-side capture is the default for single-pane ([LAB-1753](https://linear.app/weill-labs/issue/LAB-1753)) and full-session ([LAB-1760](https://linear.app/weill-labs/issue/LAB-1760)) captures, and client-side `--client` capture reads a non-blocking `prevGrid` snapshot ([LAB-1645](https://linear.app/weill-labs/issue/LAB-1645/make-client-side-capture-non-blocking-via-prevgrid-snapshot)). The Design, Migration Path, and Decision Log sections are retained as the design record. Remaining follow-ups are called out inline: deprecating the redundant `--display` alias and adding structured overlay metadata to JSON output.

## Motivation

Before this split, the `amux capture` API conflated two distinct questions an agent might be asking:

- **Q1: "What is in the pane?"** Show me what the process has output, regardless of any user's current viewport state. Source of truth: `mux.Pane.emulator` cells on the server.
- **Q2: "What does the user see right now?"** Show me the rendered terminal output from the user's perspective, including transient client-side state such as copy mode, pane labels, drop indicators, and prompts. Source of truth: the diff renderer's `prevGrid` on a specific attached client.

Both questions went through the same code path: `amux capture` forwarded `MsgTypeCaptureRequest` to the active rendering client, which re-ran the full compositor on its render-actor goroutine. That:

- Caused the flicker bug tracked in [LAB-1634](https://linear.app/weill-labs/issue/LAB-1634/pane-running-nvim-transiently-loses-background-color-after-amux) through synchronous actor blocking plus a post-capture `msgCh` drain that burst-emitted pending frames.
- Required a client to be attached. Captures failed or waited through `waitForCaptureClient` retries when no client was attached.
- Queued multiple captures behind each other on a single client's actor.
- Did not answer Q2 well either, because it re-rendered instead of reading the already-painted `prevGrid`.

tmux's `capture-pane` answers Q1 with a server-local read of `wp->base.grid` and is immune to the background-color loss seen in LAB-1634. amux adopted the same architectural pattern for Q1 while preserving a path for Q2.

## Design

Split capture into two modes with sharper semantics.

### Server-Side Capture, Default

```bash
amux capture                  # full session, server-rendered
amux capture pane-3           # single pane, server-side
amux capture --format json    # structured server-side data
```

- The server reads from `mux.Pane.emulator` cells directly.
- Server-side compositor adapters wrap `*mux.Pane` for full-session composited output.
- There is no client involvement, actor blocking, or `msgCh` backlog.
- Expected latency is microseconds because capture is a memory walk.
- Capture works with zero clients attached.

Implemented in [LAB-1644](https://linear.app/weill-labs/issue/LAB-1644/implement-server-side-capture-path), with the default flips in [LAB-1753](https://linear.app/weill-labs/issue/LAB-1753) and [LAB-1760](https://linear.app/weill-labs/issue/LAB-1760).

### Client-Side Capture, Opt-In User Perspective

```bash
amux capture --client         # what the user sees right now
amux capture --client pane-3  # one pane from the user's view, including overlays
amux capture --display        # alias for --client; reads the prevGrid snapshot
```

- Reads `Compositor.prevGrid` directly through an `atomic.Pointer[ScreenGrid]` snapshot mechanism.
- Does not block the actor: `--client`/`--display` read the published `prevGridSnap` (`atomic.Pointer[ScreenGrid]`) via an atomic load in `CaptureDisplay`, rather than running on the render actor.
- Includes what the user actually sees: copy-mode scrollback views, overlays, prompts, and drop indicators.
- Expected latency is microseconds because capture is a cached grid read.
- The mode is per-client. When multiple clients are attached, a later implementation can accept `--for-client <id>` to disambiguate.

Implemented in [LAB-1645](https://linear.app/weill-labs/issue/LAB-1645/make-client-side-capture-non-blocking-via-prevgrid-snapshot).

## Why Both Modes

| Aspect | Server-side default | Client-side `--client` |
| --- | --- | --- |
| Source of truth | `mux.Pane.emulator` cells | `Compositor.prevGrid` |
| Includes copy-mode view? | No, shows live pane | Yes, shows the user's scrollback |
| Includes overlays? | No | Yes |
| Latency | Microseconds | Microseconds with snapshot |
| Blocks live render? | No | No with snapshot fix |
| Works with no client attached? | Yes | No |
| Per-client variation? | No, canonical | Yes, whatever this client sees |

Legitimate cases where the answers differ:

- User is in copy mode: server shows latest output, client shows scrolled-back view.
- Pane label overlay is visible: server has no label, client has label.
- Two clients have different terminal sizes: server has one canonical render, each client has its size-specific view.

Different questions deserve different commands.

## Diagnostic Byproduct

With both modes available, agents can diff server and client output to detect server/client emulator desync programmatically:

```bash
diff <(amux capture pane-3) <(amux capture --client pane-3)
```

Both capture modes now exist, so this diagnostic works today. The bug classes it reveals include [LAB-1634](https://linear.app/weill-labs/issue/LAB-1634/pane-running-nvim-transiently-loses-background-color-after-amux) and [LAB-1610](https://linear.app/weill-labs/issue/LAB-1610/diff-renderer-paints-stale-and-missing-pane-headers-in-multi-row).

## Overlay Handling For `--client`

Three options exist for how `--client` should include UI overlays:

1. **Read `prevGrid` only.** Whatever is painted is what capture returns. The diff renderer already painted overlays into `prevGrid`. This is the simplest option and addresses the visual inspection use case. **Shipped.**
2. **Read `prevGrid` plus emit a structured overlay summary.** Include the client and active overlay state alongside the grid. This is useful for agents that need to detect states such as "user is in copy mode" without parsing visuals. This best fits `--format json`. (Follow-up, not yet shipped.)
3. **Re-render with current overlays.** The pre-split behavior: slow and flicker-prone. The design moved away from this.

Outcome: option 1 shipped. Option 2 remains a JSON follow-up.

## Migration Path

Roll out the change compatibly:

1. Before LAB-1644: `amux capture` called client-side rendering and could flicker.
2. LAB-1644: add server-side single-pane capture as a parallel implementation behind `AMUX_CAPTURE_SERVER=1`.
3. LAB-1753: flip single-pane capture to server-side by default. `amux capture --client pane-3` opts into the user-perspective behavior. `AMUX_CAPTURE_LEGACY_CLIENT=1` temporarily restores the old single-pane client-forwarded default as an emergency rollback knob during soak.
4. LAB-1760: flip full-session capture to server-side by default using the same `AMUX_CAPTURE_LEGACY_CLIENT=1` emergency rollback knob during soak.
5. Later: deprecate the redundant `--display` flag, making it equivalent to `--client` without overlay metadata, or keep it as a fast-path alias.

Existing scripts continue working. Most scripts call `amux capture --format json`, which is already a structural read that translates cleanly to server-side capture.

## Relationship To Other Issues

- [LAB-1634](https://linear.app/weill-labs/issue/LAB-1634/pane-running-nvim-transiently-loses-background-color-after-amux) - capture-induced background flicker. Server-side capture is the architectural root-cause fix, corresponding to Option A in that issue.
- [LAB-1642](https://linear.app/weill-labs/issue/LAB-1642/track-sgr-state-across-diff-frames-to-drop-the-blanket-reset) - cross-frame SGR state tracking. This is an orthogonal tactical fix that reduces visible flicker on all live renders, not just post-capture.
- [LAB-1610](https://linear.app/weill-labs/issue/LAB-1610/diff-renderer-paints-stale-and-missing-pane-headers-in-multi-row) - moved-pane-header bleed. A partial fix already shipped; the diagnostic value of diffing server and client capture would have caught variant cases earlier.
- [LAB-1644](https://linear.app/weill-labs/issue/LAB-1644/implement-server-side-capture-path) - child issue for the server-side capture path.
- [LAB-1645](https://linear.app/weill-labs/issue/LAB-1645/make-client-side-capture-non-blocking-via-prevgrid-snapshot) - child issue for non-blocking client-side capture.

## Effort Estimate

- **Single-pane text/JSON capture server-side**: about half a day. The pieces already exist; this calls them from a different goroutine. It fixes most of LAB-1634's visible flicker because most agent capture invocations are single-pane.
- **Full-session composited capture server-side**: two to three days. This is mostly mechanical, centered on a `serverPaneData` adapter wrapping `*mux.Pane`, with one design call about how to handle client UI state.
- **Non-blocking client-side capture**: about half a day. Refactor `prevGrid` to publish through `atomic.Pointer[ScreenGrid]`, then replace `withRendererActorValue` in `CaptureDisplay` with an atomic load.
- **Soak and regression tests**: one to two days.

Total expected effort is about one week of focused work, broken into independently shippable PRs.

## Decision Log

- **2026-05-06: Default capture is server-side.** Rationale: the default agent question is "what is in the pane?", and that should be answered from server-owned emulator state without requiring an attached client.
- **2026-05-06: Client-perspective capture is explicit.** Rationale: copy mode, overlays, prompts, drop indicators, and client-specific terminal size answer a different question, so users and agents should opt in with `--client`.
- **2026-05-06: Server-side capture reads `mux.Pane.emulator` cells directly.** Rationale: this matches the tmux `capture-pane` architecture and avoids render-actor blocking, capture-client retries, and post-capture frame backlogs.
- **2026-05-06: Full-session server capture uses server-side compositor adapters around `*mux.Pane`.** Rationale: this keeps composited output behavior aligned with existing render code while removing attached-client state from the default path.
- **2026-05-06: Client-side capture reads a published `Compositor.prevGrid` snapshot.** Rationale: the client has already painted the user-visible grid, so snapshot reads answer the user-perspective question without re-rendering or blocking the renderer actor.
- **2026-05-06: `--client` overlay handling starts with `prevGrid` only.** Rationale: the painted grid is sufficient for visual inspection and is the smallest change that preserves overlays in client capture.
- **2026-05-06: Structured overlay metadata is a JSON follow-up, not a v1 blocker.** Rationale: overlay summaries are useful for agents but not required to remove flicker or unblock the architectural split.
- **2026-05-06: Re-rendering current overlays is not the target path.** Rationale: it preserves the slow and flicker-prone behavior this design is replacing, so it should only remain temporarily for compatibility if needed.
- **2026-05-06: Rollout is staged behind an opt-in server flag before flipping defaults.** Rationale: existing scripts keep working while implementation can soak under real agent workloads before `amux capture` changes semantics.
