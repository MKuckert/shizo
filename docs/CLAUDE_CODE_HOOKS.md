# Claude Code hook protocol

`bash-gate` implements Claude Code's `PreToolUse` contract for the `Bash`
matcher. Register the installed `gate.sh` wrapper rather than invoking the
binary directly; the README contains the settings example.

## Input

The hook reads one JSON object from stdin:

| Field | Purpose |
| --- | --- |
| `tool_input.command` | raw Bash source to evaluate |
| `cwd` | base for relative commands and redirect containment |
| `session_id` | decision-log correlation |
| `tool_name` | sanity check after matcher selection |

Malformed JSON and a missing or empty command deny. The policy then follows
[`EVALUATION.md`](EVALUATION.md).

## Output modes

The global [`returntype` directive](RULES.md#directives) controls output.

In `decision` mode (the default), every result is emitted as:

```json
{
  "hookSpecificOutput": {
    "hookEventName": "PreToolUse",
    "permissionDecision": "allow|deny|ask",
    "permissionDecisionReason": "<specific reason>"
  }
}
```

`allow` bypasses Claude Code's prompt, `ask` invokes it, and `deny` blocks with
a reason. In `automode`, explicit allows and denials are still emitted, but an
unmatched `ask` defers to Claude Code's classifier:

| Policy result | `decision` | `automode` |
| --- | --- | --- |
| deny | deny object | deny object |
| allow | allow object | allow object |
| unmatched | ask object | no decision |

The binary represents a deliberate defer as the literal `defer`; `gate.sh`
converts it to empty stdout. This distinguishes defer from a crash inside the
wrapper.

## Exit status

Claude Code blocks `PreToolUse` only on exit 2. Other failures, empty output,
and invalid output can fail open, so `bash-gate` uses this contract:

| Result | stdout | Exit |
| --- | --- | --- |
| deny | deny JSON | 2 |
| allow or ask | decision JSON | 0 |
| defer | `defer` | 0 |

Exit 2 remains blocking even if Claude Code cannot parse the JSON body.

## Wrapper

`gate.sh` reads stdin once, checks that `$HOME/.bash-gate/logs` is writable,
runs `bash-gate claude-hook`, and accepts only these combinations:

- exit 0 plus decision JSON: forward the JSON
- exit 0 plus `defer`: emit nothing
- exit 2 plus deny JSON: forward it and exit 2
- anything else: emit a synthetic deny and exit 2

Stderr goes to `logs/gate.log` for diagnosis. If logging is unavailable, the
wrapper emits an actionable deny without running the policy. `GATE_DEBUG`
additionally copies raw input to `logs/stdin.log`; it must remain disabled in
normal use because command arguments may contain secrets.

The wrapper cannot intercept a Claude Code hook timeout, which therefore still
fails open. Logs are writable by the sandboxed process and are not audit-grade.
See [`ARCHITECTURE.md`](ARCHITECTURE.md#residual-risks).
