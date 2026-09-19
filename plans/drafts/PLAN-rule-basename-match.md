# Plan: basename-normalize the command token in rule matching

## The bug

`decideCommand` (`policy.go`) resolves `argv[0]` to an absolute path via
`resolveCommand`, then throws that away and calls `matchAny(cmd.argv, …)`,
which compares `r.tokens[0].text` to `argv[0]` **as written**. So every rule in
`conf/global-rules.starter` is bypassable by spelling the path:

```
python3 -c "1"            -> deny  (deny python3 ANY)
/usr/bin/python3 -c "1"   -> ask   (no rule matches)
git push                  -> deny  (deny git push ANY)
/usr/bin/git push         -> ask
git reset --hard          -> deny  (deny git reset HAS --hard)
/usr/bin/git reset --hard -> ask
```

`/usr/bin` is a `trust` dir, so resolution succeeds — only matching fails.
Under `returntype automode` that `ask` becomes `defer`, handing the command to
Claude Code's own classifier. `uncondDeny` (`policy.go`) and `lintDeadRules`
(`validate.go:163`) already basename-normalize; only the matcher does not.

## Semantics

The first literal token of a rule is a **command token**:

- **no slash** (`git`, `python3`) — matches `filepath.Base(res.path)`, the
  basename of the *resolved* executable, so every spelling of the same binary
  matches.
- **contains a slash** — must be absolute; matches `res.path` exactly, so an
  operator can still pin one binary.

Applied to `allow`, `deny` (and `ask`, if the stdin-gate plan lands first) —
symmetric, so there is one matching rule to reason about. Widening `allow` to
path spellings is safe: `resolveCommand` already gates which paths resolve at
all via `trust`, and deny is normalized too and still wins.

## Steps

1. In `match.go`, change `matchRule(argv []string, r rule) bool` to
   `matchRule(argv []string, resolved string, r rule) bool` and add a pure
   `matchCmdToken(tok, resolved string) bool` helper implementing the two cases
   above (`filepath.Base` / exact); use it for the index-0 comparison in all
   three branches (`HAS` prefix, `ANY` head, exact).
2. Guard the `ANY`-at-index-0 case (`allow ANY`, head is empty): there is no
   command token, so no canonicalization applies and nothing indexes
   `tokens[0]`.
3. In `match.go`, change `matchAny` to take and forward `resolved`.
4. In `policy.go` `decideCommand`, pass `res.path` into every `matchAny` call
   (deny, then allow). Builtins already return before rule matching, so they
   are unaffected.
5. Keep `uncondDeny` on the raw `filepath.Base(name)`: step 9 runs before
   resolution on purpose, so an unresolvable or untrusted `eval` still dies.
   Add a comment saying so, since the two now look inconsistent.
6. In `rules.go` `buildRule`, reject a first literal token that contains a
   slash but is not absolute ("rule command path must be absolute or a bare
   name"), so a meaningless `./deploy.sh` rule cannot silently never match —
   the exact failure mode this plan fixes.
7. In `validate.go`, make `denyShadowsAllow`/`lintShadowedAllows` compare
   command tokens through the same canonicalization so
   `deny git push` is seen to shadow `allow /usr/bin/git push ANY`.
8. Add `policy_test.go` regression cases asserting both spellings of
   `python3 -c`, `git push`, and `git reset --hard` deny, plus a trusted-dir
   path spelling (`/opt/homebrew/bin/git push`), driven off
   `conf/global-rules.starter` so the shipped posture is what is tested.
9. Add `match_test.go` cases for the pinned form (`deny /usr/bin/python3 ANY`
   must not match a `/opt/homebrew/bin/python3` resolution) and thread the new
   `resolved` column through the existing table.
10. Add a `rules_test.go` case for the relative-path rejection in step 6.
11. Update `docs/RULES.md` (command-token matching: bare name vs. absolute
    pin), `docs/EVALUATION.md` (step 11 matches on the resolved path from step
    10, and why step 9 does not), and `docs/ARCHITECTURE.md` (this bypass is
    closed; symlink aliasing remains open) as integrated prose.
12. Note in the release notes that rule files containing relative command paths
    now fail to load and need editing plus a re-attest; `conf/global-rules.starter`
    needs no change (all bare names).

## Residual risk

Symlink aliasing is still open: `/usr/bin/python` symlinked to `python3` has
basename `python`, so `deny python3 ANY` does not cover it. Closing that needs
`realpath`, which `docs/ARCHITECTURE.md` rules out (TOCTOU, resolution cost).
The starter file mitigates it the only way available — deny every alias by
name. Restate and document this as a known limit rather than implying the
class is fixed.

Skipped: denying safe builtins by rule (`cd`, `echo`, … return before rule
matching and cannot be denied at all); add when a rule needs to constrain one.
