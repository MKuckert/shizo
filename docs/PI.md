# pi coding agent integration

[`src/pi-shizo.ts`](../src/pi-shizo.ts) is a pi extension that gates every
`bash` tool call through the same `shizo.sh` wrapper Claude Code uses (see
[`CLAUDE_CODE_HOOKS.md`](CLAUDE_CODE_HOOKS.md)). It subscribes to pi's
`tool_call` event, so the policy pipeline in [`EVALUATION.md`](EVALUATION.md) is
unchanged.

## Install

`shizo install pi-extension` copies the extension embedded in the binary into
pi's extension directory, and materializes the `shizo.sh` wrapper next to the
binary if it is missing — writing that wrapper's path into the installed
extension, which spawns it on every `bash` call. A bare release binary thus
self-installs completely. Then run pi inside the nono sandbox like any other
shizo-gated agent:

```sh
shizo install pi-extension             # .pi/extensions/shizo.ts in the cwd
shizo install pi-extension --global    # ~/.pi/agent/extensions/shizo.ts
```

A re-run is a no-op, though it still ensures the wrapper exists; if the binary
moved since, the installed path is updated in place. A stale symlink from an
older install is replaced by the regular copy; the replacement is written to a
temp file and renamed into place, so an existing extension is never missing or
partially written. Any other existing file — a hand edit, another tool's
extension — is refused, never overwritten.

The extension runs the wrapper path this install materialized next to the
binary — `$HOME/.shizo/engine/shizo.sh` in a standard layout (`shizo info
--hook-command`). Set `SHIZO_HOOK` to use another wrapper path.

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
