# A Language Server for the rules DSL?

> TODO: "Add a Language Server? https://share.gemini.google/oEPdHxfRQHPJ"
> (Link is auth-gated, couldn't retrieve — reasoning from the DSL + `validate`.)

## Idea

In-editor diagnostics/completion/hover while editing `rules` files, instead of
the write-then-`bash-gate validate` loop. The `?` is the real question: worth it
for a line-oriented DSL that already has a batch linter?

## Reality check

- `validateFile` already emits `file:line: level: msg` — exactly what editor
  error-matchers want.
- Project ethos: one external dependency, small surface. A real LSP framework is
  a second major dep for a config format touched rarely.
- The main value (live diagnostics on save) is reachable **today** with zero new
  Go code.

## Options

- **B. Reuse `validate` + editor glue, no LSP (pick).** Optionally add `validate
  --format=json` (reuses `finding`). Ship `conf/editor/` configs
  (efm-langserver / null-ls / ALE / VS Code problem matcher) that run `validate`
  on save, plus filetype detection for `rules`/`.claude-bash-rules`. Optional
  tree-sitter/TextMate grammar for highlighting.
- **C. Diagnostics-only micro-LSP.** Works everywhere without per-editor config,
  but pulls in an LSP lib or hand-rolled JSON-RPC. Only if B's glue proves fiddly.
- **A. Full LSP** (completion/hover/actions + editor extensions). Largest effort,
  adds a dependency. Likely never.

## Pick: B now; C on demand; A likely never

Adding an LSP framework for an allowlist config is over-engineering. Escalate to
C only if real demand for completion/hover (resolvable command names, `trust`
dirs — those depend on live `PATH`, genuinely useful).

## Changes (B)

- Optional `validate --format=json` emitting `[]finding`; document the stable
  text format either way.
- `conf/editor/`: efm/null-ls/ALE/VS Code snippets running `validate`; filetype
  association keyed on path (`**/global/rules`, `**/.claude-bash-rules` — the
  global file has no extension). Optional grammar.
- `README.md`: "Editor integration" section. No new Go dep, no pipeline change.

## Open

- Retrieve the Gemini link before committing — may argue for completion/hover
  (shifts toward C).
- Real demand, or is `validate` in a pre-commit/CI hook enough? If the latter,
  this may be won't-do — document the CI pattern instead.
