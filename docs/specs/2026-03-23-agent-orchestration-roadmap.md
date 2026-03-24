# Agent Orchestration Roadmap

Date: 2026-03-23. Companion doc: [competitive-landscape-2026-03.md](../competitive-landscape-2026-03.md)

## Context

People run 12-24 parallel AI agents in tmux and it's painful. Five core pain points emerged from X/Twitter sentiment analysis (31 sources, March 2026). See [competitive-landscape-2026-03.md](../competitive-landscape-2026-03.md) for full analysis.

1. **Persistence & resilience** — sessions die, state handoff is brutal
2. **Monitoring & visibility** — pane-hopping at 15+ agents is exhausting
3. **Coordination & conflicts** — agents stomp files, no shared awareness
4. **Reliability & nudging** — agents go idle/derail, need constant babysitting
5. **Usability & scaling** — agent-spawned sessions break MCP, poor multi-machine support

amux already addresses #1 (checkpoint/restore) and has strong foundations for the rest.

## Existing Foundation

| Capability | Status | Key Code |
|---|---|---|
| Checkpoint/restore with metadata | Shipped | `checkpoint/checkpoint.go` |
| Process detection (idle/busy/command/PIDs) | Shipped | `mux/process.go`, `server/idle.go` |
| NDJSON event stream with filtering | Shipped | `server/event.go` |
| Structured JSON capture (full session) | Shipped | `capture/capture.go`, `proto/types.go` |
| Pane metadata (task, PR, issues, branch) | Shipped | `mux/pane.go`, `server/commands_meta.go` |
| Metadata push via escape sequences | Shipped | `mux/meta_scanner.go` |
| Broadcast to multiple panes | Shipped | `server/commands_input.go` |
| Per-pane status icons (●/○/◇) | Shipped | `render/statusbar.go` |
| Blocking waits (idle, busy, layout) | Shipped | `server/event.go` |

## Proposed Features

### Tier 1: Agent Identity & Health (Pain Points #2, #4)

#### 1.1 Agent Auto-Detection

**Problem:** Users must manually tag panes with metadata. cmux, Agent Deck, and NTM all auto-detect agent processes.

**Proposal:** Extend `AgentStatus` to identify known agent processes by name.

```
// Known agent process signatures
claude → "Claude Code"
codex  → "Codex"
aider  → "Aider"
gemini → "Gemini CLI"
copilot → "Copilot"
cursor → "Cursor"
```

**Implementation:**
- Extend `Pane.AgentStatus()` in `mux/process.go` to match child process names against a known-agents table
- Add `AgentType string` and `AgentName string` to `PaneAgentStatus` in `proto/types.go`
- Surface in JSON capture and event stream
- Auto-set pane `Task` field if unset when agent detected
- Config: allow users to add custom agent signatures in `hosts.toml`

**Effort:** Small. Builds directly on existing `pgrep` + process name extraction.

#### 1.2 Agent Health States

**Problem:** Current idle tracking is binary (idle/busy). Agent workflows need richer states: waiting for input, executing, errored, rate-limited, completed.

**Proposal:** Extend idle tracking with agent-specific health heuristics.

```
States:
  running   — agent process alive, producing output
  waiting   — agent process alive, no output, PTY has prompt-like pattern
  stalled   — agent process alive, no output for >N minutes (configurable)
  errored   — agent process exited non-zero, or error pattern detected in output
  completed — agent process exited zero
  idle      — no agent process, shell at prompt
```

**Implementation:**
- Add `AgentHealth` enum to `PaneAgentStatus`
- `stalled` detection: extend `idleTracker` with configurable timeout (default 5m)
- `errored` detection: watch PTY output for common error patterns (stack traces, "Error:", non-zero exit)
- `completed` detection: agent process exits cleanly
- New event types: `agent-stalled`, `agent-errored`, `agent-completed`
- Status bar icon update: use distinct icons/colors per state

**Effort:** Medium. Core logic in `server/idle.go` + new `server/agent_health.go`.

#### 1.3 Agent Status in Session Bar

**Problem:** No at-a-glance view of agent health across panes.

**Proposal:** Add a summary line or mode to the global session bar showing agent counts by state.

```
[amux] [1:main] │ 3 running  2 waiting  1 stalled │ 12 panes │ 14:32
```

**Implementation:**
- Aggregate agent health states in `render/statusbar.go`
- Color-code counts (green=running, yellow=waiting, red=stalled/errored)
- Toggle with `amux set show-agent-summary on/off`
- Detailed view: `amux agents` CLI command (table of all agents with state, pane, duration)

**Effort:** Small. Rendering change + new CLI command.

---

### Tier 2: MCP Integration (Pain Point #5)

#### 2.1 Native MCP Server

**Problem:** MCP is becoming the standard bridge between AI agents and tools. Agents currently can't drive amux sessions without custom integration. Multiple tmux MCP servers exist; amux should have a native one.

**Proposal:** Implement an MCP server that exposes amux capabilities as tools.

```
Tools:
  amux_list_panes      — list all panes with metadata and agent status
  amux_capture_pane    — get structured content of a specific pane
  amux_send_keys       — send input to a specific pane (by name/ID)
  amux_create_pane     — split and create a new pane
  amux_close_pane      — close a pane
  amux_set_metadata    — set pane metadata
  amux_wait_idle       — block until pane becomes idle
  amux_wait_output     — block until pane produces matching output
  amux_broadcast       — send keys to multiple panes
  amux_events          — subscribe to event stream
```

**Implementation:**
- New package `internal/mcp/` implementing MCP protocol (JSON-RPC over stdio or SSE)
- Tools map directly to existing command handlers in `server/commands*.go`
- Launch: `amux mcp-server` starts the MCP server connected to the running session
- Config: `.mcp.json` entry for agent tools

**Effort:** Medium-Large. New package, but all underlying operations already exist.

**Why this is highest leverage:** Every MCP-compatible agent (Claude Code, Codex, Gemini CLI) immediately gets native amux integration without any amux-specific code on their side.

---

### Tier 3: Coordination & Conflict Awareness (Pain Point #3)

#### 3.1 File Reservation System

**Problem:** Agents stomp on each other's files. NTM has file reservation with conflict detection.

**Proposal:** Pane-level file reservations with conflict warnings.

```
amux reserve pane-1 src/server.go src/client.go
amux reserve pane-2 src/render.go
amux reservations                              # list all
amux reserve --check src/server.go             # who owns it?
```

**Implementation:**
- Server-side reservation table: `map[string]uint32` (file path → pane ID)
- Glob patterns supported: `amux reserve pane-1 "src/server/**"`
- Conflict detection: warn when a pane tries to reserve an already-reserved file
- Expose in JSON capture and event stream
- Optional: watch pane CWD + git status for actual file modifications and warn on conflicts

**Effort:** Medium. New `server/commands_reserve.go` + reservation tracking.

#### 3.2 Shared Context Board

**Problem:** No native shared state between agents. Workarounds (shared Markdown files) are fragile.

**Proposal:** A simple key-value store scoped to the session that any pane/agent can read/write.

```
amux context set "architecture-decision" "Using gRPC for inter-service communication"
amux context get "architecture-decision"
amux context list
amux context watch                             # stream changes
```

**Implementation:**
- Server-side `map[string]string` with timestamp tracking
- Accessible via CLI, escape sequences, and MCP tools
- Broadcast context changes via event stream
- Persisted in checkpoint

**Effort:** Small-Medium. Simple CRUD + event integration.

---

### Tier 4: Agent Lifecycle Management (Pain Point #4)

#### 4.1 Auto-Nudge Hooks

**Problem:** Agents go idle/stall and need manual intervention. People build custom daemons for this.

**Proposal:** Configurable hooks that fire on agent state transitions.

```toml
# hosts.toml or session config
[[hooks]]
event = "agent-stalled"
after = "5m"
action = "send-keys"
keys = "Continue with the current task.\n"
max_retries = 3

[[hooks]]
event = "agent-errored"
action = "notify"
message = "Agent in {pane} errored: {error}"
```

**Implementation:**
- Hook definitions in config (or per-session via CLI)
- Hook engine watches event stream, fires actions on matching events
- Built-in actions: `send-keys`, `notify`, `close-pane`, `run-command`
- Hooks fire on event loop, respecting retry limits and cooldowns
- `amux hooks list`, `amux hooks add`, `amux hooks rm`

**Effort:** Medium. New `server/hooks.go` + config parsing.

#### 4.2 Agent Kill-Switch

**Problem:** No graceful way to stop a derailing agent without killing the pane.

**Proposal:** Interrupt and replace agent command.

```
amux interrupt pane-3                          # send SIGINT + wait for idle
amux interrupt pane-3 --replace "aider ..."    # interrupt and start new command
amux interrupt --stalled                       # interrupt all stalled agents
```

**Implementation:**
- `interrupt`: send SIGINT to foreground process group, wait for idle
- `--replace`: after idle, send new command as keys
- `--stalled`/`--errored`: target by health state
- Cooldown to prevent interrupt storms

**Effort:** Small. Compose existing `send-keys` + signal + wait primitives.

---

### Tier 5: Dashboard & Multi-Device (Pain Point #2)

#### 5.1 Dashboard Mode

**Problem:** With 15+ panes, the normal tiled view is unreadable. People want a canvas/dashboard.

**Proposal:** A special rendering mode that shows a compact overview of all panes.

```
amux dashboard                                 # enter dashboard mode
```

Dashboard shows a grid of pane cards:
```
┌─ pane-1 (Claude) ──────┐ ┌─ pane-2 (Codex) ──────┐
│ ● running  2m          │ │ ◇ idle  45s            │
│ task: implement auth   │ │ task: write tests       │
│ branch: feat/auth      │ │ branch: feat/tests      │
│ PR: #42                │ │                         │
│ > last 3 lines...      │ │ > last 3 lines...       │
└────────────────────────┘ └────────────────────────┘
```

**Implementation:**
- New rendering mode in `render/` that composites pane cards instead of full pane content
- Cards show: name, agent type, health state, duration, metadata, last N lines of output
- Keyboard navigation: Enter to zoom into a pane, Esc to return to dashboard
- Auto-updates via existing broadcast mechanism

**Effort:** Large. New rendering mode, but builds on existing compositor and metadata.

#### 5.2 Web Dashboard (Future)

**Problem:** Phone/multi-device access is clunky (Tailscale + SSH). Zellij has a web client.

**Proposal:** Lightweight HTTP server serving a read-only dashboard.

- Expose event stream over WebSocket
- Render dashboard view in browser (pane cards with live status)
- Read-only initially (no input), upgradeable to interactive later
- Auth: session token or Tailscale identity

**Effort:** Large. New package. Defer until after Tier 1-4 prove the model.

---

## Prioritized Roadmap

| Priority | Feature | Pain Point | Effort | Leverage |
|---|---|---|---|---|
| P0 | 1.1 Agent auto-detection | #2, #4 | S | High — table stakes, cmux already does this |
| P0 | 1.3 Agent status in session bar | #2 | S | High — immediate visibility improvement |
| P1 | 2.1 MCP server | #5 | M-L | Highest — unlocks all MCP-compatible agents |
| P1 | 1.2 Agent health states | #2, #4 | M | High — foundation for auto-nudge and dashboard |
| P2 | 4.2 Agent kill-switch | #4 | S | Medium — composes existing primitives |
| P2 | 4.1 Auto-nudge hooks | #4 | M | Medium — eliminates manual babysitting |
| P2 | 3.2 Shared context board | #3 | S-M | Medium — simple but novel |
| P3 | 3.1 File reservation | #3 | M | Medium — coordination primitive |
| P3 | 5.1 Dashboard mode | #2 | L | High but expensive — defer until health states proven |
| P4 | 5.2 Web dashboard | #2 | L | High but expensive — defer until dashboard mode proven |

## Success Metrics

- **Agent auto-detection accuracy:** >95% for Claude Code, Codex, Aider (the top 3)
- **MCP integration time:** <5 minutes from install to first MCP tool call
- **Stall detection latency:** <30s from agent stall to event firing
- **Dashboard readability:** 15+ agents visible and scannable without scrolling

## Open Questions

1. Should agent auto-detection be opt-in or on-by-default?
2. Should the MCP server run as a subprocess of amux or as a standalone binary?
3. Should file reservations be advisory (warn) or enforced (block writes)?
4. What's the right stall timeout default? 5 minutes? Configurable per-agent?
5. Should the shared context board support structured data (JSON) or just strings?
