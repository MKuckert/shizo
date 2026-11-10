package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupTest mirrors setupRun but leaves NONO_CAP_FILE unset so the sandbox gate
// is exercisable via --assume-sandboxed. Returns the trusted bin dir.
func setupTest(t *testing.T, rulesBody string) string {
	t.Helper()
	root := t.TempDir()
	bin := t.TempDir()
	mkExe(t, bin, "git")
	body := "sandbox nono\ntrust " + bin + "\n" + rulesBody
	writeFile(t, root, "global/rules", body)
	t.Setenv("SHIZO_DIR", root)
	t.Setenv("PATH", bin)
	t.Setenv("NONO_CAP_FILE", "") // gate off by default; --assume-sandboxed flips it
	t.Setenv("HOME", t.TempDir())
	attestGlobal(t, root, body)
	return bin
}

func test(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	rc := runTest(args, strings.NewReader(stdin), &out, &errb)
	return rc, out.String(), errb.String()
}

func TestTestAllowAskDeny(t *testing.T) {
	setupTest(t, "allow git status ANY\ndeny git push ANY\n")

	rc, out, _ := test(t, "", "--assume-sandboxed", "git", "status")
	if rc != 0 || !strings.Contains(out, "decision: allow") {
		t.Errorf("allow: rc=%d out=%q", rc, out)
	}

	rc, out, _ = test(t, "", "--assume-sandboxed", "git", "commit")
	if rc != 0 || !strings.Contains(out, "decision: ask") {
		t.Errorf("ask: rc=%d out=%q", rc, out)
	}

	rc, out, _ = test(t, "", "--assume-sandboxed", "git", "push")
	if rc != 2 || !strings.Contains(out, "decision: deny") {
		t.Errorf("deny: rc=%d out=%q", rc, out)
	}
}

// --cwd selects which project tier is loaded, changing the decision.
func TestTestCwdPicksProjectTier(t *testing.T) {
	setupTest(t, "allow git status ANY\n")
	proj := t.TempDir()
	projBody := "deny git status ANY\n"
	projPath := writeFile(t, proj, ".shizo", projBody)
	attestProject(t, installRoot(), projPath, projBody, "off")

	rc, out, _ := test(t, "", "--assume-sandboxed", "--cwd", proj, "git", "status")
	if rc != 2 || !strings.Contains(out, "decision: deny") {
		t.Errorf("project tier deny not applied: rc=%d out=%q", rc, out)
	}

	// Elsewhere the global allow stands.
	rc, out, _ = test(t, "", "--assume-sandboxed", "--cwd", t.TempDir(), "git", "status")
	if rc != 0 || !strings.Contains(out, "decision: allow") {
		t.Errorf("global allow not applied: rc=%d out=%q", rc, out)
	}
}

// Without --assume-sandboxed the sandbox gate denies; the flag flips it.
func TestTestAssumeSandboxedFlipsGate(t *testing.T) {
	setupTest(t, "allow git status ANY\n")

	rc, out, errb := test(t, "", "git", "status")
	if rc != 2 || !strings.Contains(out, "decision: deny") {
		t.Errorf("gate should deny unsandboxed: rc=%d out=%q", rc, out)
	}
	if !strings.Contains(errb, "--assume-sandboxed") {
		t.Errorf("missing sandbox-gate hint: %q", errb)
	}

	rc, out, _ = test(t, "", "--assume-sandboxed", "git", "status")
	if rc != 0 || !strings.Contains(out, "decision: allow") {
		t.Errorf("--assume-sandboxed should allow: rc=%d out=%q", rc, out)
	}
}

func TestTestJSONShape(t *testing.T) {
	setupTest(t, "allow git status ANY\n")
	rc, out, _ := test(t, "", "--assume-sandboxed", "--json", "git", "status")
	if rc != 0 {
		t.Fatalf("rc=%d out=%q", rc, out)
	}
	var ll logLine
	if err := json.Unmarshal([]byte(out), &ll); err != nil {
		t.Fatalf("bad JSON %q: %v", out, err)
	}
	if ll.Decision != "allow" || ll.Command != "git status" || len(ll.Commands) != 1 {
		t.Errorf("unexpected log line: %+v", ll)
	}
}

func TestTestStdinJSON(t *testing.T) {
	setupTest(t, "allow git status ANY\n")
	in := `{"tool_name":"Bash","tool_input":{"command":"git status"},"cwd":"/tmp"}`
	rc, out, _ := test(t, in, "--assume-sandboxed", "--stdin-json")
	if rc != 0 || !strings.Contains(out, "decision: allow") {
		t.Errorf("stdin-json: rc=%d out=%q", rc, out)
	}
}

func TestTestMissingCommand(t *testing.T) {
	setupTest(t, "allow git status ANY\n")
	rc, _, errb := test(t, "", "--assume-sandboxed")
	if rc != 2 || !strings.Contains(errb, "no command given") {
		t.Errorf("missing command: rc=%d err=%q", rc, errb)
	}
}

// Leading -h/--help prints usage; but -h/--help inside the command under test
// must reach the pipeline, not short-circuit to usage.
func TestTestHelp(t *testing.T) {
	setupTest(t, "allow git status ANY\n")

	for _, a := range []string{"-h", "--help"} {
		rc, out, _ := test(t, "", a)
		if rc != 0 || !strings.Contains(out, "usage: shizo test") {
			t.Errorf("leading %s: rc=%d out=%q", a, rc, out)
		}
	}

	// `git status -h` is a real command with a -h token; it must be evaluated.
	rc, out, _ := test(t, "", "--assume-sandboxed", "git", "status", "-h")
	if rc != 0 || !strings.Contains(out, "decision:") || strings.Contains(out, "usage: shizo test") {
		t.Errorf("trailing -h should test the command, not print usage: rc=%d out=%q", rc, out)
	}
}

// A relative --cwd must behave like the hook's absolute cwd: the project
// tier's fence base and slash-path resolution both need it absolute.
func TestTestRelativeCwdHonorsProjectTrust(t *testing.T) {
	setupTest(t, "project-trust under-cwd\nallow dep-cli ANY\n")
	proj := t.TempDir()
	binDir := filepath.Join(proj, "node_modules", ".bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mkExe(t, binDir, "dep-cli")
	projBody := "trust node_modules/.bin\n"
	projPath := writeFile(t, proj, ".shizo", projBody)
	attestProject(t, installRoot(), projPath, projBody, "under-cwd")

	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(proj); err != nil {
		t.Fatal(err)
	}

	rc, out, _ := test(t, "", "--assume-sandboxed", "--cwd", ".", "node_modules/.bin/dep-cli", "build")
	if rc != 0 || !strings.Contains(out, "decision: allow") {
		t.Errorf("relative --cwd should honor project trust: rc=%d out=%q", rc, out)
	}
}
