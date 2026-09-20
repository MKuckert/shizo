## Plan: `ask` rule follow-ups

The `ask` rule action ships: parsing (`cfg.askRules`), three-pass evaluation
(deny → ask → allow → unmatched ask), the automode gate that defers only
unmatched fallthrough, attestation, validation, docs and tests. Two items
from the original draft remain.

### 1. Semantic warning: `ask` shadowed by `deny`

`lintShadowedAllows` (`validate.go`) only pairs `cfg.allowRules` against
`cfg.denyRules`. An `ask` rule fully covered by a deny is equally dead.

- Generalize the loop to iterate over `allowRules` and `askRules`, reusing
  `denyShadowsAllow` (matching is action-agnostic) and wording the finding
  with the rule's own action: `"%s rule is fully shadowed by deny rule %q
  (deny always wins): %s"`. Rename the helper to `lintShadowedRules`.
- `validate_test.go`: add a case for an `ask` line shadowed by a broader
  `deny`; keep the existing allow case green.

### 2. `Outcome` struct refactor

`Outcome` is a string plus a side-channel `askExplicit bool`, threaded through
`decide()`'s four-value return and the `setDeny`/`setAsk` closures. Collapse
that into one value.

- `policy.go`: `type Outcome struct { Kind OutcomeKind; Reason string;
  Explicit bool }` with `OutcomeKind` ordered strongest to weakest —
  `Deny > Ask > Undecided > Allow` — where `Undecided` is today's unmatched
  fallthrough and `Ask` is an explicit ask rule. Drop `outAllow`/`outDeny`/
  `outAsk` and the `askExplicit` flag.
- Replace `setDeny`/`setAsk` with `escalate(current, candidate Outcome)
  Outcome` keeping the stronger kind; this preserves "one explicit ask in a
  pipeline prevents automode defer" without the special-case branch.
- `decide()` returns `(Outcome, []cmdLog)`; `decideCommand` takes an
  `*Outcome` instead of the two closures.
- `cmdLog.Decision` and `emitDecision`/`logDecision` use
  `Outcome.Kind.String()`; `Undecided` serializes as `"ask"` on the wire.
- `main.go` automode gate: `Undecided` → `defer`, everything else emits.
- `policy_test.go` / `main_test.go`: update call sites; assert on `Kind`
  rather than the string, and cover `Undecided` vs `Ask` under automode.
- `docs/EVALUATION.md` aggregation section: four levels instead of three.

Skipped: cross-tier precedence (project-allow-overrides-global-ask), add when
the simple strong-to-weak ordering proves insufficient.
