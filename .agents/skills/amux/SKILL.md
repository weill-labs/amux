---
name: amux
description: >
  Interact with amux, an agent-centric terminal multiplexer. Use this skill whenever
  the user mentions amux panes, capturing pane output, delegating tasks to agents in
  panes, monitoring agent progress, typing keys into panes, or managing terminal
  layouts. Also trigger when AMUX_SESSION is set in the environment and the user
  wants to interact with other panes or agents. Covers: JSON capture for structured
  pane inspection, send-keys for delegating to agents (codex, claude, grok),
  wait primitives for monitoring, orchestration scripts, and event streaming.
---

# amux — Agent Orchestration Skill

amux is a terminal multiplexer designed for AI agents. It runs a background server that owns PTYs, while clients connect over a Unix socket. This skill covers using amux as an orchestration layer — capturing pane content, delegating tasks, and monitoring agent progress.

## Critical Rules

**Read before write.** Always read pane scrollback (`amux capture --history <pane> | grep -v '^$' | tail -30`) before sending keys or taking any action on a pane. Never assume a pane's state — check it.

**Use `wait idle` for agent readiness.** `wait exited` checks for child processes, which is useless for persistent agents (codex, claude, grok are always running). `wait idle` checks if the screen stopped producing output — this works for both shells and agents.

**Always start codex with `--yolo`.** Without it, codex stalls at permission prompts that are invisible to the orchestrator.

**Use `amux list` for status sweeps.** The branch column shows what workers are working on. Then use `wait idle` to check which are actively producing output vs sitting screen-quiet.

## Quick Reference

### Capture (inspect pane content)

```bash
# Full session JSON — all visible panes with metadata, positions, agent status
amux capture --format json

# Single pane JSON — one pane's content, cursor, metadata
amux capture --format json pane-1

# Plain text — composited screen without escape codes
amux capture

# Single pane plain text
amux capture pane-1

# With ANSI escape codes preserved
amux capture --ansi pane-1

# Scrollback history (server-side, includes content above viewport)
# IMPORTANT: filter blank lines before tail — buffer has trailing padding
amux capture --history pane-1 | grep -v '^$' | tail -30
```

The JSON capture is the primary interface for agents. It includes pane content lines, cursor position, active/minimized/zoomed state, host, task, color, cwd, git branch, and agent status.

**Note:** JSON capture currently only returns the visible viewport. `--history` only works with plain text output. See LAB-527 for tracking JSON + history support.

### Send Input to Panes

```bash
# Send raw keystrokes directly to PTY (preferred for agent delegation)
amux send-keys pane-1 'ls -la' Enter

# Type text through client input pipeline (handles key translation)
amux type-keys pane-1 'echo hello'
amux type-keys pane-1 Enter

# Wait for the pane to go screen-quiet before sending
amux wait idle pane-31 --settle 2s --timeout 30s

# Or fold the screen-quiet wait into the send itself
amux send-keys pane-31 --wait idle 'Fix the bug in auth.go' Enter

# Special keys
amux type-keys pane-1 Escape
amux type-keys pane-1 C-c        # Ctrl-C
amux type-keys pane-1 C-u        # Clear line
amux type-keys pane-1 C-a        # Beginning of line
```

Prefer `send-keys` over `type-keys` for agent delegation — it sends text and Enter in one call. `type-keys` goes through the client input pipeline which can be slower.

### Wait Primitives

```bash
# Block until screen output quiesces (use this for agent readiness)
amux wait idle pane-1 --settle 2s --timeout 60s

# Block until pane has no child processes
amux wait exited pane-1 --timeout 300s

# Block until specific text appears in pane
amux wait content pane-1 "Tests passed" --timeout 60s

# Block until pane has child processes (command started)
amux wait busy pane-1 --timeout 5s

# Block until layout changes (split, close, resize)
amux wait layout --after 42 --timeout 3s
```

**Which wait to use:**
- **`wait idle`** — screen stopped changing. Use for: "is the agent at its prompt?", "did it finish rendering?"
- **`wait exited`** — no child processes. Useless for persistent agent panes (codex/claude are always a child process). Useful for bare shell panes and short-lived commands.
- **`wait content`** — specific text appeared. Use for: "did the test output `PASS`?"
- **`wait busy`** — child processes started. Use for: "did the command begin?"

### Event Streaming

```bash
# Stream all events as NDJSON
amux events

# Filter to specific pane
amux events --pane pane-1

# Filter by event type
amux events --filter idle,busy,exited
```

### Pane Management

```bash
amux list                    # List all panes with metadata
amux status                  # Pane/window summary
amux focus pane-1            # Focus a pane
amux zoom pane-1             # Toggle zoom (maximize)
amux spawn --name my-agent   # Create a new pane
amux kill pane-1             # Kill a pane
amux split pane-1 --horizontal  # Split a pane
amux equalize                # Equalize column widths
amux equalize --vertical     # Equalize row heights within columns
amux equalize --all          # Equalize both dimensions
```

### Window Management

```bash
amux list-windows            # List all windows
amux new-window              # Create a new window
amux select-window 2         # Switch to window by index
amux next-window             # Next window
amux prev-window             # Previous window
```

### Pane Metadata

```bash
amux add-meta pane-1 issue=LAB-123    # Tag pane with Linear issue
amux add-meta pane-1 pr=456           # Tag pane with PR number
amux set-meta pane-1 task="fix auth"  # Set task description
amux rm-meta pane-1 issue=LAB-123     # Remove metadata
amux list                             # Shows metadata in META column
```

## Orchestration Scripts

These scripts compose amux primitives for common workflows. They are in the `scripts/` directory alongside this SKILL.md file.

### Spawn a worker

```bash
scripts/spawn-worker.sh --parent pane-109 --issue LAB-499
```

Does: split pane, create git worktree, start `codex --yolo`, wait for `idle`, accept the trust dialog when needed, and set issue metadata. Returns the new pane name.

### Delegate a task with verification

```bash
# Send a task and verify the worker started working
scripts/delegate-task.sh pane-47 --issue LAB-468 "Fix the black screen bug"
```

Does: send task via `send-keys`, wait for `idle` to break (output flowing = accepted), report if stuck.

### Batch delegation

```bash
# Dispatch multiple tasks from a JSON manifest
scripts/batch-delegate.sh tasks.json
```

Manifest format:
```json
[
  {"pane": "pane-47", "issue": "LAB-468", "task": "Fix black screen"},
  {"pane": "pane-51", "issue": "LAB-174", "task": "Fix flaky tests"}
]
```

### Worker status dashboard

```bash
# One-table view of all workers
scripts/worker-status.sh
```

Shows: pane name, issue, idle/busy/stuck state, PR number, last output line.

### Recover a stuck worker

```bash
# Detect and recover a codex worker stuck at a permission prompt
scripts/recover-worker.sh pane-68
```

Does: detect stuck state, Escape, `/exit`, `codex --yolo resume`, select session, send `.` to continue.

## Delegating Tasks to Codex Workers

### The read-before-write rule

Before sending anything to a pane, always check what it's doing:

```bash
# Read scrollback (filter blank padding)
amux capture --history pane-31 | grep -v '^$' | tail -30
```

### Spawning and delegating

```bash
# Manual steps (spawn-worker.sh pending PR #505)
amux split pane-109 --horizontal --name worker-499
# ... set up worktree, start codex --yolo, etc.
```

### Sending a task

```bash
# Check the pane is ready first
amux wait idle pane-31 --settle 2s --timeout 30s

# Send the task
amux send-keys pane-31 "Fix the black screen bug (LAB-468). TDD approach. Open a PR when done." Enter

# Verify it started working (idle should timeout = output flowing)
amux wait idle pane-31 --settle 2s --timeout 5s
# exit 1 (timeout) = good, it's working
# exit 0 (idle) = bad, it didn't start — check the pane
```

### Monitoring progress

```bash
# Check if a worker is active or idle
amux wait idle pane-31 --settle 2s --timeout 3s
# exit 0 = idle (screen quiet)
# exit 1 = working (output flowing)

# For a status sweep across all workers, use amux list + idle:
amux list  # check branches
for pane in 47 51 54; do
  amux wait idle pane-$pane --settle 2s --timeout 3s 2>&1 | sed "s/^/pane-$pane: /"
done

# Or use the worker-status script
scripts/worker-status.sh
```

### Resuming interrupted codex sessions

If codex was interrupted (Escape, context ran out, error):

```bash
# Send "." to continue — do NOT re-send the full task
amux send-keys pane-31 "." Enter
```

If codex needs a full restart:

```bash
# Exit cleanly first — Escape to cancel any prompt, then /exit
amux send-keys pane-31 Escape
amux wait idle pane-31 --settle 2s --timeout 10s
amux send-keys pane-31 "/exit" Enter
amux wait idle pane-31 --settle 3s --timeout 15s
# Restart with --yolo and resume the session
amux send-keys pane-31 "codex --yolo resume" Enter
amux wait idle pane-31 --settle 3s --timeout 30s
# Select the session (Enter) then continue with "."
amux send-keys pane-31 Enter
amux wait idle pane-31 --settle 3s --timeout 15s
amux send-keys pane-31 "." Enter
```

Or use the recovery script: `scripts/recover-worker.sh pane-31`

## Post-Delegation Cleanup

Agents (especially codex) may leave artifacts that need cleanup before a PR:

```bash
# Check for staged junk files
git diff --cached --name-only | grep -E '\.claude/worktrees/|\.context/'

# Unstage and remove if found
git reset HEAD .claude/worktrees/ .context/ 2>/dev/null
rm -rf .claude/worktrees/ .context/ 2>/dev/null
```

## Post-PR Protocol

When opening a PR from an amux pane, prefer the shared wrapper so pane PR metadata syncs for any agent:

```bash
scripts/gh-pr-create.sh --fill
```

With `make setup` active, later `git push` calls re-sync via the repo `pre-push` hook. If you already opened the PR with plain `gh pr create`, repair the pane metadata with:

```bash
scripts/sync-pane-pr-meta.sh
```

## JSON Capture Structure

The `--format json` output looks like:

```json
{
  "session": "default",
  "window": {"id": 1, "name": "main", "index": 0},
  "width": 200,
  "height": 50,
  "panes": [
    {
      "id": 1,
      "name": "pane-1",
      "active": true,
      "minimized": false,
      "zoomed": false,
      "host": "local",
      "task": "",
      "color": "rosewater",
      "cwd": "/path/to/project",
      "git_branch": "main",
      "cursor": {"col": 0, "row": 24},
      "content": ["line 1", "line 2", "..."],
      "position": {"x": 0, "y": 0, "width": 100, "height": 25},
      "agent_status": {
        "exited": false,
        "idle": false,
        "current_command": "go test ./...",
        "child_pids": [12345]
      }
    }
  ]
}
```

Key fields for agent orchestration:
- `agent_status.exited` — true when pane shell has no child processes. This corresponds to `wait exited`, not `wait idle`.
- `agent_status.idle` — true when pane output is screen-quiet. This corresponds to `wait idle`.
- `agent_status.current_command` — what's currently running
- `agent_status.child_pids` — PIDs of child processes in the pane
- `content` — array of strings, one per visible line (viewport only, no scrollback)
- `active` — whether this is the focused pane

## CI-Style Commands Inside amux

Never run `make install` from a worker agent. It installs the shared amux binary and can hot-reload unrelated sessions. When running compile checks or tests from within an amux session, strip `AMUX_SESSION` and `TMUX`:

```bash
env -u AMUX_SESSION -u TMUX go build ./...
env -u AMUX_SESSION -u TMUX go test ./...
```

## Pane References

Panes can be referenced by:
- Name: `pane-1`, `my-agent`, `worker-499`
- Numeric ID: `1`, `31`
- Prefix match: `pane-` matches `pane-1` if unambiguous
