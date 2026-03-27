---
name: amux
description: >
  Interact with amux, an agent-centric terminal multiplexer. Use this skill whenever
  the user mentions amux panes, capturing pane output, delegating tasks to agents in
  panes, monitoring agent progress, typing keys into panes, or managing terminal
  layouts. Also trigger when AMUX_SESSION is set in the environment and the user
  wants to interact with other panes or agents. Trigger phrases: "delegate to pane",
  "send to pane", "tell pane-X", "check pane", "status of panes", "what are the
  workers doing", "spawn a pane", "split pane", "create pane for", "worker status",
  "agent status", "which pane is working on", "give me a status report",
  "notify workers", "tell all panes". Covers: JSON capture for structured pane
  inspection, send-keys for delegating to agents (codex, claude, grok), wait
  primitives for monitoring, event streaming, and multi-pane orchestration workflows.
---

# amux — Agent Orchestration Skill

amux is a terminal multiplexer designed for AI agents. It runs a background server that owns PTYs, while clients connect over a Unix socket. This skill covers using amux as an orchestration layer — capturing pane content, delegating tasks, and monitoring agent progress.

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
amux capture --history pane-1
```

The JSON capture is the primary interface for agents. It includes pane content lines, cursor position, active/minimized/zoomed state, host, task, color, cwd, git branch, and agent status (idle, current_command, child_pids).

### Send Input to Panes

```bash
# Type text through client input pipeline (handles key translation)
amux type-keys pane-1 'echo hello'
amux type-keys pane-1 Enter

# Send raw keystrokes directly to PTY (bypasses client)
amux send-keys pane-1 'ls -la' Enter

# Wait for an agent prompt before sending a task
amux wait-ready pane-31 --timeout 30s

# Or fold readiness into the send itself
amux send-keys pane-31 --wait-ready 'Fix the bug in auth.go' Enter

# Special keys
amux type-keys pane-1 Escape
amux type-keys pane-1 C-c        # Ctrl-C
amux type-keys pane-1 C-u        # Clear line
amux type-keys pane-1 C-a        # Beginning of line
```

### Wait Primitives (monitoring)

```bash
# Block until pane has no child processes (agent finished)
amux wait-idle pane-1 --timeout 300s

# Block until specific text appears in pane
amux wait-for pane-1 "Tests passed" --timeout 60s

# Block until pane has child processes (command started)
amux wait-busy pane-1 --timeout 5s

# Block until layout changes (split, close, resize)
amux wait-layout --after 42 --timeout 3s
```

`wait-idle` returns exit 0 on success, exit 1 on timeout or EOF (pane exited). Distinguish by checking if the pane still exists afterward.

### Event Streaming

```bash
# Stream all events as NDJSON
amux events

# Filter to specific pane
amux events --pane pane-1

# Filter by event type
amux events --filter idle,busy
```

### Pane Management

```bash
amux list                    # List all panes with metadata
amux status                  # Pane/window summary
amux focus pane-1            # Focus a pane
amux zoom pane-1             # Toggle zoom (maximize)
amux spawn --name my-agent   # Create a new pane
amux kill pane-1             # Kill a pane
amux minimize pane-1         # Minimize a pane
amux restore pane-1          # Restore minimized pane
```

### Window Management

```bash
amux list-windows            # List all windows
amux new-window              # Create a new window
amux select-window 2         # Switch to window by index
amux next-window             # Next window
amux prev-window             # Previous window
```

## Quick Pane Overview

Use `amux list` for a fast tabular view of all panes with metadata — faster than `capture --format json` when you just need to see who's working on what:

```bash
# Tabular overview: pane ID, name, host, branch, cwd, task, metadata (issues/PRs)
amux list
```

This shows issue assignments (`issues=[LAB-XXX]`), PR numbers (`prs=[NNN]`), git branches, and CWDs at a glance. Use this before delegating to avoid assigning work to a pane that's already busy.

## Worker Status Report

To get a detailed status report across all worker panes:

```bash
# 1. Quick overview of assignments
amux list

# 2. For each pane, check scrollback for what it accomplished
for pane in 47 51 54; do
  echo "=== pane-$pane ==="
  amux capture --history pane-$pane 2>/dev/null | tail -30
  echo ""
done
```

Look for: "Working (Xs)" = still active, tool calls/edits = progress, "Ran git push" / "gh pr create" = PR opened, idle prompt = finished or stalled.

## Delegating Tasks to Agents

When delegating work to an agent (codex, claude, grok) running in a pane, follow this sequence:

### 0. Check existing assignments first

Before delegating, check which panes are already working on issues:

```bash
amux list   # Shows issues, PRs, branches for all panes
```

Avoid assigning work to a pane that's already busy with another issue.

### 0b. Tag the pane with issue metadata

```bash
amux add-meta <pane> issue=LAB-XXX
```

Do this before sending the task so the pane is trackable.

### 1. Confirm the agent is ready

Before typing a task, verify the agent is at its input prompt:

```bash
# Prompt-aware wait for codex / claude / grok panes
amux wait-ready pane-31 --timeout 30s

# Codex trust dialog: fail fast by default, or auto-continue if you want
amux wait-ready pane-31 --continue-known-dialogs --timeout 30s

# Generic — capture and check the last lines
amux capture --format json pane-31 | python3 -c "
import sys, json
d = json.load(sys.stdin)
lines = [l for l in d.get('content', []) if l.strip()]
print('\n'.join(lines[-3:]))
"
```

### 2. Clear any stale input

```bash
amux type-keys pane-31 C-u    # Kill line — clears any partial input
```

### 3. Send the task

```bash
# One-shot delegation with readiness wait built in
amux send-keys pane-31 --wait-ready --continue-known-dialogs \
  'Fix the bug in auth.go where tokens expire too early' Enter
```

If you already ran `wait-ready`, you can also send the task with `type-keys` or plain `send-keys`.

### 3b. Confirm the agent accepted the task

```bash
# Block until the agent starts working (look for "Working" in codex output)
amux wait content pane-31 "Working" --timeout 15s
```

This replaces sleep+grep polling. If it times out, capture the pane to see what happened.

### 4. Monitor progress

```bash
# Option A: Block until done
amux wait-idle pane-31 --timeout 300s

# Option B: Poll periodically
while true; do
  amux capture --format json pane-31 | python3 -c "
import sys, json
d = json.load(sys.stdin)
lines = [l for l in d.get('content', []) if l.strip()]
for l in lines[-5:]: print(l)
"
  sleep 30
done

# Option C: Background wait with periodic capture
amux wait-idle pane-31 --timeout 300s &
WAIT_PID=$!
while kill -0 $WAIT_PID 2>/dev/null; do
  sleep 30
  echo "=== $(date +%H:%M:%S) ==="
  amux capture --format json pane-31 | python3 -c "
import sys, json; d = json.load(sys.stdin)
lines = [l for l in d.get('content', []) if l.strip()]
for l in lines[-5:]: print(l)
"
done
```

### 5. Check results

After the agent finishes, capture the final state:

```bash
amux capture --format json pane-31 | python3 -c "
import sys, json
d = json.load(sys.stdin)
lines = [l for l in d.get('content', []) if l.strip()]
for l in lines[-20:]: print(l)
"
```

Also check what the agent left behind (branches, staged files):

```bash
git branch --list '*lab-*' '*fix-*'
git status --short
```

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

After opening a PR with `gh pr create`, set pane metadata for the current amux pane:

```bash
amux add-meta "$AMUX_PANE" pr=NUMBER issue=LAB-XXX
```

`$AMUX_PANE` is already set in pane shells. Replace `NUMBER` with the new PR number and `LAB-XXX` with the Linear issue ID.

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
        "idle": false,
        "current_command": "go test ./...",
        "child_pids": [12345]
      }
    }
  ]
}
```

Key fields for agent orchestration:
- `agent_status.idle` — true when pane shell has no child processes
- `agent_status.current_command` — what's currently running
- `content` — array of strings, one per visible line
- `active` — whether this is the focused pane

## CI-Style Commands Inside amux

Never run `make build` from an agent. It installs the shared amux binary and can hot-reload unrelated sessions. When running compile checks or tests from within an amux session, strip `AMUX_SESSION` and `TMUX`:

```bash
env -u AMUX_SESSION -u TMUX go build ./...
env -u AMUX_SESSION -u TMUX go test ./...
```

## Prefer `send-keys` Over `type-keys` for Agent Delegation

When sending prompts to agents (codex, claude), use `send-keys` — it takes a pane target and sends directly to the PTY. `type-keys` sends to the **active pane only** (no pane argument) and goes through the client input pipeline.

```bash
# Preferred — targets a specific pane, fast, readiness-aware
amux send-keys 32 --wait-ready "Fix the bug in auth.go" Enter

# WRONG — type-keys has no pane argument, sends to active pane
amux type-keys 32 'Fix the bug in auth.go'   # "32" is sent as keystrokes!

# type-keys is for the active pane only
amux type-keys 'Fix the bug in auth.go'
amux type-keys Enter
```

**Common mistake:** `amux type-keys <pane> <text>` treats the pane number as keystroke text and sends everything to the active pane. Always use `send-keys` when targeting a specific pane.

## Prefer `wait-idle` Over `sleep` Loops

Never use `sleep` to poll for agent completion. `wait-idle` blocks until the pane's shell has no child processes — it's event-driven, not polling.

```bash
# Good — event-driven, returns immediately when done
amux wait-idle 32 --timeout 300s

# Bad — wastes time, misses completion
sleep 30 && amux capture 32
```

If `wait-idle` times out, capture the pane to check progress, then wait again with a longer timeout.

## Feedback Pattern

After an agent finishes work, you can send conversational feedback:

```bash
amux send-keys 32 "Nice work. One thing to optimize: the scan is slow because it fetches all markets before filtering. Consider server-side filtering." Enter
```

This is useful for iterative delegation — give feedback, let the agent refine.

## Spawning a New Agent for a Task

When no free panes exist, split an existing one and start a new agent session:

```bash
# Split an existing pane to create a new one
amux split <existing-pane> --horizontal

# Tag, start codex, and delegate
amux add-meta <new-pane> issue=LAB-XXX
amux send-keys <new-pane> "codex --yolo" Enter
amux wait content <new-pane> "model:" --timeout 15s   # Wait for codex to start
amux send-keys <new-pane> "Fix LAB-XXX: description of the task. Branch from main, open a PR with Closes LAB-XXX."
sleep 1
amux send-keys <new-pane> Enter
amux wait content <new-pane> "Working" --timeout 15s   # Confirm accepted
```

## Notifying Workers About CI/Merge Issues

Check open PRs for failures, map them to panes, and notify:

```bash
# 1. Check PR status
gh pr list --state open --json number,title,mergeable,statusCheckRollup \
  --jq '.[] | select(.statusCheckRollup[] | .conclusion == "FAILURE") | {number, title, mergeable}'

# 2. Find which pane owns each PR
amux list   # Look at prs=[NNN] column

# 3. Notify the worker
amux send-keys <pane> "PR #NNN has failing CI tests. Fix the test failures, push, and verify CI goes green." Enter
amux wait content <pane> "Working" --timeout 15s

# For merge conflicts, add rebase instructions:
amux send-keys <pane> "PR #NNN has merge conflicts with main. Rebase onto origin/main, resolve conflicts, push." Enter
```

## Reassigning Workers to New Tasks

When workers finish a task and need new assignments, follow this sequence strictly. **Never skip the postmortem verification step** — `/clear` destroys context permanently.

### 1. Verify postmortem completed

Before clearing any worker, confirm it logged its postmortem:

```bash
# Check each worker's last lines for postmortem completion
for pane in 47 51 54; do
  echo "=== pane-$pane ==="
  amux capture pane-$pane 2>/dev/null | tail -10
  echo ""
done
```

Look for: "Logged to ~/sync/postmortems/..." or a postmortem summary block. If a worker hasn't run its postmortem yet, send it the command and wait:

```bash
amux send-keys pane-47 '/postmortem' Enter
amux wait-idle pane-47 --timeout 60s
```

### 2. Clear context

Only after confirming postmortem completion:

```bash
for pane in 47 51 54; do
  amux send-keys pane-$pane '/clear' Enter
done
```

### 3. Assign new work by priority

Tag metadata first, then send the task. Assign highest priority issues first:

```bash
# Tag and assign
amux add-meta pane-47 issue=LAB-XXX
amux send-keys pane-47 'Fix LAB-XXX: description. Branch from main, open a PR with Closes LAB-XXX.' Enter
```

### 4. Confirm workers accepted

```bash
for pane in 47 51 54; do
  amux wait content pane-$pane "Working" --timeout 15s 2>/dev/null && \
    echo "pane-$pane: ACCEPTED" || echo "pane-$pane: NOT STARTED"
done
```

## Pane References

Panes can be referenced by:
- Name: `pane-1`, `my-agent`
- Numeric ID: `1`, `31`
- Prefix match: `pane-` matches `pane-1` if unambiguous
