# Evaluation pipeline

Every Bash invocation passes through this ordered, fail-closed pipeline. A deny
short-circuits later work. Rule details are in [`RULES.md`](RULES.md); hook I/O
is defined by [`CLAUDE_CODE_HOOKS.md`](CLAUDE_CODE_HOOKS.md).

1. **Read input.** Malformed hook JSON and a missing or empty command deny.
2. **Load policy.** The global tier is required; the project tier is optional.
   Any unreadable or malformed present tier denies everything. Under `sandbox
   nono`, each tier must match its attestation and the global trusted-`PATH`
   snapshot must match the live environment.
3. **Check sandbox.** `sandbox nono` requires `NONO_CAP_FILE`. Only the global
   `sandbox dangerously-none` directive disables this check.
4. **Parse Bash.** A `mvdan.cc/sh/v3/syntax` parse error denies with its source
   position and message.
5. **Walk the complete AST.** Commands and redirects are collected from lists,
   pipelines, substitutions, subshells, loops, conditionals, cases, and function
   bodies. Any function declaration denies because it can shadow a trusted name.
6. **Resolve words.** Literals, single-quoted words, and fully literal
   double-quoted words become argv tokens. Command and process substitutions
   become `<subst>` in the outer argv while their contents are checked
   independently.
7. **Reject dynamic argv.** Parameter and arithmetic expansions deny because
   their values are unknown during policy evaluation.
8. **Reject assignments.** Assignment prefixes and bare assignments deny; they
   can alter execution without appearing in the matched argv.
9. **Apply unconditional denials.** Execution-hiding commands such as `eval`,
   `env`, and `sudo` deny before rules and cannot be re-allowed.
10. **Resolve commands.** Safe builtins are accepted directly. Other commands
    must resolve through inherited `PATH` or from `cwd` to an executable in a
    trusted directory.
11. **Match rules.** Global and project denies run before all allows. A match
    yields `deny` or `allow`; no match yields `ask`.
12. **Contain writes.** Redirect targets must be literal and stay below `cwd`,
    `$TMPDIR`, or `/tmp`; `/dev/null`, `/dev/stdout`, and `/dev/stderr` are also
    valid. If the command list contains `cd`, all write redirects deny. Read
    redirects and heredocs are unrestricted.
13. **Aggregate and report.** Per-command results become one hook result and log
    record.

The implementation is split across `src/policy.go`, `src/extract.go`,
`src/resolve.go`, and `src/match.go`.

## Aggregation

Pipelines, lists, and substitutions may produce several commands. Each is
evaluated independently, then folded with this precedence:

> any `deny` -> `deny`; otherwise any `ask` -> `ask`; otherwise `allow`.

Thus one denied pipeline stage denies the complete Bash invocation. Output mode
may turn the final unmatched `ask` into an automode defer, but the log retains
the actual policy result.

## Logging

Hook mode appends one object per invocation to
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

The wrapper refuses to start if the log directory is not writable. Once policy
has fixed a verdict, an individual append failure does not alter it.
