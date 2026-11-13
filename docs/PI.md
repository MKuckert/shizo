# pi coding agent integration

[`src/pi-shizo.ts`](../src/pi-shizo.ts) is a pi extension that gates every
`bash` tool call through the same `shizo.sh` wrapper Claude Code uses (see
[`CLAUDE_CODE_HOOKS.md`](CLAUDE_CODE_HOOKS.md)). It subscribes to pi's
`tool_call` event, so the policy pipeline in [`EVALUATION.md`](EVALUATION.md) is
unchanged.

## Install

`shizo install pi-extension` copies the extension embedded in the binary into
pi's extension directory. Then run pi inside the nono sandbox like any other
shizo-gated agent:

```sh
shizo install pi-extension             # .pi/extensions/shizo.ts in the cwd
shizo install pi-extension --global    # ~/.pi/agent/extensions/shizo.ts
```

A re-run is a no-op. A stale symlink from an older install is replaced by the
regular copy; an existing file with different content is never overwritten.

The extension runs `$HOME/.shizo/engine/shizo.sh` (`shizo info --hook-command`);
set `SHIZO_HOOK` to use another wrapper path.

## Decisions

| Wrapper result | Extension behaviour |
| --- | --- |
| deny (exit 2) | block; the model receives the reason as the tool result |
| allow | run the command |
| ask | `ctx.ui.confirm`; decline blocks |
| defer (empty stdout, `automode`) | treated like ask |

Fail-closed: a wrapper that fails to start, exits unexpectedly, or emits
unparseable output blocks the call. Without a UI (`ctx.hasUI` false) there is
nobody to confirm, so ask and defer block.

Only `bash` is gated. pi's `read`, `write`, and `edit` tools are not routed
through shizo; confine them with the sandbox.
