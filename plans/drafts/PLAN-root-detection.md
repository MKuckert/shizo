## Plan: Explicit root (euid == 0) detection

`sandbox nono` + root = hard deny everywhere. `dangerously-none` bypasses.
No stderr warnings for `dangerously-none` + root (user accepted all risk).
`selfcheck` reports root status as a named check line.

### Steps

1. **`rules.go` — root gate in `loadConfig`**: after parsing sandbox mode (before
   `verifyAttestation`), if `sandboxMode == "nono" && os.Geteuid() == 0`, return
   `denyAllConfig("sandbox nono requires a non-root user (euid=0); use 'sandbox dangerously-none' for root environments")`.
   This covers the hook path and any code that calls `loadConfig`.

2. **`selfcheck.go` — add root check line**: insert `not running as root` check
   (pass when `os.Geteuid() != 0`, fail with `"euid=0"` detail) into
   `runSelfcheck`, before the existing `sandboxed` check.

3. **`validate.go` — root + nono = error**: `validate` has its own rule-loading
   path (`loadAndValidate`). After parsing the sandbox directive, if
   `sandbox nono` + `euid == 0`, emit an error line and set exit 1. This makes
   `bash-gate validate` fail when the operator is root with nono rules.

4. **Tests — `main_test.go`**: the existing `setupRun` fix (use
   `dangerously-none` when root, skip `NONO_CAP_FILE`) is already correct for
   this design. Add one test `TestRootDeniedUnderNono` that, when not root,
   simulates the check by calling `loadConfig` with nono rules and a mock
   `euid == 0` (extract the check into a helper that takes euid as a parameter so
   it's testable without actual root).

5. **Tests — `selfcheck_test.go`**: add a case for the new root check line in
   the existing selfcheck test coverage.

6. **Docs — `docs/RULES.md`**: in the `sandbox` directive section, add that
   `sandbox nono` requires a non-root user and denies with a clear message when
   `euid == 0`.

7. **Docs — `docs/ARCHITECTURE.md`**: in the "Residual risks" or sandbox
   section, note that root bypasses POSIX file permissions and therefore
   `sandbox nono` explicitly rejects root to maintain the read-only attestation
   invariant.

### Design decisions

- Root check uses `os.Geteuid()` (effective UID), not `os.Getuid()` — matches
  what the kernel actually enforces for file access.
- The check in `loadConfig` is a pure euid+sandboxMode gate, independent of the
  writability probe. The probe stays — it catches non-root sandbox misconfigs.
- To make step 4 testable without root, extract the condition into a function
  like `rootBlocksNono(euid int, mode string) string` that returns "" or a deny
  reason.

Skipped: `bash-gate test --assume-sandboxed` root warning — add when operators
report confusion running `test` as root with nono rules.
