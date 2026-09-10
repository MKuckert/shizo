# Rule attestation is security theatre without a verified anchor

> Research + plan. Reads the shipped attestation feature (`attest.go`,
> `attest_verify.go`, `rules.go:verifyAttestation`, `policy.go`) against its own
> threat model (`plans/archive/PLAN-2026-09-10-rule-attestation.md`,
> `docs/RULES.md §10`, `docs/ARCHITECTURE.md:125-137`) and asks: what security
> does the SHA-256 buy? Answer: on its own, none. Every bit of protection is
> loaned from a read-only-store guarantee that bash-gate never checks, that a
> common misconfig voids, and that does not exist at all in the one unsandboxed
> state where the check still runs.

## The mechanism, in one paragraph

`bash-gate attest` (run outside the sandbox) normalizes a rule tier and writes
`sha256(normalized) → $CLAUDE_BASH_RULES_DIR/attestations/*.attest`. On every
hook load, `verifyAttestation` recomputes the hash inside the sandbox, reads the
record, and denies all commands on any mismatch or missing record. The record
carries no signature and no secret — just the same public hash the loader
recomputes. So the record is trivially forgeable by anyone who can write the
store: run `bash-gate attest` again, or write the four header lines by hand.

The feature is therefore **not authentication, only integrity-with-a-trusted-
witness** — and the witness (the record store) is trusted purely because it is
assumed read-only in the sandbox. That assumption is the whole feature.

## Why it is theatre

### 1. The load-bearing assumption is never verified by bash-gate

`docs/ARCHITECTURE.md:133-137` and `RULES.md §10` say it plainly: security "is
only as strong as the read-only grant on `$CLAUDE_BASH_RULES_DIR`." That grant is
delegated to the sandbox profile and to `PLAN-sandbox-selftest` — out of scope
for bash-gate, which **trusts it blindly**. Nothing in the load path probes
whether the store it just read is actually unwritable from this process. A
feature whose entire value rests on precondition X, that never checks X, is
theatre exactly when X quietly fails.

### 2. The "ancestor-grant footgun" voids it silently

Any profile that grants a writable ancestor (`$HOME`, a workdir parent) makes the
store writable in-sandbox. Then in-session tampering edits `.claude-bash-rules`
**and** rewrites the matching `.attest` in the same breath. No error, no deny —
the hash matches because the attacker recomputed it. The gate reports "attested &
trusted" while enforcing attacker rules.

### 3. `installRoot()` follows an attacker-adjacent env var

`installRoot()` (`main.go:194`) is `$CLAUDE_BASH_RULES_DIR` or
`$HOME/.claude-bash-rules`. Whoever influences the hook's environment — e.g. a
workdir `.claude/settings.local.json` `env` block, which lives in the writable
workdir — redirects the store to a location full of pre-forged records, or to a
read-only decoy that a naive writability probe would bless (see rung 1). The
trust anchor is only as fixed as the env that names it.

### 4. Attestation guards the rules but not the interpreter

The hash covers rule *content*. It does not cover the `bash-gate` binary,
`gate.sh` (the installed hook `command`), or the resolved rules-dir path. If any
of those are agent-writable the check is moot — you don't tamper with the rules,
you tamper with the thing that reads them. We cryptographically fuss over rules
while the enforcer sits unprotected.

### 5. Where the anchor *does* hold, the hash is redundant

If the store is genuinely read-only in-sandbox, so can the rules be — the sandbox
already enforces "this file did not change since the operator set it." The
SHA-256 re-derives a fact the filesystem grant already guarantees. No key, no
signer identity (`attested-by` is self-reported `$USER@host`, cosmetic), no
session binding, no append-only guarantee against re-attest. Ceremony, not a
security property.

**Thesis.** Attestation is detection that presupposes an integrity anchor it does
not itself provide. Anchor holds → hash is redundant with the sandbox. Anchor
fails → hash is forgeable in one command. Neither case is bought by cryptography.

## Which states are actually reachable

The sandbox gate in `decide()` step 3 (`policy.go:34`) already denies **every**
command when `sandboxMode != "dangerously-none" && !env.sandboxed`. That collapses
the theatre surface to a single reachable state:

| declared `sandbox` | `NONO_CAP_FILE` | outcome | attestation |
|---|---|---|---|
| `nono` | set | runs, sandboxed, store should be read-only | meaningful |
| `nono` | unset | **step 3 denies all** | already moot |
| `dangerously-none` | set | runs, sandboxed | meaningful |
| `dangerously-none` | **unset** | **runs, no sandbox, store writable** | **theatre** |
| missing | any | `loadConfig` denies all | moot |

Two consequences drive the plan:

- The only state where attestation runs, allows commands, and has a writable
  store is **`dangerously-none` + unsandboxed** — precisely the mode whose name
  means "trusted environment, I opted out of the sandbox." Enforcing attestation
  there can only brick a deliberate setup on a stale record or imply protection
  that isn't there.
- Every branch must key off **`cfg.sandboxMode`** (from the attested global tier,
  trustworthy) — never off the raw `env.sandboxed` bit, which an attacker can
  toggle from the environment.

## Considerations before "fixing" it

- **Question whether it should exist (YAGNI).** If rules need not be repo-local
  *and* writable, Option C from the original research (rules under the read-only
  install root) deletes the feature: the sandbox enforces integrity directly, no
  hash, no `attest`, no store. Re-litigate that requirement first — it is the
  laziest correct answer.
- **The real invariant is filesystem, not cryptographic.** The honest fix turns
  the unverified precondition into an *enforced* one; it does not stack more crypto
  on an unchecked assumption.
- **Don't sign your way out of a writable-config problem.** Signatures help only
  if the verifier's pubkey + the bash-gate binary + gate.sh live on a genuinely
  read-only mount. If the pubkey sits in the writable store, the attacker swaps
  it. Same anchor problem, more moving parts.

## Plan (lazy → thorough)

Ship rungs 1–3; they remove the silent-theatre failures with the smallest diffs.
Rungs 4–5 are optional and mostly a requirements question, not a crypto one.

### 1. Enforce the anchor instead of assuming it

In `verifyAttestation`, before trusting any record, prove the store is genuinely
unwritable from *this* process. Necessary checks (rung 1 only if **all** hold):

- **Overwrite, not just create.** Attempt to rewrite an existing `.attest` record
  (or a sentinel co-located with it) and to create a new file in
  `attestations/`; the attack is *replacing* a record, so a create-only probe is
  insufficient.
- **Parent chain.** Confirm the `attestations/` dir and its parent can't be
  renamed/replaced (a writable grandparent defeats a read-only child).
- **Ambiguous → deny.** Any probe error (EPERM ambiguity, stat failure, missing
  dir) is a mismatch, not a pass — fail closed, per the core invariant.

Any writable result while `cfg.sandboxMode == "nono"` (i.e. a real sandbox is
expected) → deny-all: *"attestation store is writable in-sandbox — records are
forgeable, re-check the sandbox profile."* This kills failure modes 1, 2, 3.
`ponytail:` this is a load-time sentinel probe, not a full profile audit — upgrade
to `PLAN-sandbox-selftest`'s check if that lands. Requirement to state in the
plan: the store must be granted as a path `read` grant, not a glob `deny` — on
Linux, glob denies only cover files present at start and don't survive
delete+recreate (`docs/research/nono-readonly-project-rules.md`), so a load-time
probe wouldn't reflect runtime.

Test: writable store / writable parent / probe error → deny; read-only path grant
→ normal verdict.

### 2. Skip attestation under `dangerously-none`

When `cfg.sandboxMode == "dangerously-none"`, skip the attestation check entirely
— do not deny on a stale or missing record. The operator has explicitly opted out
of the sandbox anchor attestation requires; enforcing it only bricks a deliberate
setup or fakes assurance. The nono-unsandboxed case needs **no** change: step 3
(`policy.go:34`) already denies every command there, so attestation is moot.

Gate strictly on `cfg.sandboxMode` (attested global tier), never on
`env.sandboxed`. This opens no new hole: reaching the skip path requires flipping
global to `dangerously-none`, which changes the global hash → deny, unless the
attacker can forge the global record — which needs store-write, already covered by
rung 1. If the store is writable, the game was already lost.

Test: `dangerously-none` + missing/stale record → commands still evaluated (no
attestation deny); `nono` + unset `NONO_CAP_FILE` → still deny-all via step 3;
env-only toggle of `NONO_CAP_FILE` never changes the attestation branch taken.

### 3. Pin the store location when sandboxed

Stop letting the environment steer the anchor for the in-sandbox verdict:

- In-sandbox (`env.sandboxed`): `installRoot()` **ignores** `$CLAUDE_BASH_RULES_DIR`
  and resolves home via `getpwuid(getuid())`, not `$HOME`, so nothing the agent
  can write to the workdir env redirects the store.
- Out-of-sandbox (the `attest`/`validate`/`test` CLIs, `make install PREFIX=…`,
  the test harness): keep honoring `$CLAUDE_BASH_RULES_DIR` as today.

Don't delete the env var — it's load-bearing for `make install PREFIX` (Makefile:61)
and the test harness (5 `_test.go` files), and deleting it wouldn't close the class
anyway (`$HOME` is equally env-derived). Neutering it *in-sandbox* is the smaller,
sufficient diff. Caveat: pinning the path still only helps if the profile grants
that pinned path read-only — same anchor requirement, but the attacker can no
longer move the goalposts to a pre-forged location.

Test: with `CLAUDE_BASH_RULES_DIR` set to an attacker dir and `env.sandboxed`
true, `installRoot()` resolves to the passwd-home path, not the env path.

### 4. Only with a real read-only anchor — authenticate instead of hash

Sign records with a key held outside the sandbox (`ssh-keygen -Y`, `minisign`, or
`age`); verify with a pubkey on the read-only mount. Then store-write alone can't
forge — forgery needs the private key, which never enters the sandbox. `Skipped`
by default: adds a dependency + key management and buys nothing over rungs 1–3
unless the pubkey **and** the binary live on a read-only mount and multi-writer
forgery (not just the in-sandbox agent) is in scope. Add when that anchor is real.

### 5. Reconsider the feature (Option C)

If the repo-local-writable requirement is soft, move rules to the read-only
install root and delete `attest.go` / `attest_verify.go` / the `verifyAttestation`
wiring. Largest deletion, zero residual anchor risk. Gate on a product decision,
not code.

## Bottom line

The current feature's security equals `store_is_readonly_in_sandbox`, a fact
bash-gate assumes but never checks, that a writable ancestor voids, and that is
by definition absent under `dangerously-none` — the one unsandboxed state where the
check still runs. Rung 1 makes bash-gate verify the anchor (overwrite + parent +
fail-closed-on-ambiguity); rung 2 stops it lying under `dangerously-none`; rung 3
stops the environment moving the anchor. Everything past that is a requirements
question, not a crypto one.
