# Rename `.claude-bash-rules` to be harness-neutral

## Problem

The directory name `.claude-bash-rules` hard-codes "claude" — it's a
bash-gate concern, not a Claude concern. If another harness (Cursor, Windsurf,
etc.) adopted the hook, the name is confusing. A neutral name like
`.bash-gate` makes the tool its own thing.

## Scope

Pure rename: every path literal, env-var default, doc reference, config
example, and test fixture that spells `.claude-bash-rules` becomes
`.bash-gate`. The env-var override (`CLAUDE_BASH_RULES_DIR`) is removed, and
the new env-var `BASH_GATE_DIR` is used instead. This is a breaking change
for anyone who has been using the env-var override — just me.

## Changes

- `main.go`: `defaultHookRoot` / `installRoot` / `projectRulesPathFor` /
  `logDir` — change the literal `.claude-bash-rules` to `.bash-gate`.
  Add `BASH_GATE_DIR` env-var check before `CLAUDE_BASH_RULES_DIR` fallback.
- `gate.sh`: `log=` path.
- `Makefile`: `PREFIX` default.
- `conf/`: starter rules header comment, `claude-settings.json` path,
  `nono-profile.example.json` paths.
- `.sandbox/profile.template.json`: all path entries.
- `.claude/settings.json`: hook command path.
- `docs/`: `RULES.md`, `EVALUATION.md`, `ARCHITECTURE.md`,
  `CLAUDE_CODE_HOOKS.md` — find-and-replace.
- `README.md`, `CLAUDE.md`: same.
- Tests: `main_test.go` suffix assertion, fixture paths in `rules_test.go`,
  `policy_test.go`, `attest_test.go`, `selfcheck_test.go`, `validate_test.go`,
  `gate_test.go`, `test_test.go`.
- `make install`: migration note — detect old `~/.claude-bash-rules`, print
  one-line hint to rename (don't auto-migrate, operator chooses).

## Test

`grep` the repository for a remaining `claude-bash-rules` string.

## Not included

- Renaming the Go module or binary (`bash-gate` stays).
- Auto-migration logic.
