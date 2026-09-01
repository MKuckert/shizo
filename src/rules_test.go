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
trust /opt/homebrew/bin
allow git status ANY
deny git push ANY
`

func TestMissingGlobalDenyAll(t *testing.T) {
	dir := t.TempDir()
	cfg := loadConfig(filepath.Join(dir, "nope"), "")
	if !cfg.denyAll {
		t.Error("missing global should deny all")
	}
}

func TestMissingProjectGlobalOnly(t *testing.T) {
	dir := t.TempDir()
	g := writeFile(t, dir, "rules", validGlobal)
	cfg := loadConfig(g, filepath.Join(dir, "no-project"))
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
	cfg := loadConfig(g, "")
	if !cfg.denyAll {
		t.Error("malformed line should deny all")
	}
}

func TestProjectLooseningIgnored(t *testing.T) {
	dir := t.TempDir()
	g := writeFile(t, dir, "rules", validGlobal)
	proj := writeFile(t, dir, ".claude-bash-rules",
		"trust /evil/bin\nallow unsandboxed\nreturntype automode\ndeny rm ANY\n")
	cfg := loadConfig(g, proj)
	if cfg.denyAll {
		t.Fatal("project loosening should be ignored, not deny-all")
	}
	if cfg.allowUnsandboxed {
		t.Error("project `allow unsandboxed` must be ignored")
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
	g := writeFile(t, dir, "a/rules", "returntype automode\nallow ls ANY\n")
	if cfg := loadConfig(g, ""); cfg.denyAll || cfg.returnMode != "automode" {
		t.Errorf("automode: %+v", cfg)
	}
	// absent => decision
	g = writeFile(t, dir, "b/rules", "allow ls ANY\n")
	if cfg := loadConfig(g, ""); cfg.returnMode != "decision" {
		t.Error("absent returntype should default to decision")
	}
	// unknown value => deny all
	g = writeFile(t, dir, "c/rules", "returntype wat\n")
	if cfg := loadConfig(g, ""); !cfg.denyAll {
		t.Error("unknown returntype should deny all")
	}
	// duplicate => deny all
	g = writeFile(t, dir, "d/rules", "returntype decision\nreturntype automode\n")
	if cfg := loadConfig(g, ""); !cfg.denyAll {
		t.Error("duplicate returntype should deny all")
	}
}

func TestDirectiveAfterRuleDenyAll(t *testing.T) {
	dir := t.TempDir()
	g := writeFile(t, dir, "rules", "allow ls ANY\ntrust /opt/homebrew/bin\n")
	if cfg := loadConfig(g, ""); !cfg.denyAll {
		t.Error("directive after a rule should deny all")
	}
}

func TestTrustNeedsAbsolute(t *testing.T) {
	dir := t.TempDir()
	g := writeFile(t, dir, "rules", "trust relative/bin\n")
	if cfg := loadConfig(g, ""); !cfg.denyAll {
		t.Error("relative trust dir should deny all")
	}
}

func TestBadRuleShapeDenyAll(t *testing.T) {
	dir := t.TempDir()
	for _, body := range []string{
		"allow git ANY HAS -c",    // ANY + HAS
		"allow git ANY ANY",       // two ANY
		"allow HAS -c",            // HAS with no command
		"allow git HAS -c status", // token after HAS flag
	} {
		g := writeFile(t, dir, "rules", body+"\n")
		if cfg := loadConfig(g, ""); !cfg.denyAll {
			t.Errorf("%q should deny all", body)
		}
	}
}
