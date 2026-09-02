# bash-gate

A Claude Code `PreToolUse` hook that allowlists Bash commands at subcommand
granularity. It parses each command with `mvdan.cc/sh/v3/syntax`, decomposes
pipes and `&&`/`||`/`;` chains, resolves every command against the inherited
`PATH` + a configured trusted-dir set, and matches argv tokens against a
line-oriented rule DSL. Fail-closed: anything it cannot statically verify is
denied.

**Documentation**

- [`docs/RULES.md`](docs/RULES.md) — the rules DSL reference (matching, directives, resolution).
- [`docs/EVALUATION.md`](docs/EVALUATION.md) — the ordered evaluation pipeline.
- [`docs/CLAUDE_CODE_HOOKS.md`](docs/CLAUDE_CODE_HOOKS.md) — the Claude Code hook protocol (I/O contract, exit codes, `gate.sh`).
- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — design decisions, residual risks, and rationale.

## Build & install

```sh
make test
make install                 # installs to $HOME/.claude-bash-rules
make install PREFIX=/tmp/x   # or anywhere, for verification
```

Then register the hook in `~/.claude/settings.json`:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [{ "type": "command", "command": "$HOME/.claude-bash-rules/engine/gate.sh" }]
      }
    ]
  }
}
```

## Layout

| Path                                    | Access                | Contents                       |
| --------------------------------------- | --------------------- | ------------------------------ |
| `$HOME/.claude-bash-rules/engine/`      | read-only             | `bash-gate`, `gate.sh`         |
| `$HOME/.claude-bash-rules/global/rules` | read-only             | global rule tier               |
| `$HOME/.claude-bash-rules/logs/`        | writable              | `YYYY-MM-DD.jsonl` per decision |
| `<cwd>/.claude-bash-rules`              | repo, writable        | project tier (optional)        |

`$CLAUDE_BASH_RULES_DIR` overrides the install root; the global rules are then
read from `<that dir>/global/rules`. Logs always live under `$HOME`.

## Prerequisite: nono read grant

The sandbox must grant **read** (deliberately not write) on
`$HOME/.claude-bash-rules/`. Without it the hook cannot load its rules and
`gate.sh` denies everything.

## Rules

Rules are a line-oriented, inert DSL: `allow`/`deny` rules matched against argv
tokens (`ANY` matches 0-n tokens, `HAS` matches one flag anywhere), plus the
global-only `trust`/`sandbox`/`returntype` directives. Any deny wins over any
allow; an unmatched command yields `ask`.

**See [`docs/RULES.md`](docs/RULES.md) for the full DSL reference** — matching
semantics, directives, command resolution, and worked examples.

## Validate

Lint rule files before installing them — a build-time check, completely separate
from the hook pipeline (no stdin, sandbox gate, logging, or decision):

```sh
dist/bash-gate validate                 # resolved global + project (cwd) tiers
dist/bash-gate validate FILE...         # explicit files, validated as a global tier
dist/bash-gate validate --project FILE  # validate FILE as a project tier
```

Unlike the fail-closed loader (which collapses to deny-all on the first bad line),
`validate` reports **every** syntax error with its line number, plus semantic
warnings: dead rules (a command that is unconditionally denied or a safe builtin),
allow rules fully shadowed by a deny, missing/non-directory `trust` paths, project-
tier directives that will be ignored, and duplicate rules. Output is
`file:line: error|warning: message`. Exit `0` = no errors (warnings allowed),
`1` = errors, `2` = usage.

## Kill switch

The rules directory is read-only from inside the sandbox and a load failure
denies everything, so recovery happens from **outside** the sandbox:

1. Remove the `PreToolUse` entry from `~/.claude/settings.json`, **or**
2. Set `CLAUDE_BASH_RULES_DIR` to a directory containing a permissive
   `global/rules` file.

## GATE_DEBUG (sensitive)

Setting `GATE_DEBUG` makes `gate.sh` mirror the raw hook JSON to
`logs/stdin.log`. That file contains attacker-controlled command text and any
secrets passed as argv. Leave it unset in normal operation; gitignore/rotate
`stdin.log` so it never lands in a commit.
