# Can nono make the project rules file read-only in a writable workdir?

Research for `plans/drafts/PLAN-readonly-project-rules.md`. Question: can a nono
profile keep the agent workdir writable while making `<cwd>/.shizo`
**readable but not writable** from inside the sandbox, so in-session tampering
that widens policy is blocked?

**Short answer: no.** nono has no write-scoped deny / read-only-downgrade
primitive. You can deny a path *entirely* (loses read too) or invert the workdir
to read-only and carve writable paths back — but you cannot subtract *write*
from one child of a writable directory.

## Test environment

- macOS (Darwin 25.6.0, arm64), `nono 0.75.0`.
- Probes run via `nono run --profile <p> --workdir <W> -- sh probe.sh`, checking
  read/write against the rules file vs. a normal file.
- **Gotcha that first fooled the test:** nono's baseline macOS groups grant
  `r+w` to `/private/tmp` (and `$TMPDIR`). A workdir under `/private/tmp` looks
  writable no matter what the profile says. All results below use a workdir under
  `$HOME`, where only nono's injected system-read grants apply.

## nono's filesystem model

Positive grants are a **union** (most-permissive wins): `allow` = read+write,
`read` = read-only, `write` = write-only, plus the `*_file` single-file variants.
There is exactly one negative rule, `filesystem.deny`, and it is **absolute** —
it removes *all* access (read and write), overriding every grant. There is no
"deny write but keep read" for a subpath.

## Results

Workdir granted writable via `filesystem.allow: ["$WORKDIR"]` unless noted.

| # | Profile combo | READ rules | WRITE rules | rest of workdir |
|---|---|---|---|---|
| ro1 | `allow $WORKDIR` + `read_file` + `deny` rules | ❌ blocked | ❌ blocked | writable |
| ro2 | `allow $WORKDIR` + `read_file` rules | ✅ OK | ❌ **still writable** | writable |
| ro3 | `read $WORKDIR` + `allow_file normal.txt` | ✅ OK | ✅ blocked | **not writable** (CREATE blocked) |
| ro4 | `allow $WORKDIR` + `deny **/.shizo` | ❌ blocked | ❌ blocked | writable |

Key readings:

- **ro2 — grants don't subtract.** Adding `read_file` on top of a writable
  `allow` leaves the file read **+ write**. You cannot downgrade a child to
  read-only by layering a read grant.
- **ro1 / ro4 — `deny` wins but kills read.** `deny` overrides the broader
  `allow` (write blocked, rest of workdir stays writable), but the file becomes
  **completely inaccessible**, including read.
- **ro3 — the only readable+unwritable combo** requires inverting the workdir to
  read-only and adding write back per-path. `CREATE-new` was blocked, i.e. the
  workdir is no longer writable by default — impractical for a general agent
  workdir.

### Glob + post-start recreate (ro4, macOS)

`deny: ["**/.shizo"]` matched correctly (patterns without an absolute
prefix are rooted at the workdir) and, on macOS, the deny is backed by a runtime
regex: I confirmed **delete-then-recreate after sandbox start is still blocked**
(`DELETE`, `RECREATE`, `READ-after` all blocked). An attacker inside the sandbox
cannot escape the deny by removing and rewriting the file.

## Why full `deny` is self-defeating for this feature

shizo runs **inside** the sandbox (it's a Claude Code `PreToolUse` hook, a
child of the sandboxed session). If `.shizo` is `deny`'d, shizo
can't read it either, so the **project tier is simply never loaded** in-sandbox.

That is fail-safe — an unreadable project tier can't widen `ask`→`allow`, and a
hostile *shipped* file can't widen either — but it silently turns the
project-rules feature **off** inside the sandbox rather than making it
"read-only." If project rules are meant to work in-sandbox (the normal case),
`deny` is the wrong tool.

## Platform caveat (Linux, from docs — not tested here)

- Landlock is allow-list only and cannot deny-within-allow. A `deny` overlapping
  the `allow $WORKDIR` grant is a **hard startup error** unless
  `security.capability_elevation: true` (supervised mode).
- Glob denies on Linux only cover files present at sandbox start; the
  delete+recreate protection observed on macOS does **not** hold.

So even the "deny entirely" path is macOS-clean but needs extra config and is
weaker on Linux.

## Bearing on the plan's options

- **Option A ("read-only, workdir stays writable")** — **not expressible on
  nono.** The mechanism it depends on (write-only deny) does not exist.
- **Option A′ (full `deny` of `**/.shizo`)** — enforceable and
  recreate-proof on macOS, but the project tier is *unreadable* in-sandbox, so
  the selfcheck would have to assert "not readable," not "readable and not
  writable." Feature is effectively off in-sandbox.
- **Option C (project rules in a read-only install root)** — a plain `read`
  grant on a path outside the workdir gives exactly readable + not-writable
  (ro3 proved a `read` grant behaves this way). This is the **only** way to get
  the plan's literal goal, so this result **strengthens C over A**. Cost is the
  known ergonomic trade: rules no longer travel committed with the repo.

## New vertical to evaluate: attestation

Instead of enforcing read-only via the sandbox, **detect tampering** with a
user-acknowledged hash:

- The user runs `shizo attest <project-rules>` **outside** the sandbox. This
  writes a hash (e.g. SHA-256) of the project rules file into the **global**
  directory (`~/.shizo/...`), which the profile grants **read-only**
  inside the sandbox.
- At evaluation time, shizo hashes the live project tier and compares against
  the attested hash. On mismatch (or missing attestation), it **refuses to honor
  the project tier's widening `allow`s** (falls back to `ask`) — fail-closed.
- In-session tampering that rewrites `.shizo` changes the hash but
  cannot update the attestation (global dir is read-only in-sandbox, and the
  `attest` step is an explicit human action outside the sandbox).

Open points to work through when revising the plan:

- **Enforcement of the store's integrity depends on the same nono read-only
  grant** as Option C — the attestation record must be genuinely unwritable
  in-sandbox, or the attacker just rewrites the hash to match. (ro3 confirms a
  `read` grant on a path outside the workdir is unwritable, so this is
  enforceable.)
- Keying: attestation is per project path (and possibly per repo/worktree). Needs
  a stable key — e.g. hash of the absolute project-rules path, or `@git:toplevel`
  — and a decision on what happens for never-attested projects (default `ask`,
  i.e. project tier ignored until attested).
- This narrows the same vector as A/C (in-session widening) **and additionally**
  catches a hostile *shipped* file that A′/C do not — the shipped file has no
  matching attestation, so its `allow`s are ignored until a human acks them.
- Cost: a human step per change to project rules, and a small amount of new
  hook-pipeline logic (hash + compare + read the global attestation store) —
  unlike A/C, this is *not* purely a profile concern.
