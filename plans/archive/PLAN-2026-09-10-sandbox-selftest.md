# Self-test the sandbox environment

> TODO: "Add self-testing for sandbox env: global rules readable but not
> writable, logs writable, …"

## Problem

The model rests on the sandbox enforcing a read/write shape on the install root:
`engine/` + `global/rules` read-only (writable rules = total bypass), `logs/`
writable (else `gate.sh` denies everything). Nothing verifies the deployed nono
profile actually delivers it. Want a command to assert it, run inside the sandbox.

## Key subtlety

nono enforces at the syscall layer, so OS mode bits don't reflect what the
sandbox allows. Probe by *attempting* the op, and be non-destructive: never
truncate `global/rules` — test file-write by `OpenFile(O_WRONLY)` + close (no
write), dir-write by create+remove a `.probe-<pid>` file.

## Checks

| Invariant                    | Probe                          | Expect |
| ---------------------------- | ------------------------------ | ------ |
| actually sandboxed           | `NONO_CAP_FILE` set            | set    |
| `global/rules` readable      | `ReadFile`                     | ok     |
| `global/rules` not writable  | `OpenFile(O_WRONLY)`, close    | fail   |
| `global/`, `engine/` not writable | create+remove `.probe-<pid>` | fail   |
| `logs/` writable             | create+remove `.probe-<pid>`   | ok     |

## Decision

`bash-gate selfcheck` subcommand. Runs the probes, PASS/FAIL per check, exit
0/1. Path logic (`installRoot`/`logDir`) already lives in the binary → one
source of truth, probe helpers unit-testable.
Include a "config loads clean" check, e.g. use `validate` for the default paths.

Rejected: a shell script in `engine/` (duplicates path logic, harder to test),
and always-on checks in `gate.sh` (latency + fragile deny on every call —
`gate.sh` already does the one cheap essential check: logs writable).

## Changes

- `main.go`: `case "selfcheck": os.Exit(runSelfcheck(...))`.
- New `selfcheck.go`: a `[]check` table off the install paths; non-destructive
  probe helpers (`probeWritable`, `probeFileNotWritable`); `--json`, optional
  `--root`; print table + summary; exit 0/1.
- `selfcheck_test.go`: drive probes against temp dirs with toggled perms (the
  full sandbox behavior only runs inside nono; unit-test the primitives).
- Docs: `README.md` "Self-check" (must run inside the sandbox; mode bits aren't
  the signal), pointer from `ARCHITECTURE.md` Install/Self-protection.
