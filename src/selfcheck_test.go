package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Permission-bit probes are meaningless running as root (every open
// succeeds regardless of mode). The real enforcement only runs inside nono
// anyway — see the plan note in selfcheck.go's doc comment.
func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("permission probes are meaningless as root")
	}
}

func TestProbeFileNotWritable(t *testing.T) {
	skipIfRoot(t)
	dir := t.TempDir()
	path := writeFile(t, dir, "rules", "sandbox nono\n")

	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	notWritable, detail := probeFileNotWritable(path)
	if !notWritable {
		t.Errorf("read-only file: notWritable=false, detail=%q", detail)
	}

	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	notWritable, detail = probeFileNotWritable(path)
	if notWritable {
		t.Errorf("writable file: notWritable=true, detail=%q", detail)
	}
}

func TestProbeDirWrite(t *testing.T) {
	skipIfRoot(t)
	dir := t.TempDir()

	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	writable, detail := probeDirWrite(dir)
	if writable {
		t.Errorf("read-only dir: writable=true, detail=%q", detail)
	}
	if !strings.Contains(detail, "create failed") {
		t.Errorf("detail = %q, want mention of create failed", detail)
	}

	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writable, detail = probeDirWrite(dir)
	if !writable {
		t.Errorf("writable dir: writable=false, detail=%q", detail)
	}
	if _, err := os.Stat(filepath.Join(dir, ".probe-0")); !os.IsNotExist(err) {
		// not asserting the exact name (pid-based), just that the dir was left clean
		entries, _ := os.ReadDir(dir)
		if len(entries) != 0 {
			t.Errorf("probe file not cleaned up: %v", entries)
		}
	}
}

func TestProbeStoreReadOnly(t *testing.T) {
	skipIfRoot(t)

	// Writable store (overwrite existing record)
	dir := t.TempDir()
	writeFile(t, dir, "global.attest", "schema 1\n")
	ok, _ := probeStoreReadOnly(dir)
	if ok {
		t.Error("writable store with .attest file: should report not read-only")
	}

	// Writable store (create-only, no .attest files)
	empty := t.TempDir()
	ok, _ = probeStoreReadOnly(empty)
	if ok {
		t.Error("writable empty dir: should report not read-only via create check")
	}

	// Writable parent (attestations/ read-only but parent writable)
	root := t.TempDir()
	attestDir := filepath.Join(root, "attestations")
	os.MkdirAll(attestDir, 0o755)
	os.Chmod(attestDir, 0o555)
	t.Cleanup(func() { os.Chmod(attestDir, 0o755) })
	ok, detail := probeStoreReadOnly(attestDir)
	if ok {
		t.Errorf("writable parent: should fail, got detail=%q", detail)
	}
	if !strings.Contains(detail, "parent is writable") {
		t.Errorf("expected parent-writable detail, got %q", detail)
	}

	// Non-existent dir (fail-closed)
	ok, _ = probeStoreReadOnly(filepath.Join(t.TempDir(), "nope"))
	if ok {
		t.Error("non-existent dir: should fail closed")
	}

	// Fully read-only path
	roRoot := t.TempDir()
	roDir := filepath.Join(roRoot, "attestations")
	os.MkdirAll(roDir, 0o755)
	writeFile(t, roDir, "global.attest", "schema 1\n")
	os.Chmod(filepath.Join(roDir, "global.attest"), 0o444)
	os.Chmod(roDir, 0o555)
	os.Chmod(roRoot, 0o555)
	t.Cleanup(func() {
		os.Chmod(roRoot, 0o755)
		os.Chmod(roDir, 0o755)
		os.Chmod(filepath.Join(roDir, "global.attest"), 0o644)
	})
	ok, detail = probeStoreReadOnly(roDir)
	if !ok {
		t.Errorf("read-only path should pass, got detail=%q", detail)
	}
}

// setupWorkspace builds a temp workspace with every execution-controlling
// path present, optionally making them read-only.
func setupWorkspace(t *testing.T, readOnly bool) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, ".git/config", "[core]\n")
	os.MkdirAll(filepath.Join(dir, ".git", "hooks"), 0o755)
	writeFile(t, dir, ".claude/settings.json", "{}\n")
	writeFile(t, dir, ".shizo", "allow git status ANY\n")
	if readOnly {
		os.Chmod(filepath.Join(dir, ".git", "config"), 0o444)
		os.Chmod(filepath.Join(dir, ".git", "hooks"), 0o555)
		os.Chmod(filepath.Join(dir, ".claude", "settings.json"), 0o444)
		os.Chmod(filepath.Join(dir, ".shizo"), 0o444)
	}
	return dir
}

func TestWorkspaceCheckResults(t *testing.T) {
	// Structure and N/A handling are meaningful under any euid.

	// Empty workspace: all four paths missing, N/A and passing.
	empty := t.TempDir()
	results := workspaceCheckResults(empty)
	if len(results) != 4 {
		t.Fatalf("got %d results, want 4", len(results))
	}
	byName := map[string]checkResult{}
	for _, r := range results {
		byName[r.Name] = r
	}
	for _, name := range []string{
		".git/config not writable",
		".git/hooks/ not writable",
		".claude/settings.json not writable",
		".shizo not writable",
	} {
		r, ok := byName[name]
		if !ok {
			t.Errorf("missing check %q", name)
			continue
		}
		if !r.Warn {
			t.Errorf("%s: Warn=false, want true", name)
		}
		if !r.Pass {
			t.Errorf("%s: missing path must pass, detail=%q", name, r.Detail)
		}
		if !r.NA {
			t.Errorf("%s: NA=false, want true (path absent)", name)
		}
	}

	// Partial layout: an existing path is probed, the rest are N/A.
	partial := t.TempDir()
	writeFile(t, partial, ".git/config", "[core]\n")
	got := workspaceCheckResults(partial)
	if len(got) != 4 {
		t.Fatalf("got %d results, want 4", len(got))
	}
	for _, r := range got {
		if r.Name == ".git/config not writable" {
			if r.NA {
				t.Error(".git/config: NA=true, want false (path exists)")
			}
			if !r.Warn {
				t.Error(".git/config: Warn=false, want true")
			}
		} else {
			if !r.NA {
				t.Errorf("%s: NA=false, want true (path absent)", r.Name)
			}
			if !r.Pass {
				t.Errorf("%s: missing path must pass, detail=%q", r.Name, r.Detail)
			}
		}
	}

	// Worktree layout: .git is a file, so .git/config and .git/hooks are
	// ENOTDIR — an unsupported layout, N/A rather than a failure.
	wt := t.TempDir()
	writeFile(t, wt, ".git", "gitdir: /other/repo/.git/worktrees/wt\n")
	got = workspaceCheckResults(wt)
	for _, r := range got {
		if !r.NA || !r.Pass {
			t.Errorf("%s: NA=%v Pass=%v, want N/A pass in worktree layout (detail=%q)", r.Name, r.NA, r.Pass, r.Detail)
		}
	}

	// Symlink loop: the path cannot be inspected — a failed advisory check
	// with the diagnostic, not a passing N/A.
	loop := t.TempDir()
	os.MkdirAll(filepath.Join(loop, ".claude"), 0o755)
	if err := os.Symlink("settings.json", filepath.Join(loop, ".claude", "settings.json")); err != nil {
		t.Fatal(err)
	}
	got = workspaceCheckResults(loop)
	for _, r := range got {
		if r.Name != ".claude/settings.json not writable" {
			continue
		}
		if r.NA || r.Pass {
			t.Errorf("%s: NA=%v Pass=%v, want failed advisory check", r.Name, r.NA, r.Pass)
		}
		if !strings.Contains(r.Detail, "cannot stat") {
			t.Errorf("%s: detail=%q, want cannot stat diagnostic", r.Name, r.Detail)
		}
	}
}

func TestWorkspaceCheckResultsPermissions(t *testing.T) {
	skipIfRoot(t)

	// Every path present and writable: four warn-level failures.
	dir := setupWorkspace(t, false)
	results := workspaceCheckResults(dir)
	byName := map[string]checkResult{}
	for _, r := range results {
		byName[r.Name] = r
	}
	for name, r := range byName {
		if !r.Warn || r.NA {
			t.Errorf("%s: Warn=%v NA=%v, want warn-level probe of an existing path", name, r.Warn, r.NA)
		}
		if r.Pass {
			t.Errorf("%s: writable path reported Pass=true, detail=%q", name, r.Detail)
		}
	}
	if !strings.Contains(byName[".shizo not writable"].Detail, "attestation") {
		t.Errorf(".shizo writable detail should mention the attestation backstop: %q",
			byName[".shizo not writable"].Detail)
	}

	// Every path present and read-only: four warn-level passes, no notes.
	roDir := setupWorkspace(t, true)
	for _, r := range workspaceCheckResults(roDir) {
		if !r.Pass {
			t.Errorf("%s: read-only path reported Pass=false, detail=%q", r.Name, r.Detail)
		}
		if strings.Contains(r.Detail, "attestation") {
			t.Errorf("%s: unexpected attestation note in passing detail %q", r.Name, r.Detail)
		}
	}

	// Permission error on stat: the path was never inspected — failed
	// advisory check, not N/A.
	eacces := t.TempDir()
	os.MkdirAll(filepath.Join(eacces, ".claude"), 0o755)
	os.Chmod(filepath.Join(eacces, ".claude"), 0o000)
	t.Cleanup(func() { os.Chmod(filepath.Join(eacces, ".claude"), 0o755) })
	for _, r := range workspaceCheckResults(eacces) {
		if r.Name != ".claude/settings.json not writable" {
			continue
		}
		if r.NA || r.Pass {
			t.Errorf("%s: NA=%v Pass=%v, want failed advisory check", r.Name, r.NA, r.Pass)
		}
		if !strings.Contains(r.Detail, "cannot stat") {
			t.Errorf("%s: detail=%q, want cannot stat diagnostic", r.Name, r.Detail)
		}
	}

	// Read-only hooks dir with a writable hook inside: the existing hook is
	// still mutable, so the check fails despite the protected directory.
	hooksDir := setupWorkspace(t, false)
	writeFile(t, hooksDir, ".git/hooks/pre-commit", "#!/bin/sh\n")
	os.Chmod(filepath.Join(hooksDir, ".git", "hooks"), 0o555)
	t.Cleanup(func() { os.Chmod(filepath.Join(hooksDir, ".git", "hooks"), 0o755) })
	got := workspaceCheckResults(hooksDir)
	for _, r := range got {
		if r.Name != ".git/hooks/ not writable" {
			continue
		}
		if r.Pass {
			t.Errorf("%s: writable hook in read-only dir reported Pass=true, detail=%q", r.Name, r.Detail)
		}
		if !strings.Contains(r.Detail, "pre-commit is writable") {
			t.Errorf("%s: detail=%q, want the writable hook named", r.Name, r.Detail)
		}
	}

	// Read-only hooks dir with a symlink to a writable file: same vector.
	symlinkDir := setupWorkspace(t, false)
	target := writeFile(t, t.TempDir(), "evil-hook", "#!/bin/sh\n")
	if err := os.Symlink(target, filepath.Join(symlinkDir, ".git", "hooks", "post-commit")); err != nil {
		t.Fatal(err)
	}
	os.Chmod(filepath.Join(symlinkDir, ".git", "hooks"), 0o555)
	t.Cleanup(func() { os.Chmod(filepath.Join(symlinkDir, ".git", "hooks"), 0o755) })
	got = workspaceCheckResults(symlinkDir)
	for _, r := range got {
		if r.Name != ".git/hooks/ not writable" {
			continue
		}
		if r.Pass {
			t.Errorf("%s: symlink to writable hook reported Pass=true, detail=%q", r.Name, r.Detail)
		}
		if !strings.Contains(r.Detail, "post-commit is writable") {
			t.Errorf("%s: detail=%q, want the symlinked hook named", r.Name, r.Detail)
		}
	}

	// Read-only hooks dir with read-only hooks: passes, naming the state.
	cleanHooks := setupWorkspace(t, false)
	roHook := writeFile(t, cleanHooks, ".git/hooks/pre-commit", "#!/bin/sh\n")
	os.Chmod(roHook, 0o444)
	os.Chmod(filepath.Join(cleanHooks, ".git", "hooks"), 0o555)
	t.Cleanup(func() { os.Chmod(filepath.Join(cleanHooks, ".git", "hooks"), 0o755) })
	got = workspaceCheckResults(cleanHooks)
	for _, r := range got {
		if r.Name != ".git/hooks/ not writable" {
			continue
		}
		if !r.Pass {
			t.Errorf("%s: read-only dir and hooks reported Pass=false, detail=%q", r.Name, r.Detail)
		}
	}

	// Unreadable hooks dir: enumeration itself is a failed advisory check.
	noRead := setupWorkspace(t, true)
	os.Chmod(filepath.Join(noRead, ".git", "hooks"), 0o000)
	t.Cleanup(func() { os.Chmod(filepath.Join(noRead, ".git", "hooks"), 0o555) })
	got = workspaceCheckResults(noRead)
	for _, r := range got {
		if r.Name != ".git/hooks/ not writable" {
			continue
		}
		if r.Pass {
			t.Errorf("%s: unreadable hooks dir reported Pass=true, detail=%q", r.Name, r.Detail)
		}
		if !strings.Contains(r.Detail, "cannot enumerate hooks") {
			t.Errorf("%s: detail=%q, want enumeration diagnostic", r.Name, r.Detail)
		}
	}
}

func TestSelfcheckExitCode(t *testing.T) {
	hard := []checkResult{{Name: "a", Pass: false}, {Name: "b", Pass: true}}
	if got := selfcheckExitCode(hard, false); got != 1 {
		t.Errorf("hard failure: exit=%d, want 1", got)
	}
	warn := []checkResult{{Name: "a", Pass: false, Warn: true}, {Name: "b", Pass: true}}
	if got := selfcheckExitCode(warn, false); got != 0 {
		t.Errorf("warn-only failure without --strict: exit=%d, want 0", got)
	}
	if got := selfcheckExitCode(warn, true); got != 1 {
		t.Errorf("warn failure with --strict: exit=%d, want 1", got)
	}
	ok := []checkResult{{Name: "a", Pass: true}, {Name: "b", Pass: true, NA: true}}
	if got := selfcheckExitCode(ok, true); got != 0 {
		t.Errorf("all pass: exit=%d, want 0", got)
	}
}

func TestRunSelfcheckWorkspaceFlag(t *testing.T) {
	setupSelfcheck(t)
	ws := t.TempDir()

	var out, errb bytes.Buffer
	rc := runSelfcheck([]string{"--workspace", ws}, &out, &errb)
	// Outside the sandbox the install checks fail (rc 1); the workspace
	// probes must still be present and warn-level, not failing on their own.
	if rc != 1 {
		t.Errorf("rc=%d, want 1 (unsandboxed install probes fail)", rc)
	}
	for _, name := range []string{".git/config not writable", ".shizo not writable"} {
		if !strings.Contains(out.String(), name) {
			t.Errorf("output missing %q: %q", name, out.String())
		}
	}
	if !strings.Contains(out.String(), "N/A") {
		t.Errorf("empty workspace should report N/A: %q", out.String())
	}

	// A writable workspace path renders as WARN, not FAIL or N/A.
	writable := setupWorkspace(t, false)
	out.Reset()
	rc = runSelfcheck([]string{"--workspace", writable}, &out, &errb)
	if !strings.Contains(out.String(), "WARN .git/config not writable") {
		t.Errorf("writable path should render WARN: %q", out.String())
	}

	rc = runSelfcheck([]string{"--workspace"}, &out, &errb)
	if rc != 2 {
		t.Errorf("--workspace with no value: rc=%d, want 2", rc)
	}
}

func TestRunSelfcheckStrictFlag(t *testing.T) {
	setupSelfcheck(t)
	ws := setupWorkspace(t, false)

	var out, errb bytes.Buffer
	rc := runSelfcheck([]string{"--workspace", ws, "--strict"}, &out, &errb)
	if rc != 1 {
		t.Errorf("rc=%d, want 1", rc)
	}
	// Under --strict a writable workspace path renders as FAIL, not WARN.
	if !strings.Contains(out.String(), "FAIL .git/config not writable") {
		t.Errorf("--strict should render the writable path as FAIL: %q", out.String())
	}
	if strings.Contains(out.String(), "WARN") {
		t.Errorf("--strict output should not contain WARN: %q", out.String())
	}

	// Help mentions the new flags.
	var help bytes.Buffer
	rc = runSelfcheck([]string{"--help"}, &help, &errb)
	if rc != 0 || !strings.Contains(help.String(), "--workspace") ||
		!strings.Contains(help.String(), "--strict") {
		t.Errorf("help: rc=%d, missing flags: %q", rc, help.String())
	}
}

func TestConfigLoadErrors(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "global/rules", "sandbox nono\nallow git status ANY\n")
	if errs := configLoadErrors(root); len(errs) != 0 {
		t.Errorf("clean config: errs=%v", errs)
	}

	root2 := t.TempDir()
	writeFile(t, root2, "global/rules", "sandbox nono\nbogus directive\n")
	if errs := configLoadErrors(root2); len(errs) == 0 {
		t.Error("broken config: expected errors, got none")
	}

	root3 := t.TempDir() // no global/rules at all
	if errs := configLoadErrors(root3); len(errs) == 0 {
		t.Error("missing config: expected errors, got none")
	}
}

// setupSelfcheck builds a temp install root with a clean global/rules,
// engine/ and logs/ dirs, and points HOME + SHIZO_DIR at it.
func setupSelfcheck(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "global/rules", "sandbox nono\nallow git status ANY\n")
	if err := os.MkdirAll(filepath.Join(root, "engine"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "attestations"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHIZO_DIR", root)
	t.Setenv("HOME", root) // logDir() is always $HOME/.shizo/logs
	if err := os.MkdirAll(filepath.Join(root, ".shizo", "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// Outside the sandbox nothing blocks writes, so the "not writable" checks
// fail and "sandboxed" fails without NONO_CAP_FILE — this proves the check
// table runs and reports honestly, not that the sandbox itself works.
func TestSelfcheckResultsOutsideSandbox(t *testing.T) {
	root := setupSelfcheck(t)
	t.Setenv("NONO_CAP_FILE", "")

	results := selfcheckResults(root)
	byName := map[string]checkResult{}
	for _, r := range results {
		byName[r.Name] = r
	}

	if !byName["not running as root"].Pass {
		t.Error("not-root check should pass for non-root user")
	}
	if byName["sandboxed (NONO_CAP_FILE set)"].Pass {
		t.Error("sandboxed check should fail without NONO_CAP_FILE")
	}
	if !byName["global/rules readable"].Pass {
		t.Error("global/rules readable should pass")
	}
	if !byName["logs/ writable"].Pass {
		t.Errorf("logs/ writable should pass: %+v", byName["logs/ writable"])
	}
	if !byName["config loads clean"].Pass {
		t.Errorf("config loads clean should pass: %+v", byName["config loads clean"])
	}
	if len(results) != 9 {
		t.Errorf("got %d results, want 9", len(results))
	}
}

func TestSelfcheckRootCheckFails(t *testing.T) {
	root := setupSelfcheck(t)

	old := euidFn
	euidFn = func() int { return 0 }
	defer func() { euidFn = old }()

	results := selfcheckResults(root)
	byName := map[string]checkResult{}
	for _, r := range results {
		byName[r.Name] = r
	}
	r := byName["not running as root"]
	if r.Pass {
		t.Error("not-root check should fail when euid=0")
	}
	if r.Detail != "euid=0" {
		t.Errorf("detail=%q, want euid=0", r.Detail)
	}
}

func TestRenderSelfcheck(t *testing.T) {
	var b bytes.Buffer
	results := []checkResult{
		{Name: "a", Pass: true},
		{Name: "b", Pass: false, Warn: true, Detail: "opened for write"},
		{Name: "c", Pass: true, NA: true},
	}
	renderSelfcheck(&b, results, false)
	s := b.String()
	for _, want := range []string{
		"PASS a",
		"WARN b  (opened for write)",
		"N/A  c",
		"selfcheck: PASS (1 warning)",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("render missing %q:\n%s", want, s)
		}
	}

	// Two warnings pluralize; a clean run prints no count.
	b.Reset()
	renderSelfcheck(&b, append(results, checkResult{Name: "d", Pass: false, Warn: true}), false)
	if !strings.Contains(b.String(), "selfcheck: PASS (2 warnings)") {
		t.Errorf("want plural warning count:\n%s", b.String())
	}
	b.Reset()
	renderSelfcheck(&b, []checkResult{{Name: "a", Pass: true}}, false)
	if s := b.String(); !strings.Contains(s, "selfcheck: PASS") || strings.Contains(s, "warning") {
		t.Errorf("clean run should have no warning count:\n%s", s)
	}

	// Strict renders warn failures as FAIL and drops the warning count.
	b.Reset()
	renderSelfcheck(&b, results, true)
	s = b.String()
	for _, want := range []string{"FAIL b", "selfcheck: FAIL"} {
		if !strings.Contains(s, want) {
			t.Errorf("strict render missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "WARN") {
		t.Errorf("strict render should not contain WARN:\n%s", s)
	}
}

func TestRunSelfcheckJSON(t *testing.T) {
	setupSelfcheck(t)
	t.Setenv("NONO_CAP_FILE", "/fake")

	var out, errb bytes.Buffer
	rc := runSelfcheck([]string{"--json"}, &out, &errb)
	// at least one "not writable" probe will pass=false outside the sandbox
	if rc != 1 {
		t.Errorf("rc=%d, want 1 (unsandboxed writes succeed)", rc)
	}
	var results []checkResult
	if err := json.Unmarshal(out.Bytes(), &results); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, out.String())
	}
	if len(results) == 0 {
		t.Fatal("no results")
	}
}

func TestCheckResultJSONShape(t *testing.T) {
	b, err := json.Marshal(checkResult{Name: "x", Pass: true, Warn: true, NA: true})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["warn"] != true || m["na"] != true {
		t.Errorf("warn/na should be present and true: %s", b)
	}

	b, _ = json.Marshal(checkResult{Name: "x", Pass: true})
	m = nil
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["warn"]; ok {
		t.Errorf("warn should be omitted when unset: %s", b)
	}
	if _, ok := m["na"]; ok {
		t.Errorf("na should be omitted when unset: %s", b)
	}
}

func TestRunSelfcheckHelp(t *testing.T) {
	var out, errb bytes.Buffer
	rc := runSelfcheck([]string{"--help"}, &out, &errb)
	if rc != 0 || !strings.Contains(out.String(), "usage: shizo selfcheck") {
		t.Errorf("help: rc=%d out=%q", rc, out.String())
	}
}

func TestRunSelfcheckUnknownArg(t *testing.T) {
	var out, errb bytes.Buffer
	rc := runSelfcheck([]string{"--bogus"}, &out, &errb)
	if rc != 2 || !strings.Contains(errb.String(), "unknown argument") {
		t.Errorf("unknown arg: rc=%d err=%q", rc, errb.String())
	}
}

func TestRunSelfcheckRootFlag(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "global/rules", "sandbox nono\n")
	t.Setenv("HOME", t.TempDir())

	var out, errb bytes.Buffer
	rc := runSelfcheck([]string{"--root", root}, &out, &errb)
	if rc != 1 {
		t.Errorf("rc=%d, want 1", rc)
	}
	if !strings.Contains(out.String(), "global/rules readable") {
		t.Errorf("output missing checks: %q", out.String())
	}

	rc = runSelfcheck([]string{"--root"}, &out, &errb)
	if rc != 2 {
		t.Errorf("--root with no value: rc=%d, want 2", rc)
	}
}
