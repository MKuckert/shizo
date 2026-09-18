package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runGate copies shizo.sh into an engine dir, optionally installs a stub
// shizo producing the given stdout/exit, then runs the wrapper.
func runGate(t *testing.T, installStub bool, stubOut string, stubRC int) (int, string) {
	return runGateEnv(t, t.TempDir(), installStub, stubOut, stubRC)
}

// runGateEnv is like runGate but with an explicit HOME (to exercise the
// unwritable-log-dir path).
func runGateEnv(t *testing.T, home string, installStub bool, stubOut string, stubRC int) (int, string) {
	t.Helper()
	engine := t.TempDir()
	src, err := os.ReadFile("shizo.sh")
	if err != nil {
		t.Fatal(err)
	}
	gate := filepath.Join(engine, "shizo.sh")
	if err := os.WriteFile(gate, src, 0o755); err != nil {
		t.Fatal(err)
	}
	if installStub {
		stub := "#!/bin/sh\nprintf '%s' " + shQuote(stubOut) + "\nexit " + itoa(stubRC) + "\n"
		if err := os.WriteFile(filepath.Join(engine, "shizo"), []byte(stub), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.Command("/bin/sh", gate)
	cmd.Env = []string{"HOME=" + home, "PATH=/bin:/usr/bin"}
	cmd.Stdin = strings.NewReader(`{"tool_input":{"command":"git status"}}`)
	var out bytes.Buffer
	cmd.Stdout = &out
	err = cmd.Run()
	rc := 0
	if ee, ok := err.(*exec.ExitError); ok {
		rc = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run gate: %v", err)
	}
	return rc, out.String()
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func isDenyJSON(s string) bool {
	return strings.Contains(s, `"permissionDecision":"deny"`)
}

func TestGateMissingBinary(t *testing.T) {
	rc, out := runGate(t, false, "", 0)
	if rc != 2 || !isDenyJSON(out) {
		t.Errorf("missing binary: rc=%d out=%s", rc, out)
	}
}

func TestGateEmptyStdout(t *testing.T) {
	rc, out := runGate(t, true, "", 0)
	if rc != 2 || !isDenyJSON(out) {
		t.Errorf("empty stdout: rc=%d out=%s", rc, out)
	}
}

func TestGateGarbageStdout(t *testing.T) {
	rc, out := runGate(t, true, "hello world", 0)
	if rc != 2 || !isDenyJSON(out) {
		t.Errorf("garbage stdout: rc=%d out=%s", rc, out)
	}
}

func TestGateDeferSentinel(t *testing.T) {
	rc, out := runGate(t, true, "defer", 0)
	if rc != 0 || out != "" {
		t.Errorf("defer: rc=%d out=%q", rc, out)
	}
}

func TestGateAllowJSON(t *testing.T) {
	j := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"ok"}}`
	rc, out := runGate(t, true, j, 0)
	if rc != 0 || out != j {
		t.Errorf("allow: rc=%d out=%s", rc, out)
	}
}

func TestGateDenyJSONExit2(t *testing.T) {
	j := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"nope"}}`
	rc, out := runGate(t, true, j, 2)
	if rc != 2 || out != j {
		t.Errorf("deny propagate: rc=%d out=%s", rc, out)
	}
}

// A non-writable log dir must fail closed with a specific deny (no command runs
// unlogged), not fall through to the binary's decision. Here HOME's
// .shizo is a regular file, so mkdir of logs/ fails.
func TestGateUnwritableLogDirDenies(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".shizo"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Even if the (unreached) binary would allow, the gate must deny.
	allow := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"ok"}}`
	rc, out := runGateEnv(t, home, true, allow, 0)
	if rc != 2 || !isDenyJSON(out) || !strings.Contains(out, "not writable") {
		t.Errorf("unwritable log dir should deny with a specific reason: rc=%d out=%s", rc, out)
	}
}

func TestGateNonTwoCrashForcedDeny(t *testing.T) {
	// exit 3 with a JSON body is still a crash → forced deny exit 2.
	j := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"x"}}`
	rc, out := runGate(t, true, j, 3)
	if rc != 2 || !isDenyJSON(out) {
		t.Errorf("non-2 crash: rc=%d out=%s", rc, out)
	}
}
