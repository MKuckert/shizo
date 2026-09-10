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
	rc := runClaudeHook(strings.NewReader(input), &out)
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

func TestRunHelp(t *testing.T) {
	cases := []struct {
		args []string
		want string // a substring unique to the expected usage
	}{
		{nil, "usage: bash-gate [command]"},
		{[]string{"claude-hook"}, "usage: bash-gate claude-hook"},
		{[]string{"validate"}, "usage: bash-gate validate"},
		{[]string{"test"}, "usage: bash-gate test"},
		{[]string{"selfcheck"}, "usage: bash-gate selfcheck"},
		{[]string{"bogus"}, "usage: bash-gate [command]"}, // unknown -> root
	}
	for _, c := range cases {
		var out bytes.Buffer
		rc := runHelp(c.args, &out)
		if rc != 0 {
			t.Errorf("help %v: rc=%d, want 0", c.args, rc)
		}
		if !strings.Contains(out.String(), c.want) {
			t.Errorf("help %v: output %q missing %q", c.args, out.String(), c.want)
		}
		// A help request must never emit a hook decision.
		if strings.Contains(out.String(), "permissionDecision") {
			t.Errorf("help %v leaked a hook decision: %q", c.args, out.String())
		}
	}
}

// An unknown subcommand must error (exit 2) instead of falling through to hook
// mode and blocking on stdin.
func TestDispatchUnknownCommand(t *testing.T) {
	var out, errb bytes.Buffer
	// stdin is a command JSON that would ALLOW in hook mode; a stray arg must
	// not let that through — it must error without reading stdin.
	stdin := strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"git status"}}`)
	rc := dispatch([]string{"halp"}, stdin, &out, &errb)
	if rc != 2 || !strings.Contains(errb.String(), "unknown command") {
		t.Errorf("unknown command: rc=%d err=%q out=%q", rc, errb.String(), out.String())
	}
	if strings.Contains(out.String(), "permissionDecision") {
		t.Errorf("unknown command leaked a hook decision: %q", out.String())
	}
}

// dispatch("claude-hook", ...) must behave exactly as arg-less run() did
// before this change: hook JSON on stdin -> a decision.
func TestDispatchClaudeHook(t *testing.T) {
	setupRun(t, "allow git status ANY\n")
	var out, errb bytes.Buffer
	stdin := strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"git status"},"cwd":"/tmp"}`)
	rc := dispatch([]string{"claude-hook"}, stdin, &out, &errb)
	if rc != 0 || decisionOf(t, out.String()) != "allow" {
		t.Errorf("claude-hook allow: rc=%d out=%s", rc, out.String())
	}
}

// failingReader errors on any Read; used to prove a code path never reads stdin.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	panic("stdin was read")
}

// Arg-less must print usage and exit 0 without ever touching stdin (it must
// not block waiting for hook JSON that will never arrive).
func TestDispatchArglessNeverReadsStdin(t *testing.T) {
	var out, errb bytes.Buffer
	rc := dispatch(nil, failingReader{}, &out, &errb)
	if rc != 0 {
		t.Errorf("arg-less: rc=%d, want 0", rc)
	}
	if !strings.Contains(out.String(), "usage: bash-gate [command]") {
		t.Errorf("arg-less: output %q missing usage", out.String())
	}
	if strings.Contains(out.String(), "permissionDecision") {
		t.Errorf("arg-less leaked a hook decision: %q", out.String())
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
