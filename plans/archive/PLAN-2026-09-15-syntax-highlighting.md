# Syntax highlighting for rule files

## Problem

Rule files (`global/rules`, `.bash-gate`) and attestations are plain
text with no editor highlighting. Directives, allow/deny, `ANY`/`HAS`, comments
— all render as unformatted text.

## Shape

A TextMate grammar (`.tmLanguage.json`) is the smallest artifact that gives
highlighting in VS Code, Sublime, and any TextMate-compatible editor. A
tree-sitter grammar is heavier — save it for if/when a language server lands.

## Changes

- New `conf/editor/bash-gate.tmLanguage.json`: scope mappings for comments
  (`#`), directives (`sandbox`, `trust`, `returntype`, `project-trust`),
  actions (`allow`, `deny`), wildcards (`ANY`, `HAS`), and single-quoted
  literals.
- New `conf/editor/bash-gate.tmLanguage.json` language config: file
  associations (`**/global/rules`, `**/.bash-gate`), comment toggling.
- `README.md`: one-line "Editor support" note pointing at `conf/editor/`.

## Not included

- VS Code extension packaging — the grammar works via "Install from VSIX" or
  a manual copy to `~/.vscode/extensions/`. Package later if demand appears.
- tree-sitter grammar, LSP, or completion (see `PLAN-language-server.md`).
- Vim/Neovim ftdetect — add when someone asks.
