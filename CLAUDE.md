# CLAUDE.md

## What this is

`shizo` is a Claude Code `PreToolUse` hook (matcher `Bash`) that allowlists
Bash commands at **subcommand granularity**. It parses each command with
`mvdan.cc/sh/v3/syntax`, splits pipes and `&&`/`||`/`;` chains, resolves every
command against the inherited `PATH` plus a configured trusted-dir set, and
matches argv tokens against a line-oriented rule DSL.

**Fail-closed is the core invariant:** anything that cannot be statically
verified is denied. When in doubt, deny — never widen behavior to make a case
"work." A deny always wins over an allow; an unmatched command yields `ask`.

The `docs/` folder is the authoritative design reference — `ARCHITECTURE.md`
(decisions, residual risks), `EVALUATION.md` (the ordered pipeline), `RULES.md`
(the DSL), and `CLAUDE_CODE_HOOKS.md` (the hook I/O contract). Update the
relevant doc when you change the design. The original design plan is archived
under `plans/`.

## Build, test, install

```sh
make test         # cd src && go test ./...  — run before every commit
make build        # -> dist/shizo
make install      # installs to $HOME/.shizo (never clobbers rules)
make install PREFIX=/tmp/x   # install elsewhere for verification
```

`shizo help` prints a subcommand overview; `help <cmd>` (or `<cmd> --help`)
prints that command's usage. `shizo info` prints the paths this install
resolves — one `key<TAB>value` line per entry, or a single bare value with
`--<key>` (`rules=$(shizo info --global-rules-path)`).

Lint rule files before installing them:

```sh
dist/shizo validate                 # resolved global + project (cwd) tiers
dist/shizo validate FILE...         # explicit files, validated as a global tier
dist/shizo validate --project FILE  # validate FILE as a project tier
```

Dry-run a command through the pipeline (operator-only; never logs or emits hook JSON):

```sh
dist/shizo test --assume-sandboxed 'git status'   # command from args or stdin
dist/shizo test --cwd DIR --json 'git push'       # pick a project tier; log-line shape
dist/shizo test --stdin-json < hook.json          # replay a full hook JSON
```

`validate` is operator-only — it never touches the hook pipeline. It reports every
syntax error (not just the first, unlike the fail-closed loader) plus semantic
warnings (dead rules, missing trust dirs, shadowed allows, ignored project-tier
directives). Exit 0 = no errors (warnings allowed), 1 = errors, 2 = usage.

- Go 1.27, single external dep (`mvdan.cc/sh/v3`). Unix-only in practice.
- Tests are table-driven `_test.go` files next to each source file. Add cases
  there; every behavior change needs a test.

## Layout

Everything ships from `src/` (Go) + `src/shizo.sh` (bash wrapper).

| File            | Responsibility                                             |
| --------------- | ---------------------------------------------------------- |
| `main.go`       | CLI dispatch + help; hook I/O: read JSON stdin, emit decision JSON, logging |
| `policy.go`     | `decide()` — the §2 evaluation pipeline, aggregates outcomes |
| `rules.go`      | load/parse global + project rule tiers into `config`; wires the attestation verdict into `loadConfig` |
| `match.go`      | `matchRule()` — pure argv-vs-rule token matching (`ANY`/`HAS`) |
| `extract.go`    | argv extraction from the parsed AST; `<subst>`/`<var>` tokens |
| `resolve.go`    | command resolution: safe builtins, uncond-deny, PATH lookup |
| `validate.go`   | `shizo validate` — operator-only rule linter (all errors + semantic warnings) |
| `info.go`       | `shizo info` — operator-only; prints the install's resolved paths |
| `attest_verify.go` | pure normalize/hash/compare helpers for rule attestation (no I/O) |
| `attest.go`     | `shizo attest` — operator-only; writes/reads `.attest` records |
| `shizo.sh`       | wrapper installed as the hook `command`; fails closed      |
| `conf/`         | starter global rules, example Claude settings, editor grammar, pi extension (`conf/pi/shizo.ts`) |

## Rules DSL (see `docs/RULES.md`)

- Directives (`returntype`, `trust`, `sandbox`) are **global-tier only** and
  must precede any `allow`/`deny`. In a project file they are ignored and logged.
- Project tier can never **widen a global `deny`**. It may add `deny` rules, and its `allow`
  rules can turn an otherwise-unmatched (`ask`) command into `allow` — but a global `deny`
  always wins. Loosening directives (`trust`/`sandbox`/`returntype`) are global-only.
- `allow git status ANY` — `ANY` matches 0-n tokens (max one per rule).
- `deny git reset HAS --hard` — `HAS` matches one flag at any position.
- A literal argument spelled `ANY`/`HAS` must be single-quoted.
- `sandbox` is **required** in the global tier (no default) — a missing directive
  denies everything. `sandbox nono` denies unless `NONO_CAP_FILE` is set;
  `sandbox dangerously-none` removes that gate — trusted environments only.

## Conventions & guardrails

- Documentation reads as one piece, not a changelog. When updating `docs/` or `README.md`,
  integrate new material into the existing narrative — rewrite surrounding
  sentences so the result reads as if it were always written that way. Never
  append "now mitigated" / "three historical vectors" / "as of v…" lists to
  existing prose; a reader who didn't see the diff should not be able to tell
  where old text ends and new text begins.
- Match the existing terse comment style; comments cite the relevant `docs/` section.
- Keep matching/extraction functions **pure** (no I/O) — that is what makes them
  testable. I/O lives in `main.go`, `rules.go`, `resolve.go`.
- Never log or echo secrets. `SHIZO_DEBUG` mirrors raw hook JSON (attacker-
  controlled command text, argv secrets) to `logs/stdin.log` — keep it unset by
  default and never commit that file.
- `dist/` is gitignored; don't commit build artifacts.
- Don't loosen the uncond-deny set (`eval`, `exec`, `source`, `xargs`, `env`,
  `sudo`, …) or the safe-builtin allowlist without a corresponding `docs/ARCHITECTURE.md`
  update and tests.
- Prefer builtin tools `Read`, `Edit`, `Grep`, `Glob` over `Bash` calls.
- You're running inside a `nono` sandbox with restricted file access. Don't assume you can read/write arbitrary files or  directories.
