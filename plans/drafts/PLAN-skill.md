# A Skill for rules, nono sandbox, and hook installation

> TODO: "Add a Skill to help with rules, nono sandbox and hook installation"

## Problem

Setup is multi-step and easy to get subtly wrong (install engine, register the
hook, author the global tier with the fail-closed `sandbox` directive + `trust`
dirs, stand up a nono profile). The knowledge is spread across `README.md` and
four `docs/` files. A Skill can drive it on demand.

## Shape (per writing-for-agents)

Trigger-rich `description`; thin body that **delegates** to `docs/` and the
tooling (`validate`, `test`, `selfcheck`, `conf/nono-profile.example.json`) —
don't duplicate `RULES.md`.

## Options

- **A. One `bash-gate` skill with task sections (pick).** Install → author rules
  → sandbox setup, each ending in a verify step (`validate` / `test` /
  `selfcheck`) and a link into the relevant doc. Plus a "command unexpectedly
  denied" path: `bash-gate test --json '<cmd>'`, read the reason, map to
  `EVALUATION.md` steps.
- **B. Three separate skills.** Over-fragmented; the tasks chain. Rejected.
- **C. More prose in README/CLAUDE.** The TODO wants an invocable skill.
  Rejected.

## Pick: A

Depends on the `validate`/`test`/`selfcheck` subcommands + example profile —
sequence it after those plans.

## Changes

- New `.claude/skills/bash-gate/SKILL.md`: frontmatter (name + trigger-dense
  description); the three checklists, imperative, links into `docs/`, no
  duplicated semantics; a short denial-troubleshooting path.
- `README.md`: getting-started pointer to the skill.

## Open

- Name `bash-gate` vs `bash-gate-setup` — lean `bash-gate` (covers authoring +
  debugging too).
- Repo-local `.claude/skills/` only for now; packaging later.
