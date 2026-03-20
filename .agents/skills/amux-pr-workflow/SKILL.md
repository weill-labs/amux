---
name: amux-pr-workflow
description: Use when creating, updating, reviewing, or merging a PR in this repo. Covers first-push rebases, review and simplification passes, benchmark baseline requirements, and post-merge postmortems.
---

# amux PR Workflow

Use this skill when the task involves `git push`, `gh pr create`, `gh pr merge`, PR review, benchmark PR prep, or "wrap this up" work near the end of a change.

## Rules

- Rebase onto `origin/main` before the first push: `git fetch origin main && git rebase origin/main`.
- This repo is squash-only on GitHub. Use `gh pr merge --squash`; merge and rebase merges will fail.
- GitHub auto-merge is disabled on this repo. Wait for green checks, then merge manually.
- Prefer `gh pr create --body-file ...` for multiline PR descriptions, especially when they include backticks or code fences.
- Do not present a PR as done until it has had both a review pass and a simplification pass.
- If benchmarks changed, add a `Baseline numbers` section to the PR description with representative results and hardware.
- If a rebase triggers broad noisy local failures, verify with a targeted regression slice before making invasive code changes.
- After merge, verify local state explicitly: confirm the checkout is on `main`, the worktree is clean, and `HEAD` matches `origin/main`.
- After merge, any follow-up fix goes on a fresh branch and PR. Do not make extra commits on local `main`.
- After merge, run the `postmortem` skill and turn action items into issues or doc updates.

## Workflow

1. Confirm the relevant tests ran and note any gaps.
2. If this is the first push for the branch, rebase onto `origin/main`.
3. Create or update the PR. Use `gh pr create --body-file ...` when the body is multiline.
4. Run a review pass. Prefer `codex review` when available.
5. Run a simplification pass focused on unnecessary complexity and cleanup opportunities.
6. If the change affects layout math or resize behavior, compare against tmux before adding new layout state or diverging from tmux semantics.
7. If the change touched benchmarks, add baseline numbers before calling the PR ready.
8. If a rebase causes broad noisy local failures, run a targeted regression slice that represents the suspected regression before making invasive code changes.
9. Before merging, re-check the live PR state and mergeability after the latest green checks. If `main` moved, fetch and rebase again before merging.
10. After merge, verify local state with `git branch --show-current`, `git status --short --branch`, and `git rev-parse HEAD origin/main`. If needed, run `git checkout main && git pull --ff-only`.
11. If you discover a follow-up fix after merge, create a fresh branch before editing.
12. After merge, explicitly invoke the `postmortem` skill to capture learnings, pain points, and follow-up actions.

## Output Checklist

- Tests run, or an explicit testing gap is called out.
- Rebase-before-first-push handled or explicitly not needed.
- Review pass completed.
- Simplification pass completed.
- Squash merge policy followed.
- Final mergeability check handled before merge.
- Benchmark baseline section added when relevant.
- Local post-merge state verified.
- Follow-up fixes kept off local `main`.
- `postmortem` skill run after merge.
