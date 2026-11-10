package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// attestGlobal points SHIZO_DIR at root and writes a passing
// attestation record for global tier content, so loadConfig's attestation
// check succeeds for fixtures/tests that aren't exercising attestation itself.
func attestGlobal(t *testing.T, root, content string) {
	t.Helper()
	t.Setenv("SHIZO_DIR", root)
	t.Setenv("NONO_CAP_FILE", "") // probe is sandbox-only; most tests aren't
	norm, reason, ok := normalizeTier(content, false, "", "")
	if !ok {
		t.Fatalf("attestGlobal: fixture content failed to normalize: %s", reason)
	}
	rec := map[string]string{
		"schema": attestSchema, "tier": "global", "norm-version": normVersion,
		"rules-hash":      hashCanonical(norm.canonical),
		"normalized-path": normalizedPath(os.Getenv("PATH"), norm.trusted),
	}
	if err := writeAttestRecord(globalAttestPath(root), rec, norm.canonical); err != nil {
		t.Fatalf("attestGlobal: %s", err)
	}
}

// attestProject writes a passing attestation record for a project tier at
// path, under root's attestation store. gate is the global project-trust mode
// in effect; the fence base is path's own directory.
func attestProject(t *testing.T, root, path, content, gate string) {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("attestProject: %s", err)
	}
	norm, reason, ok := normalizeTier(content, true, gate, filepath.Dir(abs))
	if !ok {
		t.Fatalf("attestProject: fixture content failed to normalize: %s", reason)
	}
	rec := map[string]string{
		"schema": attestSchema, "tier": "project", "norm-version": normVersion,
		"rules-hash": hashCanonical(norm.canonical),
	}
	if err := writeAttestRecord(projectAttestPath(root, abs), rec, norm.canonical); err != nil {
		t.Fatalf("attestProject: %s", err)
	}
}

// --- normalization: determinism, comment/whitespace/order invariance ---

func TestNormalizeTierDeterministic(t *testing.T) {
	a := "sandbox nono\ntrust /bin\ntrust /usr/bin\nallow git status ANY\ndeny git push ANY\n"
	b := "# reordered, commented, re-spaced — must hash the same\n" +
		"sandbox   nono\n\n# trust dirs, reversed order\ntrust /usr/bin\ntrust /bin\n" +
		"deny git push ANY\nallow git status ANY\n"

	na, reason, ok := normalizeTier(a, false, "", "")
	if !ok {
		t.Fatalf("a: %s", reason)
	}
	nb, reason, ok := normalizeTier(b, false, "", "")
	if !ok {
		t.Fatalf("b: %s", reason)
	}
	if na.canonical != nb.canonical {
		t.Errorf("canonical forms differ:\na=%q\nb=%q", na.canonical, nb.canonical)
	}
	if hashCanonical(na.canonical) != hashCanonical(nb.canonical) {
		t.Error("hashes differ for cosmetically-different but semantically-identical tiers")
	}
}

func TestNormalizeTierRejectsMalformed(t *testing.T) {
	if _, _, ok := normalizeTier("wat is this\n", false, "", ""); ok {
		t.Error("malformed line should fail to normalize")
	}
	if _, _, ok := normalizeTier("allow git status ANY\n", false, "", ""); ok {
		t.Error("global tier missing sandbox should fail to normalize")
	}
}

func TestNormalizeTierRuleChangeAltersHash(t *testing.T) {
	na, _, _ := normalizeTier("sandbox nono\nallow git status ANY\n", false, "", "")
	nb, _, _ := normalizeTier("sandbox nono\nallow git status HAS -s\n", false, "", "")
	if hashCanonical(na.canonical) == hashCanonical(nb.canonical) {
		t.Error("a real rule change must change the hash")
	}
}

// Token-boundary changes must alter the hash: `allow foo 'a b'` (one quoted
// arg) and `allow foo a b` (two args) are different match rules and must not
// collide, or a tamper could rewrite one as the other undetected.
func TestNormalizeTierTokenBoundaryInjective(t *testing.T) {
	na, r1, ok1 := normalizeTier("sandbox nono\nallow foo 'a b'\n", false, "", "")
	nb, r2, ok2 := normalizeTier("sandbox nono\nallow foo a b\n", false, "", "")
	if !ok1 || !ok2 {
		t.Fatalf("normalize failed: %s / %s", r1, r2)
	}
	if hashCanonical(na.canonical) == hashCanonical(nb.canonical) {
		t.Errorf("token-boundary change collided: %q == %q", na.canonical, nb.canonical)
	}
	// A literal argument spelled ANY must not collide with the ANY keyword.
	nc, _, _ := normalizeTier("sandbox nono\nallow foo 'ANY'\n", false, "", "")
	nd, _, _ := normalizeTier("sandbox nono\nallow foo ANY\n", false, "", "")
	if hashCanonical(nc.canonical) == hashCanonical(nd.canonical) {
		t.Error("literal 'ANY' collided with the ANY keyword")
	}
}

// --- record round-trip ---

func TestAttestRecordRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.attest")
	fields := map[string]string{
		"schema": attestSchema, "tier": "global", "norm-version": normVersion,
		"rules-path": "/repo/.shizo", "rules-hash": "sha256:deadbeef",
		"attested-at": "2026-09-09T12:34:56Z", "attested-by": "me@host",
		"normalized-path": "/usr/bin:/bin",
	}
	if err := writeAttestRecord(path, fields, "allow git status ANY"); err != nil {
		t.Fatal(err)
	}
	got, err := readAttestRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range fields {
		if got[k] != v {
			t.Errorf("field %q: got %q, want %q", k, got[k], v)
		}
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "\nrules\nallow git status ANY\n") {
		t.Errorf("advisory rules block missing from record:\n%s", data)
	}
}

// --- loadConfig integration: the attestation verdict ---

func TestLoadConfigDeniesWithoutAttestation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SHIZO_DIR", dir) // no attestations/ written
	body := "sandbox nono\nallow git status ANY\n"
	g := writeFile(t, dir, "rules", body)
	cfg := loadConfig(g, "", installRoot())
	if !cfg.denyAll {
		t.Fatal("unattested global rules must deny all")
	}
}

func TestLoadConfigDeniesOnHashMismatch(t *testing.T) {
	dir := t.TempDir()
	body := "sandbox nono\nallow git status ANY\n"
	g := writeFile(t, dir, "rules", body)
	attestGlobal(t, dir, body)
	// tamper after attesting
	writeFile(t, dir, "rules", body+"allow git push ANY\n")
	if cfg := loadConfig(g, "", installRoot()); !cfg.denyAll {
		t.Fatal("global rules changed since attestation must deny all")
	}
}

func TestLoadConfigDeniesOnNormVersionBump(t *testing.T) {
	dir := t.TempDir()
	body := "sandbox nono\nallow git status ANY\n"
	g := writeFile(t, dir, "rules", body)
	attestGlobal(t, dir, body)
	rec, err := readAttestRecord(globalAttestPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	rec["norm-version"] = "999"
	if err := writeAttestRecord(globalAttestPath(dir), rec, ""); err != nil {
		t.Fatal(err)
	}
	if cfg := loadConfig(g, "", installRoot()); !cfg.denyAll {
		t.Fatal("norm-version mismatch must deny all, not silently pass")
	}
}

func TestLoadConfigDeniesOnMissingProjectAttestation(t *testing.T) {
	dir := t.TempDir()
	body := "sandbox nono\nallow git status ANY\n"
	g := writeFile(t, dir, "rules", body)
	attestGlobal(t, dir, body)
	projDir := t.TempDir()
	proj := writeFile(t, projDir, ".shizo", "deny git push ANY\n") // tightening-only, unattested
	if cfg := loadConfig(g, proj, installRoot()); !cfg.denyAll {
		t.Fatal("an unattested project tier must deny all, even though it only tightens")
	}
}

func TestLoadConfigDeniesOnProjectHashMismatch(t *testing.T) {
	dir := t.TempDir()
	body := "sandbox nono\nallow git status ANY\n"
	g := writeFile(t, dir, "rules", body)
	attestGlobal(t, dir, body)
	projDir := t.TempDir()
	projBody := "deny git push ANY\n"
	proj := writeFile(t, projDir, ".shizo", projBody)
	attestProject(t, dir, proj, projBody, "off")
	writeFile(t, projDir, ".shizo", projBody+"allow rm ANY\n") // tamper after attesting
	if cfg := loadConfig(g, proj, installRoot()); !cfg.denyAll {
		t.Fatal("project rules changed since attestation must deny all")
	}
}

func TestLoadConfigNoProjectFileSkipsProjectCheck(t *testing.T) {
	dir := t.TempDir()
	body := "sandbox nono\nallow git status ANY\n"
	g := writeFile(t, dir, "rules", body)
	attestGlobal(t, dir, body)
	if cfg := loadConfig(g, filepath.Join(t.TempDir(), ".shizo"), installRoot()); cfg.denyAll {
		t.Fatalf("an absent project tier needs no attestation: %s", cfg.denyReason)
	}
}

func TestLoadConfigDeniesOnPathDrift(t *testing.T) {
	dir := t.TempDir()
	bin := t.TempDir()
	body := "sandbox nono\ntrust " + bin + "\nallow git status ANY\n"
	g := writeFile(t, dir, "rules", body)
	t.Setenv("PATH", bin)
	attestGlobal(t, dir, body)
	if cfg := loadConfig(g, "", installRoot()); cfg.denyAll {
		t.Fatalf("should pass right after attesting: %s", cfg.denyReason)
	}
	// PATH no longer includes the attested trust dir.
	t.Setenv("PATH", t.TempDir())
	if cfg := loadConfig(g, "", installRoot()); !cfg.denyAll {
		t.Fatal("PATH drift relative to the attested trust-dir snapshot must deny all")
	}
}

// --- the `attest` CLI subcommand ---

func TestRunAttestGlobalThenLoadConfigTrusts(t *testing.T) {
	root := t.TempDir()
	bin := t.TempDir()
	t.Setenv("SHIZO_DIR", root)
	t.Setenv("NONO_CAP_FILE", "")
	t.Setenv("PATH", bin)
	body := "sandbox nono\ntrust " + bin + "\nallow git status ANY\n"
	writeFile(t, root, "global/rules", body)

	var out, errb bytes.Buffer
	rc := runAttest([]string{"--global"}, &out, &errb)
	if rc != 0 {
		t.Fatalf("attest --global: rc=%d err=%s", rc, errb.String())
	}
	if cfg := loadConfig(globalRulesPath(), "", installRoot()); cfg.denyAll {
		t.Fatalf("global attested via CLI should be trusted: %s", cfg.denyReason)
	}
}

func TestRunAttestProjectThenLoadConfigTrusts(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SHIZO_DIR", root)
	body := "sandbox nono\nallow git status ANY\n"
	g := writeFile(t, root, "global/rules", body)
	attestGlobal(t, root, body)

	projDir := t.TempDir()
	proj := writeFile(t, projDir, ".shizo", "deny git push ANY\n")

	var out, errb bytes.Buffer
	rc := runAttest([]string{proj}, &out, &errb)
	if rc != 0 {
		t.Fatalf("attest project: rc=%d err=%s", rc, errb.String())
	}
	if cfg := loadConfig(g, proj, installRoot()); cfg.denyAll {
		t.Fatalf("project attested via CLI should be trusted: %s", cfg.denyReason)
	}
}

func TestDangerouslyNoneSkipsAttestation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SHIZO_DIR", dir)
	t.Setenv("NONO_CAP_FILE", "")
	body := "sandbox dangerously-none\nallow git status ANY\n"
	g := writeFile(t, dir, "rules", body)
	// No attestation written — dangerously-none should skip the check entirely.
	cfg := loadConfig(g, "", installRoot())
	if cfg.denyAll {
		t.Fatalf("dangerously-none should skip attestation: %s", cfg.denyReason)
	}
	if cfg.sandboxMode != "dangerously-none" {
		t.Error("sandboxMode should be dangerously-none")
	}
}

func TestRunAttestInsideSandboxRefuses(t *testing.T) {
	t.Setenv("NONO_CAP_FILE", "/tmp/fake-cap")
	var out, errb bytes.Buffer
	if rc := runAttest([]string{"--global"}, &out, &errb); rc != 1 {
		t.Errorf("attest inside sandbox: rc=%d, want 1", rc)
	}
	if !strings.Contains(errb.String(), "must be run outside the sandbox") {
		t.Errorf("missing sandbox hint: %q", errb.String())
	}
}

func TestRunAttestRejectsMalformedRules(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SHIZO_DIR", root)
	writeFile(t, root, "global/rules", "wat is this\n")
	var out, errb bytes.Buffer
	if rc := runAttest([]string{"--global"}, &out, &errb); rc != 1 {
		t.Errorf("attest --global on malformed rules: rc=%d", rc)
	}
}

func TestProjectTrustLineFlipsHash(t *testing.T) {
	base := "/repo"
	na, _, ok := normalizeTier("deny rm ANY\n", true, "under-cwd", base)
	if !ok {
		t.Fatal("normalize a")
	}
	nb, _, ok := normalizeTier("deny rm ANY\ntrust node_modules/.bin\n", true, "under-cwd", base)
	if !ok {
		t.Fatal("normalize b")
	}
	if hashCanonical(na.canonical) == hashCanonical(nb.canonical) {
		t.Error("an honored project trust line must change the canonical hash")
	}
	// Gate off: the same line is ignored, so it must not enter the hash.
	nc, _, ok := normalizeTier("deny rm ANY\ntrust node_modules/.bin\n", true, "off", base)
	if !ok {
		t.Fatal("normalize c")
	}
	if hashCanonical(na.canonical) != hashCanonical(nc.canonical) {
		t.Error("an ignored (gate-off) trust line must not change the canonical hash")
	}
}

func TestGlobalProjectTrustFlipsHash(t *testing.T) {
	a, _, ok := normalizeTier("sandbox nono\nallow ls ANY\n", false, "", "")
	if !ok {
		t.Fatal("normalize a")
	}
	b, _, ok := normalizeTier("sandbox nono\nproject-trust under-cwd\nallow ls ANY\n", false, "", "")
	if !ok {
		t.Fatal("normalize b")
	}
	if hashCanonical(a.canonical) == hashCanonical(b.canonical) {
		t.Error("the project-trust mode must be part of the global canonical hash")
	}
}

func TestLoadConfigDeniesOnProjectTrustChange(t *testing.T) {
	dir := t.TempDir()
	body := "project-trust under-cwd\nsandbox nono\ndeny git push ANY\n"
	g := writeFile(t, dir, "rules", body)
	attestGlobal(t, dir, body)
	projDir := t.TempDir()
	projBody := "deny rm ANY\n"
	proj := writeFile(t, projDir, ".shizo", projBody)
	attestProject(t, dir, proj, projBody, "under-cwd")
	if cfg := loadConfig(g, proj, installRoot()); cfg.denyAll {
		t.Fatal(cfg.denyReason)
	}
	// Attacker appends a within-cwd trust line to the attested project file.
	writeFile(t, projDir, ".shizo", projBody+"trust node_modules/.bin\n")
	if cfg := loadConfig(g, proj, installRoot()); !cfg.denyAll {
		t.Error("adding a project trust line must break attestation")
	}
}

func TestLoadConfigProjectTrustEscapeDoesNotFlipHash(t *testing.T) {
	dir := t.TempDir()
	body := "project-trust under-cwd\nsandbox nono\ndeny git push ANY\n"
	g := writeFile(t, dir, "rules", body)
	attestGlobal(t, dir, body)
	projDir := t.TempDir()
	projBody := "deny rm ANY\n"
	proj := writeFile(t, projDir, ".shizo", projBody)
	attestProject(t, dir, proj, projBody, "under-cwd")
	// An escaping trust line is ignored by the loader, so it hashes to nothing.
	writeFile(t, projDir, ".shizo", projBody+"trust /tmp/evil\n")
	if cfg := loadConfig(g, proj, installRoot()); cfg.denyAll {
		t.Errorf("an ignored (escaping) trust line must not break attestation: %s", cfg.denyReason)
	}
}

func TestRunAttestProjectTrustHonored(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SHIZO_DIR", root)
	body := "project-trust under-cwd\nsandbox nono\nallow git status ANY\n"
	g := writeFile(t, root, "global/rules", body)
	attestGlobal(t, root, body)

	projDir := t.TempDir()
	projBody := "trust node_modules/.bin\n"
	proj := writeFile(t, projDir, ".shizo", projBody)
	var out, errb bytes.Buffer
	if rc := runAttest([]string{proj}, &out, &errb); rc != 0 {
		t.Fatalf("runAttest: rc=%d %s", rc, errb.String())
	}
	abs, _ := filepath.Abs(proj)
	recData, err := os.ReadFile(projectAttestPath(root, abs))
	if err != nil {
		t.Fatal(err)
	}
	want := "trust " + filepath.Join(projDir, "node_modules", ".bin")
	if !strings.Contains(string(recData), want) {
		t.Errorf("record should attest the honored trust line %q:\n%s", want, recData)
	}
	cfg := loadConfig(g, proj, installRoot())
	if cfg.denyAll {
		t.Fatal(cfg.denyReason)
	}
	if len(cfg.trusted) != 1 || cfg.trusted[0] != filepath.Join(projDir, "node_modules", ".bin") {
		t.Errorf("trusted = %+v", cfg.trusted)
	}
}

func TestRunAttestProjectTrustGateOff(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SHIZO_DIR", root) // no global/rules -> gate off
	projDir := t.TempDir()
	projBody := "trust node_modules/.bin\n"
	proj := writeFile(t, projDir, ".shizo", projBody)
	var out, errb bytes.Buffer
	if rc := runAttest([]string{proj}, &out, &errb); rc != 0 {
		t.Fatalf("runAttest: rc=%d %s", rc, errb.String())
	}
	abs, _ := filepath.Abs(proj)
	recData, err := os.ReadFile(projectAttestPath(root, abs))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(recData), "trust ") {
		t.Errorf("gate off: record must not attest the trust line:\n%s", recData)
	}
}
