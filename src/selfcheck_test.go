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
// engine/ and logs/ dirs, and points HOME + CLAUDE_BASH_RULES_DIR at it.
func setupSelfcheck(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "global/rules", "sandbox nono\nallow git status ANY\n")
	if err := os.MkdirAll(filepath.Join(root, "engine"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_BASH_RULES_DIR", root)
	t.Setenv("HOME", root) // logDir() is always $HOME/.claude-bash-rules/logs
	if err := os.MkdirAll(filepath.Join(root, ".claude-bash-rules", "logs"), 0o755); err != nil {
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
	if len(results) != 7 {
		t.Errorf("got %d results, want 7", len(results))
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

func TestRunSelfcheckHelp(t *testing.T) {
	var out, errb bytes.Buffer
	rc := runSelfcheck([]string{"--help"}, &out, &errb)
	if rc != 0 || !strings.Contains(out.String(), "usage: bash-gate selfcheck") {
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
