# bash-gate

`bash-gate` is a fail-closed `PreToolUse` hook for Agentic Harnesses providing
a Bash tool. It parses shell syntax, extracts every command from pipelines and
command lists, resolves executables through `PATH`, and applies an argv-based
rule set. Anything it cannot verify statically is denied.

## Why?

Every Bash tool call in an Agentic Harness like _Claude Code_ passes through a
permission check. Without policy, the operator sees every command and must decide
in real time: safe or not. That constant prompting causes two kinds of fatigue.

- **Operator fatigue:** a human who approves hundreds of `git status` and `ls`
  invocations stops reading them, and a dangerous command slips through on muscle
  memory.
- **Model fatigue:** the model forgets its instructions from AGENTS.md or other
  files mid-conversion. They are no hard rules, more like a checklist, a guardrail.
  The model may issue dangerous commands because it forgot the policy or misread
  the prompt.

Most commands fall into two obvious buckets. `git status` is always fine.
`curl | bash` is never fine. Only a narrow middle band — an unfamiliar flag, a
write to an unexpected path, a tool the policy author hasn't reviewed — deserves
a human decision. `bash-gate` encodes the obvious buckets as rules so that
operators only see what actually requires judgment.

The design depends on two deployment properties:

- **The agent runs inside an OS sandbox.** Normally `nono`, the sandbox protects
  the engine, global rules, and attestations from writes while limiting
  filesystem and network reach. `bash-gate` supplements that boundary; it does
  not replace it.
- **Shell state does not persist between tool calls** and hook input `cwd`
  reflects the command's actual working directory. These properties make it
  practical to reject assignments and functions and meaningful to enforce
  redirect containment and relative-path resolution.

Together, the sandbox and the static rule layer produce three outcomes:
- **allow** for known-safe commands (no prompt),
- **deny** for known-dangerous commands (blocked before execution)
- and **ask** for everything else (the human or harness auto classifier decides).
  The goal is to shrink that third bucket until it contains only genuine decisions.

## Documentation

- [`docs/RULES.md`](docs/RULES.md): rule files, matching, trust, and attestation
- [`docs/EVALUATION.md`](docs/EVALUATION.md): ordered decision pipeline
- [`docs/CLAUDE_CODE_HOOKS.md`](docs/CLAUDE_CODE_HOOKS.md): hook I/O and wrapper behavior
- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md): assumptions, decisions, and residual risks

## Install

The `/bash-gate` Claude Code skill walks through each step below interactively,
including rule authoring, sandbox configuration, and troubleshooting.

```sh
make test
make install                 # $HOME/.bash-gate
make install PREFIX=/tmp/x   # alternate root
```

Register the wrapper, not the binary, in `~/.claude/settings.json`:

```json
{
  "hooks": {
    "PreToolUse": [{
      "matcher": "Bash",
      "hooks": [{ "type": "command", "command": "$HOME/.bash-gate/engine/gate.sh" }]
    }]
  }
}
```

The sandbox must grant read-only access to `$HOME/.bash-gate` and write access
to its `logs` directory. Without these grants, the hook denies every command.
[`conf/nono-profile.example.json`](conf/nono-profile.example.json) is a portable
`nono` profile template; replace `<AGENT_WORKDIR>` before use. It grants:

| Path or capability | Access |
| --- | --- |
| `~/.bash-gate` | read |
| `~/.bash-gate/logs`, `<AGENT_WORKDIR>` | read/write |
| network | denied by default |
| environment | explicit variables only |

The installed layout is:

| Path | Access | Purpose |
| --- | --- | --- |
| `$HOME/.bash-gate/engine/` | read-only | binary and `gate.sh` |
| `$HOME/.bash-gate/global/rules` | read-only | required global policy |
| `$HOME/.bash-gate/attestations/` | read-only | approved rule hashes |
| `$HOME/.bash-gate/logs/` | writable | daily decision logs |
| `<cwd>/.bash-gate` | workspace | optional project policy |

`$BASH_GATE_DIR` selects another install root for operator commands. Hook mode
uses the account home directory instead, preventing environment-based policy
redirection. Logs always remain below `$HOME`.

## Rules

Rules are inert, line-oriented argv patterns. `ANY` matches zero or more tokens;
`HAS` finds one flag after a literal prefix. Denies override allows, and an
unmatched command produces `ask`. Directives that can loosen policy (`trust`,
`sandbox`, and `returntype`) are global-only. See [`docs/RULES.md`](docs/RULES.md).

## Operator commands

Use `bash-gate help`, `help <command>`, or `<command> --help` for command help.

### Validate rules

```sh
dist/bash-gate validate                 # resolved global and cwd project tiers
dist/bash-gate validate FILE...         # files treated as a global tier
dist/bash-gate validate --project FILE  # file treated as a project tier
```

`validate` reports all syntax errors and semantic warnings. Its output is
`file:line: error|warning: message`; exit codes are 0 for no errors, 1 for
errors, and 2 for bad usage. Warnings cover duplicates, dead or shadowed rules,
missing trust directories, and ignored project directives.

### Attest rules

After reviewing and validating a tier, approve it outside the sandbox:

```sh
dist/bash-gate attest --global   # resolved global tier and trusted PATH snapshot
dist/bash-gate attest [FILE]     # project tier; defaults to <cwd>/.bash-gate
```

The hook denies everything if a present tier lacks a matching attestation or if
the trusted portion of `PATH` changed. Comments, whitespace, and rule order are
excluded from the hash. A fresh install attests its starter global rules.

### Test a decision

```sh
dist/bash-gate test 'git status'
dist/bash-gate test --cwd /path/to/repo 'git status'
echo 'git push' | dist/bash-gate test
dist/bash-gate test --stdin-json < hook.json
```

`test` runs the hook policy without logging or hook JSON. It exits 2 for deny
and 0 for allow/ask. Outside the sandbox, use `--assume-sandboxed` to bypass only
the sandbox-presence check. `--json` prints the log-record shape.

### Verify deployment

Run the syscall-based checks from inside the sandbox:

```sh
dist/bash-gate selfcheck
dist/bash-gate selfcheck --json
dist/bash-gate selfcheck --root DIR
```

The check verifies that the engine, rules, and attestations cannot be written
and that logs can. It does not rely on file mode bits or modify the rules.

## Operations and recovery

Hook decisions are appended to `$HOME/.bash-gate/logs/YYYY-MM-DD.jsonl`.
`gate.sh` refuses to evaluate commands if that directory is not writable.

To disable a broken deployment, work outside the sandbox and either remove its
`PreToolUse` registration or point operator tooling at an alternate install
containing a permissive `global/rules` file.

`GATE_DEBUG` copies raw hook input to `logs/stdin.log`. This can expose command
text and argv secrets; leave it unset and never commit that file. `NO_COLOR`
disables ANSI output from operator commands.

TextMate-compatible syntax highlighting lives in [`conf/editor/`](conf/editor/).
For VS Code, symlink that directory into `~/.vscode/extensions/bash-gate` and
reload the editor.
