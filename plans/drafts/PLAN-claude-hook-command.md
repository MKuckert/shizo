# `bash-gate claude-hook` — explicit hook subcommand

> TODO: "Better DX: No arg-less calling of `bash-gate`, so app doesn't wait on
> stdin by default. Use `bash-gate claude-hook` instead."

## Problem

Arg-less `bash-gate` is hook mode: `dispatch` falls through to `run()`, which
blocks on `io.ReadAll(stdin)`. An operator who types `bash-gate` (to see what it
is, or by muscle memory) gets a silent hang with no prompt — the worst possible
first-run experience. Every other invocation shape is explicit; hook mode should
be too.

## Reuse

`run()` already is hook mode — just route a named subcommand to it. `rootUsage`
already exists as the one-screen overview to print when no command is given.
`dispatch` already errors on unknown subcommands; this just moves the arg-less
default off "block on stdin" and onto "print usage".

## The one decision: what arg-less does now

Arg-less must stop reading stdin. Two ways it can go:

- **A. Arg-less prints `rootUsage`, exit 0; `claude-hook` runs the gate (pick).**
  Symmetric with `help`. Discoverable: a bare run tells you `claude-hook` is the
  hook entrypoint. No stdin read on the no-arg path.
- **B. Keep arg-less as hook mode, add `claude-hook` as an alias.** Solves
  nothing — the hang stays. Rejected.

## Pick: A

## Breaking change (must land together)

`gate.sh:22` invokes `"$dir/bash-gate"` arg-less. After this change that prints
usage instead of gating → the wrapper's `*)` catch-all turns it into a
fail-closed deny. Safe (deny, not allow) but breaks the gate until reinstalled.
So: **update `gate.sh` and the binary in the same commit**, and note in the
changelog/README that installed wrappers must be refreshed (`make install`
re-copies `gate.sh`, never clobbers rules).

No back-compat shim for the old arg-less call — an old wrapper against a new
binary fails closed (deny-all), which is the correct direction to fail, and
`make install` fixes it.

## Changes

- `main.go`: add `case "claude-hook": return run(stdin, stdout)` to `dispatch`;
  change the arg-less fallthrough from `return run(...)` to print `rootUsage` +
  exit 0 (reuse `runHelp(nil, stdout)`). Update the `dispatch` doc comment and
  `rootUsage` (the `(none)` line becomes `claude-hook`; add a `(none) → help`
  line). Add a `claude-hook` entry to `runHelp`'s switch (its own usage: reads
  hook JSON on stdin, emits decision, exit 0/2 — the only mode that logs).
- `gate.sh`: `"$dir/bash-gate" claude-hook`.
- `main_test.go`: `claude-hook` + stdin JSON → decision (allow/ask/deny) exactly
  as arg-less did before; arg-less → `rootUsage` + exit 0 + **never reads stdin**
  (feed a blocking/failing reader to prove it). Unknown command still exit 2.
- Docs: `README.md` install snippet + `CLAUDE.md` (hook invocation is now
  `claude-hook`); `docs/CLAUDE_CODE_HOOKS.md` (the `command` line); the `conf/`
  example settings if they name the wrapper.

## Open

- `run` mode is the only one that logs — keep it that way; `claude-hook` inherits
  it unchanged.
