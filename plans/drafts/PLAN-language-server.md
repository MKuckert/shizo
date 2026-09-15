# `bash-gate lsp` — diagnostics-only language server

## Problem

Editing rule files requires a write-then-`bash-gate validate` loop. Seven
major editors support LSP; a diagnostics-only server gives live feedback in
all of them with zero per-editor glue.

## Why now (was rejected as option C)

The original rejection assumed per-editor glue (option B) was simpler. But B
requires shipping and maintaining configs for efm-langserver, null-ls/none-ls,
ALE, VS Code problem matchers — one per editor, each with its own format. A
stdio LSP is one binary that works everywhere. The research on editor support
confirmed: VS Code, JetBrains, Sublime (via LSP package), Vim (via vim-lsp /
coc.nvim), Neovim (built-in), Zed, and Helix all speak LSP natively or via a
first-party package.

## Shape

One new file: `src/lsp.go`. Uses `go.lsp.dev/protocol` (the community-standard
Go LSP package, generated from the spec) for JSON-RPC 2.0 framing and LSP
types. Adds a second external dependency — justified because the LSP wire
format is fiddly to get right (Content-Length headers, UTF-16 offsets, shutdown
sequencing) and the library makes future capabilities (completion, hover) a
function addition, not a protocol exercise.

### LSP methods handled

| Method | Action |
|---|---|
| `initialize` | Return server capabilities (diagnostics only) |
| `initialized` | No-op |
| `textDocument/didOpen` | Run `validateFile`, push diagnostics |
| `textDocument/didSave` | Run `validateFile`, push diagnostics |
| `textDocument/didChange` | Run `validateFile` on full content (if `syncKind=Full`) |
| `textDocument/didClose` | Clear diagnostics |
| `shutdown` | Acknowledge |
| `exit` | `os.Exit(0)` |

Everything else: silently ignored (no error for unknown methods — LSP spec
allows this for notifications).

### Diagnostic mapping

`validateFile` already returns `[]finding{file, line, level, msg}`. Mapping:

```
finding.level == levelError → DiagnosticSeverity 1 (Error)
finding.level == levelWarn  → DiagnosticSeverity 2 (Warning)
finding.line                → LSP line (0-indexed, so line-1)
```

### Tier detection

The server needs to know whether a file is global or project tier (affects
which directives are valid). Heuristic: if the file path ends in
`.bash-gate`, it's a project tier; if it matches `global/rules`, it's
global. Configurable via an initialization option if the heuristic proves
insufficient.

## CLI

```
bash-gate lsp          # starts stdio LSP server; no flags needed
bash-gate lsp --help   # usage
```

Editors configure it as:

```json
{ "command": "bash-gate", "args": ["lsp"] }
```

## Changes

| File | What |
|---|---|
| `src/lsp.go` | JSON-RPC 2.0 reader/writer, LSP message types, `runLSP()` |
| `src/lsp_test.go` | Table-driven: feed JSON-RPC messages in, assert diagnostic notifications out |
| `src/main.go` | Add `"lsp"` case to CLI dispatch |
| `conf/editor/` | Example LSP configs for VS Code, Neovim, Zed (3 one-liners) |
| `docs/ARCHITECTURE.md` | Note the LSP subcommand |

## Not included

- Completion, hover, go-to-definition, code actions — add when someone asks.
- `validate --format=json` — the LSP subsumes the use case; cut if no other
  consumer needs it.
- Automatic file watching / workspace indexing — editor pushes content, server
  validates it. Stateless per document.

## Open

- Should `didChange` revalidate on every keystroke (noisy) or debounce?
  Simplest: only validate on `didOpen` + `didSave`, skip `didChange`. Editors
  already debounce saves. Add `didChange` later if latency bothers anyone.
