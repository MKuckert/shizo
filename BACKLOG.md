# Backlog

## CLI surface & adoption

- [**info-command**](plans/drafts/PLAN-info-subcommand.md)
  > Add `shizo info` command to retrieve information like `--global-rules-path` or `--attestation-dir`.

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
- harden sandbox template (git read-only)
- make bash builtin allowlist configurable?
