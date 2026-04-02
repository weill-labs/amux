# Orca: Agent Orchestration Daemon

Date: 2026-04-02. Related: [agent-orchestration-roadmap.md](2026-03-23-agent-orchestration-roadmap.md)

## Motivation

Today, multi-agent orchestration in amux is driven by shell scripts
(`spawn-worker.sh`, `delegate-task.sh`, `batch-delegate.sh`, `worker-status.sh`)
and a human or AI "lieutenant" in the lead pane making decisions. This works but
has real problems:

- **No persistent state.** Every script invocation rediscovers the world via
  `amux capture --format json`. If a Claude Code session dies, all orchestration
  context is lost.
- **No background monitoring.** Stuck detection requires active polling by the
  lieutenant. If the lieutenant is busy, stuck workers sit indefinitely.
- **No task queue.** Work can only be dispatched as fast as the lieutenant can
  manually assign it.
- **Brittle glue.** Shell scripts composing CLI commands are hard to test, hard
  to extend, and fail silently.

**Orca** is a separate Go binary that solves these problems. It is a
deterministic orchestration daemon that manages the full lifecycle of coding
tasks: clone allocation, agent spawning, health monitoring, PR merge detection,
and cleanup. It owns no AI — all non-deterministic intelligence lives in Claude
Code (the lieutenant) or the worker agents in panes.

## Design Principles

1. **amux knows nothing about orca.** Orca is a consumer of amux's public API
   (CLI commands + event stream). Other tools could replace orca without amux
   changes.
2. **Orca is 100% deterministic.** It fetches issues, manages clones, spawns
   panes, monitors health, and cleans up. It never decides *what* to work on or
   *how* to handle failures — that intelligence lives in Claude Code.
3. **Claude Code is the glue.** The lieutenant in the lead pane decides which
   issues to assign, what prompts to send, how to handle escalations, and when to
   intervene. Orca is its reliable execution layer.
4. **Agent-agnostic with profiles.** Orca works with any coding agent (Codex,
   Claude Code, Aider, etc.) via pluggable profiles that encode agent-specific
   quirks (start commands, idle signals, stuck patterns, nudge commands).

## System Relationship

```
User (CEO)
  └─ Claude Code (lieutenant, lead pane)
       └─ orca CLI ─── orca daemon
            ├─ amux        (pane/PTY infrastructure)
            ├─ GitHub API  (issue fetch, PR merge detection)
            ├─ Linear API  (issue fetch)
            └─ clone pool  (filesystem)
```

The user talks to Claude Code. Claude Code calls `orca` subcommands. Orca calls
`amux` subcommands. amux manages PTYs and rendering. Information flows back up
via `orca status`, `orca events`, and push notifications to the lead pane.

## Architecture

### Daemon

Orca runs as a long-lived background process started manually per project:

```bash
orca start --session my-session --project /path/to/repo
```

It connects to amux in two ways:
- **CLI commands** for actions (spawn, send-keys, meta, capture) — same stable
  API the shell scripts use today.
- **Persistent event stream** (`amux events`) for real-time monitoring of pane
  state changes.

If amux restarts (checkpoint/restore), orca auto-reconnects: it polls until amux
is back, reconciles surviving panes by stable pane ID (not name — names can be
duplicated or reused) against its known workers, marks vanished workers as lost,
and reports the delta. Orca persists the pane ID as the worker identity; the
pane name is display-only metadata.

### State

All state lives in a global SQLite database at `~/.config/orca/state.db`.
One orca daemon runs per project. All tables are scoped by a `project` column
(the `--project` path canonicalized). Running two projects simultaneously means
two daemon instances sharing the same DB file, each seeing only its own rows.

- **Tasks** — project, issue ID, status (queued/active/done/cancelled), assigned
  worker, assigned clone, PR number, timestamps.
- **Workers** — pane ID, pane name, agent profile, current task, health state,
  clone
  assignment.
- **Clones** — filesystem path, status (free/occupied), current branch, assigned
  task.
- **Event log** — timestamped record of state transitions for debugging and
  `orca logs`.

SQLite is global (not per-clone) because clones are at known filesystem paths
and task state doesn't belong to any single clone.

### Clone Pool

Orca manages a pool of independent git clones of the project repository. Clones
are used instead of git worktrees for full isolation — each clone has its own
`.git` directory, node_modules, build cache, etc.

**Discovery.** Orca finds clones by convention-based path patterns configured in
`config.toml`. Clones must contain an `.orca-pool` marker file to be eligible —
this prevents orca from accidentally allocating human-owned checkouts, the
coordinator repo, or backup directories that happen to match the glob.

```toml
[pool]
pattern = "~/sync/github/amux/amux*"
# Only directories containing .orca-pool are eligible.
# Create markers: for i in {01..36}; do touch amux$i/.orca-pool; done
```

**Creation.** Orca can create new clones on demand:

```bash
orca clone --count 5
```

This clones from origin into the next available numbered path (e.g., `amux37`,
`amux38`, ...).

**Lifecycle.** A clone is occupied for the entire task lifecycle — from
assignment through PR merge. On task completion or cancellation, orca resets the
clone:

```
git reset --hard && git checkout main && git pull && git clean -fdx --exclude=.orca-pool && git branch -D TASK_BRANCH
```

The clone returns to the free pool. Pool size equals max concurrency; there is no
upper bound.

### Agent Profiles

Profiles encode agent-specific behavior without orca containing any AI:

```toml
[agents.codex]
start_command = "codex --yolo"
idle_signal = "screen_quiet"
idle_timeout = "30s"
stuck_text_patterns = ["permission prompt"]  # matched against screen output
stuck_timeout = "5m"                         # idle longer than this = stuck
nudge_command = "y\n"
max_nudge_retries = 3

[agents.claude]
start_command = "claude --dangerously-skip-permissions"
idle_signal = "screen_quiet"
idle_timeout = "30s"
stuck_text_patterns = ["tool denied"]
stuck_timeout = "5m"
nudge_command = "\n"
max_nudge_retries = 3

[agents.aider]
start_command = "aider"
idle_signal = "screen_quiet"
idle_timeout = "30s"
stuck_text_patterns = []
stuck_timeout = "5m"
nudge_command = "/run\n"
max_nudge_retries = 2
```

Stuck detection uses two independent mechanisms:
- **Text patterns** (`stuck_text_patterns`): matched against captured screen
  output via `amux capture`. Triggers immediately on match.
- **Timeout** (`stuck_timeout`): fires when the agent has been idle (no screen
  output) for longer than this duration without having created a PR.

Either mechanism triggers the nudge sequence. Orca uses these profiles to:
1. Start agents in panes with the correct command.
2. Detect stuck states via text pattern matching or idle timeout.
3. Auto-nudge up to `max_nudge_retries` times before escalating.

### Notifications

Orca needs to alert the lieutenant (Claude Code in the lead pane) when events
require attention. Typing directly into the pane via `amux send-keys` would
corrupt Claude Code's state if it's mid-task — it has no input queue, just a PTY.

**Approach: orca emits events, Claude Code polls.** Orca writes notifications to
its own event stream (`orca events`) and to a notification log in SQLite. Claude
Code (or a monitoring loop it runs) periodically checks `orca status` or
subscribes to `orca events` to pick up alerts:

```
[orca] pane-5 stuck on LAB-123 — nudge failed 3x, needs attention
[orca] LAB-124 PR #590 merged — task complete, clone amux08 freed
[orca] worker pool exhausted — 3 tasks queued, 0 clones free
```

The exact integration mechanism (Claude Code polling, a background watcher
process that alerts the lead pane during idle windows, or amux gaining a
notification overlay) is an open question — the key constraint is that
notifications must not corrupt an active agent's PTY state.

### PR Merge Detection

Orca polls GitHub for PR merge status using the `gh` CLI:

```bash
gh pr view --repo owner/repo --json mergedAt BRANCH_OR_NUMBER
```

Polling interval: 30 seconds. PR merges are human-timescale events (minutes to
hours), so polling is cheap and requires zero infrastructure. A webhook-based
approach (GitHub Actions triggering a callback) can replace polling later without
changing orca's architecture.

On merge detection:
1. Notify the agent in the pane: "PR merged, wrap up."
2. Wait for the agent to go idle or exit (max 10 minutes; force-kill if exceeded).
3. Clean the clone (reset to main) and return it to the pool.
4. Mark the task as done.
5. Notify the lead pane.

## Task Lifecycle

```
Claude: orca assign LAB-123 --prompt "Implement the auth feature..."

  1. Fetch issue from Linear or GitHub
  2. Pick a free clone from the pool
  3. In clone: git checkout main && git pull && git checkout -B LAB-123
  4. Spawn amux pane with cwd = clone path
  5. Start agent per profile (e.g., codex --yolo)
  6. Send prompt (provided by Claude) to the pane
  7. Set pane metadata: task, issue, branch
  8. Monitor: health checks, stuck detection, auto-nudge per profile
  9. Poll for PR creation (gh pr list --head BRANCH, 30s interval) → record PR number
 10. Poll for PR merge
 11. On merge: notify agent "PR merged, wrap up"
 12. Wait for agent idle/exit (max 10m, force-kill if exceeded)
 13. Clean clone: git reset --hard && git checkout main && git pull && git clean -fdx --exclude=.orca-pool && git branch -D TASK_BRANCH
 14. Return clone to pool, mark task done
 15. Notify lead pane

Escalation path:
  stuck → auto-nudge (up to N retries) → notify lead pane

Cancellation:
  Claude: orca cancel LAB-123
  → kill agent process, clean clone, return to pool
```

## CLI Reference

### Daemon

```bash
orca start [--session NAME] [--project PATH]   # start daemon
orca stop                                       # stop daemon
orca status                                     # daemon health + summary
```

### Tasks

```bash
orca assign ISSUE [--prompt "..."] [--agent PROFILE] [--worker PANE]
                                                # assign issue to a worker
orca batch tasks.json                           # queue multiple tasks
orca cancel ISSUE                               # abort task, clean clone
orca complete ISSUE                             # manual completion trigger
orca status ISSUE                               # task status + history
orca logs ISSUE                                 # detailed event log for task
```

### Workers

```bash
orca workers                                    # list workers + state
orca spawn [--count N] [--host HOST] [--agent PROFILE]
                                                # add workers to pool
orca kill PANE                                  # remove worker from pool
orca recover PANE                               # attempt to unstick worker
```

### Clone Pool

```bash
orca pool                                       # list clones + status
orca clone --count N                            # create new clones
```

### Fleet (Multi-Machine)

```bash
orca fleet                                      # all hosts + worker counts
orca spawn --host devbox-1 --count 3            # spawn on remote host
```

### Monitoring

```bash
orca dash                                       # live TUI dashboard (own pane)
orca events                                     # NDJSON event stream
```

`orca events` emits orca-level events as NDJSON: task state transitions
(queued → active → done), worker health changes (healthy → stuck → recovered),
clone pool changes, and escalation alerts. These are distinct from `amux events`
(pane output, idle, layout changes) — orca consumes amux events internally and
produces higher-level orchestration events.

### Dashboard

`orca dash` runs a TUI in its own amux pane. At-a-glance view:

```
TASK          WORKER    CLONE     AGENT    STATUS     PR       AGE
LAB-123       pane-3    amux12    codex    coding     #587     2h
LAB-124       pane-5    amux08    claude   review     #590     45m
LAB-125       —         —         —        queued     —        5m

POOL: 34 free / 36 total    QUEUE: 1    STUCK: 0    MERGED: 12
```

## Configuration

```
~/.config/orca/config.toml    # global: fleet hosts, agent profiles
~/.config/orca/state.db       # global: tasks, workers, clones, event log
.orca/config.toml             # per-project overrides (clone pattern, etc.)
```

### Example `config.toml`

```toml
[daemon]
poll_interval = "30s"        # PR merge poll frequency
notification_pane = "pane-1" # fragile — see open question 2 re: auto-detection

[pool]
pattern = "~/sync/github/amux/amux*"
clone_origin = "git@github.com:weill-labs/amux.git"

[agents.codex]
start_command = "codex --yolo"
idle_timeout = "30s"
stuck_timeout = "5m"
stuck_text_patterns = ["permission prompt"]
nudge_command = "y\n"
max_nudge_retries = 3

[agents.claude]
start_command = "claude --dangerously-skip-permissions"
idle_timeout = "30s"
stuck_timeout = "5m"
stuck_text_patterns = ["tool denied"]
nudge_command = "\n"
max_nudge_retries = 3

# Fleet uses a single-coordinator topology: one orca daemon (on the local
# machine) owns all state in SQLite and drives remote amux sessions over SSH.
# Remote hosts run amux but not orca.
[fleet]
hosts = ["localhost", "devbox-1", "devbox-2"]

[fleet.devbox-1]
amux_session = "amux"
pool_pattern = "/home/cweill/sync/github/amux/amux*"
ssh_target = "devbox-1"      # Tailscale hostname
```

## Relationship to Orchestration Roadmap

The [agent orchestration roadmap](2026-03-23-agent-orchestration-roadmap.md)
proposed features that would live inside amux. With orca as a separate binary,
responsibilities shift:

| Roadmap Feature | Originally | With Orca |
|---|---|---|
| Agent auto-detection (1.1) | amux | **amux** — generic, useful for all consumers |
| Agent health states (1.2) | amux | **amux** — orca reads these, doesn't own them |
| Status bar summary (1.3) | amux | **amux** — rendering belongs in amux |
| MCP server (2.1) | amux | **amux** — generic API exposure |
| File reservation (3.1) | amux | **amux** — coordination primitive |
| Shared context board (3.2) | amux | **amux** — session-scoped state |
| Auto-nudge hooks (4.1) | amux | **orca** — policy decision, not infrastructure |
| Agent kill-switch (4.2) | amux | **amux** provides `interrupt`, orca decides *when* |
| Dashboard mode (5.1) | amux | **orca dash** — orchestration-aware, not just panes |
| Web dashboard (5.2) | amux | **orca** — future, depends on orca state |

The principle: amux provides generic capabilities (detection, health, rendering,
coordination primitives). Orca provides orchestration policy (task assignment,
lifecycle management, monitoring decisions, fleet coordination).

## MVP Scope

First milestone — enough to replace the shell scripts:

- `orca start` / `orca stop` — daemon lifecycle
- `orca assign ISSUE --prompt "..."` — single task assignment
- `orca status` / `orca status ISSUE` — task and worker status
- `orca cancel ISSUE` — abort and clean up
- `orca workers` / `orca spawn` — worker management
- `orca pool` — clone pool listing
- SQLite state persistence
- Convention-based clone pool discovery
- Single agent profile (codex)
- Stuck detection + notification via `orca events` / `orca status`
- PR merge polling via `gh` CLI
- Clone cleanup on completion/cancellation

**Deferred to post-MVP:**
- `orca dash` TUI
- `orca batch` multi-task queuing
- `orca clone` creation
- Multi-machine fleet support
- Multiple agent profiles
- Webhook-based merge detection

## Open Questions

1. **Clone identity.** Should orca assign stable names to clones (e.g.,
   `clone-01` through `clone-36`) or use the directory basename (`amux01`)?
   Directory basename is simpler but couples orca to the naming convention.

2. **Lead pane discovery.** How does orca know which pane is the lead pane for
   notifications? Config (`notification_pane`), auto-detect (pane running Claude
   Code), or flag on `orca start`?

3. **Task prompts.** `orca assign LAB-123 --prompt "..."` requires Claude Code
   to compose the prompt. Should orca also accept `--prompt-file` for longer
   prompts, or is piping via stdin sufficient?

4. **Partial failures.** If clone setup succeeds but agent spawn fails, orca
   should roll back (free the clone). What other partial failure modes need
   explicit rollback handling?

5. **Notification delivery.** Claude Code needs to learn about orca events
   (stuck workers, merged PRs, exhausted pool). Options: Claude Code polls
   `orca status` periodically, a background watcher tails `orca events` and
   injects alerts during idle windows, or amux gains a notification overlay
   that orca can post to without corrupting active panes.

6. **`gh` auth expiry.** The daemon runs unattended for hours. GitHub tokens
   can expire mid-session. Orca should detect `gh` auth failures (401/403)
   and escalate immediately rather than silently failing to poll.

7. **Repo scope.** `--project` is required on `orca start` and all rows are
   scoped by project (resolved in the State section). Remaining question: should
   `orca status` (without `--project`) show all projects or require explicit
   scoping?
