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
make install                 # installs to $HOME/.bash-gate
make install PREFIX=/tmp/x   # or anywhere, for verification
```

Then register the hook in `~/.claude/settings.json`:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [{ "type": "command", "command": "$HOME/.bash-gate/engine/gate.sh" }]
      }
    ]
  }
}
```

## Layout

| Path                                    | Access                | Contents                       |
| --------------------------------------- | --------------------- | ------------------------------ |
| `$HOME/.bash-gate/engine/`      | read-only             | `bash-gate`, `gate.sh`         |
| `$HOME/.bash-gate/global/rules` | read-only             | global rule tier               |
| `$HOME/.bash-gate/logs/`        | writable              | `YYYY-MM-DD.jsonl` per decision |
| `$HOME/.bash-gate/attestations/`| read-only             | `.attest` records (see Attest)  |
| `<cwd>/.bash-gate`              | repo, writable        | project tier (optional)        |

`$BASH_GATE_DIR` overrides the install root; the global rules are then
read from `<that dir>/global/rules`. Logs always live under `$HOME`.

## Prerequisite: nono read grant

The sandbox must grant **read** (deliberately not write) on
`$HOME/.bash-gate/`. Without it the hook cannot load its rules and
`gate.sh` denies everything.

## Example sandbox profile

[`conf/nono-profile.example.json`](conf/nono-profile.example.json) is a
deployment template for wrapping the agent in [`nono`](https://nono.sh/), an OS sandbox. It's standalone (no
`extends`) so it's portable across machines; replace `<AGENT_WORKDIR>` with
the repo path the agent operates on before use.

| Field                          | Value                                        | Why                                                                 |
| ------------------------------- | --------------------------------------------- | -------------------------------------------------------------------- |
| `filesystem.read`               | `~/.bash-gate`                        | Lets the hook load its rules — read only, so the agent can't rewrite them. |
| `filesystem.allow`              | `~/.bash-gate/logs`, `<AGENT_WORKDIR>` | Readwrite only where the agent needs to write: decision logs and its own workdir. |
| `workdir.access`                | `readwrite`                                   | The agent's working directory itself.                               |
| `network.block`                 | `true`                                        | Default-deny network; add `network.allow_domain` entries as needed. |
| `environment.allow_vars`        | minimal set                                   | Strip everything not explicitly needed by the agent or `bash-gate`. |

`$HOME/.bash-gate/engine/` and `global/rules` stay outside `allow`,
so they're readable but never writable from inside the sandbox — this is
what `sandbox nono` (see [`docs/RULES.md`](docs/RULES.md)) assumes.

This is a template, not `.sandbox/profile.template.json` (that one is
machine-specific, used to develop `bash-gate` itself — see
[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)).

## Rules

Rules are a line-oriented, inert DSL: `allow`/`deny` rules matched against argv
tokens (`ANY` matches 0-n tokens, `HAS` matches one flag anywhere), plus the
global-only `trust`/`sandbox`/`returntype` directives. Any deny wins over any
allow; an unmatched command yields `ask`.

**See [`docs/RULES.md`](docs/RULES.md) for the full DSL reference** — matching
semantics, directives, command resolution, and worked examples.

## Hook entrypoint (`claude-hook` / `gate.sh`)

`claude-hook` is the subcommand Claude Code actually drives: it reads the hook
JSON on stdin (`tool_input.command`, `cwd`, `session_id`), evaluates it through
the same pipeline as `test`, and emits a decision object — exit `2` for deny,
`0` for allow/ask.

```sh
echo '{"tool_name":"Bash","tool_input":{"command":"git status"},"cwd":"."}' \
  | dist/bash-gate claude-hook
```

It's never invoked directly in practice — `gate.sh` (installed as the hook
`command`, see above) wraps it and turns every fail-open gap (crash, timeout,
empty/non-JSON output) into a blocking deny, and refuses to run at all if its
log directory isn't writable. This is also the only mode that logs decisions,
to `$HOME/.bash-gate/logs/YYYY-MM-DD.jsonl`.

**See [`docs/CLAUDE_CODE_HOOKS.md`](docs/CLAUDE_CODE_HOOKS.md)** for the full
I/O contract, `returntype automode`, exit codes, and the `gate.sh` source.

## Validate

Lint rule files before installing them — a build-time check, completely separate
from the hook pipeline (no stdin, sandbox gate, logging, or decision):

```sh
dist/bash-gate validate                 # resolved global + project (cwd) tiers
dist/bash-gate validate FILE...         # explicit files, validated as a global tier
dist/bash-gate validate --project FILE  # validate FILE as a project tier
```

Run `dist/bash-gate help` for a subcommand overview, or `help <command>` (also
`<command> --help`) for per-command usage.

Unlike the fail-closed loader (which collapses to deny-all on the first bad line),
`validate` reports **every** syntax error with its line number, plus semantic
warnings: dead rules (a command that is unconditionally denied or a safe builtin),
allow rules fully shadowed by a deny, missing/non-directory `trust` paths, project-
tier directives that will be ignored, and duplicate rules. Output is
`file:line: error|warning: message`. Exit `0` = no errors (warnings allowed),
`1` = errors, `2` = usage.

## Attest

The project tier lives in the writable workdir, so it's agent/attacker-editable.
`attest` records operator approval of a tier's current content (a hash of its
normalized rules); the hook recomputes that hash on every load and **denies
everything** if it doesn't match — same fail-closed shape as a load error. Run
outside the sandbox, after reviewing the rules (e.g. with `validate`):

```sh
dist/bash-gate attest --global   # attest the resolved global tier
dist/bash-gate attest [FILE]     # attest a project tier (default: <cwd>/.bash-gate)
```

Cosmetic edits (comments, whitespace, rule order) don't force a re-attest —
only a change that alters the parsed rule set does. `make install` auto-attests
the starter global rules on a fresh install; a later edit to the global tier
needs an explicit re-attest. See [`docs/RULES.md §10`](docs/RULES.md#10-attestation).

The global record also snapshots the live `PATH`∩trust-dirs, so if the agent's
runtime `PATH` differs from the shell you attested in, the hook will deny
everything with a `PATH ... changed since global attestation` reason — re-run
`bash-gate attest --global` in (or matching) that environment.

## Test

Dry-run a command through the full pipeline to see what the gate would do —
operator-facing, so it never logs and never emits hook JSON:

```sh
dist/bash-gate test 'git status'                      # command as arguments
dist/bash-gate test --cwd /path/to/repo 'git status'  # pick a project tier
echo 'git push' | dist/bash-gate test                 # command from stdin
dist/bash-gate test --stdin-json < hook.json          # replay a full hook JSON
```

The evaluation is faithful to the hook (§2), including exit codes (`2` on deny,
`0` on allow/ask). Because operators run outside the sandbox — where the sandbox
gate denies everything — pass `--assume-sandboxed` to exercise the rules; a
sandbox-gate-only deny prints a hint pointing at that flag. `--json` emits the
machine-readable log-line shape instead of the human table.

## Self-check

Verify the deployed sandbox profile actually delivers the read/write shape
the fail-closed model rests on — `engine/` and `global/rules` read-only,
`logs/` writable. **Must be run inside the sandbox**: nono enforces at the
syscall layer, so file mode bits don't reflect what's actually allowed; each
check attempts the real operation (open-for-write, create+remove a probe
file) instead of inspecting permissions. Non-destructive — `global/rules` is
never truncated.

```sh
dist/bash-gate selfcheck             # table output, exit 0/1
dist/bash-gate selfcheck --json      # machine-readable
dist/bash-gate selfcheck --root DIR  # check a different install root
```

## Kill switch

The rules directory is read-only from inside the sandbox and a load failure
denies everything, so recovery happens from **outside** the sandbox:

1. Remove the `PreToolUse` entry from `~/.claude/settings.json`, **or**
2. Set `BASH_GATE_DIR` to a directory containing a permissive
   `global/rules` file.

## GATE_DEBUG (sensitive)

Setting `GATE_DEBUG` makes `gate.sh` mirror the raw hook JSON to
`logs/stdin.log`. That file contains attacker-controlled command text and any
secrets passed as argv. Leave it unset in normal operation; gitignore/rotate
`stdin.log` so it never lands in a commit.

## NO_COLOR

Operator commands (`validate`, `test`, `selfcheck`, `attest`) use ANSI colors
when writing to a terminal. Set `NO_COLOR` to any non-empty value to suppress
them (see [no-color.org](https://no-color.org)).

## Editor support

[`conf/editor/`](conf/editor/) contains a TextMate grammar for syntax
highlighting of rule files in VS Code, Sublime Text, and other
TextMate-compatible editors.

For VS Code, symlink the directory into your extensions folder and reload:

```sh
ln -s /path/to/bash-gate/conf/editor ~/.vscode/extensions/bash-gate
```
