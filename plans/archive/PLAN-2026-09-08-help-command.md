# Add `bash-gate` help output

> TODO: "Add `bash-gate --help`, `bash-gate -?` and `bash-gate help` command to
> show help for subcommands"

## Problem

There's no help. `main()` dispatches a bare `switch os.Args[1]` on `--version`
and `validate`, then falls through to `run()` (hook mode) for everything else —
so `bash-gate help` / `--help` / `-?` today get read as hook JSON and block on
stdin. An operator has no way to list subcommands or see per-command usage;
the only usage text (`validateUsage`) is buried behind a flag error.

## Options

- **A. Top-level help + delegate to existing per-command usage (pick).** Add
  `help`, `--help`, `-h`, `-?` cases in the `main()` switch. Bare help prints a
  one-screen overview (synopsis, subcommand list, where rules live). `help
  <cmd>` / `<cmd> --help` prints that command's own usage const
  (`validateUsage`, and future `testUsage`/`selfcheckUsage`). Exit 0.
- **B. One static usage blob.** Single `const usage` covering everything,
  printed for any help request; no per-command dispatch. Less code, but goes
  stale as subcommands grow and can't answer `help validate`. Fine only while
  there's one subcommand.
- **C. flag/cobra.** A CLI framework gives help for free. Rejected: cobra is a
  heavy new dep for a hook binary whose "CLI" is three operator subcommands;
  violates the single-dep minimalism.

## Pick: A

Thin and consistent with the existing `validateUsage` pattern — each subcommand
already owns (or will own, per the test/selfcheck plans) a usage const; top-level
help just indexes them. No new dep.

## Changes

- `main.go`: add `help`/`--help`/`-h`/`-?` to the switch. Add a `rootUsage`
  const (synopsis + subcommand one-liners + rules/sandbox pointers). A tiny
  `runHelp(args)` maps `help <cmd>`→that command's usage, unknown/empty→
  `rootUsage`; writes to stdout, exit 0.
- Route each subcommand's own `--help`/`-h` to its usage (validate already
  half-does this via its flag loop — make it explicit and exit 0, not an error).
- `main_test.go`: `help`/`--help`/`-?` emit `rootUsage` to stdout + exit 0;
  `help validate` emits `validateUsage`; unknown `help bogus` falls back to root
  (exit 0). Assert a help request never reads stdin / never emits a hook decision.
- Docs: mention `bash-gate help` where `validate` is documented (CLAUDE.md
  build/test section, README).

## Open

- Keep `-v` vs adding `-h`/`-?`: fine, no collision. Should an *unknown*
  subcommand (`bash-gate bogus`) print `rootUsage`+exit 2 instead of silently
  entering hook mode? Probably yes — but only reject args that look like
  subcommands (no leading data on stdin), since hook mode is the arg-less
  default. Decide when implementing; low risk to leave hook fallthrough as-is.
