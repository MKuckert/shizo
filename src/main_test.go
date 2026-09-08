package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// setupRun points CLAUDE_BASH_RULES_DIR at a temp install with a global/rules
// file and a trusted bin dir, and marks the process sandboxed.
func setupRun(t *testing.T, rulesBody string) string {
	t.Helper()
	root := t.TempDir()
	bin := t.TempDir()
	mkExe(t, bin, "git")
	body := "sandbox nono\ntrust " + bin + "\n" + rulesBody
	writeFile(t, root, "global/rules", body)
	t.Setenv("CLAUDE_BASH_RULES_DIR", root)
	t.Setenv("PATH", bin)
	t.Setenv("NONO_CAP_FILE", "/tmp/fake-cap")
	t.Setenv("HOME", t.TempDir()) // isolate logs
	return bin
}

func runWith(t *testing.T, input string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	rc := run(strings.NewReader(input), &out)
	return rc, out.String()
}

func decisionOf(t *testing.T, body string) string {
	t.Helper()
	var obj struct {
		H struct {
			D string `json:"permissionDecision"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(body), &obj); err != nil {
		t.Fatalf("bad JSON %q: %v", body, err)
	}
	return obj.H.D
}

func TestRunMalformedInput(t *testing.T) {
	setupRun(t, "allow git status ANY\n")
	rc, out := runWith(t, "not json")
	if rc != 2 || decisionOf(t, out) != "deny" {
		t.Errorf("malformed input: rc=%d out=%s", rc, out)
	}
}

func TestRunEmptyCommand(t *testing.T) {
	setupRun(t, "allow git status ANY\n")
	rc, out := runWith(t, `{"tool_name":"Bash","tool_input":{"command":"   "}}`)
	if rc != 2 || decisionOf(t, out) != "deny" {
		t.Errorf("empty command: rc=%d out=%s", rc, out)
	}
}

func TestRunAllow(t *testing.T) {
	setupRun(t, "allow git status ANY\n")
	rc, out := runWith(t, `{"tool_name":"Bash","tool_input":{"command":"git status"},"cwd":"/tmp"}`)
	if rc != 0 || decisionOf(t, out) != "allow" {
		t.Errorf("allow: rc=%d out=%s", rc, out)
	}
}

func TestRunDenyExit2(t *testing.T) {
	setupRun(t, "allow git status ANY\ndeny git push ANY\n")
	rc, out := runWith(t, `{"tool_name":"Bash","tool_input":{"command":"git push"},"cwd":"/tmp"}`)
	if rc != 2 || decisionOf(t, out) != "deny" {
		t.Errorf("deny: rc=%d out=%s", rc, out)
	}
}

// A logging failure (unwritable log dir) must not change the decision (T9).
func TestLoggingFailureDoesNotChangeDecision(t *testing.T) {
	setupRun(t, "allow git status ANY\n")
	// Make HOME a regular file so MkdirAll(logDir) fails.
	homeFile := writeFile(t, t.TempDir(), "home", "x")
	t.Setenv("HOME", homeFile)
	rc, out := runWith(t, `{"tool_name":"Bash","tool_input":{"command":"git status"},"cwd":"/tmp"}`)
	if rc != 0 || decisionOf(t, out) != "allow" {
		t.Errorf("logging failure changed decision: rc=%d out=%s", rc, out)
	}
}

func TestRunAutomodeDefer(t *testing.T) {
	setupRun(t, "returntype automode\nallow git status ANY\n")
	// allow-rule match -> emit allow decision, exit 0 (only fallthrough defers)
	rc, out := runWith(t, `{"tool_name":"Bash","tool_input":{"command":"git status"},"cwd":"/tmp"}`)
	if rc != 0 || decisionOf(t, out) != "allow" {
		t.Errorf("automode allow should emit allow: rc=%d out=%q", rc, out)
	}
	// unmatched (ask fallthrough) -> defer sentinel, exit 0
	rc, out = runWith(t, `{"tool_name":"Bash","tool_input":{"command":"git log"},"cwd":"/tmp"}`)
	if rc != 0 || out != "defer" {
		t.Errorf("automode unmatched should defer: rc=%d out=%q", rc, out)
	}
	// deny-rule match still emits the deny object on exit 2
	setupRun(t, "returntype automode\ndeny git push ANY\n")
	rc, out = runWith(t, `{"tool_name":"Bash","tool_input":{"command":"git push"},"cwd":"/tmp"}`)
	if rc != 2 || decisionOf(t, out) != "deny" {
		t.Errorf("automode deny: rc=%d out=%q", rc, out)
	}
}
