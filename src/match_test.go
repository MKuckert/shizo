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
	if !matchRule([]string{"ls"}, r) {
		t.Error("bare ls should match `ls`")
	}
	if matchRule([]string{"ls", "-la"}, r) {
		t.Error("`ls` must not match `ls -la`")
	}
}

func TestMatchANY(t *testing.T) {
	cases := []struct {
		rule string
		argv []string
		want bool
	}{
		{`ls ANY`, []string{"ls"}, true},        // 0 tokens
		{`ls ANY`, []string{"ls", "-la"}, true}, // n tokens
		{`git status ANY`, []string{"git", "status"}, true},
		{`git status ANY`, []string{"git", "log"}, false},
		{`ANY status`, []string{"git", "status"}, true},                   // ANY at head
		{`ANY status`, []string{"status"}, true},                          // head zero
		{`ddev ANY phpunit`, []string{"ddev", "x", "y", "phpunit"}, true}, // ANY in middle
		{`ddev ANY phpunit`, []string{"ddev", "phpunit"}, true},           // middle zero
		{`ddev ANY phpunit`, []string{"ddev", "phpunit", "x"}, false},     // tail mismatch
	}
	for _, tc := range cases {
		if got := matchRule(tc.argv, mkRule(t, tc.rule)); got != tc.want {
			t.Errorf("rule %q argv %v: got %v want %v", tc.rule, tc.argv, got, tc.want)
		}
	}
}

func TestMatchHAS(t *testing.T) {
	cases := []struct {
		rule string
		argv []string
		want bool
	}{
		{`git reset HAS --hard`, []string{"git", "reset", "--hard"}, true},
		{`git reset HAS --hard`, []string{"git", "reset", "--soft"}, false},
		{`git reset HAS --hard`, []string{"git", "status"}, false}, // prefix mismatch
		{`git HAS --ext-diff`, []string{"git", "-c", "x", "--ext-diff"}, true},
		{`git HAS -c`, []string{"git", "-c", "core.pager=sh", "status"}, true},
		{`find HAS -exec`, []string{"find", ".", "-exec", "rm"}, true},
		{`cmd HAS --output`, []string{"cmd", "--output=/tmp/x"}, true}, // =-split
		{`cmd HAS -f`, []string{"cmd", "-rf"}, true},                   // short bundle
		{`cmd HAS -f`, []string{"cmd", "-r"}, false},
		{`cmd HAS --force`, []string{"cmd", "-force"}, false}, // no bundle for long flag
	}
	for _, tc := range cases {
		if got := matchRule(tc.argv, mkRule(t, tc.rule)); got != tc.want {
			t.Errorf("rule %q argv %v: got %v want %v", tc.rule, tc.argv, got, tc.want)
		}
	}
}

// A literal argument spelled ANY/HAS must be single-quoted, then it is not a keyword.
func TestQuotedKeywordLiteral(t *testing.T) {
	r := mkRule(t, `grep 'ANY'`)
	if !matchRule([]string{"grep", "ANY"}, r) {
		t.Error("quoted 'ANY' should be a literal token")
	}
	if matchRule([]string{"grep", "foo"}, r) {
		t.Error("quoted 'ANY' must not behave like the ANY keyword")
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
		matchRule(strings.Fields(cmd), r) // must not panic
	})
}
