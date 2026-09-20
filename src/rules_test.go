package main

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile writes content and returns its path.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const validGlobal = `# global
returntype decision
sandbox nono
trust /opt/homebrew/bin
allow git status ANY
deny git push ANY
`

func TestMissingGlobalDenyAll(t *testing.T) {
	dir := t.TempDir()
	cfg := loadConfig(filepath.Join(dir, "nope"), "", installRoot())
	if !cfg.denyAll {
		t.Error("missing global should deny all")
	}
}

func TestMissingProjectGlobalOnly(t *testing.T) {
	dir := t.TempDir()
	g := writeFile(t, dir, "rules", validGlobal)
	attestGlobal(t, dir, validGlobal)
	cfg := loadConfig(g, filepath.Join(dir, "no-project"), installRoot())
	if cfg.denyAll {
		t.Fatal("missing project must not deny all")
	}
	if len(cfg.allowRules) != 1 || len(cfg.denyRules) != 1 {
		t.Errorf("rules = %+v", cfg)
	}
}

func TestMalformedLineDenyAll(t *testing.T) {
	dir := t.TempDir()
	g := writeFile(t, dir, "rules", "wat is this\n")
	cfg := loadConfig(g, "", installRoot())
	if !cfg.denyAll {
		t.Error("malformed line should deny all")
	}
}

func TestProjectLooseningIgnored(t *testing.T) {
	dir := t.TempDir()
	g := writeFile(t, dir, "rules", validGlobal)
	attestGlobal(t, dir, validGlobal)
	projRules := "trust /evil/bin\nsandbox dangerously-none\nreturntype automode\ndeny rm ANY\n"
	proj := writeFile(t, dir, ".shizo", projRules)
	attestProject(t, dir, proj, projRules)
	cfg := loadConfig(g, proj, installRoot())
	if cfg.denyAll {
		t.Fatal("project loosening should be ignored, not deny-all")
	}
	if cfg.sandboxMode != "nono" {
		t.Error("project `sandbox dangerously-none` must be ignored")
	}
	if cfg.returnMode != "decision" {
		t.Error("project `returntype` must be ignored")
	}
	for _, tr := range cfg.trusted {
		if tr == "/evil/bin" {
			t.Error("project `trust` must be ignored")
		}
	}
	if len(cfg.ignored) != 3 {
		t.Errorf("expected 3 ignored directives, got %v", cfg.ignored)
	}
	if len(cfg.denyRules) != 2 { // global push + project rm
		t.Errorf("project deny rule should apply: %+v", cfg.denyRules)
	}
}

func TestReturntype(t *testing.T) {
	dir := t.TempDir()
	// valid automode
	body := "returntype automode\nsandbox nono\nallow ls ANY\n"
	g := writeFile(t, dir, "a/rules", body)
	attestGlobal(t, dir, body)
	if cfg := loadConfig(g, "", installRoot()); cfg.denyAll || cfg.returnMode != "automode" {
		t.Errorf("automode: %+v", cfg)
	}
	// absent => decision
	body = "sandbox nono\nallow ls ANY\n"
	g = writeFile(t, dir, "b/rules", body)
	attestGlobal(t, dir, body)
	if cfg := loadConfig(g, "", installRoot()); cfg.returnMode != "decision" {
		t.Error("absent returntype should default to decision")
	}
	// unknown value => deny all
	g = writeFile(t, dir, "c/rules", "returntype wat\n")
	if cfg := loadConfig(g, "", installRoot()); !cfg.denyAll {
		t.Error("unknown returntype should deny all")
	}
	// duplicate => deny all
	g = writeFile(t, dir, "d/rules", "returntype decision\nreturntype automode\n")
	if cfg := loadConfig(g, "", installRoot()); !cfg.denyAll {
		t.Error("duplicate returntype should deny all")
	}
}

func TestSandbox(t *testing.T) {
	dir := t.TempDir()
	// explicit nono
	body := "sandbox nono\nallow ls ANY\n"
	g := writeFile(t, dir, "a/rules", body)
	attestGlobal(t, dir, body)
	if cfg := loadConfig(g, "", installRoot()); cfg.denyAll || cfg.sandboxMode != "nono" {
		t.Errorf("nono: %+v", cfg)
	}
	// explicit dangerously-none
	body = "sandbox dangerously-none\nallow ls ANY\n"
	g = writeFile(t, dir, "b/rules", body)
	attestGlobal(t, dir, body)
	if cfg := loadConfig(g, "", installRoot()); cfg.denyAll || cfg.sandboxMode != "dangerously-none" {
		t.Errorf("dangerously-none: %+v", cfg)
	}
	// absent => deny all (sandbox is required in the global tier)
	g = writeFile(t, dir, "c/rules", "allow ls ANY\n")
	if cfg := loadConfig(g, "", installRoot()); !cfg.denyAll {
		t.Error("absent sandbox should deny all")
	}
	// unknown value => deny all
	g = writeFile(t, dir, "d/rules", "sandbox wat\n")
	if cfg := loadConfig(g, "", installRoot()); !cfg.denyAll {
		t.Error("unknown sandbox should deny all")
	}
	// duplicate => deny all
	g = writeFile(t, dir, "e/rules", "sandbox nono\nsandbox dangerously-none\n")
	if cfg := loadConfig(g, "", installRoot()); !cfg.denyAll {
		t.Error("duplicate sandbox should deny all")
	}
}

func TestDirectiveAfterRuleDenyAll(t *testing.T) {
	dir := t.TempDir()
	g := writeFile(t, dir, "rules", "allow ls ANY\ntrust /opt/homebrew/bin\n")
	if cfg := loadConfig(g, "", installRoot()); !cfg.denyAll {
		t.Error("directive after a rule should deny all")
	}
}

func TestTrustNeedsAbsolute(t *testing.T) {
	dir := t.TempDir()
	g := writeFile(t, dir, "rules", "trust relative/bin\n")
	if cfg := loadConfig(g, "", installRoot()); !cfg.denyAll {
		t.Error("relative trust dir should deny all")
	}
}

func TestRelativePathRuleDenyAll(t *testing.T) {
	dir := t.TempDir()
	for _, body := range []string{
		"allow ./deploy.sh ANY",
		"deny ../bin/tool ANY",
		"allow sub/run ANY",
	} {
		g := writeFile(t, dir, "rules", "sandbox nono\n"+body+"\n")
		if cfg := loadConfig(g, "", installRoot()); !cfg.denyAll {
			t.Errorf("%q should deny all (relative command path)", body)
		}
	}
}

func TestBadRuleShapeDenyAll(t *testing.T) {
	dir := t.TempDir()
	for _, body := range []string{
		"allow git ANY HAS -c",    // ANY + HAS
		"allow git ANY ANY",       // two ANY
		"allow HAS -c",            // HAS with no command
		"allow git HAS -c status", // token after HAS flag
		"allow STDIN",             // STDIN with no command after it
		"allow git STDIN ANY",     // STDIN in mid-rule position
	} {
		g := writeFile(t, dir, "rules", body+"\n")
		if cfg := loadConfig(g, "", installRoot()); !cfg.denyAll {
			t.Errorf("%q should deny all", body)
		}
	}
}

func TestStdinOnlyParsing(t *testing.T) {
	dir := t.TempDir()
	body := "sandbox nono\ndeny STDIN python3 ANY\n"
	g := writeFile(t, dir, "rules", body)
	attestGlobal(t, dir, body)
	cfg := loadConfig(g, "", installRoot())
	if cfg.denyAll {
		t.Fatal("STDIN rule should parse: " + cfg.denyReason)
	}
	if len(cfg.denyRules) != 1 || !cfg.denyRules[0].stdinOnly {
		t.Errorf("STDIN rule not parsed correctly: %+v", cfg.denyRules)
	}
}

func TestQuotedStdinLiteral(t *testing.T) {
	dir := t.TempDir()
	body := "sandbox nono\nallow tool 'STDIN'\n"
	g := writeFile(t, dir, "rules", body)
	attestGlobal(t, dir, body)
	cfg := loadConfig(g, "", installRoot())
	if cfg.denyAll {
		t.Fatal("quoted 'STDIN' should be a literal: " + cfg.denyReason)
	}
	if len(cfg.allowRules) != 1 || cfg.allowRules[0].stdinOnly {
		t.Error("quoted 'STDIN' must not set stdinOnly")
	}
	if cfg.allowRules[0].tokens[1].text != "STDIN" {
		t.Error("quoted 'STDIN' should be a literal token with text STDIN")
	}
}

func TestAskVerb(t *testing.T) {
	dir := t.TempDir()
	body := "sandbox nono\nask rm HAS -rf\n"
	g := writeFile(t, dir, "rules", body)
	attestGlobal(t, dir, body)
	cfg := loadConfig(g, "", installRoot())
	if cfg.denyAll {
		t.Fatal("ask rule should parse: " + cfg.denyReason)
	}
	if len(cfg.askRules) != 1 || cfg.askRules[0].action != "ask" {
		t.Errorf("ask rule not parsed: %+v", cfg.askRules)
	}
}

func TestStdinDirective(t *testing.T) {
	dir := t.TempDir()
	// valid stdin ask
	body := "stdin ask\nsandbox nono\nallow ls ANY\n"
	g := writeFile(t, dir, "a/rules", body)
	attestGlobal(t, dir, body)
	cfg := loadConfig(g, "", installRoot())
	if cfg.denyAll || cfg.stdinDefault != "ask" {
		t.Errorf("stdin ask: %+v", cfg)
	}
	// duplicate stdin
	g = writeFile(t, dir, "b/rules", "stdin ask\nstdin deny\nsandbox nono\n")
	if cfg := loadConfig(g, "", installRoot()); !cfg.denyAll {
		t.Error("duplicate stdin should deny all")
	}
	// unknown value
	g = writeFile(t, dir, "c/rules", "stdin wat\nsandbox nono\n")
	if cfg := loadConfig(g, "", installRoot()); !cfg.denyAll {
		t.Error("unknown stdin value should deny all")
	}
	// project tier ignores stdin
	body = "sandbox nono\nallow ls ANY\n"
	g = writeFile(t, dir, "d/rules", body)
	attestGlobal(t, dir, body)
	projDir := t.TempDir()
	projBody := "stdin deny\ndeny rm ANY\n"
	proj := writeFile(t, projDir, ".shizo", projBody)
	attestProject(t, dir, proj, projBody)
	cfg = loadConfig(g, proj, installRoot())
	if cfg.denyAll {
		t.Fatal("project stdin should be ignored: " + cfg.denyReason)
	}
	if cfg.stdinDefault == "deny" {
		t.Error("project stdin directive must not take effect")
	}
	if len(cfg.ignored) != 1 {
		t.Errorf("expected 1 ignored directive, got %v", cfg.ignored)
	}
}
