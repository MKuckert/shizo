## Plan: HAS value-prefix matching for compound flags

`HAS --checkpoint-action=exec` should match any argv token starting with
`--checkpoint-action=exec` (e.g. `--checkpoint-action=exec=CMD`). Currently
`hasFlag` splits at `=` and only compares the base, so the `=exec` part is
discarded and the match fails.

1. In `src/match.go` `hasFlag`: when `flag` contains `=`, compare using
   `strings.HasPrefix(tok, flag)` instead of `base == flag`. The existing
   base-only path stays for flags without `=`.
2. Add test cases in `src/match_test.go` `TestMatchHAS`:
   - `deny tar HAS --checkpoint-action=exec` matches `tar cf x --checkpoint-action=exec=CMD` → true
   - same rule, `tar cf x --checkpoint-action=display=progress` → false
   - same rule, `tar cf x --checkpoint-action=exec` (exact, no trailing `=`) → true
   - `HAS --output=` matches `--output=file` → true (plain value-prefix)
3. Add a fuzz seed in `FuzzMatch` with `--checkpoint-action=exec=CMD`.
4. Update `docs/RULES.md` §HAS paragraph: note that a flag containing `=`
   matches any token starting with that prefix.

Skipped: no new uncond-deny entry for `tar` — too broad; a `HAS` rule in the
operator's tier is the right granularity.
