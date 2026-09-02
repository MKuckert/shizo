# bash-gate

A Claude Code `PreToolUse` hook that allowlists Bash commands at subcommand
granularity. It parses each command with `mvdan.cc/sh/v3/syntax`, decomposes
pipes and `&&`/`||`/`;` chains, resolves every command against the inherited
`PATH` + a configured trusted-dir set, and matches argv tokens against a
line-oriented rule DSL. Fail-closed: anything it cannot statically verify is
denied. See `../../PLAN-bash-allowlist.md` for the full design.

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
| `<cwd>/.claude-bash-rules`              | repo, writable        | project tier (tighten-only)    |

`$CLAUDE_BASH_RULES_DIR` overrides the install root; the global rules are then
read from `<that dir>/global/rules`. Logs always live under `$HOME`.

## Prerequisite: nono read grant

The sandbox must grant **read** (deliberately not write) on
`$HOME/.claude-bash-rules/`. Without it the hook cannot load its rules and
`gate.sh` denies everything.

## Rules

Directives (`returntype`, `trust`, `sandbox`) are global-tier only and
must precede any `allow`/`deny` rules. In a project file they are ignored and
logged. Any deny wins over any allow; an unmatched command yields `ask`.

- `allow git status ANY` — `ANY` matches 0-n tokens (max one per rule).
- `deny git reset HAS --hard` — `HAS` matches one flag at any position
  (with `=`-splitting and single-dash bundle matching, so `-f` catches `-rf`).
- A literal argument spelled `ANY`/`HAS` must be single-quoted.

`returntype decision` (default) makes the hook decide every case. `returntype
automode` makes it emit only denials and defer everything else to Claude Code's
own auto-mode classifier.

`sandbox nono` (default) requires a nono sandbox: commands are denied unless
`NONO_CAP_FILE` is set. `sandbox dangerously-none` removes that gate, allowing
commands to run with no sandbox at all — use only when you fully trust the
environment.

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
