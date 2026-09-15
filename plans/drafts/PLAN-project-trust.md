# Allow `trust` in project config to add project-specific binaries

## Problem

Repos ship tools in local bin dirs (`node_modules/.bin`, `vendor/bin`,
`.venv/bin`). Today the only way to resolve them is the machine-wide global
trust list. Operators want per-repo trust.

## Why this isn't already allowed

`trust` is global-only **by design**: it widens policy, and the project tier is
attacker-editable (prompt injection can write `<cwd>/.bash-gate`). A
naïve project `trust` would be a full bypass:

```
trust /any/attacker/dir
allow eviltool ANY          # project allow already widens ask→allow
```

Two controls already stand between that file and execution, and the design must
keep both:

- **Attestation.** `bash-gate attest` (`attest.go`/`attest_verify.go`, enforced
  in `loadConfig` via `verifyAttestation`) is mandatory: a tier that is
  unattested or changed since attest yields deny-all. So the project tier is not
  *freely* attacker-editable in effect — an attacker who writes or edits
  `<cwd>/.bash-gate` breaks its hash and gets deny-all, not a bypass. The
  operator must review and re-attest out-of-band before rules take effect.
- **The rest of the pipeline.** A trusted dir alone allows nothing: the command
  still needs an allow rule, still loses to any deny, still hits `uncondDeny`.

Attestation covers **rule text, not the contents of a trusted dir**. An attested
`trust node_modules/.bin` still authorizes whatever binary later lands there. So
project trust must stay narrow, and any design has to (a) keep the widening
*authority* in the operator's hands and (b) make every honored project `trust`
line part of what attestation hashes.

## Design

**Global opt-in + within-cwd fence.** A new global-only directive
`project-trust off|under-cwd` (default `off` = today's behavior). Under
`under-cwd`, a project `trust <dir>` is honored only if the dir resolves inside
`cwd` (reuse `within()`); otherwise it is ignored and logged as today. A dir may
be spelled relative (`trust node_modules/.bin`) and is resolved against `cwd`, so
the common case needs no absolute paths. This covers every real case — all repo
bin dirs live under cwd — while blocking `trust /tmp/evil`. The authority to opt
in stays global (operator, per machine); the specific dirs go per-repo (and must
be attested).

There are deliberately only two modes: `off` and `under-cwd`. No `anywhere` mode
— it would put unfenced widening back in the attacker tier, and nothing needs it.

Alternatives rejected:

- **Global-declared allowlist** (`project-trust-allow node_modules/.bin`):
  tighter but more ceremony, and a strict subset of the above. Add later only if
  a repo needs to forbid specific under-cwd dirs.
- **Silent within-cwd, no global gate**: widening in the attacker tier by
  default. No.

## Changes

- `rules.go`: add `config.projectTrust`; parse `project-trust` (global-only,
  before rules). In the project `trust` branch, when the gate is `under-cwd`,
  accept the dir if `within(dir, cwd)`, else keep ignoring + logging. Thread
  `cwd` into the project parse.
- `attest_verify.go` (**load-bearing**): `canonicalizeTier` currently emits
  `trust` lines only for the global tier. Make it also emit *honored* project
  `trust` lines for project tiers — otherwise an attacker can add `trust` to an
  attested project file without changing its hash, and attestation silently stops
  covering the new authority. Project records get no `normalized-path` snapshot
  (unlike global): project trust dirs are cwd-relative and typically absent from
  PATH, so there's nothing to snapshot — the within-cwd fence plus the rules-hash
  covering the `trust` line is the control.
- `validate.go`: warn when a project `trust` will be ignored (gate off / escapes
  cwd); stat project-trusted dirs.
- Docs: `RULES.md` §1/§3 + "Attestation", `EVALUATION.md` step 10,
  `ARCHITECTURE.md` (the carve-out, the attestation coupling, and the residual
  risk below).
- Tests: parse `project-trust`; honored/ignored by mode and by within-cwd;
  resolve-in-project-dir still needs an allow rule; a project `trust` change
  flips the attestation hash.

## Residual risk to document

`under-cwd` lets a repo self-authorize a binary an attacker dropped *inside* the
repo (poisoned dependency), and attestation doesn't close this — it hashes rule
text, so an attested `trust node_modules/.bin` blesses whatever binary lands
there afterward. This is supply-chain exposure the operator opts into per
machine by enabling the gate and has to be well documented to make the operator aware. The operator can mitigate by turning `project-trust off`, or not explicitly `allow`ing binaries, falling back to `ask` behaviour.
