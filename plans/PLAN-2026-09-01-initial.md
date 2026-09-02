# Make the `sandbox` directive required

## Context

Previously the global tier's `sandbox <nono | dangerously-none>` directive was
**optional** — when absent it silently defaulted to `nono`. That default was
convenient but subtly unsafe: an operator who forgot the directive got a policy
that *looked* configured but was never explicitly declared, and the sandbox
posture of a rules file could only be known by reasoning about absence.

This change makes `sandbox` **required in the global tier**. A global rules file
with no `sandbox` line now **denies everything** (fail-closed, consistent with
the core invariant), and `bash-gate validate` reports it as an error. `nono` and
`dangerously-none` must be spelled out. The project tier is unchanged (its
`sandbox` line is still ignored + logged).

No behavioral change for any file that already declares `sandbox` (e.g. the
shipped `conf/global-rules.starter`, which already has `sandbox nono`).

## Changes

### 1. `src/rules.go` — enforce presence, drop the default

- **`config.sandboxMode`**: required in the global tier, no default.
- **`loadConfig`**: initialize `cfg := &config{returnMode: "decision"}` — no
  `sandboxMode` seed, so absence cannot masquerade as a valid value. Immediately
  after the global-tier `parseTier` succeeds (before the project tier loads),
  deny-all when `cfg.sandboxMode == ""`:
  ```go
  if cfg.sandboxMode == "" {
      return denyAllConfig("global tier must declare a sandbox directive (nono or dangerously-none)")
  }
  ```
  The requirement is on the global tier only — matching the "directives are
  global-only" model.
- **`denyAllConfig`**: no `sandboxMode` field (irrelevant on a deny-all config;
  `decide` short-circuits on `denyAll` first).
- The `case "sandbox"` parsing block is **unchanged** — value validation,
  duplicate guard, project-tier ignore, and directive-after-rule all still apply.

### 2. `src/policy.go` — keep the gate fail-closed

- Sandbox gate: condition is `cfg.sandboxMode != "dangerously-none" && !env.sandboxed`.
  Functionally identical for the two valid values, but any empty/unknown mode
  denies rather than falling through — defense in depth aligned with fail-closed.

### 3. `src/validate.go` — report absent sandbox as an error

- `validateFile` initializes `cfg := &config{returnMode: "decision"}` (no seed).
- After the per-line parse loop, for the **global tier only**, an error finding
  is added when the directive was never seen (`!project && !st.sawSandbox`), as a
  whole-file finding (line 0). This mirrors the loader so `validate` and the hook
  agree.

### 4. Tests — every global-tier fixture declares `sandbox`

Any global-tier rules body without a `sandbox` line now denies-all / errors, so
existing behavior fixtures gain a `sandbox nono` line:

- **`src/rules_test.go`**: `validGlobal` const gains `sandbox nono`; `TestSandbox`
  absent case inverts to expect `denyAll`; `TestReturntype` positive fixtures gain
  `sandbox nono`.
- **`src/policy_test.go`**: `policyFixture` rules gain `sandbox nono`;
  `TestProjectTighteningApplies` global body gains `sandbox nono`; the
  `TestPolicySandboxGate` "default" wording becomes "explicit `sandbox nono`".
- **`src/main_test.go`**: `setupRun` prepends `sandbox nono`.
- **`src/validate_test.go`**: global-tier bodies gain `sandbox nono` (asserted line
  numbers bumped by one where the prepend shifts them); a focused test covers a
  global body with no `sandbox` line yielding a `levelError` and `loadConfig`
  denying-all.

### 5. Docs & starter config — "required", not "default"

- **`docs/RULES.md`** §3 `sandbox`: "**Required.** Must appear exactly once,
  before any rule. A global tier with no `sandbox` line denies everything." §8
  worked-example global tier gains a `sandbox nono` line.
- **`CLAUDE.md`**: `sandbox` is required (no default); `nono` gates on
  `NONO_CAP_FILE`, `dangerously-none` drops the gate.
- **`conf/global-rules.starter`**: comment no longer calls `nono` the "default".

## Verification

1. `make test` — all `src/*_test.go` pass.
2. `make build`, then:
   - `printf 'trust /bin\nallow ls ANY\n' > /tmp/nosb` →
     `dist/bash-gate validate /tmp/nosb` exits **1** and reports
     "must declare a sandbox directive".
   - `printf 'sandbox nono\ntrust /bin\nallow ls ANY\n' > /tmp/withsb` →
     `dist/bash-gate validate /tmp/withsb` exits **0**.
3. Hook smoke test: a global rules file with no `sandbox` line denies-all; adding
   `sandbox nono` restores normal evaluation, `sandbox dangerously-none` runs
   unsandboxed.
