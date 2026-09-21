# Plan: `shizo install claude-hook`

Operator-only subcommand that writes the `PreToolUse`/`Bash` hook entry into a
Claude Code settings file, merging into whatever is already there.

1. Add `src/install.go` with `installUsage` const and `runInstall(args []string, stdout, stderr io.Writer) int`, following the flag-loop + usage style of `runSelfcheck`.
2. Require `claude-hook` as the first positional arg (anything else → usage error, exit 2); parse `--project` (default, `.claude/settings.json`), `--local` (`.claude/settings.local.json`), `--global` (`$HOME/.claude/settings.json`), plus `-h/--help`; mutually exclusive.
3. Resolve the hook command via `os.Executable()` + `filepath.EvalSymlinks`, then `filepath.Join(filepath.Dir(exe), "shizo.sh")`; error out (exit 1) if that file does not exist, so we never write a dangling hook.
4. Read the target file into `map[string]any` (missing file → empty map); on malformed JSON, fail with a message and exit 1 rather than clobbering.
5. Walk `hooks.PreToolUse` as `[]any`, find the entry whose `matcher == "Bash"`, and append `{"type":"command","command":<path>}` to its `hooks` list — creating the missing levels when absent, and returning "already installed" (exit 0) if a `shizo.sh` command is already present there.
6. `os.MkdirAll` the parent dir, marshal with `json.MarshalIndent(v, "", "  ")` + trailing newline, and write via temp-file + `os.Rename` at 0o644.
7. Print `installed hook -> <path>` on success.
8. Wire `install` into `dispatch` and `runHelp` in `src/main.go`, and list it in `rootUsage` next to `selfcheck`.
9. Add `src/install_test.go`: table-driven cases for fresh file, file with unrelated keys preserved, existing Bash matcher appended to, idempotent re-run, non-Bash matcher left alone, and malformed JSON refused — all against `t.TempDir()`.
10. Document the subcommand in `README.md` and `docs/CLAUDE_CODE_HOOKS.md` (integrated into the existing install narrative, replacing the manual copy-`conf/claude-settings.json` instructions).

Skipped: `--uninstall`, `--dry-run`, backup-file creation, and preserving JSON key order/comments (jsonc); add when an operator actually loses a hand-edited settings file.
