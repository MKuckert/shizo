package main

import (
	"strings"
	"testing"
)

// mkRule builds a rule from a DSL line body (without the action word).
func mkRule(t *testing.T, body string) rule {
	t.Helper()
	toks, err := parseRuleWords(body)
	if err != nil {
		t.Fatalf("parseRuleWords(%q): %v", body, err)
	}
	r, reason, ok := buildRule("allow", toks, "allow "+body)
	if !ok {
		t.Fatalf("buildRule(%q): %s", body, reason)
	}
	return r
}

func TestMatchExact(t *testing.T) {
	r := mkRule(t, `ls`)
	if !matchRule([]string{"ls"}, "/usr/bin/ls", false, r) {
		t.Error("bare ls should match `ls`")
	}
	if matchRule([]string{"ls", "-la"}, "/usr/bin/ls", false, r) {
		t.Error("`ls` must not match `ls -la`")
	}
}

func TestMatchANY(t *testing.T) {
	cases := []struct {
		rule     string
		argv     []string
		resolved string
		want     bool
	}{
		{`ls ANY`, []string{"ls"}, "/usr/bin/ls", true},        // 0 tokens
		{`ls ANY`, []string{"ls", "-la"}, "/usr/bin/ls", true}, // n tokens
		{`git status ANY`, []string{"git", "status"}, "/usr/bin/git", true},
		{`git status ANY`, []string{"git", "log"}, "/usr/bin/git", false},
		{`ANY status`, []string{"git", "status"}, "/usr/bin/git", true},                   // ANY at head
		{`ANY status`, []string{"status"}, "/usr/bin/status", true},                       // head zero
		{`ddev ANY phpunit`, []string{"ddev", "x", "y", "phpunit"}, "/usr/bin/ddev", true}, // ANY in middle
		{`ddev ANY phpunit`, []string{"ddev", "phpunit"}, "/usr/bin/ddev", true},           // middle zero
		{`ddev ANY phpunit`, []string{"ddev", "phpunit", "x"}, "/usr/bin/ddev", false},     // tail mismatch
	}
	for _, tc := range cases {
		if got := matchRule(tc.argv, tc.resolved, false, mkRule(t, tc.rule)); got != tc.want {
			t.Errorf("rule %q argv %v: got %v want %v", tc.rule, tc.argv, got, tc.want)
		}
	}
}

func TestMatchHAS(t *testing.T) {
	cases := []struct {
		rule     string
		argv     []string
		resolved string
		want     bool
	}{
		{`git reset HAS --hard`, []string{"git", "reset", "--hard"}, "/usr/bin/git", true},
		{`git reset HAS --hard`, []string{"git", "reset", "--soft"}, "/usr/bin/git", false},
		{`git reset HAS --hard`, []string{"git", "status"}, "/usr/bin/git", false}, // prefix mismatch
		{`git HAS --ext-diff`, []string{"git", "-c", "x", "--ext-diff"}, "/usr/bin/git", true},
		{`git HAS -c`, []string{"git", "-c", "core.pager=sh", "status"}, "/usr/bin/git", true},
		{`find HAS -exec`, []string{"find", ".", "-exec", "rm"}, "/usr/bin/find", true},
		{`cmd HAS --output`, []string{"cmd", "--output=/tmp/x"}, "/usr/bin/cmd", true}, // =-split
		{`cmd HAS -f`, []string{"cmd", "-rf"}, "/usr/bin/cmd", true},                   // short bundle
		{`cmd HAS -f`, []string{"cmd", "-r"}, "/usr/bin/cmd", false},
		{`cmd HAS --force`, []string{"cmd", "-force"}, "/usr/bin/cmd", false}, // no bundle for long flag
	}
	for _, tc := range cases {
		if got := matchRule(tc.argv, tc.resolved, false, mkRule(t, tc.rule)); got != tc.want {
			t.Errorf("rule %q argv %v: got %v want %v", tc.rule, tc.argv, got, tc.want)
		}
	}
}

// A literal argument spelled ANY/HAS must be single-quoted, then it is not a keyword.
func TestQuotedKeywordLiteral(t *testing.T) {
	r := mkRule(t, `grep 'ANY'`)
	if !matchRule([]string{"grep", "ANY"}, "/usr/bin/grep", false, r) {
		t.Error("quoted 'ANY' should be a literal token")
	}
	if matchRule([]string{"grep", "foo"}, "/usr/bin/grep", false, r) {
		t.Error("quoted 'ANY' must not behave like the ANY keyword")
	}
}

// Basename canonicalization: a resolved absolute path matches bare-name rules.
func TestMatchCmdTokenBasename(t *testing.T) {
	cases := []struct {
		rule     string
		argv     []string
		resolved string
		want     bool
	}{
		// Path spelling matches bare-name rule via basename.
		{`git status ANY`, []string{"/usr/bin/git", "status"}, "/usr/bin/git", true},
		{`git push ANY`, []string{"/opt/homebrew/bin/git", "push"}, "/opt/homebrew/bin/git", true},
		{`python3 ANY`, []string{"/usr/bin/python3", "-c", "1"}, "/usr/bin/python3", true},
		// Pinned absolute rule must match resolved path exactly.
		{`/usr/bin/python3 ANY`, []string{"/usr/bin/python3", "-c"}, "/usr/bin/python3", true},
		{`/usr/bin/python3 ANY`, []string{"python3", "-c"}, "/opt/homebrew/bin/python3", false},
		// Different resolved paths under the same basename still match bare-name rules.
		{`git push ANY`, []string{"git", "push", "origin"}, "/opt/homebrew/bin/git", true},
	}
	for _, tc := range cases {
		if got := matchRule(tc.argv, tc.resolved, false, mkRule(t, tc.rule)); got != tc.want {
			t.Errorf("rule %q argv %v resolved %q: got %v want %v",
				tc.rule, tc.argv, tc.resolved, got, tc.want)
		}
	}
}

func TestMatchStdinOnly(t *testing.T) {
	toks, _ := parseRuleWords("STDIN python3 ANY")
	r, _, _ := buildRule("deny", toks, "deny STDIN python3 ANY")

	// STDIN rule matches when stdinFed=true
	if !matchRule([]string{"python3", "-c", "1"}, "/usr/bin/python3", true, r) {
		t.Error("STDIN rule should match stdin-fed command")
	}
	// STDIN rule does NOT match when stdinFed=false
	if matchRule([]string{"python3", "-c", "1"}, "/usr/bin/python3", false, r) {
		t.Error("STDIN rule must not match non-stdin-fed command")
	}
}

// Fuzz: the matcher must never panic on arbitrary argv/rule input.
func FuzzMatch(f *testing.F) {
	f.Add("git status ANY", "git status -s")
	f.Add("git HAS --hard", "git reset --hard")
	f.Add("ls", "ls -la")
	f.Fuzz(func(t *testing.T, ruleBody, cmd string) {
		toks, err := parseRuleWords(ruleBody)
		if err != nil {
			return
		}
		r, _, ok := buildRule("allow", toks, ruleBody)
		if !ok {
			return
		}
		argv := strings.Fields(cmd)
		resolved := "/usr/bin/fuzz"
		if len(argv) > 0 {
			resolved = "/usr/bin/" + argv[0]
		}
		matchRule(argv, resolved, false, r) // must not panic
	})
}
