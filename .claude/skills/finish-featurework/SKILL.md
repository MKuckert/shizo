---
name: finish-featurework
description: >
  Finish feature work: archive the plan, move the kanzo task to review, and stage
  the changes. Use when the user says "finish", "done", "ship it", or wants
  to wrap up a completed feature.
user_invocable_only: true
---

Close out the feature you're currently working on.

## Steps

1. **Identify the plan.** Find the matching file in `plans/drafts/PLAN-*.md`.
   If the argument is ambiguous or matches multiple files, ask the user to
   pick one. If no match is found, stop and say so.

2. **Archive the plan.** Move the file to
   `plans/archive/PLAN-YYYY-MM-DD-<slug>.md` using today's date. The slug is
   the part after `PLAN-` in the original filename (minus `.md`). Use
   `git mv` so git tracks the rename.

3. **Update the kanzo task.** Find the task in the kanzo `shizo` project
   whose description links the plan file (look for the
   `plans/drafts/PLAN-<slug>.md` path, or match the title if no link exists).
   Add a comment summarizing what was done and noting the archived plan path,
   then move the task to `review`. Never move it to `done` — that's the
   user's call. If no matching task is found, warn the user but continue.

4. **Stage everything.** Run `git add -A` to stage the plan move and any other
   local changes the user made as part of this feature.

5. **Report.** Print what was done:
   - A short summary of the changes made (e.g., "Added feature X, updated docs, fixed bug Y").
   - Plan archived to: `<new path>`
   - Kanzo task moved to review: shizo#<n> (or "no matching task found")
   - Changes staged (show `git status --short` output)
   - Propose a commit message summarizing the changes (e.g., "feat: adds feature X")
   - A reminder that the user should review the changes, commit them when ready and then do the following (quote literally):
      - merge the feature branch into main
      - remove the git worktree (`git worktree remove ../<feature-path>`)
      - remove the feature branch (`git branch -d <feature-branch>`)
      - push the changes to the remote repository

Do **not** commit. The user decides when and how to commit.
