# OpenAI Codex integration

Codex has a `PreToolUse` hook event: before it runs a matched tool, it feeds
the hook the tool name and input on stdin and reads a decision JSON from
stdout. shizo registers for it with the `hook codex` event — same pipeline as
Claude Code, a different envelope. The policy pipeline in
[`EVALUATION.md`](EVALUATION.md) is unchanged; only the adapter in
[`src/harness_codex.go`](../src/harness_codex.go) differs.

## Install

Codex discovers hooks from `hooks.json` files: `$CODEX_HOME/hooks.json`
(default `~/.codex/hooks.json`) for the user, `.codex/hooks.json` in a trusted
project, and `/etc/codex/hooks.json` system-wide. Register shizo for the
`Bash` matcher:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [{ "type": "command", "command": "shizo hook codex" }]
      }
    ]
  }
}
```

The `command` runs with the session's working directory as cwd; put `shizo`
on that PATH or use an absolute path. Codex requires you to review and trust
each hook definition before it runs (`/hooks` in the CLI); a changed command
is re-marked for review.

## Decisions

Codex's `permissionDecision` has no working `"ask"`: the value is parsed but
unsupported, the hook run is marked failed, and **the tool call proceeds**.
Exit 0 with empty output also proceeds. shizo therefore maps both `ask` and
the unmatched fallthrough to `deny`, with a reason that says so — fail-closed
over fail-open.

| shizo outcome | Codex envelope | effect |
| --- | --- | --- |
| allow | `permissionDecision: "allow"` + `updatedInput` re-emitting the original command | runs without the approval prompt (the documented no-block rewrite shape; a bare `allow` without `updatedInput` is rejected by Codex) |
| deny | `permissionDecision: "deny"` + reason | blocked; the reason is shown to the user |
| ask / no rule matches | `permissionDecision: "deny"` + downgrade reason | blocked (Codex would have proceeded) |

The adapter always exits 0 and carries the decision in the JSON: Codex blocks
only on a parsed deny (or exit 2 with a non-empty stderr reason), while every
other shape — `"ask"`, empty output, exit 2 without a stderr reason, any other
non-zero exit — fails the run and lets the tool call proceed. A deny without a
non-empty `permissionDecisionReason` is likewise rejected, so the adapter
substitutes one if shizo's reason is empty. A Go panic exits 2 with its stack
trace on stderr, which Codex does block.

Residual risk: a hard crash (signal kill, OOM) exits non-zero without a
stderr reason and fails open, the same class of residual as any hook.

Only `Bash` is gated: the matcher selects shell commands, and a non-Bash tool
that reaches the hook without a `command` field is denied as malformed input.
