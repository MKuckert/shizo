# The bash-gate Rules DSL

The rules DSL tells `bash-gate` which Bash commands to `allow`, `deny`, or leave
to `ask`. It is **line-oriented and inert** — never executed, only matched. Each
line is tokenised with mvdan's shell word-splitter, so quoting in a rule behaves
exactly like quoting on a command line.

This document is the reference for authoring rules. For the full evaluation
pipeline see [`EVALUATION.md`](EVALUATION.md); the behaviour here is implemented
in `src/rules.go` (loading/validation), `src/match.go` (matching), and
`src/resolve.go` (command resolution).

---

## 1. Tiers and file locations

Rules live in two tiers. Both are plain text files named `rules` / `.claude-bash-rules`.

| Tier        | Location                                                                            | Required | Directives honoured |
| ----------- | ----------------------------------------------------------------------------------- | -------- | ------------------- |
| **Global**  | `$CLAUDE_BASH_RULES_DIR/global/rules`, else `$HOME/.claude-bash-rules/global/rules` | **yes**  | yes                 |
| **Project** | `<cwd>/.claude-bash-rules` (exact cwd, no ancestor walk)                            | no       | **no**              |

- **Global tier is required.** If it is missing or unreadable, or any line is
  malformed, **every command is denied**.
- **Project tier is optional.** If absent, only the global tier applies. If
  present but unreadable or malformed, **every command is denied**.
- **Project-tier directives are ignored.** Its `trust`, `sandbox`, and
  `returntype` lines are **ignored and logged** (they can loosen policy, so they
  are global-only).
- **Project-tier rules apply in both directions, within one hard limit.** A
  project `deny` tightens policy. A project `allow` also takes effect — it can
  turn an otherwise-unmatched (`ask`) command into `allow`, bypassing the prompt.
  The one thing a project rule can **never** do is override a global `deny`: deny
  always wins (§4). So the project tier can add allows and denies, but it can
  never widen a decision the global tier has already made to `deny`.

---

## 2. Line syntax

```
# comment — only when '#' is the first non-whitespace character
                          # blank lines are ignored

<directive> <args...>     # trust / sandbox / returntype  (global tier only)
allow <command> [tokens]  # allow rule
deny  <command> [tokens]  # deny rule
```

- **Comments**: a line is a comment only when its first non-whitespace character
  is `#`. **Trailing comments are not supported** — a `#` mid-line is an ordinary
  token.
- **Blank lines** are ignored.
- **Any malformed line, in either tier, denies everything**, quoting the
  offending line in the reason. There is no "skip the bad line" behaviour.
- **Directives must precede all `allow`/`deny` rules.** In the **global tier**, a
  directive after the first rule is malformed → deny-all. (In the project tier
  directives are ignored regardless of position, so ordering there is moot.)

---

## 3. Directives (global tier only)

All three loosen policy, so they are honoured only in the global tier; in a
project file they are ignored and logged. Each must appear **before** any rule.

### `trust <absolute-dir>`

Adds one **absolute** directory to the trusted set. A resolved command is only
allowed to run if its containing directory is trusted (see §6). `trust` does
**not** change `PATH` search order — lookup always uses the inherited `PATH`;
`trust` only gates the result. Repeatable.

```
trust /opt/homebrew/bin
trust /opt/homebrew/opt/coreutils/libexec/gnubin
trust /usr/bin
trust /bin
```

Malformed if the argument is missing, not a single token, or not an absolute
path → deny-all.

### `sandbox <nono | dangerously-none>`

Selects the sandbox gate. At most once; absent ⇒ `nono`.

- **`nono`** (default) — denies **everything** unless the `NONO_CAP_FILE`
  environment variable is set. This is the safe default for the nono sandbox.
- **`dangerously-none`** — removes that gate. Trusted environments only.

Any other value → deny-all.

### `returntype <decision | automode>`

Selects the output contract. At most once; absent ⇒ `decision`.

- **`decision`** (default) — the hook decides every case and emits `allow`,
  `deny`, or `ask`.
- **`automode`** — the hook emits **only** denials; `allow` and unmatched
  (`ask`) outcomes defer to Claude Code's own auto-mode classifier. The
  allowlist becomes a pure pre-filter that strips known-bad. Note: under
  `automode`, `allow` no longer guarantees a prompt bypass and unmatched
  commands no longer hit a guaranteed `ask`.

What each aggregated outcome produces:

| Aggregated outcome  | `decision` (default) | `automode`                      |
| ------------------- | -------------------- | ------------------------------- |
| deny-rule match     | `deny` object        | `deny` object                   |
| allow-rule match    | `allow` object       | **defer** — no decision emitted |
| neither (unmatched) | `ask` object         | **defer** — no decision emitted |

The wire-level details (the `defer` sentinel, exit codes) are in
[`CLAUDE_CODE_HOOKS.md`](CLAUDE_CODE_HOOKS.md#output).

Any other value → deny-all.

---

## 4. `allow` / `deny` rules

```
allow <command> [arg ...] [ANY | HAS <flag>]
deny  <command> [arg ...] [ANY | HAS <flag>]
```

A rule is `<action> <token...>` where tokens are matched against a command's
argv (its resolved words: program name plus arguments). Matching is at
**subcommand granularity** — `git status` and `git push` are distinct.

**Precedence:** all `deny` rules are checked first (global, then project); a deny
match denies. Then `allow` rules (both tiers); an allow match allows. If neither
matches, the outcome is **`ask`**. **A deny always beats an allow.**

Matching is **case-sensitive**.

---

## 5. Matching semantics

A rule matches when its tokens equal the command's argv **token-for-token**,
after expanding the one wildcard keyword if present.

### Exact (no keyword)

Tokens must equal argv exactly, same length.

```
allow ls          # matches bare `ls` only — NOT `ls -la`
allow git status  # matches `git status` only
```

### `ANY` — 0-to-n tokens

`ANY` matches zero or more tokens. **At most one `ANY` per rule**, at any
position. The matcher splits the pattern at `ANY`, matches the head forward and
the tail backward (linear, no backtracking).

```
allow git status ANY        # `git status`, `git status -s`, `git status --short --branch`
allow ls ANY                # `ls`, `ls -la`, `ls -la /tmp`
allow ddev ANY phpunit      # `ddev`  <anything>  `phpunit`  (head=ddev, tail=phpunit)
```

Because `ANY` matches zero tokens, `allow git status ANY` also matches bare
`git status`.

### `HAS <flag>` — one flag, any position

`HAS` matches when `<flag>` appears anywhere among the argv tokens **after** the
literal prefix. Used for denylisting dangerous flags. **At most one `HAS` per
rule**, it needs at least one command token before it, and it must be followed by
**exactly one** literal flag token.

`HAS` matching applies two normalisations:

- **`=`-splitting**: `--output=/tmp/x` tests as `--output`, so `HAS --output`
  matches it.
- **Short-flag bundle matching**: a two-char short flag like `-f` is found inside
  a bundle, so `HAS -f` matches `-rf`. (Only applies when the rule flag is a
  single-dash, two-character flag.)

```
deny git reset HAS --hard   # `git reset --hard`, `git reset --hard HEAD~1`
deny git HAS --ext-diff     # `git --ext-diff ...` anywhere after `git`
deny git HAS -c             # blocks `git -c core.pager=... status`
deny find HAS -exec         # `find . -exec rm {} ;`
deny ssh HAS -o             # `ssh -o ProxyCommand=evil host`
```

### Keyword rules

- **`ANY` and `HAS` may not appear in the same rule.**
- At most **one** `ANY` and at most **one** `HAS` per rule.
- To match a **literal** argument spelled `ANY` or `HAS`, single-quote it:
  `allow foo 'ANY'` matches the argument `ANY`, not the wildcard.

---

## 6. Command resolution and trust

Before a rule is matched, `bash-gate` resolves the command. The checks run in
this order (`src/policy.go decideCommand`, then `src/resolve.go resolveCommand`):

1. **Unconditional deny** — these execution-hiding names are denied first, before
   any resolution and regardless of any `allow` rule; they cannot be re-allowed:

   ```
   eval  exec  source  .  command  builtin  enable  trap
   xargs env  sudo    su  doas     nohup    setsid
   ```

2. **Safe builtins short-circuit to allow** — no path resolution, no rule
   required. The nine safe builtins are:

   ```
   cd  echo  printf  pwd  true  false  test  [  :
   ```

   Everything else (`export`, `set`, `shopt`, `alias`, `kill`, `history`, …) is
   not a safe builtin and must resolve to a trusted binary.

3. **PATH lookup** — the resolution depends on the command name:

   - A **bare name** is searched against the **inherited `PATH`**, first match
     wins. Empty `PATH` entries are skipped (unlike bash, which treats an empty
     entry as the current directory — a deliberate hardening).
   - A name **containing `/`** is used as-is if absolute (`/usr/bin/git`), or
     resolved relative to the hook's `cwd` if relative (`./x`, `bin/tool`).

   In both cases the resolved file must be executable, and its **containing
   directory must be in the trusted set** (§3 `trust`). No `realpath`, no symlink
   following. Not found, or resolved outside trust → **deny** (naming the path).

So a rule like `allow git status ANY` only takes effect if `git` resolves to a
binary in a `trust`ed directory.

---

## 7. What is denied before rules are ever consulted

These are structural denials (see [`EVALUATION.md`](EVALUATION.md)); no `allow`
rule can rescue them:

- **Parameter/arithmetic expansion in argv** — `git $g`, `$EDITOR foo`,
  `echo $((1+2))` as a command word. Not knowable at parse time.
- **Assignment prefixes and bare assignments** — `a=1 git status`,
  `GIT_SSH_COMMAND=x git fetch`, `g=push`. These are arbitrary execution vectors
  and are unreachable by `HAS`.
- **Function declarations anywhere** — a function could shadow a trusted name.
- **Write redirects that escape containment** — write targets must resolve within
  `cwd`, `$TMPDIR`, or `/tmp` (`/dev/null`, `/dev/stdout`, `/dev/stderr` also
  pass). If any command in the list is `cd`, all write redirects are denied. A
  non-literal (`$VAR`, `$(...)`) redirect target is denied.
- Command substitutions (`$(...)`, `<(...)`) are **not** denied outright — the
  outer command sees an opaque `<subst>` token, and the inner commands are
  extracted and checked independently.

---

## 8. Worked example — a complete global tier

```
# $HOME/.claude-bash-rules/global/rules

# --- directives (must come first) ---
returntype decision

trust /opt/homebrew/bin
trust /opt/homebrew/opt/coreutils/libexec/gnubin
trust /usr/bin
trust /bin

# --- allow rules ---
allow git status ANY
allow git log ANY
allow git diff ANY
allow ls ANY
allow ddev ANY phpunit

# --- deny rules (win over any allow) ---
deny git push ANY
deny git reset HAS --hard
deny git HAS --ext-diff
deny git HAS -c
deny find HAS -exec
deny ssh HAS -o
```

Given the rules above:

| Command                       | Outcome | Why                                           |
| ----------------------------- | ------- | --------------------------------------------- |
| `git status`                  | allow   | `allow git status ANY` (ANY matches 0 tokens) |
| `git status -sb`              | allow   | `allow git status ANY`                        |
| `git log --oneline`           | allow   | `allow git log ANY`                           |
| `git push origin main`        | deny    | `deny git push ANY`                           |
| `git reset --hard HEAD~1`     | deny    | `deny git reset HAS --hard`                   |
| `git -c core.pager=sh status` | deny    | `deny git HAS -c` (deny beats the allow)      |
| `ls -la /tmp`                 | allow   | `allow ls ANY`                                |
| `find . -exec rm {} ;`        | deny    | `deny find HAS -exec`                         |
| `git commit -m "wip"`         | ask     | no rule matches → `ask`                       |
| `a=1 git status`              | deny    | assignment prefix (structural, §7)            |
| `git $g`                      | deny    | parameter expansion in argv (§7)              |

---

## 9. Project tier example

```
# <repo>/.claude-bash-rules

# These are IGNORED here (global-only) and logged:
#   trust /some/dir
#   sandbox dangerously-none
#   returntype automode

# These take effect — additional denies for this repo:
deny composer ANY
deny git commit ANY
```

The project tier's `deny` rules join the global denies; its `allow` rules join
the global allows (and can widen `ask`→`allow`, per §1) but can never override a
global `deny`.
