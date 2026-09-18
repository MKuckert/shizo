## Plan: Rename `bash-gate` to `shizo`

Full rename — no backward compat, clean break.

### Source code (Go + shell)

1. `src/go.mod`: `module bash-gate` → `module shizo`.
2. `src/gate.sh` → `src/shizo.sh`. Update all internal refs (`GATE_DEBUG` → `SHIZO_DEBUG`, path refs).
3. `src/main.go`: rename binary name, usage strings, `defaultHookRoot()` (`~/.bash-gate` → `~/.shizo`), `projectPath()` (`<cwd>/.bash-gate` → `<cwd>/.shizo`), `logDir()`.
4. `src/main.go` + all `_test.go` files: `BASH_GATE_DIR` → `SHIZO_DIR`, `GATE_DEBUG` → `SHIZO_DEBUG`, `GATE_RETURNTYPE` → `SHIZO_RETURNTYPE`.
5. `src/attest.go`: usage strings, `$BASH_GATE_DIR/attestations/` → `$SHIZO_DIR/attestations/`.
6. `src/selfcheck.go`: usage strings, path refs.
7. `src/rules.go`, `src/validate.go`, `src/test.go`: string refs.
8. All `*_test.go`: update `.bash-gate` in test paths and env setup.

### Build / config

9. `Makefile`: binary name `bash-gate` → `shizo`, install paths `~/.bash-gate` → `~/.shizo`.
10. `.goreleaser.yaml`: `project_name` and `binary` → `shizo`.
11. `conf/claude-settings.json`: hook command path.
12. `conf/global-rules.starter`: refs.
13. `conf/nono-profile.example.json` + `.sandbox/profile.template.json`: all `bash-gate` path refs → `shizo`.

### Editor extension

14. `conf/editor/bash-gate.tmLanguage.json` → `conf/editor/shizo.tmLanguage.json`. Rename language ID, scope names, all internal identifiers.
15. `conf/editor/package.json`: extension name, language ID, file associations, scope.

### Skill / settings

16. `.claude/skills/bash-gate/` → `.claude/skills/shizo/`. Update all refs inside `SKILL.md`.
17. `.claude/settings.json`: hook command path.

### Docs

18. `README.md`, `CLAUDE.md`, `BACKLOG.md`: find-replace `bash-gate` → `shizo`, `BASH_GATE_DIR` → `SHIZO_DIR`, `GATE_DEBUG` → `SHIZO_DEBUG`, etc.
19. `docs/ARCHITECTURE.md`, `docs/RULES.md`, `docs/CLAUDE_CODE_HOOKS.md`, `docs/EVALUATION.md`: same.
20. `docs/research/*.md`: same.
21. `plans/*.md` (non-drafts): same — these are archived but should stay accurate.

### Website

22. `site/templates/layout.html`, `site/content/index.html`: `bash-gate` → `shizo`.

### SSG module

23. `cmd/ssg/go.mod`: update any `bash-gate` ref.

### Tests

24. Run `make test` — fix any breakage from renamed paths/env vars.

### Manual (human)

25. Rename GitHub repo / update remote URLs.

---

Skipped: migration tooling (detect old `~/.bash-gate` and warn) — add if users besides you show up.
