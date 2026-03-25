---
name: amux-pr-workflow
description: Use when creating, updating, reviewing, or merging a PR in this repo. Covers first-push rebases, review and simplification passes, benchmark baseline requirements, and post-merge `$postmortem` runs.
---

# amux PR Workflow

Use this skill when the task involves `git push`, `gh pr create`, `gh pr merge`, PR review, benchmark PR prep, or "wrap this up" work near the end of a change.

## Rules

- Rebase onto `origin/main` before the first push: `git fetch origin main && git rebase origin/main`.
- Do not `git pull` a dirty local `main`. If `main` has local changes, start the next branch from fresh `origin/main` instead of updating that checkout in place.
- Do not use `git worktree` unless the user explicitly asks for it.
- If `git fetch origin main` or `git pull` advances `origin/main` while a PR branch is open, refresh that branch onto `origin/main` before treating the PR as current again.
- If the PR drifts after opening and you rebase or resolve conflicts, rerun the review pass and simplification pass on the rebased diff before calling the PR ready again.
- In non-interactive sessions, prefer `GIT_EDITOR=true git rebase --continue` so rebase continuation does not stall in `vim`.
- This repo is squash-only on GitHub. Use `gh pr merge --squash`; merge and rebase merges will fail.
- GitHub auto-merge is disabled on this repo. Wait for green checks, then merge manually.
- Prefer `gh pr create --body-file ...` for multiline PR descriptions, especially when they include backticks or code fences.
- In `amux`, if the change is ready for review, open the PR proactively instead of asking whether to make one.
- Once a PR is open, keep related follow-up fixes on that PR branch. Do not leave a relevant fix only on a side branch or local branch after reporting PR status.
- Do not present a PR as done until it has had both a review pass and a simplification pass.
- If `codex review` or other external review tooling stalls, fall back to a manual diff review and say so explicitly.
- If benchmarks changed, add a `Baseline numbers` section to the PR description with representative results and hardware.
- If a rebase triggers broad noisy local failures, verify with a targeted regression slice before making invasive code changes.
- After merge, verify local state explicitly: confirm the checkout is on `main`, the worktree is clean, and `HEAD` matches `origin/main`.
- After merge, any follow-up fix goes on a fresh branch and PR. Do not make extra commits on local `main`.
- After merge, explicitly run `$postmortem`. A short manual summary does not count. Turn action items into issues or doc updates.
- Do not say `$postmortem` ran unless you have the logged `~/.local/share/postmortems/...` path.
- In the final merge closeout, include the `$postmortem` log path, summarize the key learnings and concrete action items, and say whether they were implemented now or left as follow-up work.
- If `$postmortem` is skipped, say so explicitly and give the reason. Do not imply it was done.

## Workflow

1. Confirm the relevant tests ran and note any gaps.
2. If this is the first push for the branch, rebase onto `origin/main`. If the local `main` checkout is dirty, do not update it in place; create the next branch directly from `origin/main`. If the PR is already open and a fetch/pull advanced `origin/main`, refresh the branch onto `origin/main` before continuing.
3. Create or update the PR as soon as the branch is ready for review. Use `gh pr create --body-file ...` when the body is multiline. If the PR is already open and you make a related fix, commit it on that PR branch, rerun the relevant verification slice, and push before reporting status.
4. Run a review pass. Prefer `codex review` when available, but if it stalls, do a manual diff review and state that explicitly.
5. Run a simplification pass focused on unnecessary complexity and cleanup opportunities.
6. If the branch had to be rebased or conflict-resolved after the PR was open, rerun both passes on that rebased diff before pushing again.
7. If the change affects layout math or resize behavior, compare against tmux before adding new layout state or diverging from tmux semantics.
8. If the change touched benchmarks, add baseline numbers before calling the PR ready.
9. If a rebase causes broad noisy local failures, run a targeted regression slice that represents the suspected regression before making invasive code changes.
10. Before merging, re-check the live PR state and mergeability after the latest green checks. If `main` moved, fetch and rebase again before merging.
11. After merge, verify local state with `git branch --show-current`, `git status --short --branch`, and `git rev-parse HEAD origin/main`. If needed, run `git checkout main && git pull --ff-only`.
12. If you discover a follow-up fix after merge, create a fresh branch before editing.
13. After merge, explicitly run `$postmortem` to capture learnings, pain points, and follow-up actions. Do not substitute a brief ad hoc summary.
14. In the final merge closeout, state either the logged `$postmortem` path or the explicit reason it was skipped.
15. If `$postmortem` ran, summarize the main learnings and follow-up actions in the user-facing closeout so the user gets the result directly, not only via the log file.

## Output Checklist

- Tests run, or an explicit testing gap is called out.
- Rebase-before-first-push handled or explicitly not needed.
- Open PR branches refreshed after any fetch/pull that advanced `origin/main`.
- Related follow-up fixes pushed to the already-open PR branch before PR status is reported complete.
- Review pass completed.
- Simplification pass completed.
- Review/simplification rerun after any post-open rebase or conflict resolution.
- Squash merge policy followed.
- Final mergeability check handled before merge.
- Benchmark baseline section added when relevant.
- Local post-merge state verified.
- Follow-up fixes kept off local `main`.
- `$postmortem` explicitly run after merge, or a clear skip reason is stated.
- If `$postmortem` ran, the log path is reported.
- If `$postmortem` ran, the key learnings and action items are also summarized in the final handoff.
