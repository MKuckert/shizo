# Plan: Fix attestation security theatre

## Problem

Attestation's trust anchor is "the attestation store is read-only" — but the
runtime never verifies that, and two code paths let an agent sidestep it.

Three attack vectors:

1. **Writable attestation store.** Agent writes a widening rule file, computes
   the SHA-256 hash, writes a matching `.attest` record. `selfcheck` probes
   `engine/` and `global/` but not `attestations/`.
2. **`$CLAUDE_BASH_RULES_DIR` redirect.** `installRoot()` (`main.go:194`)
   trusts this env var unconditionally. Agent points it at a directory with
   pre-computed records — the gate loads attacker-controlled rules.
3. **No sandbox = no enforcement.** Under `sandbox dangerously-none` the agent
   can rewrite rules, records, `gate.sh`, the binary itself. Attestation adds
   latency for zero security value.

## Fixes

### Fix 1: Probe store writability in `verifyAttestation` and `selfcheck`

The probe lives in `verifyAttestation` — every hook invocation checks the
anchor before trusting any record. `selfcheck` gets the same probe for
operator visibility, but the security gate is inline.

**`verifyAttestation` (primary, fail-closed):**

Before reading any `.attest` record, probe `attestations/` writability. Three
checks, all must pass (any failure or ambiguity → deny-all):

1. **Overwrite an existing record.** Attempt to open an existing `.attest`
   file for writing (O_WRONLY, no O_CREAT). Create-only probes miss the
   actual attack (replacing a record with a forged one).
2. **Create in the directory.** Attempt to create a temp file in
   `attestations/`. Catches profiles that block overwrites but allow new
   files.
3. **Parent not renameable.** `stat` the parent of `attestations/` and
   confirm it is not writable by this process (a writable grandparent lets
   the agent `mv attestations/ attestations.bak && mkdir attestations/`).

```go
func probeStoreReadOnly(attestDir string) (ok bool, detail string) {
    // 1. Try overwriting an existing record
    entries, err := os.ReadDir(attestDir)
    if err != nil {
        return false, "cannot read attestations/: " + err.Error()
    }
    for _, e := range entries {
        if filepath.Ext(e.Name()) != ".attest" { continue }
        f, err := os.OpenFile(filepath.Join(attestDir, e.Name()), os.O_WRONLY, 0)
        if err == nil {
            f.Close()
            return false, "attestations/ is writable (overwrite succeeded)"
        }
        break // one probe is enough
    }
    // 2. Try creating a new file
    tmp := filepath.Join(attestDir, ".probe-"+strconv.FormatInt(time.Now().UnixNano(), 36))
    f, err := os.Create(tmp)
    if err == nil {
        f.Close()
        os.Remove(tmp)
        return false, "attestations/ is writable (create succeeded)"
    }
    // 3. Parent must not be writable (rename attack)
    parent := filepath.Dir(attestDir)
    ptmp := filepath.Join(parent, ".probe-"+strconv.FormatInt(time.Now().UnixNano(), 36))
    pf, err := os.Create(ptmp)
    if err == nil {
        pf.Close()
        os.Remove(ptmp)
        return false, "attestations/ parent is writable (rename attack possible)"
    }
    return true, "store is read-only"
}
```

In `verifyAttestation`, before any record read:

```go
if cfg.sandboxMode == "nono" {
    attestDir := filepath.Join(root, "attestations")
    if ok, detail := probeStoreReadOnly(attestDir); !ok {
        return "attestation store is writable in-sandbox: " + detail +
            " — records are forgeable, re-check the sandbox profile"
    }
}
```

Only probe under `sandboxMode == "nono"` — that's the mode that promises a
sandbox. `dangerously-none` skips attestation entirely (fix 2).

**`selfcheck` (operator visibility):**

```go
attestDir := filepath.Join(root, "attestations")
writable, detail = probeDirWrite(attestDir)
out = append(out, checkResult{"attestations/ not writable", !writable, detail})
```

**Tests:**

- **Writable store (overwrite).** Create a temp `attestations/` dir with a
  `.attest` file, leave it writable. Probe returns `false`, `verifyAttestation`
  returns deny string.
- **Writable store (create-only).** Dir has no `.attest` files but is writable.
  Probe returns `false` via the create check.
- **Writable parent.** `attestations/` is read-only but its parent is writable.
  Probe returns `false` via the parent check.
- **Probe error.** `attestations/` doesn't exist. Probe returns `false`
  (fail-closed).
- **Read-only path grant.** All three sub-probes fail to write. Probe returns
  `true`, attestation proceeds normally.

Update `conf/nono-profile.example.json` to annotate that `attestations/` must
be read-only (currently covered by default-deny, but explicit prevents the
ancestor-grant footgun from `ARCHITECTURE.md`).

### Fix 2: `dangerously-none` skips attestation

Early return in `verifyAttestation` before any file I/O:

```go
if cfg.sandboxMode == "dangerously-none" {
    return ""
}
```

Attestation's security value comes from the store being read-only, which
requires a sandbox. Without one it's pure overhead — the agent can rewrite
both rules and records.

If drift-detection UX is wanted for unsandboxed operators, log a warning on
mismatch instead of deny-all. Separate concern from security.

### Fix 3: Ignore `$CLAUDE_BASH_RULES_DIR` in hook mode, resolve home via `getpwuid`

`$HOME` is env-derived — same class of bug as `$CLAUDE_BASH_RULES_DIR`. An
agent that can write `.claude/settings.local.json` can set `HOME` in the
hook's env block, redirecting the store to a pre-forged location. Resolve
home from the password database instead.

```go
import "os/user"

func hookRoot() string {
    u, err := user.Current()
    if err != nil {
        // fail closed: no home → no rules → deny-all
        return ""
    }
    return filepath.Join(u.HomeDir, ".claude-bash-rules")
}
```

`user.Current()` calls `getpwuid_r(getuid())` on Unix — reads the passwd
entry for the real UID, not the environment. The agent cannot influence this
without root.

- Add `hookGlobalRulesPath()` using `hookRoot()`.
- `runClaudeHook` (line 125) calls `hookGlobalRulesPath()`.
- Thread `hookRoot()` through `loadConfig`/`verifyAttestation` for attestation
  record lookups.
- `hookRoot()` returning `""` (lookup failure) → `loadConfig` finds no rules
  file → deny-all. Fail-closed, no special case needed.
- Operator commands (`validate`, `attest`, `test`, `selfcheck`) keep using
  `installRoot()` which honours `$CLAUDE_BASH_RULES_DIR` and `$HOME`.
- Tests keep using `t.Setenv("CLAUDE_BASH_RULES_DIR", ...)` — only affects
  operator commands and test helpers.

## What not to do

- **No `rules-path` in attestation records.** Cross-project replay requires
  byte-identical rules. If the rules are identical, the policy is the same.
- **No HMAC/signing.** The threat model is agent-inside-sandbox. Read-only
  store + SHA-256 content hashes are sufficient. If the store is writable, the
  agent has the key — crypto adds nothing.
- **Don't eliminate `$CLAUDE_BASH_RULES_DIR` globally.** Tests and non-standard
  installs need it. Wall it off from hook mode only.

## Doc updates

- `docs/ARCHITECTURE.md`: document the three vectors as resolved residual risks
- `conf/nono-profile.example.json`: annotate `attestations/` read-only requirement
