## Plan: `ask` rule action

An `ask` rule forces operator prompting even when automode would defer.
Evaluation order becomes deny → ask → allow → undecided. Both tiers may
declare `ask` rules; the strong-to-weak ordering means a project `ask`
tightens a global `allow` and a global `deny` still beats everything.

### Outcome type refactor

1. In `policy.go`, replace the `Outcome` string type with a struct:
   `type Outcome struct { Kind OutcomeKind; Rule rule; Reason string }`
   where `OutcomeKind` is `Deny | Ask | Undecided | Allow` (strongest to
   weakest). Remove `outAllow`, `outDeny`, `outAsk` string constants.
2. Replace `setDeny`/`setAsk` with a single `escalate(current, candidate
   Outcome) Outcome` that keeps whichever is stronger. Aggregation across
   pipeline stages follows four levels: deny > ask > undecided > allow.
   This ensures one explicit `ask` in a pipeline prevents automode defer.
3. Simplify `decide()` return to `(Outcome, []cmdLog)` — the reason now
   lives inside `Outcome.Reason`.
4. Update `cmdLog.Decision` population to use `Outcome.Kind.String()`.
5. In `main.go` automode gate (~line 166): `Undecided` → emit `defer`;
   `Ask` → emit `ask`; `Allow` and `Deny` unchanged in behavior.

### `ask` rule parsing

6. In `config` struct: add `askRules []rule`.
7. In `rules.go` `parseLine`: add `case "ask"` next to `allow`/`deny`,
   calling `buildRule("ask", ...)` and appending to `cfg.askRules`.

### Evaluation order

8. In `decideCommand` (`policy.go`), change step 11 to three passes:
   `matchAny(deny)` → `matchAny(ask)` → `matchAny(allow)` → undecided.
   An ask-rule match returns `Outcome{Kind: Ask, Rule: r}`.

### Validation & attestation

9. In `validate.go` `validateTier`: accept `ask` as a rule action.
   Add semantic warning for `ask` rules shadowed by a broader `deny`.
10. In `attest_verify.go` `normalizeTier`: include `ask` rules in
    canonical serialization (sort alongside allow/deny).

### Docs

11. `docs/RULES.md`: add `ask` to syntax, precedence, and example sections.
12. `docs/EVALUATION.md`: update step 11 to three-pass order; update
    aggregation to show four levels (deny > ask > undecided > allow) and
    describe automode behavior for `ask` vs `undecided`.
13. `docs/ARCHITECTURE.md`: note the `ask` action and outcome type refactor.

### Tests

14. `policy_test.go`: ask rule matches → `Ask` outcome; ask beats allow;
    deny beats ask; aggregation across pipeline: ask + undecided → ask;
    ask + allow → ask; deny + ask → deny.
15. `rules_test.go`: parse `ask` rules in both tiers; verify `askRules`
    populated.
16. `match_test.go`: no changes (matching is action-agnostic).
17. `main_test.go`: end-to-end hook JSON — automode + ask-rule → `ask`
    (not defer); automode + undecided → `defer`; decision mode + ask-rule
    → `ask`.
18. `validate_test.go`: accept `ask` lines; warn on ask shadowed by deny.

Skipped: cross-tier precedence (project-allow-overrides-global-ask), add when
the simple strong-to-weak ordering proves insufficient.
