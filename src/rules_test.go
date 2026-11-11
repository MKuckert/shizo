package main

import (
	"os"
	"path/filepath"
	"strings"
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
	attestProject(t, dir, proj, projRules, "off")
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
	attestProject(t, dir, proj, projBody, "off")
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

func TestProjectTrustDirective(t *testing.T) {
	dir := t.TempDir()

	// Absent: defaults to off.
	body := "sandbox nono\nallow ls ANY\n"
	g := writeFile(t, dir, "a/rules", body)
	attestGlobal(t, dir, body)
	if cfg := loadConfig(g, "", installRoot()); cfg.denyAll || cfg.projectTrust != "off" {
		t.Errorf("absent project-trust should default to off: %+v", cfg)
	}

	for _, mode := range []string{"off", "under-cwd"} {
		body := "project-trust " + mode + "\nsandbox nono\nallow ls ANY\n"
		g := writeFile(t, dir, "b/rules", body)
		attestGlobal(t, dir, body)
		if cfg := loadConfig(g, "", installRoot()); cfg.denyAll || cfg.projectTrust != mode {
			t.Errorf("project-trust %s: %+v", mode, cfg)
		}
	}

	for name, body := range map[string]string{
		"unknown value": "project-trust anywhere\nsandbox nono\n",
		"duplicate":     "project-trust off\nproject-trust under-cwd\nsandbox nono\n",
		"after rule":    "sandbox nono\nallow ls ANY\nproject-trust under-cwd\n",
	} {
		g := writeFile(t, dir, "c/rules", body)
		if cfg := loadConfig(g, "", installRoot()); !cfg.denyAll {
			t.Errorf("%s should deny all: %+v", name, cfg)
		}
	}
}

func TestProjectTrustDirectiveIgnoredInProjectTier(t *testing.T) {
	dir := t.TempDir()
	body := "project-trust under-cwd\nsandbox nono\nallow ls ANY\n"
	g := writeFile(t, dir, "rules", body)
	attestGlobal(t, dir, body)
	projDir := t.TempDir()
	projBody := "project-trust under-cwd\ndeny rm ANY\n"
	proj := writeFile(t, projDir, ".shizo", projBody)
	attestProject(t, dir, proj, projBody, "under-cwd")
	cfg := loadConfig(g, proj, installRoot())
	if cfg.denyAll {
		t.Fatal(cfg.denyReason)
	}
	if len(cfg.ignored) != 1 || !strings.Contains(cfg.ignored[0], "project-trust") {
		t.Errorf("a project project-trust line should be ignored and logged: %+v", cfg.ignored)
	}
}

func TestProjectTrustHonoredWithinCwd(t *testing.T) {
	dir := t.TempDir()
	body := "project-trust under-cwd\nsandbox nono\nallow ls ANY\n"
	g := writeFile(t, dir, "rules", body)
	attestGlobal(t, dir, body)

	projDir := t.TempDir()
	abs := filepath.Join(projDir, "node_modules", ".bin")

	for _, line := range []string{"trust node_modules/.bin", "trust " + abs} {
		projBody := line + "\n"
		proj := writeFile(t, projDir, ".shizo", projBody)
		attestProject(t, dir, proj, projBody, "under-cwd")
		cfg := loadConfig(g, proj, installRoot())
		if cfg.denyAll {
			t.Fatalf("%s: %s", line, cfg.denyReason)
		}
		if len(cfg.trusted) != 1 || cfg.trusted[0] != abs {
			t.Errorf("%s: trusted = %+v, want [%s]", line, cfg.trusted, abs)
		}
		if len(cfg.ignored) != 0 {
			t.Errorf("%s: ignored = %+v", line, cfg.ignored)
		}
	}
}

func TestProjectTrustGateOffIgnores(t *testing.T) {
	dir := t.TempDir()
	body := "sandbox nono\nallow ls ANY\n" // gate defaults to off
	g := writeFile(t, dir, "rules", body)
	attestGlobal(t, dir, body)

	projDir := t.TempDir()
	projBody := "trust node_modules/.bin\n" // within cwd, but the gate is off
	proj := writeFile(t, projDir, ".shizo", projBody)
	attestProject(t, dir, proj, projBody, "off")
	cfg := loadConfig(g, proj, installRoot())
	if cfg.denyAll {
		t.Fatal(cfg.denyReason)
	}
	if len(cfg.trusted) != 0 {
		t.Errorf("gate off: trusted = %+v", cfg.trusted)
	}
	if len(cfg.ignored) != 1 {
		t.Errorf("gate off: ignored = %+v", cfg.ignored)
	}
}

func TestProjectTrustEscapesIgnored(t *testing.T) {
	dir := t.TempDir()
	body := "project-trust under-cwd\nsandbox nono\nallow ls ANY\n"
	g := writeFile(t, dir, "rules", body)
	attestGlobal(t, dir, body)

	projDir := t.TempDir()
	cases := []string{
		"trust /tmp/evil",                       // absolute, outside
		"trust ../sibling/bin",                  // relative escape
		"trust node_modules/.bin/../../../evil", // dot-dot escape
	}
	for _, line := range cases {
		projBody := line + "\n"
		proj := writeFile(t, projDir, ".shizo", projBody)
		attestProject(t, dir, proj, projBody, "under-cwd")
		cfg := loadConfig(g, proj, installRoot())
		if cfg.denyAll {
			t.Fatalf("%s: %s", line, cfg.denyReason)
		}
		if len(cfg.trusted) != 0 {
			t.Errorf("%s: escaped dir must not be trusted: %+v", line, cfg.trusted)
		}
		if len(cfg.ignored) != 1 {
			t.Errorf("%s: ignored = %+v", line, cfg.ignored)
		}
	}
}

func TestProjectTrustDotHonorsRoot(t *testing.T) {
	dir := t.TempDir()
	body := "project-trust under-cwd\nsandbox nono\nallow ls ANY\n"
	g := writeFile(t, dir, "rules", body)
	attestGlobal(t, dir, body)

	projDir := t.TempDir()
	projBody := "trust .\n" // the project directory itself stays inside the fence
	proj := writeFile(t, projDir, ".shizo", projBody)
	attestProject(t, dir, proj, projBody, "under-cwd")
	cfg := loadConfig(g, proj, installRoot())
	if cfg.denyAll {
		t.Fatal(cfg.denyReason)
	}
	if len(cfg.trusted) != 1 || cfg.trusted[0] != projDir {
		t.Errorf("trusted = %+v, want [%s]", cfg.trusted, projDir)
	}
}

func TestProjectTrustRelativeProjectPath(t *testing.T) {
	dir := t.TempDir()
	body := "project-trust under-cwd\nsandbox nono\nallow ls ANY\n"
	g := writeFile(t, dir, "rules", body)
	attestGlobal(t, dir, body)

	projDir := t.TempDir()
	projBody := "trust node_modules/.bin\n"
	writeFile(t, projDir, ".shizo", projBody)
	attestProject(t, dir, filepath.Join(projDir, ".shizo"), projBody, "under-cwd")

	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(projDir); err != nil {
		t.Fatal(err)
	}
	cfg := loadConfig(g, ".shizo", installRoot())
	if cfg.denyAll {
		t.Fatal(cfg.denyReason)
	}
	want := filepath.Join(projDir, "node_modules", ".bin")
	if len(cfg.trusted) != 1 || cfg.trusted[0] != want {
		t.Errorf("relative project path: trusted = %+v, want [%s]", cfg.trusted, want)
	}
}

// A project file at the filesystem root (/.shizo) fences against "/"; relative
// and absolute spellings of a root-level dir must both be honored.
func TestProjectTrustRootBaseDir(t *testing.T) {
	for _, line := range []string{"trust bin", "trust /bin"} {
		norm, reason, ok := normalizeTier(line+"\n", true, "under-cwd", "/")
		if !ok {
			t.Fatalf("%s: %s", line, reason)
		}
		if len(norm.trusted) != 1 || norm.trusted[0] != "/bin" {
			t.Errorf("%s: trusted = %+v, want [/bin]", line, norm.trusted)
		}
	}
}
