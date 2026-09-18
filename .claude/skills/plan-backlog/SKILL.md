---
name: plan-backlog
description: >
  Plan a backlog item with the ponytail mindset — laziest solution that works.
  Takes a backlog item description as argument and produces a minimal
  implementation plan. Use when the user says "plan backlog" or wants to plan
  a task with minimal overhead.
user_invocable_only: true
---

First, load the ponytail skill to activate lazy-senior-dev mode:

/ponytail

Then plan the backlog item described in `$ARGUMENT`:

1. **Understand the item.** Read the backlog item carefully. If it references
   code, files, or systems in this repo, look them up — never guess. Trace the
   real flow end to end before planning anything.

2. **Question the need.** Apply ponytail rule 1: does this need to exist at all?
   If speculative, say so and stop. If only part of the item is needed now, say
   which part and drop the rest. Use /grill-me skill to ask the user questions
   and clarify the item.

3. **Find what already exists.** Search the codebase for existing code, helpers,
   patterns, or dependencies that already cover part or all of the work. Reuse
   beats rewrite.

4. **Produce the plan.** Write the plan to `plans/drafts/PLAN-<slug>.md` with a
   numbered list of concrete steps — files to touch, functions to change, tests
   to add. Each step is one sentence. The plan is the shortest path that satisfies
   the item. No scaffolding for later, no speculative abstractions.

5. **Link the plan.** Update the corresponding entry in the `BACKLOG.md` file to
   point to the new plan file.

6. **Call out what you're skipping** and when it would matter, in one line at
   the end.

Format:

```
## Plan: <backlog item title>

1. <step>
2. <step>
...

Skipped: <what>, add when <trigger>.
```

No essays. No design docs. No feature tours. The plan should be shorter than
the code it describes.
