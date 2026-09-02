# CLAUDE.md

## What this is

`bash-gate` is a Claude Code `PreToolUse` hook (matcher `Bash`) that allowlists
Bash commands at **subcommand granularity**. It parses each command with
`mvdan.cc/sh/v3/syntax`, splits pipes and `&&`/`||`/`;` chains, resolves every
command against the inherited `PATH` plus a configured trusted-dir set, and
matches argv tokens against a line-oriented rule DSL.

**Fail-closed is the core invariant:** anything that cannot be statically
verified is denied. When in doubt, deny — never widen behavior to make a case
"work." A deny always wins over an allow; an unmatched command yields `ask`.

`PLAN.md` is the authoritative design doc. Update `PLAN.md` when you change the design.

## Build, test, install

```sh
make test         # cd src && go test ./...  — run before every commit
make build        # -> dist/bash-gate
make install      # installs to $HOME/.claude-bash-rules (never clobbers rules)
make install PREFIX=/tmp/x   # install elsewhere for verification
```

Lint rule files before installing them:

```sh
dist/bash-gate validate                 # resolved global + project (cwd) tiers
dist/bash-gate validate FILE...         # explicit files, validated as a global tier
dist/bash-gate validate --project FILE  # validate FILE as a project tier
```

`validate` is operator-only — it never touches the hook pipeline. It reports every
syntax error (not just the first, unlike the fail-closed loader) plus semantic
warnings (dead rules, missing trust dirs, shadowed allows, ignored project-tier
directives). Exit 0 = no errors (warnings allowed), 1 = errors, 2 = usage.

- Go 1.27, single external dep (`mvdan.cc/sh/v3`). Unix-only in practice.
- Tests are table-driven `_test.go` files next to each source file. Add cases
  there; every behavior change needs a test.

## Layout

Everything ships from `src/` (Go) + `src/gate.sh` (bash wrapper).

| File            | Responsibility                                             |
| --------------- | ---------------------------------------------------------- |
| `main.go`       | hook I/O: read JSON stdin, emit decision JSON, logging     |
| `policy.go`     | `decide()` — the §2 evaluation pipeline, aggregates outcomes |
| `rules.go`      | load/parse global + project rule tiers into `config`       |
| `match.go`      | `matchRule()` — pure argv-vs-rule token matching (`ANY`/`HAS`) |
| `extract.go`    | argv extraction from the parsed AST; `<subst>`/`<var>` tokens |
| `resolve.go`    | command resolution: safe builtins, uncond-deny, PATH lookup |
| `validate.go`   | `bash-gate validate` — operator-only rule linter (all errors + semantic warnings) |
| `gate.sh`       | wrapper installed as the hook `command`; fails closed      |
| `conf/`         | starter global rules + example Claude settings             |

## Rules DSL (see `PLAN.md §4`)

- Directives (`returntype`, `trust`, `sandbox`) are **global-tier only** and
  must precede any `allow`/`deny`. In a project file they are ignored and logged.
- Project tier can never **widen a global `deny`**. It may add `deny` rules, and its `allow`
  rules can turn an otherwise-unmatched (`ask`) command into `allow` — but a global `deny`
  always wins. Loosening directives (`trust`/`sandbox`/`returntype`) are global-only.
- `allow git status ANY` — `ANY` matches 0-n tokens (max one per rule).
- `deny git reset HAS --hard` — `HAS` matches one flag at any position.
- A literal argument spelled `ANY`/`HAS` must be single-quoted.
- `sandbox nono` (default) denies unless `NONO_CAP_FILE` is set;
  `sandbox dangerously-none` removes that gate — trusted environments only.

## Conventions & guardrails

- Match the existing terse comment style; comments cite `PLAN.md` sections.
- Keep matching/extraction functions **pure** (no I/O) — that is what makes them
  testable. I/O lives in `main.go`, `rules.go`, `resolve.go`.
- Never log or echo secrets. `GATE_DEBUG` mirrors raw hook JSON (attacker-
  controlled command text, argv secrets) to `logs/stdin.log` — keep it unset by
  default and never commit that file.
- `dist/` is gitignored; don't commit build artifacts.
- Don't loosen the uncond-deny set (`eval`, `exec`, `source`, `xargs`, `env`,
  `sudo`, …) or the safe-builtin allowlist without a corresponding `PLAN.md`
  update and tests.
- Prefer builtin tools `Read`, `Edit`, `Grep`, `Glob` over `Bash` calls.
- You're running inside a `nono` sandbox with restricted file access. Don't assume you can read/write arbitrary files or  directories.
