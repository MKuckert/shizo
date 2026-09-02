package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// policyFixture builds a trusted bin dir with fake binaries plus a loaded config
// and an environ, for end-to-end decide() tests.
func policyFixture(t *testing.T, extraRules string) (*config, environ, string) {
	t.Helper()
	bin := t.TempDir()
	for _, name := range []string{"git", "ls", "curl", "find", "grep", "rm"} {
		mkExe(t, bin, name)
	}
	rules := "sandbox nono\ntrust " + bin + "\n" +
		"allow git status ANY\n" +
		"allow ls ANY\n" +
		"deny git push ANY\n" +
		"deny find HAS -exec\n" +
		extraRules
	dir := t.TempDir()
	g := writeFile(t, dir, "rules", rules)
	cfg := loadConfig(g, "")
	if cfg.denyAll {
		t.Fatalf("fixture config denied all: %s", cfg.denyReason)
	}
	env := environ{path: bin, tmpdirs: []string{"/tmp"}, sandboxed: true}
	return cfg, env, bin
}

func TestPolicy(t *testing.T) {
	cfg, env, _ := policyFixture(t, "allow eval ANY\n")
	cwd := t.TempDir()

	cases := []struct {
		cmd  string
		want Outcome
	}{
		{`git status`, outAllow},
		{`git status -s`, outAllow},
		{`git push origin`, outDeny}, // deny rule
		{`ls -la`, outAllow},
		{`echo hi`, outAllow},               // safe builtin
		{`git commit -m msg`, outAsk},       // resolves, no rule
		{`git status && git push`, outDeny}, // one deny in the chain
		{`git status && git commit -m x`, outAsk},
		{`eval "rm -rf /"`, outDeny},       // uncond deny overrides `allow eval ANY`
		{`FOO=1 git status`, outDeny},      // assignment prefix
		{`git $x status`, outDeny},         // ParamExp
		{`find . -exec rm {} \;`, outDeny}, // HAS -exec
		{`nope-missing`, outDeny},          // not found
	}
	for _, tc := range cases {
		got, reason, _ := decide(tc.cmd, cwd, cfg, env)
		if got != tc.want {
			t.Errorf("%q: got %s (%s) want %s", tc.cmd, got, reason, tc.want)
		}
	}
}

func TestPolicySandboxGate(t *testing.T) {
	// explicit `sandbox nono` denies when not sandboxed
	cfg, env, _ := policyFixture(t, "")
	env.sandboxed = false
	if got, _, _ := decide("git status", t.TempDir(), cfg, env); got != outDeny {
		t.Error("unsandboxed under `sandbox nono` should deny")
	}

	// `sandbox dangerously-none` allows running without a sandbox.
	// The directive must precede the rules, so it goes in the global tier here
	// rather than via extraRules (which is appended after the allow/deny lines).
	dir := t.TempDir()
	bin := t.TempDir()
	mkExe(t, bin, "git")
	g := writeFile(t, dir, "rules",
		"sandbox dangerously-none\ntrust "+bin+"\nallow git status ANY\n")
	cfg = loadConfig(g, "")
	if cfg.denyAll {
		t.Fatalf("config denied all: %s", cfg.denyReason)
	}
	env = environ{path: bin, tmpdirs: []string{"/tmp"}, sandboxed: false}
	if got, reason, _ := decide("git status", t.TempDir(), cfg, env); got != outAllow {
		t.Errorf("dangerously-none unsandboxed: got %s (%s), want allow", got, reason)
	}
}

func TestPolicyRedirectContainment(t *testing.T) {
	cfg, env, _ := policyFixture(t, "")
	// Use a repo cwd and a tmpdir that are genuinely separate (neither under the
	// other, neither being /tmp), so `..` escapes are testable regardless of where
	// the test's TempDir happens to live.
	cwd := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	env.tmpdirs = []string{tmp}

	cases := []struct {
		cmd  string
		want Outcome
	}{
		{`git status > out.txt`, outAllow},         // in cwd
		{`git status > ` + tmp + `/x`, outAllow},   // in a tmpdir
		{`git status > /dev/null`, outAllow},       // whitelisted device
		{`git status > /etc/passwd`, outDeny},      // absolute escape
		{`git status > ../../etc/passwd`, outDeny}, // .. escape out of cwd and tmp
		{`git status > sub/x`, outAllow},           // nested in cwd
		{`git status >> out.txt`, outAllow},        // append
		{`git status > $DEST`, outDeny},            // ParamExp target
		{`cd sub && ls > out.txt`, outDeny},        // cd moves the base
	}
	for _, tc := range cases {
		got, reason, _ := decide(tc.cmd, cwd, cfg, env)
		if got != tc.want {
			t.Errorf("%q: got %s (%s) want %s", tc.cmd, got, reason, tc.want)
		}
	}
}

func TestPolicyFuncDeclDenied(t *testing.T) {
	cfg, env, _ := policyFixture(t, "")
	got, _, _ := decide(`git() { rm -rf /; }; git status`, t.TempDir(), cfg, env)
	if got != outDeny {
		t.Error("FuncDecl should deny")
	}
}

func TestPolicyLogsRecordTrueOutcome(t *testing.T) {
	cfg, env, _ := policyFixture(t, "")
	_, _, logs := decide(`git status && git push`, t.TempDir(), cfg, env)
	if len(logs) != 2 {
		t.Fatalf("expected 2 command logs, got %d", len(logs))
	}
	if logs[0].Decision != "allow" || logs[1].Decision != "deny" {
		t.Errorf("per-command decisions wrong: %+v", logs)
	}
}

func TestParseError(t *testing.T) {
	cfg, env, _ := policyFixture(t, "")
	got, reason, _ := decide(`git commit -m "unclosed`, t.TempDir(), cfg, env)
	if got != outDeny || !strings.Contains(reason, "parse error") {
		t.Errorf("unclosed quote: got %s (%s)", got, reason)
	}
}

// T12 latency budget: the parse+policy cost must stay well under 20 ms/call.
func TestLatencyBudget(t *testing.T) {
	cfg, env, _ := policyFixture(t, "")
	cwd := t.TempDir()
	const cmd = `git log | grep foo | head -5 && ls -la > out.txt`
	const iters = 200
	start := time.Now()
	for i := 0; i < iters; i++ {
		decide(cmd, cwd, cfg, env)
	}
	avg := time.Since(start) / iters
	if avg > 20*time.Millisecond {
		t.Errorf("avg decide latency %v exceeds 20ms budget", avg)
	}
	t.Logf("avg decide latency: %v", avg)
}

func TestProjectTighteningApplies(t *testing.T) {
	bin := t.TempDir()
	mkExe(t, bin, "git")
	mkExe(t, bin, "rm")
	dir := t.TempDir()
	g := writeFile(t, dir, "rules", "sandbox nono\ntrust "+bin+"\nallow git status ANY\nallow rm ANY\n")
	projDir := t.TempDir()
	writeFile(t, projDir, ".claude-bash-rules", "deny rm ANY\n")
	cfg := loadConfig(g, filepath.Join(projDir, ".claude-bash-rules"))
	env := environ{path: bin, tmpdirs: []string{"/tmp"}, sandboxed: true}

	if got, _, _ := decide("rm x", projDir, cfg, env); got != outDeny {
		t.Error("project deny should tighten global allow")
	}
	if got, _, _ := decide("git status", projDir, cfg, env); got != outAllow {
		t.Error("global allow still applies")
	}
}
