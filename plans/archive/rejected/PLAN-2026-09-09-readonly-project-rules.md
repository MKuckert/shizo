# Enforce read-only project-level rules in the sandbox? — NOT IMPLEMENTABLE

> Superseded by [`plans/drafts/PLAN-rule-attestation.md`](plans/drafts/PLAN-rule-attestation.md).
> The picked approach (A: read-only project tier via the nono profile) cannot be
> built. We pivot from *enforcement* to *detection* (attestation). Full feasibility
> study: [`docs/research/nono-readonly-project-rules.md`](docs/research/nono-readonly-project-rules.md).

## Why this can't be implemented

The goal was a file that is **readable but not writable** inside a writable
workdir. nono cannot express that (verified empirically on macOS, nono 0.75.0):

- **No write-scoped deny.** nono's filesystem model is a *union* of positive
  grants (`allow`=rw, `read`=ro, `write`=wo) plus one **absolute** `deny`. There
  is no "deny write but keep read" for a subpath.
- **A read grant doesn't downgrade a broader write grant.** Layering `read_file`
  on top of an `allow`ed workdir leaves the file read **+ write** — grants only
  ever widen (union), never subtract.
- **`deny` kills read too, which is self-defeating.** `deny **/.claude-bash-rules`
  does block writes (and is even recreate-proof on macOS), but it also removes
  *read* — and bash-gate runs *inside* the sandbox, so it could no longer load
  the project tier at all. The feature would silently disable itself in exactly
  the environment that matters.
- **The only readable+unwritable shape inverts the workdir** (grant the dir
  read-only, carve writable paths back), which abandons the "workdir stays
  writable" requirement — impractical for a general agent workdir.
- **Linux/WSL2 is worse.** Even the self-defeating `deny` path is a hard startup
  error on Linux (Landlock can't deny-within-allow) without supervised mode, and
  its recreate-proofing is macOS-only.

Option C (move rules to the read-only install root) *would* work but loses the
repo-local ergonomic (rules must travel committed in the checkout). So both
enforcement options are out; the successor plan detects tampering instead of
preventing writes.

## Problem

The project tier (`<cwd>/.claude-bash-rules`) lives in the writable workdir, so
it's agent/attacker-editable — a documented residual risk. A project `allow`
can widen `ask`→`allow`, so prompt injection that *writes* the project rules
mid-session can bypass a prompt. Want the file read-only from inside the sandbox
while the rest of the workdir stays writable.

## Scope honesty (don't oversell)

Read-only stops **in-session tampering** (create/edit the project tier to widen
policy). It does **not** stop a hostile repo that *ships* a malicious
`.claude-bash-rules` — it's already there before the agent runs. And it can
never widen a global `deny` regardless. So this narrows one vector, not the
class.

Consistent with existing design: `ARCHITECTURE.md` already delegates
self-protection to the nono profile ("Self-protection: out of scope — Bash
gating only"; rules ship read-only *because* the sandbox enforces it). So this
is a profile concern, not a hook-pipeline change.

## Options

- **A. Deny-write the path in the nono profile (pick).** Add `**/.claude-bash-rules`
  as read-only / deny-write in the deploy profile while workdir stays readwrite.
  Encode it in the example profile (PLAN-nono-profile-example); assert it in
  `selfcheck` (PLAN-sandbox-selftest) with a non-destructive probe. Depends on
  nono supporting a glob in the read/deny lists that overrides the workdir grant.
- **B. bash-gate refuses a writable project tier.** Can't tell sandbox
  enforcement from mode bits (same subtlety as selfcheck); a probe-write per
  command is heavy and unreliable; failing closed breaks every repo without the
  profile. Rejected as runtime behavior; fine as a `selfcheck` warning (= A).
- **C. Move project rules into the read-only install root**
  (`~/.claude-bash-rules/projects/<hash>/rules`). Structurally solves it (never in
  the writable workdir), but loses the "rules travel committed with the repo"
  ergonomic. Only if that trade is acceptable.
- **D. Status quo.** Residual risk already documented, can't widen a global deny.
  The `?` leans here — but ask→allow widening is a real bypass, so A is cheap
  insurance.

## Pick: A — REJECTED

A is not implementable (see "Why this can't be implemented" above); B and C are
also out (B is unreliable at runtime; C loses repo-local rules). Superseded by
[`plans/drafts/PLAN-rule-attestation.md`](plans/drafts/PLAN-rule-attestation.md).
The analysis below is kept for provenance.

## Changes

- `conf/nono-profile.example.json` (PLAN-nono-profile-example): deny-write /
  read-only entry for `**/.claude-bash-rules`; README note on the vector it does
  and doesn't cover.
- `selfcheck` (PLAN-sandbox-selftest): add a check that the project rules file
  (when present) is readable and not writable — reuse `probeFileNotWritable`.
- `ARCHITECTURE.md`: update the "project tier is agent-editable" residual risk to
  note in-session writes are blocked when the profile enforces this.

## Open

- Does nono support a glob (`**/.claude-bash-rules`) in the filesystem lists, and
  does deny override a broader workdir readwrite grant? Verify against the schema;
  if not, fall back to per-repo concrete paths or Option C.
