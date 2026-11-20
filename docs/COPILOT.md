# GitHub Copilot CLI integration

Copilot CLI has a `preToolUse` hook: before it runs a tool, it feeds the hook
the tool name and args on stdin and reads a decision JSON from stdout. shizo
registers for it with the `hook copilot` event — same pipeline as Claude Code,
a different envelope. The policy pipeline in [`EVALUATION.md`](EVALUATION.md)
is unchanged; only the adapter in
[`src/harness_copilot.go`](../src/harness_copilot.go) differs.

## Install

Copilot discovers hooks from `hooks/*.json` files: `.github/hooks/` in the
repository and `~/.copilot/hooks/` for the user (`$COPILOT_HOME/hooks/` when
set). Register shizo:

```json
{
  "version": 1,
  "hooks": {
    "preToolUse": [
      { "type": "command", "bash": "shizo hook copilot" }
    ]
  }
}
```

The `bash` field runs with the session as its working directory; put `shizo`
on that PATH or use an absolute path (`exec`/`args` work too, and `command`
is the cross-platform fallback). The hook accepts both payload shapes: the
native camelCase (`toolName`/`toolArgs`) and the PascalCase `PreToolUse`
snake_case (`tool_name`/`tool_input`, the VS Code and Claude-compatible
format), so it also works when registered through a Claude-format plugin.

## Decisions

Copilot's `permissionDecision` supports `"ask"`, but since v1.0.53 the TUI
auto-approves a hook `"ask"` — the permission dialog flashes for milliseconds
and resolves without user input (github/copilot-cli#3590, open). shizo is
fail-closed, so in the default `decision` mode it never emits `"ask"`: ask
rules and the unmatched fallthrough both map to `deny`, with a reason that
says so. With `returntype automode` the unmatched fallthrough defers instead
(empty output, Copilot's own permission flow); an explicit ask rule still
denies, because Copilot cannot reliably prompt.

| shizo outcome | decision mode | automode |
| --- | --- | --- |
| allow | `permissionDecision: "allow"` | same |
| deny | `permissionDecision: "deny"` + reason, exit 2 | same |
| ask rule | `deny` + downgrade reason, exit 2 | `deny` + downgrade reason, exit 2 |
| no rule matches | `deny` + downgrade reason, exit 2 | empty output — Copilot's default flow |
| non-bash tool | empty output — Copilot's default flow | same |

Exit codes: allow and deferrals exit 0; deny exits 2, at which point Copilot
merges any stdout JSON with the deny decision, so the reason survives. A crash
or any other non-zero exit also denies (command hooks are fail-closed), which
covers a Go panic. Residual risk: the hook timeout (default 30 s) always
fails open — a timed-out hook warns and lets the call proceed through
Copilot's normal permission flow.

Only `bash` is gated: other tools (view, edit, grep, …) defer to Copilot's
own permission flow. A bash invocation whose args carry no command is denied
as malformed input.
