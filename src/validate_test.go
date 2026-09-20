package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func countLevels(fs []finding) (errs, warns int) {
	for _, f := range fs {
		if f.level == levelError {
			errs++
		} else {
			warns++
		}
	}
	return
}

func hasMsg(fs []finding, sub string) bool {
	for _, f := range fs {
		if strings.Contains(f.msg, sub) {
			return true
		}
	}
	return false
}

func findingAtLine(fs []finding, line int) (finding, bool) {
	for _, f := range fs {
		if f.line == line {
			return f, true
		}
	}
	return finding{}, false
}

func TestValidateClean(t *testing.T) {
	dir := t.TempDir() // a real, existing trust dir -> no trust warning
	body := "returntype decision\nsandbox nono\ntrust " + dir + "\nallow git status ANY\ndeny git push ANY\n"
	fs := validateFile("rules", body, false)
	if errs, warns := countLevels(fs); errs != 0 || warns != 0 {
		t.Errorf("clean config produced findings: %+v", fs)
	}
}

func TestValidateRequiresSandbox(t *testing.T) {
	// Global tier without a sandbox directive is an error, mirroring loadConfig.
	body := "trust /opt/x\nallow ls ANY\n"
	fs := validateFile("rules", body, false)
	if !hasMsg(fs, "must declare a sandbox directive") {
		t.Errorf("expected missing-sandbox error: %+v", fs)
	}
	if errs, _ := countLevels(fs); errs != 1 {
		t.Errorf("missing sandbox should be an error: %+v", fs)
	}
	// The loader agrees: same body denies all.
	dir := t.TempDir()
	g := writeFile(t, dir, "rules", body)
	if cfg := loadConfig(g, "", installRoot()); !cfg.denyAll {
		t.Error("missing sandbox should deny all")
	}
	// Project tier does not require it (directives are global-only there).
	fs = validateFile(".shizo", body, true)
	if hasMsg(fs, "must declare a sandbox directive") {
		t.Errorf("project tier must not require sandbox: %+v", fs)
	}
}

func TestValidateReportsAllErrors(t *testing.T) {
	// Two malformed lines (3 and 5); lines 1, 2 and 4 are fine.
	body := "sandbox nono\nallow git status ANY\nwat is this\nallow ls ANY\nallow git ANY ANY\n"
	fs := validateFile("rules", body, false)
	errs, _ := countLevels(fs)
	if errs != 2 {
		t.Fatalf("want 2 errors, got %d: %+v", errs, fs)
	}
	if _, ok := findingAtLine(fs, 3); !ok {
		t.Error("missing error on line 3")
	}
	if _, ok := findingAtLine(fs, 5); !ok {
		t.Error("missing error on line 5")
	}
}

func TestValidateDeadRules(t *testing.T) {
	body := "sandbox nono\nallow eval ANY\ndeny env ANY\nallow cd ANY\n"
	fs := validateFile("rules", body, false)
	if !hasMsg(fs, "unconditionally denied") {
		t.Error("expected uncond-deny dead-rule warning")
	}
	if !hasMsg(fs, "safe builtin") {
		t.Error("expected safe-builtin dead-rule warning")
	}
	if errs, _ := countLevels(fs); errs != 0 {
		t.Errorf("dead rules should warn, not error: %+v", fs)
	}
}

func TestValidateShadow(t *testing.T) {
	body := "sandbox nono\ndeny git push ANY\nallow git push --force\nallow git status ANY\n"
	fs := validateFile("rules", body, false)
	if !hasMsg(fs, "fully shadowed") {
		t.Errorf("expected shadow warning: %+v", fs)
	}
	// `allow git status ANY` must NOT be flagged (no false positive).
	for _, f := range fs {
		if strings.Contains(f.msg, "git status") {
			t.Errorf("false-positive shadow warning: %+v", f)
		}
	}
}

func TestValidateIgnoredDirectiveProjectTier(t *testing.T) {
	body := "trust /opt/x\nreturntype automode\ndeny rm ANY\n"
	fs := validateFile(".shizo", body, true)
	if !hasMsg(fs, "directive ignored in project tier") {
		t.Errorf("expected ignored-directive warnings: %+v", fs)
	}
	if errs, _ := countLevels(fs); errs != 0 {
		t.Errorf("ignored directives should warn, not error: %+v", fs)
	}
}

func TestValidateMissingTrustDir(t *testing.T) {
	body := "sandbox nono\ntrust /definitely/not/here/xyz\nallow ls ANY\n"
	fs := validateFile("rules", body, false)
	if !hasMsg(fs, "does not exist") {
		t.Errorf("expected missing trust-dir warning: %+v", fs)
	}
	if f, ok := findingAtLine(fs, 2); !ok || !strings.Contains(f.msg, "not/here") {
		t.Errorf("trust warning should anchor to line 2: %+v", fs)
	}
}

func TestDenyShadowsAllow(t *testing.T) {
	lit := func(s string) ruleToken { return ruleToken{text: s, kind: tokLiteral} }
	any := ruleToken{kind: tokANY}
	has := ruleToken{kind: tokHAS}
	mk := func(toks ...ruleToken) rule { return rule{tokens: toks} }

	cases := []struct {
		name string
		a, d rule
		want bool
	}{
		{"prefix+any shadows", mk(lit("git"), lit("push"), lit("--force")), mk(lit("git"), lit("push"), any), true},
		{"different subcmd", mk(lit("git"), lit("status"), any), mk(lit("git"), lit("push"), any), false},
		{"exact literal", mk(lit("rm"), lit("-rf")), mk(lit("rm"), lit("-rf")), true},
		{"closed deny, longer allow", mk(lit("rm"), lit("-rf")), mk(lit("rm")), false},
		{"deny ANY shadows all", mk(lit("anything"), any), mk(any), true},
		{"has never shadows", mk(lit("git"), lit("push")), mk(lit("git"), has, lit("-c")), false},
		// basename canonicalization
		{"bare deny shadows abs allow", mk(lit("/usr/bin/git"), lit("push"), any), mk(lit("git"), lit("push"), any), true},
		{"abs deny cannot shadow bare allow", mk(lit("git"), lit("push"), any), mk(lit("/usr/bin/git"), lit("push"), any), false},
		{"abs deny cannot shadow diff abs", mk(lit("/opt/bin/git"), lit("push"), any), mk(lit("/usr/bin/git"), lit("push"), any), false},
		{"abs deny same path shadows", mk(lit("/usr/bin/git"), lit("push"), any), mk(lit("/usr/bin/git"), lit("push"), any), true},
	}
	for _, c := range cases {
		if got := denyShadowsAllow(c.a, c.d); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestRunValidateExplicitPath(t *testing.T) {
	dir := t.TempDir()
	good := writeFile(t, dir, "good", "sandbox nono\nallow ls ANY\n")
	var out, errOut bytes.Buffer
	if rc := runValidate([]string{good}, &out, &errOut); rc != 0 {
		t.Errorf("clean file should exit 0, got %d: %s", rc, out.String())
	}

	bad := writeFile(t, dir, "bad", "sandbox nono\nallow ls ANY\nwat is this\n")
	out.Reset()
	if rc := runValidate([]string{bad}, &out, &errOut); rc != 1 {
		t.Errorf("bad file should exit 1, got %d: %s", rc, out.String())
	}
	if !strings.Contains(out.String(), "bad:3: error:") {
		t.Errorf("expected file:line: error output, got %q", out.String())
	}
}

func TestRunValidateProjectFlagRoutes(t *testing.T) {
	dir := t.TempDir()
	f := writeFile(t, dir, "proj", "sandbox nono\ntrust /opt/x\ndeny rm ANY\n")
	var out, errOut bytes.Buffer
	// As global tier the trust line is valid (dir missing -> warning, exit 0).
	if rc := runValidate([]string{f}, &out, &errOut); rc != 0 {
		t.Errorf("global-tier trust should not error: rc=%d %s", rc, out.String())
	}
	// With --project the trust line is an ignored-directive warning.
	out.Reset()
	if rc := runValidate([]string{"--project", f}, &out, &errOut); rc != 0 {
		t.Errorf("project-tier should exit 0: rc=%d %s", rc, out.String())
	}
	if !strings.Contains(out.String(), "ignored in project tier") {
		t.Errorf("expected ignored-directive warning, got %q", out.String())
	}
}

func TestRunValidateUnknownFlag(t *testing.T) {
	var out, errOut bytes.Buffer
	if rc := runValidate([]string{"--bogus"}, &out, &errOut); rc != 2 {
		t.Errorf("unknown flag should exit 2, got %d", rc)
	}
}

func TestRunValidateInstalledMissingGlobal(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SHIZO_DIR", root) // no global/rules under it
	var out, errOut bytes.Buffer
	rc := runValidate(nil, &out, &errOut)
	if rc != 1 {
		t.Errorf("missing global should exit 1, got %d: %s", rc, out.String())
	}
	if !strings.Contains(out.String(), "unreadable") {
		t.Errorf("expected unreadable global error, got %q", out.String())
	}
}

func TestValidateStdinDirectiveIgnoredInProject(t *testing.T) {
	body := "stdin deny\ndeny rm ANY\n"
	fs := validateFile(".shizo", body, true)
	if !hasMsg(fs, "directive ignored in project tier") {
		t.Errorf("expected ignored stdin directive warning: %+v", fs)
	}
}

func TestValidateDeadStdinRules(t *testing.T) {
	body := "stdin deny\nsandbox nono\nallow STDIN grep ANY\n"
	fs := validateFile("rules", body, false)
	if !hasMsg(fs, "STDIN allow rule has no effect") {
		t.Errorf("expected dead STDIN allow warning under stdin deny: %+v", fs)
	}
}

func TestValidateAskVerb(t *testing.T) {
	dir := t.TempDir()
	body := "sandbox nono\ntrust " + dir + "\nask rm HAS -rf\nallow rm ANY\n"
	fs := validateFile("rules", body, false)
	if errs, _ := countLevels(fs); errs != 0 {
		t.Errorf("ask verb should be valid: %+v", fs)
	}
}

func TestCheckTrustDir(t *testing.T) {
	dir := t.TempDir()
	if _, ok := checkTrustDir(dir); !ok {
		t.Error("existing dir should pass")
	}
	if _, ok := checkTrustDir(filepath.Join(dir, "nope")); ok {
		t.Error("missing dir should fail")
	}
	file := writeFile(t, dir, "afile", "x")
	if msg, ok := checkTrustDir(file); ok || !strings.Contains(msg, "not a directory") {
		t.Errorf("file should fail as non-dir: %q", msg)
	}
}
