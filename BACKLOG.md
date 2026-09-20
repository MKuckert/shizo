# Backlog

## Coverage gaps

Rule DSL cannot express the threat

- [**ask-rule**](plans/drafts/PLAN-ask-rule.md)
  > `ask` rule rework and refactoring

- [**has-value-prefix**](plans/drafts/PLAN-has-value-prefix.md)
  > Add support to deny `tar --checkpoint-action=exec=CMD` with a rule `deny tar HAS --checkpoint-action=exec` or similar.
  - Stands alone as deny-only; wants `ask` for the softer verdict.

## CLI surface & adoption

- [**rename-hook**](plans/drafts/PLAN-rename-hook-pretooluse.md)
  > Rename `shizo claude-hook` to `shizo hook pretooluse` to make it harness agnostic
  - Must land **before** the install command, or fold both into one change.

- [**install-hook**](plans/drafts/PLAN-install-claude-hook.md)
  > Add `shizo install claude-hook` to add PreToolUse hook. Flags: `--project` for `.claude/settings.json`, `--local` for `.claude/settings.local.json`, `--global` for `~/.claude/settings.json`, defaults to `--project`.

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
