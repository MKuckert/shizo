## Plan: `shizo docs` subcommand — embedded doc viewer

1. Create `src/docs.go`: `//go:embed` all top-level `docs/*.md` via `embed.FS`. Derive slugs automatically from filenames (lowercase, strip `.md`) — no hardcoded map. `runDocs` with no arg lists slugs with the first `# heading` as a one-line description; with a slug arg, writes that doc to stdout. When stdout is a TTY, exec `$PAGER` (fall back to `less`, then raw stdout); when piped, write directly. Exit 2 on unknown slug.
2. In `src/main.go`: add `case "docs"` to `dispatch` routing to `runDocs`; add `docs` line to `rootUsage`.
3. The `go:embed` path is `../docs/*.md` (relative to `src/`), embedding only top-level markdown — no `research/` subfolder.
4. Add table-driven tests in `src/docs_test.go`: list mode returns all slugs, known slug dumps content, unknown slug errors. Pager logic is not tested (requires TTY).
5. Update `docs/ARCHITECTURE.md` to mention the embedded docs capability in one sentence.

Skipped: `research/` inclusion, `make install` changes — add when operators ask for them.
