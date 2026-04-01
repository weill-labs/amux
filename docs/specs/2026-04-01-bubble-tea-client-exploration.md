# Bubble Tea Client Exploration

Date: 2026-04-01  
Issue: LAB-607

## Question

What would it take to rewrite `internal/client` as a Bubble Tea application, and is that a good direction for amux right now?

## Recommendation

Do not do a wholesale rewrite of `internal/client` into Bubble Tea in one step.

Do a staged migration instead:

1. Keep the existing protocol, emulator, compositor, capture, and copy-mode engine.
2. Replace the current interactive client coordinator with a Bubble Tea model only after extracting the existing renderer/capture core behind a cleaner boundary.
3. Do not depend on `bubbles` in the first pass. Use Bubble Tea core first, and only adopt Bubbles components later if the v2 ecosystem is stable enough for this repo.

The reason is simple: Bubble Tea is a good fit for amux's client-local state machine, but amux is not a normal single-process TUI. The client currently:

- forwards terminal input to remote panes with high fidelity
- hydrates pane history and live PTY output over a socket
- keeps its own per-pane emulator state
- runs its own render coalescing and active-pane prioritization logic
- exposes capture output for agents

Bubble Tea helps with the update loop and UI composition. It does not remove the need for those amux-specific responsibilities.

## What The Current Client Actually Owns

Today `internal/client` is responsible for all of the following:

| Concern | Current code |
|---|---|
| Attach/bootstrap sequencing | `internal/client/attach.go` |
| Raw terminal lifecycle | `internal/client/attach.go` |
| Attach-time capability detection | `internal/client/capabilities.go`, `internal/client/color_profile.go` |
| Outbound protocol writes | `internal/client/sender.go` |
| Client-side pane emulators + layout snapshot state | `internal/client/renderer.go`, `internal/client/renderer_state.go` |
| Client-local UI state | `internal/client/client.go`, `internal/client/ui_state.go`, `internal/client/client_state.go` |
| Copy mode integration | `internal/client/client.go`, `internal/copymode/` |
| Overlay UI | `internal/client/chooser.go`, `internal/client/display_panes.go`, `internal/client/window_rename_prompt.go` |
| Input decoding and forwarding | `internal/client/input_keys.go`, `internal/client/mouse.go`, `internal/client/attach.go` |
| Frame scheduling and render prioritization | `internal/client/client.go` |
| Capture rendering from attached clients | `internal/client/client.go`, `internal/client/renderer.go` |

That package is already two different things:

1. A reusable client-side rendering/capture core
2. An interactive session runtime with a custom event loop

That split is the real prerequisite for Bubble Tea adoption.

## What Can Be Reused

These parts should stay, or change only minimally.

### 1. Wire protocol and session model

Keep:

- `internal/proto/`
- attach/bootstrap message flow
- server-side expectations around `MsgTypeLayout`, `MsgTypePaneOutput`, `MsgTypeCaptureRequest`, `MsgTypeUIEvent`

The client is a consumer of the protocol, not its owner. A Bubble Tea rewrite does not need a protocol rewrite.

### 2. Renderer/emulator/compositor core

Keep:

- `internal/client/renderer.go`
- `internal/client/renderer_state.go`
- `internal/client/panedata.go`
- `internal/render/`
- `internal/mux.TerminalEmulator` usage

This is the strongest reusable seam in the current design. The existing renderer already:

- owns local emulators
- applies layout snapshots
- handles pane output
- produces capture JSON/plain text
- tracks per-client rescaled layout state

That is not Bubble Tea-specific logic. It is amux client state.

### 3. Copy-mode engine

Keep:

- `internal/copymode/`

The copy-mode data model and motion logic are already separated from the outer session loop. The Bubble Tea layer should drive it, not replace it.

### 4. Capability and color-profile detection

Keep:

- `internal/client/capabilities.go`
- `internal/client/color_profile.go`
- `internal/client/clipboard.go`

These are attach/runtime concerns that still exist under a Tea client.

### 5. Sender abstraction

Keep:

- `internal/client/sender.go`

`messageSender` already isolates outbound socket writes and ordering guarantees from the rest of the client.

## What Would Need To Change

### 1. `RunSession` would be replaced, not wrapped lightly

`internal/client/attach.go` currently acts as the real application runtime:

- connect
- attach
- bootstrap
- enter raw mode
- enter alt screen
- start goroutines
- read server messages
- read input bytes
- drive local state changes
- trigger reload/re-exec

If amux adopts Bubble Tea seriously, this file becomes the main migration target.

### 2. The current render loop is not a drop-in Tea model

The current client already has its own internal message/effect loop:

- `RenderMsg`
- `clientEffect`
- `RenderCoalesced`
- render timers
- active-pane priority window after local input

Bubble Tea provides a program loop, message injection, and FPS-limited rendering, but the existing amux behaviors are more specialized than "just rerender on update."

In particular, amux currently has custom behavior for:

- immediate render after layout changes
- delayed render for background pane output
- active-pane priority after local input
- render-loop-serialized local actions for copy mode and overlays

Those behaviors would need to be intentionally ported into a Tea model. They are not automatically preserved by `tea.WithFPS(...)`.

### 3. Input handling is the biggest technical mismatch

This is the hardest part of the rewrite.

amux does not only react to keys. It forwards raw terminal input to panes. The current client preserves original byte sequences whenever possible and only intercepts a subset for client-local behavior.

Current input responsibilities include:

- prefix handling
- literal prefix passthrough
- legacy key fallback behavior
- kitty/modern key decoding
- mouse parsing and pane-targeted mouse forwarding
- display-panes selection
- chooser and prompt input
- copy-mode navigation

Bubble Tea is good at decoded input events. amux often needs raw bytes or byte-preserving forwarding semantics.

That means a Tea rewrite must choose one of two approaches:

#### Option A: Let Bubble Tea own input

Pros:

- simpler Tea model
- more conventional Bubble Tea app structure

Cons:

- higher risk of pane-input fidelity regressions
- harder to preserve current byte-level forwarding semantics
- more work to map Tea key events back into exact pane input

#### Option B: Keep a custom raw input reader and inject high-level events into Tea

Pros:

- safer for pane input fidelity
- easier to preserve current prefix and passthrough semantics
- can migrate the local UI/state machine without immediately rewriting all input semantics

Cons:

- less "pure" Bubble Tea architecture
- raw terminal lifecycle remains partly amux-owned

For amux, Option B is the better first step.

### 4. Overlay rendering would need a new ownership model

Current overlays are rendered through the existing compositor path:

- display panes
- chooser
- rename prompt
- prefix/command feedback
- copy-mode overlay content

If Bubble Tea becomes the actual app shell, there are two ways forward:

1. Keep the current compositor as the screen renderer and use Bubble Tea mainly as an update/state engine
2. Add a Tea-friendly full-frame view path and let Bubble Tea own the whole screen

The second option is the cleaner end state, but it requires more renderer work because the current fast path is `RenderDiffWithOverlay`, which assumes direct ANSI diff output ownership.

## Bubble Tea Features That Are Relevant

Official Bubble Tea sources already cover several things amux cares about:

- `Program.Send(msg)` for injecting messages from outside the program
- `WithAltScreen()` for full-screen app startup
- `WithEnvironment(...)` for remote/SSH-aware environment handling
- `WithFPS(...)` for renderer frame limiting
- `ReleaseTerminal()` / `RestoreTerminal()` for temporarily giving terminal control back

That makes Bubble Tea a good candidate for the control loop.

Official Bubble Tea v2 release notes also matter here:

- Bubble Tea `v2.0.0` shipped on February 24, 2026
- v2 moved to the `charm.land/bubbletea/v2` import path
- v2 specifically calls out improved key handling and built-in color downsampling

Those improvements are directly relevant to amux's client.

## Bubbles: Useful, But Not For Phase 1

The `bubbles` component library is attractive because it has components that map well to current amux overlays:

- `list` for chooser-like flows
- `textinput` for rename prompt
- `viewport` for scrollable overlay content
- `help` and `key` for generated keybinding help

But there is an important dependency risk:

- the official `bubbles` `v2.0.0-beta.1` release says to use it alongside Bubble Tea v2 beta and Lip Gloss v2 beta

Inference from that release guidance: if amux wants Bubbles-based widgets in the same generation as Bubble Tea v2, it is likely signing up for a broader beta Charm stack upgrade, not just one new dependency.

This repo is already on stable `lipgloss v1.0.0`, so a first-phase Bubble Tea migration should avoid a Bubbles dependency. Keep custom overlays or tiny purpose-built submodels first.

## Proposed Architecture

### Target shape

Keep `internal/client` as the package entry point for now, but split responsibilities inside it:

| Layer | Responsibility |
|---|---|
| `session bridge` | attach, bootstrap, socket IO, reload hooks, size events |
| `tea model` | client-local UI state, mode switching, keymap, update routing |
| `renderer core` | emulators, layout snapshots, capture, pane lookup, copy-mode buffer snapshots |
| `input bridge` | raw byte reader, mouse parser, pane-forwarding semantics |

### Concrete file-level direction

Keep mostly as-is:

- `renderer.go`
- `renderer_state.go`
- `panedata.go`
- `capabilities.go`
- `color_profile.go`
- `clipboard.go`
- `sender.go`

Replace or heavily refactor:

- `attach.go`
- `client.go`
- `ui_state.go`
- `client_state.go`
- `local_render.go`
- `input_keys.go`
- `mouse.go`

Likely new files:

- `tea_model.go`
- `tea_messages.go`
- `tea_update.go`
- `tea_view.go`
- `session_bridge.go`
- `input_bridge.go`
- `keymap.go`

## Recommended Migration Plan

### Phase 0: Extract the stable client core

Goal: make the rewrite smaller before any Bubble Tea dependency lands.

Do:

- isolate the current reusable renderer/capture core from interactive-session logic
- make attach/bootstrap helpers callable without dragging in overlay/input concerns
- define a stable bridge interface for:
  - server messages in
  - protocol sends out
  - resize notifications
  - reload / exit

Exit criteria:

- current client behavior unchanged
- tests still pass
- `RunSession` depends on a smaller surface

### Phase 1: Add a Bubble Tea shell around local UI state

Goal: move chooser/prompt/message/copy-mode orchestration into a Tea model without changing pane rendering yet.

Do:

- introduce a Tea program for interactive mode
- keep the custom raw input reader for pane-forwarded bytes
- use `Program.Send(...)` to inject:
  - server layout/output events
  - local overlay actions
  - resize events
  - reload/exit events
- keep current renderer core and copy-mode engine

Exit criteria:

- attach/bootstrap still works
- capture forwarding still works
- prefix messaging, chooser, display panes, rename prompt, and copy mode still work

### Phase 2: Decide whether Tea should own full-screen rendering

At this point choose deliberately:

#### Path 2A: Hybrid model

- Bubble Tea owns state/update flow
- amux keeps its own compositor output path

This is lower risk and probably enough if the goal is maintainability, not aesthetic alignment with the Charm stack.

#### Path 2B: Full Tea view ownership

- add a Tea-friendly full-frame renderer path
- stop relying on amux's direct ANSI diff output for the interactive client

This is cleaner architecturally, but it is materially more work.

### Phase 3: Evaluate Bubbles components later

Only after the Tea shell is stable:

- consider `list` for chooser
- consider `textinput` for rename prompt
- consider `help`/`key` for discoverability overlays

Do this only if the dependency story is stable enough for the repo at that time.

## Testing Implications

The current client test surface is already strong and should shape the migration.

Important behaviors already covered include:

- attach bootstrap and black-screen avoidance
- resize correction after reattach
- capture forwarding through attached clients
- prefix messaging and overlay lifecycle
- chooser and prompt input
- copy-mode lifecycle and frozen buffer semantics
- mouse forwarding and copy-selection behavior
- render scheduling and active-pane prioritization
- capability detection and terminal enter/exit sequences

That means the rewrite should preserve most behavior through existing tests, then add Tea-specific coverage on top:

- model update tests
- bridge-to-Tea message injection tests
- full interactive attach tests still through `RunSession`

## Cost And Risk

### Benefits

- cleaner state/update architecture
- easier to reason about overlay modes and local UI
- better foundation for future richer client-local UI
- potential alignment with the rest of the Charm stack already in use indirectly (`lipgloss`, `colorprofile`, `ultraviolet`, `x/ansi`, `x/vt`)

### Risks

- introducing a new top-level runtime dependency that the repo does not currently use
- input fidelity regressions for forwarded pane input
- mouse behavior regressions
- accidental loss of active-pane render prioritization
- dependency churn if Bubbles/Lip Gloss v2 beta are pulled in too early
- a partial rewrite that keeps two competing client architectures alive too long

## Bottom Line

Rewriting the amux interactive client around Bubble Tea is feasible, but only if the project treats it as a control-loop migration, not a renderer rewrite.

The safest path is:

- keep the protocol
- keep the renderer/emulator/capture core
- keep the copy-mode engine
- keep raw input handling initially
- move client-local state orchestration into Bubble Tea first
- postpone Bubbles until the dependency story is less risky

If the goal is maintainability and a cleaner client architecture, this is worth exploring.

If the goal is to replace the current renderer with a generic Charm UI stack in one pass, that is too much churn for the current codebase and too likely to regress terminal fidelity.

## External References

- Bubble Tea v2 release notes: https://github.com/charmbracelet/bubbletea/releases
- Bubble Tea package docs (`Program.Send`, `WithAltScreen`, `WithEnvironment`, `WithFPS`, `ReleaseTerminal`, `RestoreTerminal`): https://pkg.go.dev/github.com/charmbracelet/bubbletea/v2
- Bubbles components repo (`list`, `textinput`, `viewport`, `help`, `key`): https://github.com/charmbracelet/bubbles
- Bubbles v2 beta release notes: https://github.com/charmbracelet/bubbles/releases
