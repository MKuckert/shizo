# Claude Code hook protocol

How `bash-gate` speaks to Claude Code's `PreToolUse` hook interface. Everything
in this document is dictated by Claude Code, not by bash-gate's own policy — it
is the contract at the two ends of the [evaluation pipeline](EVALUATION.md).

`bash-gate` is registered as a `command` hook with matcher `Bash`; the installed
`command` is the `gate.sh` wrapper, not the binary directly.

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [{ "type": "command", "command": "$HOME/.claude-bash-rules/engine/gate.sh" }]
      }
    ]
  }
}
```

---

## Input

The hook receives one JSON object on **stdin**. bash-gate reads these fields
(see the [common input fields](https://code.claude.com/docs/en/hooks#common-input-fields)):

| Field                  | Use                                             |
| ---------------------- | ----------------------------------------------- |
| `tool_input.command`   | the raw Bash command to evaluate                |
| `cwd`                  | containment base for redirects and `./` lookups |
| `session_id`           | logged with each decision                       |
| `tool_name`            | sanity check (matcher already restricts `Bash`) |

Missing or malformed JSON, or an absent/empty `command`, is treated as a crash
and **denies**.

---

## Output

There are two return-type modes, selected by the `returntype` directive (see
[`RULES.md §3`](RULES.md#returntype-decision--automode)). `decision` is the
default.

### `returntype decision` (default)

The hook decides every case and always emits an object:

```json
{
  "hookSpecificOutput": {
    "hookEventName": "PreToolUse",
    "permissionDecision": "allow|deny|ask",
    "permissionDecisionReason": "<specific, human-readable>"
  }
}
```

- `allow` **bypasses the normal permission prompt** — that is the point of the
  allowlist.
- `ask` falls through to the prompt.
- every `deny` carries a reason naming the offending command, rule, or path.

An empty or non-JSON body, or any unexpected exit code, is a crash and denies.

### `returntype automode`

The hook decides denials **and** explicit allows; only the unmatched (`ask`)
fallthrough defers to Claude Code's own auto-mode classifier. The allowlist acts
as a pre-filter that strips known-bad and confirms known-good before the
classifier ever runs.

| Aggregated outcome  | `decision` mode | `automode`                      |
| ------------------- | --------------- | ------------------------------- |
| deny-rule match     | `deny` object   | `deny` object                   |
| allow-rule match    | `allow` object  | `allow` object                  |
| neither (unmatched) | `ask` object    | **defer** — no decision emitted |

A defer emits **no permission decision** and exits 0, so Claude Code proceeds to
its normal permission flow. On the wire, defer is signalled to `gate.sh` by the
literal `defer` sentinel (below), which the wrapper maps to empty stdout —
distinguishing a deliberate defer from a crash, which is also empty.

---

## Exit codes

For `PreToolUse`, **exit code 2 is blocking**; other non-zero exits are *not*,
so a naïvely-crashed hook fails **open**. A zero exit with empty or non-JSON
output fails open too. bash-gate therefore signals a policy deny with **exit 2**
(plus the deny JSON) and exits 0 for `allow`/`ask`/defer.

| Binary outcome          | stdout        | exit |
| ----------------------- | ------------- | ---- |
| deny                    | deny object   | 2    |
| allow / ask             | decision obj  | 0    |
| defer (automode)        | `defer`       | 0    |

Exit 2 is Claude Code's native blocking channel and does not depend on the JSON
body being parsed — propagating it keeps deny fail-closed even if the body is
dropped.

---

## `gate.sh` — fail-open mitigation

`gate.sh` is the thin wrapper installed as the hook `command`. It **propagates
the binary's exit code** (2 for deny, 0 otherwise) and closes the fail-open gaps
by forcing a synthetic deny **on exit 2** for every unrecognised outcome (crash
exit, empty body, non-JSON body). The one non-JSON stdout it honours is the
`defer` sentinel, mapped to empty output; that sentinel is never produced in
`decision` mode, so that mode's "empty ⇒ deny" guarantee is untouched.

```sh
#!/bin/sh
dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
log="$HOME/.claude-bash-rules/logs"
mkdir -p "$log" 2>/dev/null
# No decision may run unlogged. If the logs dir is not writable, fail closed
# with a specific, actionable deny — not the cryptic crash-deny below. The probe
# runs in a subshell so a redirect failure on the special built-in `:` cannot
# kill the wrapper.
if ! ( : >>"$log/gate.log" ) 2>/dev/null; then
  printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"bash-gate: log directory %s is not writable ... Refusing to run unlogged."}}' "$log"
  exit 2
fi
input=$(cat)                             # drain the hook JSON once
[ -n "$GATE_DEBUG" ] && printf '%s\n' "$input" >>"$log/stdin.log" 2>/dev/null
out=$(printf '%s' "$input" | "$dir/bash-gate" claude-hook 2>>"$log/gate.log")
rc=$?
case "$rc:$out" in
  0:defer)  ;;                           # automode non-deny → emit nothing, exit 0
  0:\{*)    printf '%s' "$out" ;;        # allow / ask → JSON, exit 0
  2:\{*)    printf '%s' "$out"; exit 2 ;;# deny → JSON + blocking exit 2
  *)        printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"bash-gate failed (exit %s)"}}' "$rc"; exit 2 ;;
esac
exit 0
```

The hook JSON is drained once into `$input` and piped explicitly to `bash-gate`
rather than relying on stdin inheritance, so nothing added above the pipe can
silently consume fd 0. Stderr is captured so crash-denies are diagnosable.
Setting `GATE_DEBUG` mirrors the raw stdin to `stdin.log` — see the warning in
[`README.md`](../README.md#gate_debug-sensitive).

Two gaps remain, by design: a hook **timeout** is not handled by the wrapper and
fails open, and the logs are writable from inside the sandbox and so are not
evidence-grade. See [`ARCHITECTURE.md`](ARCHITECTURE.md#residual-risks).
