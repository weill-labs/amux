# Recover All Workers Checklist

Use this checklist when recovering workers after a crash, hot-reload failure, or session disruption. Follow every step in order. Do not skip steps.

## Step 1: Assess

Run `scripts/worker-status.sh` (or `amux list --no-cwd` if the script is broken).

For each worker, classify:
- **Shell prompt**: codex exited, at bash `$` prompt
- **Codex prompt**: codex running but idle at `›` prompt
- **Codex busy**: codex actively working (output flowing)
- **Unknown**: can't determine — read scrollback with `amux capture --history <pane> | grep -v '^$' | tail -20`

## Step 2: Classify work state

For each worker, determine:

| State | How to detect | Action |
|-------|--------------|--------|
| **Open PR** | Branch name contains PR number, `gh pr list --state open` confirms | Resume codex, continue PR work |
| **Uncommitted work** | `git status --short` shows changes | Resume codex, continue work |
| **Merged/done** | Branch is `main`, or PR was merged | Worker is free |
| **Mid-work, no PR** | On feature branch, commits exist, no PR | Resume codex, continue work |

## Step 3: Present to user

Show the classification table to the user. Include:
- Pane name
- Current branch
- Issue assignment (from pane metadata)
- Work state (open PR / uncommitted / done / mid-work)
- Recommended action

**STOP HERE. Wait for user approval before taking any action.**

## Step 4: Recover active workers (user-approved only)

For workers with in-progress work:

1. If codex is running: send `.` to continue
2. If codex exited: `/exit` then `codex --yolo resume`, select session, send `.`
3. If resume has no session: start fresh `codex --yolo`, send context-aware task describing their specific in-progress work

## Step 5: Handle done workers (user-approved only)

For workers whose work is merged/done:

1. Start `codex --yolo`
2. **STOP. Leave at idle prompt.**
3. Report to user: "N workers at idle codex prompts, ready for assignments."

**NEVER tell done workers to pick up work from the backlog.**
**NEVER assign issues without explicit user approval.**
