# automode should return `allow`, only omit `ask`

> TODO: "automode should return `allow`, only omit `ask`"

## Problem

Today automode emits a decision only for denies; both allow-matches and unmatched
(`ask`) defer to Claude Code's classifier. That discards the operator's explicit
allow rules (no guaranteed prompt-bypass — a documented residual risk). automode
should defer only the *fallthrough*, not explicit allows:

| Aggregated       | decision | automode now | want   |
| ---------------- | -------- | ------------ | ------ |
| deny match       | deny     | deny         | deny   |
| allow match      | allow    | defer        | allow  |
| unmatched (ask)  | ask      | defer        | defer  |

`decide()` already returns the aggregated outcome, and `gate.sh` already handles
both `0:{json}` and `0:defer`. So this is a one-site mapping change.

## Options

- **A. Redefine automode (pick).** allow→emit allow, ask→defer. Matches the TODO,
  no wrapper change.
- **B. New mode, keep old.** Two near-identical modes, no defender for the old
  behavior. Rejected.

## Pick: A

## Changes

- `main.go` `run()` non-deny branch:
  ```go
  if cfg.returnMode == "automode" && outcome == outAsk {
      fmt.Fprint(stdout, "defer"); return 0
  }
  emitDecision(stdout, outcome, reason); return 0
  ```
- `main_test.go` `TestRunAutomodeDefer`: allow-match now → exit 0 + allow JSON;
  add an unmatched case → `defer`; deny unchanged.
- Docs (all carry the old table): `RULES.md §3`, `CLAUDE_CODE_HOOKS.md` output,
  `EVALUATION.md` aggregation, `ARCHITECTURE.md` row + residual-risk bullet.
