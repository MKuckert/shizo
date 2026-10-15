# Backlog

## CLI surface & adoption

- [**project-trust**](plans/drafts/PLAN-project-trust.md)
  > Allow `trust` in project config to add project-specific binaries

- [**docs-command**](plans/drafts/PLAN-docs-subcommand.md)
  > `shizo docs` subcommand. Embedded doc viewer

## Reach

- **Find alpha tester**
  > Gated by live bypasses, coverage gaps and the install hook command.

- **Expand to other harnesses**
  > See [pretooluse-hook-equivalents research](docs/research/pretooluse-hook-equivalents.md)
  - Depends on the harness-agnostic command naming.

- [**language server**](plans/drafts/PLAN-language-server.md)


## Unordered inbox

- pro: no variables, no functions
- scan all binaries and classify
- add `default ask|allow|deny`?
- `selfcheck`: optionally probe that the workspace's execution-controlling paths
  (`.git/config`, `.git/hooks`, `.claude/settings.json`, `.shizo`) are not
  writable from inside the sandbox — warn, not fail, as layouts vary
  (see README#example-sandbox-profile)
- make bash builtin allowlist configurable?
