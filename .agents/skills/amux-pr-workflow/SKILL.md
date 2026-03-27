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
- After `gh pr create`, run `scripts/watch-pr-ci.sh` and wait for required checks on that PR.
- Once a PR exists, prefer `scripts/push-and-watch-ci.sh` over bare `git push` so the CI watch step is automatic.
- In an amux pane, prefer `scripts/gh-pr-create.sh ...` over raw `gh pr create` so pane PR metadata syncs for every agent; later `git push` calls re-sync via the repo `pre-push` hook installed by `make setup`.
- In `amux`, if the change is ready for review, open the PR proactively instead of asking whether to make one.
- Once a PR is open, keep related follow-up fixes on that PR branch. Do not leave a relevant fix only on a side branch or local branch after reporting PR status.
- If `scripts/watch-pr-ci.sh` reports failures, inspect the failed-check summary and failed-step logs, fix issues likely caused by your diff, rerun the relevant tests, and repeat up to 3 attempts before escalating.
- If CI looks flaky or unrelated to your diff, say so explicitly with evidence instead of blindly retrying or making speculative fixes.
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
3. Create or update the PR as soon as the branch is ready for review. In an amux pane, use `scripts/gh-pr-create.sh ...` when opening the PR; otherwise use `gh pr create --body-file ...` when the body is multiline. If the PR is already open and you make a related fix, commit it on that PR branch, rerun the relevant verification slice, and push before reporting status.
4. After `gh pr create`, run `scripts/watch-pr-ci.sh`. If you update an already-open PR, use `scripts/push-and-watch-ci.sh` so the push blocks on required checks and prints failed-step logs on failure.
5. If CI fails, fix the issue, rerun the relevant local verification slice, and repeat the push/watch loop up to 3 times. If the failure looks flaky or unrelated to your diff, report that explicitly with evidence before handing off.
6. Keep related follow-up fixes on that PR branch. Do not leave a relevant fix only on a side branch or local branch after reporting PR status.
7. Run a review pass. Prefer `codex review` when available, but if it stalls, do a manual diff review and state that explicitly.
8. Run a simplification pass focused on unnecessary complexity and cleanup opportunities.
9. If the branch had to be rebased or conflict-resolved after the PR was open, rerun both passes on that rebased diff before pushing again.
10. If the change affects layout math or resize behavior, compare against tmux before adding new layout state or diverging from tmux semantics.
11. If the change touched benchmarks, add baseline numbers before calling the PR ready.
12. If a rebase causes broad noisy local failures, run a targeted regression slice that represents the suspected regression before making invasive code changes.
13. Before merging, re-check the live PR state and mergeability after the latest green checks. If `main` moved, fetch and rebase again before merging.
14. After merge, verify local state with `git branch --show-current`, `git status --short --branch`, and `git rev-parse HEAD origin/main`. If needed, run `git checkout main && git pull --ff-only`.
15. If you discover a follow-up fix after merge, create a fresh branch before editing.
16. After merge, explicitly run `$postmortem` to capture learnings, pain points, and follow-up actions. Do not substitute a brief ad hoc summary.
17. In the final merge closeout, state either the logged `$postmortem` path or the explicit reason it was skipped.
18. If `$postmortem` ran, summarize the main learnings and follow-up actions in the user-facing closeout so the user gets the result directly, not only via the log file.

## Output Checklist

- Tests run, or an explicit testing gap is called out.
- Rebase-before-first-push handled or explicitly not needed.
- Open PR branches refreshed after any fetch/pull that advanced `origin/main`.
- PR CI watched after `gh pr create` and after subsequent PR updates.
- CI failures either fixed on the same PR branch or called out explicitly as flaky/unrelated with evidence.
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
