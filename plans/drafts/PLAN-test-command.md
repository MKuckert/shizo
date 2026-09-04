# `bash-gate test` — dry-run a command through the pipeline

> TODO: "Add `bash-gate test` command to accept a raw bash command and let the
> pipeline validate like it's a real hook. `cwd` is derived or passed."

## Problem

No way to ask "what would the gate do with this command?" without issuing it
through Claude Code and reading the log. Need a fast `command → decision +
reason + per-command breakdown` loop.

## Reuse

`decide()` already returns `(Outcome, reason, []cmdLog)`; `loadConfig` builds the
tiers; `currentEnviron()` supplies PATH/tmp/sandbox flag; `runValidate` is the
model for an operator-only subcommand (no stdin, no hook JSON, no logging).

## The one decision: the sandbox gate

`decide()` runs the sandbox gate — outside the sandbox it denies everything, and
that's where operators run `test`. So: run faithfully, but add
`--assume-sandboxed` to set `env.sandboxed=true`, and hint when a deny is
sandbox-gate-only.

## Options

- **A. `bash-gate test [flags] [COMMAND]` (pick).** COMMAND from arg or stdin;
  `--cwd DIR` (default `os.Getwd()`), `--assume-sandboxed`, `--json` (emit the
  `logLine` shape), `--stdin-json` (replay a full hook JSON — cheap, reuses
  `parseInput`). Human output = decision/reason + per-command table via
  `displayArgv`. Exit codes mirror the hook (deny=2, else 0) so "like a real
  hook" holds.
- **B. raw-JSON-only mode.** Most faithful, worst UX. Fold in as `--stdin-json`,
  not the default.

## Pick: A (with B's `--stdin-json` as a sub-mode)

## Changes

- `main.go`: `case "test": os.Exit(runTest(...))`.
- New `test.go` (mirrors `validate.go`): parse flags; resolve cwd; `loadConfig` +
  `currentEnviron` (OR-in `--assume-sandboxed`); read command; `decide`; render
  table or `--json`; sandbox-gate hint.
- `test_test.go`: allow/ask/deny, `--cwd` picks a project tier,
  `--assume-sandboxed` flips a gate deny, `--json` shape, `--stdin-json`,
  missing-command usage (exit 2).
- Docs: `README.md` "Test" section, `CLAUDE.md` build block line. Note: never
  logs, never emits hook JSON.

## Open

- Exit codes hook-faithful (0/2) vs distinct (0/1/2). Lean faithful; `--json`
  distinguishes allow/ask.
