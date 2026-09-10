# Rule attestation: detect tampered rule tiers

> Supersedes the earlier "read-only project rules" draft. That approach is dead:
> nono has no write-scoped deny, so a file cannot be made *readable-but-not-writable*
> inside a writable workdir (see `docs/research/nono-readonly-project-rules.md`).
> We switch from *enforcement* to *detection*.

## Problem

The project tier (`<cwd>/.claude-bash-rules`) lives in the writable workdir, so
it's agent/attacker-editable (`ARCHITECTURE.md:126-128`). A project `allow` can
widen an unmatched (`ask`) command to `allow`, so either in-session prompt
injection *or* a hostile repo that *ships* a malicious tier can bypass a prompt.
We want the gate to notice when a rule tier is not the one a human approved, and
fail closed when it isn't.

Enforcement via the sandbox was rejected: making the tier read-only is not
expressible on nono (no write-only deny; `deny` also kills read, so bash-gate —
which runs *inside* the sandbox — could no longer load the tier at all). So this
is a hook-pipeline feature, not a profile concern.

## Model

Detection, not enforcement. Rule files stay **writable** in-sandbox on purpose
("propose freely, inert until acked"). The operator acknowledges a known-good
ruleset **outside** the sandbox with `bash-gate attest`; the hook recomputes a
hash **inside** the sandbox and denies everything on any divergence.

- Covers **both** vectors: in-session tampering and hostile-shipped tiers — a
  shipped `.claude-bash-rules` has no effect until a human attests it *on this
  machine*.
- Trust anchor: the attestation store lives under `~/.claude-bash-rules/`, which
  the deployment profile already grants **read-only** in-sandbox
  (`.sandbox/profile.template.json:30`). No new sandbox changes. Verifying the
  profile actually delivers that shape (the ancestor-grant footgun,
  `nono-profile-guide.md:1143`) is **out of scope** — delegated to
  `PLAN-sandbox-selftest`.

## Scope honesty (don't oversell)

- Cannot distinguish a repo the user trusts from a hostile one — it forces a
  human `attest` either way. Its shipped-file value is "no project `allow` takes
  effect until a human eyeballed it once."
- The anchor is only as good as the profile's read-only grant on the store. A
  profile that grants any ancestor (`$HOME`, a workdir parent) writable makes the
  store writable in-sandbox and the whole feature is theater. That verification
  is `PLAN-sandbox-selftest`'s job.
- Never widens a global `deny`; that invariant is unchanged.

## What is hashed

The **normalized** ruleset: parse → drop comments/whitespace → sort rules →
canonical serialize → `sha256`. Cosmetic edits (comments, indentation, rule
order) don't force re-attest.

- Sound only because evaluation is order-independent (`deny` > `allow` > `ask`).
  **Implementation precondition: confirm the aggregation in `policy.go` is truly
  order-independent before relying on sorted-canonical hashing.**
- No new trust surface: the loader already parses these rules to evaluate them.

Two tiers, verified independently:

- **Project record** = `sha256(normalized project rules)`, one per project.
- **Global record** = `sha256(normalized global rules — incl. `trust`/`sandbox`/
  `returntype` directives and the trust-dir set)` **plus** `normalized_path`
  (`PATH` ∩ trust-dirs, in order), snapshotted from the attester's live `PATH`.

## Verdict

Trusted iff **all** hold; else **deny every command** until re-attest:

1. global record matches, **and**
2. project file present ⇒ its record matches, **and**
3. live normalized-`PATH` matches the attested `normalized_path`.

- No project file ⇒ nothing to attest ⇒ normal global-only operation.
- Whole-tier trust: no partial obedience, no tightening-only carve-out — one
  check, `hash == attested else deny`. A `schema`/`norm-version` mismatch is
  treated as a mismatch with a clear "re-attest" message (never a silent wrong
  verdict).
- Symmetric with the existing kill switch: any tier-integrity failure denies all,
  same as a load failure.

## Store & record format

Store: `~/.claude-bash-rules/attestations/`, one file per project
`<sha256(abs rules path)>-<basename of project dir>.attest`, plus `global.attest`.
Under the already-read-only root.

Rule-file-style: bare `key value` headers, `#` comments anywhere, embedded
normalized rules after a bare `rules` marker. The embedded rules are **advisory
only** (diagnostics/future diff on failure) — the verdict always recomputes the
hash from the live file, never trusts the record's copy. No new dependency
(`crypto/sha256` is stdlib; the format is hand-parsed like the DSL).

```
# reviewed w/ PR #123
schema 1
tier project
norm-version 1
rules-path /Users/me/repos/foo/.claude-bash-rules
rules-hash sha256:abcd...
attested-at 2026-09-09T12:34:56Z
attested-by me@host
rules
allow git status ANY
deny git reset HAS --hard
```

Global adds `normalized-path /usr/bin:/usr/local/bin`. The `rules` marker
delimits header from the verbatim block so bare keys can't collide with
global-tier directives (`trust`, `sandbox`, …).

## Options considered

- **Read-only project tier via nono** — rejected: not expressible (no write-only
  deny; `deny` kills read too). `docs/research/nono-readonly-project-rules.md`.
- **Move project rules to a read-only install root** — rejected: loses repo-local
  rules (they must travel in the checkout).
- **Attestation (pick).** Keeps rules repo-local and writable, covers both
  vectors, platform-agnostic (a plain read-only grant on the store holds on
  macOS and Linux/WSL2 alike — WSL2 degradation is network-only).

## Changes

- `main.go`: `case "attest": os.Exit(runAttest(...))`; help entry.
- New `attest.go`: normalize + `sha256` a tier; write/read `.attest` records;
  `bash-gate attest <project-rules>` and a global mode; runs outside the sandbox.
- New `attest_verify.go` (or fold into `rules.go`): recompute hashes at the single
  load path (`loadConfig`), compare against records, set the deny-all verdict on
  any miss. Pure normalize/hash/compare helpers → unit-testable.
- `rules.go`/`policy.go`: wire the verdict into `decide()` (deny-all on
  integrity failure, mirroring the load-failure path).
- `Makefile`: `make install` **auto-attests the starter global rules** so a fresh
  install isn't bricked (deny-all) on first run; later global edits require an
  explicit re-attest.
- Tests: `attest_test.go` (normalization determinism, record round-trip,
  order/comment invariance, mismatch → deny, missing → deny, tightening still
  denies, `norm-version` bump → deny).
- Docs: `RULES.md` (attestation + `attest` usage), `ARCHITECTURE.md` (rewrite the
  `:126-128` residual-risk row from "agent-editable, accepted" to "detected via
  attestation"; note the trust anchor and the ancestor-footgun caveat),
  `CLAUDE.md` layout table.
- Rename: this file is `PLAN-rule-attestation.md`; branch `feature/rule-attestation`.

## Open / residual

- **PATH context (Q9c).** `attest` captures the attester's live `PATH`; if nono
  hands the in-sandbox child a different `PATH`, `normalized_path` won't match →
  deny-all. Surface is small (only trust-dir∩`PATH` is bound), but the authoritative
  in-sandbox `PATH` is really `PLAN-sandbox-selftest`'s domain — reconcile there;
  consider shipping rules-hash verification first and adding the `PATH` component
  after.
- **Order-independence** of `policy.go` aggregation — verify before shipping the
  sorted-canonical hash.
- **Global bootstrap** — confirm `make install` auto-attest vs. requiring an
  explicit first `attest`.
- Depends on the deployment profile granting the store read-only in-sandbox
  (`PLAN-nono-profile-example`) and on `PLAN-sandbox-selftest` to assert it.
