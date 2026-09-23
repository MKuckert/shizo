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
   Stdin-fed commands are identified: the right-hand side of a pipe and commands
   with an fd 0 redirect (`<`, `<<`, `<<-`, `<<<`, `<>`) are marked. When stdin
   feeds a compound command (block, subshell, loop, conditional) rather than a
   simple `CallExpr`, the entire invocation is denied because the receiving
   command is not statically identifiable.
6. **Resolve words.** Literals, single-quoted words, and fully literal
   double-quoted words become argv tokens. Backslash escapes are removed from
   arguments and from rule tokens, so `--ha\rd` and `--hard` are the same token
   and a `HAS` rule cannot be dodged by respelling the flag; `argv[0]` keeps its
   backslashes so `\g\i\t` faces path resolution as written and fails to
   resolve. Command and process substitutions become `<subst>` in the outer argv
   while their contents are checked independently.
7. **Reject dynamic argv.** Parameter and arithmetic expansions deny because
   their values are unknown during policy evaluation.
8. **Reject assignments.** Assignment prefixes and bare assignments deny; they
   can alter execution without appearing in the matched argv.
9. **Apply unconditional denials.** Execution-hiding commands such as `eval`,
   `env`, and `sudo` deny on the raw basename before resolution and cannot be
   re-allowed. This fires before step 10 so an unresolvable or untrusted
   spelling still denies.
10. **Resolve commands.** Safe builtins are accepted directly. Other commands
    must resolve through inherited `PATH` or from `cwd` to an executable in a
    trusted directory. The resolved absolute path is carried into step 11.
11. **Match rules.** The rule's command token is compared against the resolved
    path from step 10: a bare-name token matches by basename, an absolute token
    matches exactly. Global and project denies run first, then ask rules, then
    allows. A match yields `deny`, `ask`, or `allow`; no match yields `ask`. A
    `STDIN`-marked rule only matches stdin-fed commands. When the result is
    `allow` and the command is stdin-fed, the outcome is downgraded to the
    `stdin` directive's value (`allow` by default).
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

> `deny` > explicit `ask` > unmatched > `allow` — the strongest result wins.

Thus one denied pipeline stage denies the complete Bash invocation. The two
middle levels both report `ask` on the wire and in the log; they differ only in
automode, where an invocation that is merely unmatched defers while one matched
by an explicit `ask` rule always asks.

## Logging

Hook mode appends one object per invocation to
`$HOME/.shizo/logs/YYYY-MM-DD.jsonl`:

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
