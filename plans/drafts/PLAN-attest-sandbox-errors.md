# Better error messages when attesting from within a sandbox

## Problem

Running `bash-gate attest` inside a nono sandbox fails with a generic
`attest: open $HOME/.claude-bash-rules/attestations/global.attest: operation not permitted`
from `MkdirAll` or `WriteFile` on the attestations directory. The operator
gets no hint that attest is an out-of-sandbox operation — they just see
a Go `os` error and have to figure it out.

## Fix

Detect the sandbox before attempting the write and emit a clear message.

## Changes

- `attest.go`, top of `runAttest`: if running inside a sandbox, print
  `attest: this command must be run outside the sandbox` and return 1.
  One `if`, one `Fprintf`, done.
- `attest_test.go`: one case with `NONO_CAP_FILE` set, assert the message
  and exit code.

## Not included

- Changing the `writeAttestRecord` error path — the early exit makes it
  unreachable under sandbox.
