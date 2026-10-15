---
name: shizo
description: >
  Install, configure, and troubleshoot shizo — the fail-closed PreToolUse
  hook for Claude Code's Bash tool. Use when setting up the hook, authoring or
  editing global/project rules, configuring the nono sandbox profile, attesting
  rules, validating rule files, debugging a denied command, or running selfcheck.
  Triggers: "shizo", "install shizo", "rules", "sandbox", "nono",
  "denied", "command blocked", "attest", "validate rules", "selfcheck",
  "trust directory".
---

You are guiding the operator through shizo setup and troubleshooting.
shizo is a fail-closed PreToolUse hook that parses Bash syntax, resolves
executables through PATH, and matches argv tokens against a rule DSL.

Reference docs live in `docs/` — link to them, don't duplicate semantics.

---

## Task: Install the engine

1. Build and install:

   ```sh
   make test
   make install                 # -> $HOME/.shizo
   make install PREFIX=/path    # alternate root
   ```

2. Register the hook in `~/.claude/settings.json` (register the wrapper, not
   the binary):

   ```json
   {
     "hooks": {
       "PreToolUse": [
         {
           "matcher": "Bash",
           "hooks": [
             { "type": "command", "command": "$HOME/.shizo/engine/shizo.sh" }
           ]
         }
       ]
     }
   }
   ```

   See `conf/claude-settings.json` for a ready-made example.

3. Verify: run `shizo test --assume-sandboxed 'echo hello'` — it should
   allow (safe builtin). Then `shizo test --assume-sandboxed 'eval foo'`
   — it should deny (unconditional deny).

Installed layout:

| Path                             | Access    | Purpose                    |
| -------------------------------- | --------- | -------------------------- |
| `$HOME/.shizo/engine/`       | read-only | binary + `shizo.sh` wrapper |
| `$HOME/.shizo/global/rules`  | read-only | required global policy     |
| `$HOME/.shizo/attestations/` | read-only | approved rule hashes       |
| `$HOME/.shizo/logs/`         | writable  | daily decision logs        |
| `<cwd>/.shizo`               | workspace | optional project policy    |

Details: `docs/CLAUDE_CODE_HOOKS.md` (hook I/O), `README.md` (full install).

---

## Task: Author rules

The global tier (`$HOME/.shizo/global/rules`) is required. The project
tier (`<cwd>/.shizo`) is optional and cannot override global denies.

### Directives (global-only, must precede rules)

- `sandbox <nono|dangerously-none>` — required; missing = deny everything. `nono`
  is default for a [nono OS sandbox](https://nono.sh/).
- `trust <absolute-directory>` — repeatable; executable's directory must match.
- `returntype <decision|automode>` — optional; `decision` is default.

### Rule syntax

```text
allow <command> [tokens...] [ANY | HAS <flag>]
deny  <command> [tokens...] [ANY | HAS <flag>]
```

- `ANY` matches 0+ tokens at one position (max one per rule).
- `HAS <flag>` finds one flag anywhere after the literal prefix (max one, must be last).
- `ANY` and `HAS` cannot share a rule.
- Literal `ANY`/`HAS` as an argument: single-quote it.
- Denies always beat allows. No match = `ask`.

### Starter rules

`conf/global-rules.starter` is installed as the initial global tier. Review it,
adjust trust directories to match the machine's PATH, and edit rules to fit.

### Project tier

A `<cwd>/.shizo` file can add `allow` rules (turns `ask` into `allow`) and
`deny` rules. It cannot contain directives or override a global deny.

### Validate

```sh
shizo validate                        # resolved global + project tiers
shizo validate FILE...                # files as global tier
shizo validate --project FILE         # file as project tier
```

Reports all syntax errors and semantic warnings (dead rules, missing trust dirs,
shadowed allows, ignored project directives). Exit 0 = no errors, 1 = errors.

### Attest

Under `sandbox nono`, every present tier needs attestation:

```sh
shizo attest --global    # global tier + trusted PATH snapshot
shizo attest [FILE]      # project tier; defaults to <cwd>/.shizo
```

Run outside the sandbox after reviewing and validating the tier. A fresh
`make install` attests the starter global rules automatically.

### Verify with test

```sh
shizo test --assume-sandboxed 'git status'     # allow/deny/ask?
shizo test --assume-sandboxed --json 'git push' # log-record shape
shizo test --cwd /path 'command'                # pick a project tier
```

Workflow: validate → attest → test representative commands.

Details: `docs/RULES.md`.

---

## Task: Sandbox setup (nono)

1. Copy `conf/nono-profile.example.json` to your nono profile location.
2. Replace `<AGENT_WORKDIR>` with the agent working directory, and the
   example's writable `src`, `docs`, and `README.md` with your project's
   source directories and top-level files.
3. The profile grants:
   - read: `~/.shizo` (engine, rules, attestations), `<AGENT_WORKDIR>`
   - read/write: `~/.shizo/logs`, the named source directories and files only.
     Keep `.git`, `.claude`, and `.shizo` read-only: allowed redirects into
     them can run arbitrary commands or unregister the hook
     (`README.md#example-sandbox-profile`).
4. Verify from inside the sandbox:

   ```sh
   shizo selfcheck         # text output
   shizo selfcheck --json  # structured output
   ```

   `selfcheck` probes that engine/rules/attestations are not writable and logs
   are writable. If anything fails, fix the profile grants.

Details: `docs/ARCHITECTURE.md` (sandbox assumptions), `README.md#install`.

---

## Task: Command unexpectedly denied

When a command is denied and shouldn't be:

1. Reproduce with `shizo test --json '<command>'` to see the full decision.
2. Read the `reason` field — it maps to a step in `docs/EVALUATION.md`:
   - "unconditional deny" → the command name is in the hardcoded deny set
     (`eval`, `exec`, `source`, `env`, `sudo`, `xargs`, …). No rule can allow it.
   - "untrusted directory" → the resolved executable is not in a `trust` dir.
     Add a `trust` directive to the global tier if appropriate.
   - "denied by rule" → a deny rule matched. Check global and project denies.
   - "unmatched" → no rule matched; result is `ask`. Add an `allow` rule.
   - "parse error" → the shell syntax itself failed to parse.
   - "dynamic argv" / "assignment" / "function declaration" / "redirect" →
     structural denial; see `docs/EVALUATION.md` steps 7–12.
3. After fixing rules: `shizo validate`, then `shizo attest`, then
   re-test.
