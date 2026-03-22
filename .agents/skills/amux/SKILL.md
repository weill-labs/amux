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
amux events --filter idle,busy,hook
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

## Delegating Tasks to Agents

When delegating work to an agent (codex, claude, grok) running in a pane, follow this sequence to avoid garbled input:

### 1. Confirm the agent is ready

Before typing a task, verify the agent is at its input prompt. Each agent has a prompt marker:

```bash
# For codex — wait for the ">" prompt
amux wait-for pane-31 ">" --timeout 30s

# For claude — wait for the ">" prompt
amux wait-for pane-5 ">" --timeout 30s

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
amux type-keys pane-31 'Fix the bug in auth.go where tokens expire too early'
amux type-keys pane-31 Enter
```

For long prompts, `type-keys` handles the full string — no need to chunk it.

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

When running tests or CI-like commands from within an amux session, the `AMUX_SESSION` environment variable can cause nesting issues. Strip it:

```bash
env -u AMUX_SESSION -u TMUX make test
env -u AMUX_SESSION -u TMUX go test ./...
```

## Prefer `send-keys` Over `type-keys` for Agent Delegation

When sending prompts to agents (codex, claude), use `send-keys` — it sends the text and Enter in one call. `type-keys` goes through the client input pipeline which can be slower and introduce translation issues.

```bash
# Preferred — one call, fast
amux send-keys 32 "Fix the bug in auth.go" Enter

# Also works but slower
amux type-keys 32 'Fix the bug in auth.go'
amux type-keys 32 Enter
```

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

## Pane References

Panes can be referenced by:
- Name: `pane-1`, `my-agent`
- Numeric ID: `1`, `31`
- Prefix match: `pane-` matches `pane-1` if unambiguous
