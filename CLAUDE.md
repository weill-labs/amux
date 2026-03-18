# CLAUDE.md

This repo uses [AGENTS.md](AGENTS.md) as the canonical shared guidance. Read and follow `AGENTS.md` first.

If `CLAUDE.md` and `AGENTS.md` ever disagree, follow `AGENTS.md` and update this file.

## Claude-Specific Behavior

Claude Code has extra repo automation configured in `.claude/settings.json`.

- `SessionStart` runs `.claude/hooks/session-start.sh` to ensure `make setup` has activated `.githooks`.
- `Stop` runs `.claude/hooks/tdd-check.sh` and warns when implementation `.go` files changed without test changes.
- `PostToolUse` on Bash runs `.claude/hooks/post-pr-review.sh` and `.claude/hooks/post-merge-postmortem.sh`.

Treat hook feedback as repo policy, not optional guidance.

## Claude PR Workflow

When Claude hook messaging says to run review and simplification passes, use the Claude review agents that fit the task. The goal is the shared repo rule from `AGENTS.md`: do not present a PR as done until it has had both a review pass and a simplification pass.
