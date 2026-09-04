# Allow `trust` in project config to add project-specific binaries

> TODO: "Allow `trust` in project config to add project-specific binaries"

## Problem

Repos ship tools in local bin dirs (`node_modules/.bin`, `vendor/bin`,
`.venv/bin`). Today the only way to resolve them is the machine-wide global
trust list. Operators want per-repo trust.

## The blocker (don't skip this)

`trust` is global-only **by design**: it widens policy, and the project tier is
attacker-editable (prompt injection can write `<cwd>/.claude-bash-rules`). Naïve
project `trust` is a full bypass:

```
trust /any/attacker/dir
allow eviltool ANY          # project allow already widens ask→allow
```

So the widening authority must stay in the global tier. (Mitigations still
apply: the command still needs an allow rule, still loses to any deny, still
hits `uncondDeny` — a trusted dir alone allows nothing.)

## Options

- **A. Global opt-in + within-cwd fence (pick this).** New global-only directive
  `project-trust off|under-cwd` (default `off` = today). Under `under-cwd`, a
  project `trust <dir>` is honored only if the dir resolves inside `cwd` (reuse
  `within()`). Covers every real case (all repo bin dirs live under cwd); blocks
  `trust /tmp/evil`. Authority stays global; specifics go per-repo.
- **B. Global-declared allowlist** (`project-trust-allow node_modules/.bin`).
  Tighter but more ceremony, and it's a subset of A. Do it later if a repo needs
  to forbid specific dirs.
- **C. Silent within-cwd, no global gate.** Rejected: widening in the attacker
  tier by default.

## Pick: A

Residual risk to document: `under-cwd` lets a repo self-authorize a binary an
attacker dropped *inside* the repo (poisoned dependency). Supply-chain exposure
the operator opts into per-machine.

## Changes

- `rules.go`: `config.projectTrust`; parse `project-trust` (global-only,
  before rules); in the project `trust` branch, when gate is on, accept the dir
  if `within(dir, cwd)` else keep ignoring+logging. Thread `cwd` into the
  project parse.
- `validate.go`: warn when a project `trust` will be ignored (gate off / escapes
  cwd); stat project-trusted dirs.
- Docs: `RULES.md §1/§3`, `EVALUATION.md` step 10, `ARCHITECTURE.md` (carve-out +
  new residual risk).
- Tests: parse; honored/ignored by mode and by within-cwd; resolve-in-project-dir
  needs an allow rule.

## Open

- Allow relative spelling (`trust node_modules/.bin`)? Lean yes.
- Ship an `anywhere` mode? Lean no — YAGNI until asked.
