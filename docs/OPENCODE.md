# OpenCode integration

[`src/opencode-shizo.js`](../src/opencode-shizo.js) is an OpenCode plugin that
routes every shell tool call through the same `shizo.sh` wrapper Claude Code
uses (see [`CLAUDE_CODE_HOOKS.md`](CLAUDE_CODE_HOOKS.md)). It subscribes to
OpenCode's before-execution tool hook — `tool.execute.before` in 1.x,
`execute.before` in 2.x — so the policy pipeline in
[`EVALUATION.md`](EVALUATION.md) is unchanged.

One file serves both plugin generations: 2.x loads the default export's
`setup()` and registers a tool hook, 1.x loads the default export's `server()`
and receives a hook table. Both block by throwing from the before-hook, which
fails that one call and hands the error to the model.

## Install

`shizo install opencode-plugin` copies the plugin embedded in the binary into
OpenCode's project plugin directory, and materializes the `shizo.sh` wrapper
next to the binary if it is missing — writing that wrapper's path into the
installed plugin, which spawns it on every shell call. A bare release binary
thus self-installs completely. Then run opencode inside the nono sandbox like
any other shizo-gated agent:

```sh
shizo install opencode-plugin             # .opencode/plugins/shizo.js in the cwd
shizo install opencode-plugin --global    # ~/.config/opencode/plugins/shizo.js
```

A re-run is a no-op, though it still ensures the wrapper exists; if the binary
moved since, the installed path is updated in place. A stale symlink from an
older install is replaced by the regular copy; the replacement is written to a
temp file and renamed into place, so an existing plugin is never missing or
partially written. Any other existing file — a hand edit, another tool's
plugin — is refused, never overwritten.

The plugin runs the wrapper path this install materialized next to the binary
— `$HOME/.shizo/engine/shizo.sh` in a standard layout (`shizo info
--hook-command`). Set `SHIZO_HOOK` to use another wrapper path.

## Decisions

| Wrapper result | Plugin behaviour |
| --- | --- |
| deny (exit 2) | block; the model receives the reason as the tool error |
| allow | run the command (OpenCode's own permission flow still applies) |
| ask | OpenCode's permission prompt — that prompt is the confirm mechanism |
| defer (empty stdout, `automode`) | OpenCode's permission flow decides |

Fail-closed: a wrapper that fails to start, exits unexpectedly, or emits
unparseable output blocks the call.

OpenCode's permission system prompts for each new command pattern by default,
so allow/ask/defer all land where a human can still say no. If you configure
OpenCode to auto-approve bash (a permissive `permission` setting or yolo mode),
those commands run without an interactive prompt — shizo's `deny` rules still
block.

## Notes

- Only the shell tool is gated: it is named `shell` in 2.x and `bash` in 1.x
  (the rename ships with 2.0). OpenCode's `read`, `write`, and `edit` tools are
  not routed through shizo; confine them with the sandbox.
- The working directory passed to the hook is the command's `workdir` (resolved
  against the session directory) or the session directory itself, so shizo
  loads the project tier of rules that owns the command's cwd.
- If OpenCode fails to load the plugin (for example a future API change),
  shizo silently stops gating — check OpenCode's logs after upgrading.
