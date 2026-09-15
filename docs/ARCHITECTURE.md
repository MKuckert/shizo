# Architecture

`bash-gate` is a static policy layer around Claude Code's Bash tool. The
[evaluation pipeline](EVALUATION.md), [rules reference](RULES.md), and
[hook protocol](CLAUDE_CODE_HOOKS.md) define behavior; this document explains
the constraints behind it.

## Assumptions

The design depends on two deployment properties:

- The agent runs inside an OS sandbox, normally `nono`. The sandbox protects the
  engine, global rules, and attestations from writes while limiting filesystem
  and network reach. `bash-gate` supplements that boundary; it does not replace
  it. `sandbox nono` checks `NONO_CAP_FILE` to prevent accidental unconfined use.
- Known-safe commands should run without prompts, known-dangerous commands
  should stop, and unfamiliar but statically valid commands should retain a
  human backstop. This produces the `allow`/`deny`/`ask` outcome model.

The Bash integration also assumes that shell state does not persist between tool
calls and that hook input `cwd` is the command's actual working directory. The
first makes rejecting assignments and functions practical; the second makes
relative resolution and redirect containment meaningful. These controls must be
revisited if either behavior changes.

## Design choices

| Area                   | Choice                                                                             | Consequence                                                                        |
| ---------------------- | ---------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------- |
| Failure model          | Refuse anything not statically verified                                            | Parse, config, trust, and wrapper failures deny                                    |
| Outcomes               | deny match → `deny`; allow match → `allow`; unmatched → `ask`                      | Common safe operations avoid prompts without silently accepting unknown commands   |
| Parser                 | `mvdan.cc/sh/v3/syntax`                                                            | Native Go AST, one dependency, no runtime bridge                                   |
| Matching               | argv tokens with one linear `ANY` or `HAS` construct                               | Avoids unsafe command-string globbing and backtracking                             |
| Dynamic shell features | Inspect substitutions; reject variables, arithmetic, assignments, and functions    | Unknown runtime values cannot bypass matching                                      |
| Executable trust       | Search inherited `PATH`, then require a configured trusted directory               | Models the binary Bash would choose while rejecting user-writable shadows          |
| Canonical paths        | Do not use `realpath`                                                              | Policies refer to stable lookup paths rather than versioned symlink targets        |
| Redirects              | Contain writes; deny writes when `cd` appears                                      | Prevents policy-approved commands from writing arbitrary sandbox-visible files     |
| Policy tiers           | Required global tier plus optional cwd project tier                                | Repositories can add policy, but a global deny remains absolute                    |
| Loosening controls     | `trust`, `sandbox`, and `returntype` are global-only                               | A project cannot alter the execution boundary or output mode                       |
| Project integrity      | Hash normalized tiers and verify a read-only attestation store                     | Workspace edits fail closed until approved by an operator                          |
| Installation           | Engine, rules, and attestations below `$HOME/.bash-gate`, read-only in the sandbox | Writable Claude configuration cannot replace the gate                              |
| Hook failures          | Exit 2 for denial; wrapper converts malformed outcomes to exit 2                   | Crashes and invalid output do not silently permit execution                        |
| Automode               | Defer only unmatched results                                                       | Explicit allow and deny policy remains authoritative                               |
| Logging                | Require a writable log directory before evaluation                                 | Every attempted hook decision has a log destination, though logs remain tamperable |

Self-protection belongs to the sandbox profile, not to Bash policy.
[`conf/nono-profile.example.json`](../conf/nono-profile.example.json) documents
the intended grants, and `bash-gate selfcheck` probes their effective syscall
behavior from inside the sandbox.

## Parser choice

Shell source cannot be split safely with strings or regular expressions:
operators inside quotes differ from actual pipelines, and nested substitutions
hide executable commands. `mvdan.cc/sh/v3/syntax` provides a current Bash AST,
handles constructs such as `[[ ]]` and arithmetic syntax, and avoids the runtime
and bridge overhead of Node, Python, or WASM alternatives. Direct AST traversal
also keeps extraction explicit and testable.

## Attestation boundary

Project policy is workspace-writable and can add allows, so deny precedence
alone is insufficient. Attestation records the normalized contents reviewed by
an operator. On every sandboxed load, the gate verifies tier hashes, the global
trusted-`PATH` snapshot, and that the record store cannot be overwritten,
extended, or replaced through its parent. Unknown record schema versions fail
closed.

In hook mode, the installation root comes from the password database rather
than `$HOME` or `$BASH_GATE_DIR`, preventing an agent-controlled environment
from selecting forged rules. Operator commands continue to honor those
variables. `dangerously-none` skips attestation because an unconfined process
that can edit both policy and records makes verification meaningless.

## Residual risks

- Allowlisting an interpreter such as `bash`, `python`, or `node` can grant
  arbitrary execution through its evaluation flags. This is a policy error, not
  something argv matching can repair.
- `HAS` blocks only listed flags. Dangerous alternatives remain possible until
  policy authors enumerate them.
- Matching occurs before glob, brace, and tilde expansion. Runtime-expanded
  arguments may differ from the literal tokens the policy inspected.
- Project attestation detects changes but does not prevent them. It trusts the
  sandbox to keep the attestation store read-only and requires one approval per
  machine for every present project tier.
- `automode` delegates unmatched commands to Claude Code's classifier, so its
  treatment of unknown commands replaces the guaranteed prompt.
- Rejecting assignments and dynamic argv blocks legitimate forms such as
  `FOO=1 make` and `git commit -m "$MSG"`.
- Claude Code hook timeouts fail open because the wrapper cannot handle them.
- Decision logs are writable in the sandbox and are not evidence-grade.
- `GATE_DEBUG` records raw, attacker-controlled input and may expose argv
  secrets.

## Environment-specific policy

The binary contains no trusted directories. Starter rules must reflect the live
machine: user-writable directories early in `PATH` can shadow system tools,
world-writable locations must not be trusted, and package-manager shims may
resolve outside their apparent bin directory. Global attestation records the
trusted subset of `PATH` in order so such resolution changes fail closed.

## Validation

`bash-gate validate` is an operator-only linter, separate from hook evaluation.
It reuses the runtime line parser but continues after errors to report all of
them. Its semantic warnings are deliberately conservative: duplicates, ignored
project directives, absent trust directories, rules for unconditional-deny or
safe-builtin commands, and allow rules provably covered by a deny. Uncertain
shadowing is not reported.

## Recovery

Because the sandbox cannot repair read-only policy and a load error denies all
commands, recovery occurs outside it: remove the `PreToolUse` registration or
replace the global policy through trusted operator access.
