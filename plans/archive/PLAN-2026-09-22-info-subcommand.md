# Plan: `shizo info` — resolved paths

Operator-only subcommand that prints the paths shizo resolves, so install
scripts and troubleshooting stop hardcoding `~/.shizo/...`.

Output (no args): one `key<TAB>value` line per entry, stable order.
With `--<key>`: that value only, bare, newline-terminated (scriptable:
`rules=$(shizo info --global-rules-path)`). Unknown flag → usage, exit 2.

Keys: `install-root`, `global-rules-path`, `project-rules-path`,
`attestation-dir`, `log-dir`, `engine-dir`, `hook-command`, `version`.

`engine-dir` is `$PREFIX/engine` (today only the Makefile knows it) and
`hook-command` is `engine/shizo.sh` — the string that goes into
`~/.claude/settings.json`, which `shizo install claude-hook` will reuse.

1. Add `src/info.go` with `infoUsage` const and `runInfo(args []string, stdout, stderr io.Writer) int`.
2. In `runInfo`, build an ordered `[]struct{key, val string}` from `installRoot()`, `globalRulesPath()`, `projectRulesPathFor(cwd)` (cwd from `os.Getwd`, honouring a `--cwd DIR` flag like `test` does), `filepath.Join(installRoot(), "attestations")`, `logDir()`, and `version`.
3. Factor the literal `filepath.Join(root, "attestations")` used in `rules.go` and `selfcheck.go` into `attestDirIn(root string) string` in `main.go` next to `globalRulesPath()`, and use it from all three; add `engineDirIn(root string) string` there too, returning `filepath.Join(root, "engine")`.
4. Wire `case "info": return runInfo(args[1:], stdout, stderr)` into `dispatch`, add `case "info": usage = infoUsage` to `runHelp`, and list `info` in `rootUsage`.
5. Add `src/info_test.go`: table test asserting no-arg output contains every key, `--global-rules-path` prints exactly the `globalRulesPath()` value with no key prefix, `--hook-command` matches the path `make install` writes, and an unknown flag exits 2.
6. Mention `shizo info` in the CLI section of `CLAUDE.md` and `README.md`, integrated into the existing prose (no changelog phrasing).

Skipped: `--json` output, add when a wrapper needs more than one value per call.
