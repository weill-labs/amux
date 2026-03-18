---
name: amux-pr-workflow
description: Use when creating, updating, reviewing, or merging a PR in this repo. Covers first-push rebases, review and simplification passes, benchmark baseline requirements, and post-merge postmortems.
---

# amux PR Workflow

Use this skill when the task involves `git push`, `gh pr create`, `gh pr merge`, PR review, benchmark PR prep, or "wrap this up" work near the end of a change.

## Rules

- Rebase onto `origin/main` before the first push: `git fetch origin main && git rebase origin/main`.
- Do not present a PR as done until it has had both a review pass and a simplification pass.
- If benchmarks changed, add a `Baseline numbers` section to the PR description with representative results and hardware.
- After merge, capture a short postmortem and turn action items into issues or doc updates.

## Workflow

1. Confirm the relevant tests ran and note any gaps.
2. If this is the first push for the branch, rebase onto `origin/main`.
3. Create or update the PR.
4. Run a review pass. Prefer `codex review` when available.
5. Run a simplification pass focused on unnecessary complexity and cleanup opportunities.
6. If the change touched benchmarks, add baseline numbers before calling the PR ready.
7. After merge, record a short postmortem with learnings, pain points, and follow-up actions.

## Output Checklist

- Tests run, or an explicit testing gap is called out.
- Rebase-before-first-push handled or explicitly not needed.
- Review pass completed.
- Simplification pass completed.
- Benchmark baseline section added when relevant.
- Postmortem captured after merge.
