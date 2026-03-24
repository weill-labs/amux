---
name: amux
description: >
  Interact with amux, an agent-centric terminal multiplexer. Use this skill whenever
  the user mentions amux panes, capturing pane output, delegating tasks to agents in
  panes, monitoring agent progress, typing keys into panes, or managing terminal
  layouts. Also trigger when AMUX_SESSION is set in the environment and the user
  wants to interact with other panes or agents. Covers: JSON capture for structured
  pane inspection, type-keys for delegating to agents (codex, claude, grok),
  wait primitives for monitoring, and event streaming.
---

# amux — Agent Orchestration Skill

amux is a terminal multiplexer designed for AI agents. It runs a background server that owns PTYs, while clients connect over a Unix socket. This skill covers using amux as an orchestration layer — capturing pane content, delegating tasks, and monitoring agent progress.

## Session Targeting

All amux commands accept `-s <session>` to target a specific session by name. When running inside an amux pane, the `$AMUX_SESSION` env var is already set — pass it with `-s "$AMUX_SESSION"` to ensure commands reach the correct session, especially when multiple sessions are running. When `$AMUX_SESSION` is not set, omit `-s` entirely to use the default session (`amux -s ""` will fail).

```bash
# Inside an amux pane (AMUX_SESSION is set)
amux -s "$AMUX_SESSION" capture --format json

# Target a specific session by name
amux -s mysession list
```

## Quick Reference

### Capture (inspect pane content)

```bash
# Full session JSON — all visible panes with metadata, positions, agent status
amux -s "$AMUX_SESSION" capture --format json

# Single pane JSON — one pane's content, cursor, metadata
amux -s "$AMUX_SESSION" capture --format json pane-1

# Plain text — composited screen without escape codes
amux -s "$AMUX_SESSION" capture

# Single pane plain text
amux -s "$AMUX_SESSION" capture pane-1

# With ANSI escape codes preserved
amux -s "$AMUX_SESSION" capture --ansi pane-1

# Scrollback history (server-side, includes content above viewport)
amux -s "$AMUX_SESSION" capture --history pane-1
```

The JSON capture is the primary interface for agents. It includes pane content lines, cursor position, active/minimized/zoomed state, host, task, color, cwd, git branch, and agent status (idle, current_command, child_pids).

### Send Input to Panes

```bash
# Type text through client input pipeline (handles key translation)
amux -s "$AMUX_SESSION" type-keys pane-1 'echo hello'
amux -s "$AMUX_SESSION" type-keys pane-1 Enter

# Send raw keystrokes directly to PTY (bypasses client)
amux -s "$AMUX_SESSION" send-keys pane-1 'ls -la' Enter

# Wait for an agent prompt before sending a task
amux -s "$AMUX_SESSION" wait-ready pane-31 --timeout 30s

# Or fold readiness into the send itself
amux -s "$AMUX_SESSION" send-keys pane-31 --wait-ready 'Fix the bug in auth.go' Enter

# Special keys
amux -s "$AMUX_SESSION" type-keys pane-1 Escape
amux -s "$AMUX_SESSION" type-keys pane-1 C-c        # Ctrl-C
amux -s "$AMUX_SESSION" type-keys pane-1 C-u        # Clear line
amux -s "$AMUX_SESSION" type-keys pane-1 C-a        # Beginning of line
```

### Wait Primitives (monitoring)

```bash
# Block until pane has no child processes (agent finished)
amux -s "$AMUX_SESSION" wait-idle pane-1 --timeout 300s

# Block until specific text appears in pane
amux -s "$AMUX_SESSION" wait-for pane-1 "Tests passed" --timeout 60s

# Block until pane has child processes (command started)
amux -s "$AMUX_SESSION" wait-busy pane-1 --timeout 5s

# Block until layout changes (split, close, resize)
amux -s "$AMUX_SESSION" wait-layout --after 42 --timeout 3s
```

`wait-idle` returns exit 0 on success, exit 1 on timeout or EOF (pane exited). Distinguish by checking if the pane still exists afterward.

### Event Streaming

```bash
# Stream all events as NDJSON
amux -s "$AMUX_SESSION" events

# Filter to specific pane
amux -s "$AMUX_SESSION" events --pane pane-1

# Filter by event type
amux -s "$AMUX_SESSION" events --filter idle,busy,hook
```

### Pane Management

```bash
amux -s "$AMUX_SESSION" list                    # List all panes with metadata
amux -s "$AMUX_SESSION" status                  # Pane/window summary
amux -s "$AMUX_SESSION" focus pane-1            # Focus a pane
amux -s "$AMUX_SESSION" zoom pane-1             # Toggle zoom (maximize)
amux -s "$AMUX_SESSION" spawn --name my-agent   # Create a new pane
amux -s "$AMUX_SESSION" kill pane-1             # Kill a pane
amux -s "$AMUX_SESSION" minimize pane-1         # Minimize a pane
amux -s "$AMUX_SESSION" restore pane-1          # Restore minimized pane
```

### Window Management

```bash
amux -s "$AMUX_SESSION" list-windows            # List all windows
amux -s "$AMUX_SESSION" new-window              # Create a new window
amux -s "$AMUX_SESSION" select-window 2         # Switch to window by index
amux -s "$AMUX_SESSION" next-window             # Next window
amux -s "$AMUX_SESSION" prev-window             # Previous window
```

## Delegating Tasks to Agents

When delegating work to an agent (codex, claude, grok) running in a pane, follow this sequence to avoid garbled input:

### 1. Confirm the agent is ready

Before typing a task, verify the agent is at its input prompt:

```bash
# Prompt-aware wait for codex / claude / grok panes
amux -s "$AMUX_SESSION" wait-ready pane-31 --timeout 30s

# Codex trust dialog: fail fast by default, or auto-continue if you want
amux -s "$AMUX_SESSION" wait-ready pane-31 --continue-known-dialogs --timeout 30s

# Generic — capture and check the last lines
amux -s "$AMUX_SESSION" capture --format json pane-31 | python3 -c "
import sys, json
d = json.load(sys.stdin)
lines = [l for l in d.get('content', []) if l.strip()]
print('\n'.join(lines[-3:]))
"
```

### 2. Clear any stale input

```bash
amux -s "$AMUX_SESSION" type-keys pane-31 C-u    # Kill line — clears any partial input
```

### 3. Send the task

```bash
# One-shot delegation with readiness wait built in
amux -s "$AMUX_SESSION" send-keys pane-31 --wait-ready --continue-known-dialogs \
  'Fix the bug in auth.go where tokens expire too early' Enter
```

If you already ran `wait-ready`, you can also send the task with `type-keys` or plain `send-keys`.

### 4. Monitor progress

```bash
# Option A: Block until done
amux -s "$AMUX_SESSION" wait-idle pane-31 --timeout 300s

# Option B: Poll periodically
while true; do
  amux -s "$AMUX_SESSION" capture --format json pane-31 | python3 -c "
import sys, json
d = json.load(sys.stdin)
lines = [l for l in d.get('content', []) if l.strip()]
for l in lines[-5:]: print(l)
"
  sleep 30
done

# Option C: Background wait with periodic capture
amux -s "$AMUX_SESSION" wait-idle pane-31 --timeout 300s &
WAIT_PID=$!
while kill -0 $WAIT_PID 2>/dev/null; do
  sleep 30
  echo "=== $(date +%H:%M:%S) ==="
  amux -s "$AMUX_SESSION" capture --format json pane-31 | python3 -c "
import sys, json; d = json.load(sys.stdin)
lines = [l for l in d.get('content', []) if l.strip()]
for l in lines[-5:]: print(l)
"
done
```

### 5. Check results

After the agent finishes, capture the final state:

```bash
amux -s "$AMUX_SESSION" capture --format json pane-31 | python3 -c "
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
amux -s "$AMUX_SESSION" add-meta "$AMUX_PANE" pr=NUMBER issue=LAB-XXX
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

When sending prompts to agents (codex, claude), use `send-keys` — it sends the text and Enter in one call. `type-keys` goes through the client input pipeline which can be slower and introduce translation issues.

```bash
# Preferred — one call, fast, and readiness-aware
amux -s "$AMUX_SESSION" send-keys 32 --wait-ready "Fix the bug in auth.go" Enter

# Also works but slower
amux -s "$AMUX_SESSION" type-keys 32 'Fix the bug in auth.go'
amux -s "$AMUX_SESSION" type-keys 32 Enter
```

## Prefer `wait-idle` Over `sleep` Loops

Never use `sleep` to poll for agent completion. `wait-idle` blocks until the pane's shell has no child processes — it's event-driven, not polling.

```bash
# Good — event-driven, returns immediately when done
amux -s "$AMUX_SESSION" wait-idle 32 --timeout 300s

# Bad — wastes time, misses completion
sleep 30 && amux -s "$AMUX_SESSION" capture 32
```

If `wait-idle` times out, capture the pane to check progress, then wait again with a longer timeout.

## Feedback Pattern

After an agent finishes work, you can send conversational feedback:

```bash
amux -s "$AMUX_SESSION" send-keys 32 "Nice work. One thing to optimize: the scan is slow because it fetches all markets before filtering. Consider server-side filtering." Enter
```

This is useful for iterative delegation — give feedback, let the agent refine.

## Pane References

Panes can be referenced by:
- Name: `pane-1`, `my-agent`
- Numeric ID: `1`, `31`
- Prefix match: `pane-` matches `pane-1` if unambiguous
