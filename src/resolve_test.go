package main

import (
	"os"
	"path/filepath"
	"testing"
)

func mkExe(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestResolveSafeBuiltin(t *testing.T) {
	for _, b := range []string{"cd", "echo", "printf", "test", "["} {
		if res := resolveCommand(b, nil, "/", ""); !res.builtin {
			t.Errorf("%q should be a safe builtin", b)
		}
	}
}

func TestResolveTrusted(t *testing.T) {
	trusted := t.TempDir()
	mkExe(t, trusted, "git")
	res := resolveCommand("git", []string{trusted}, "/", trusted)
	if res.err != "" || res.path != filepath.Join(trusted, "git") {
		t.Errorf("expected resolve in trusted dir, got %+v", res)
	}
}

func TestResolveUntrusted(t *testing.T) {
	dir := t.TempDir()
	mkExe(t, dir, "git")
	res := resolveCommand("git", []string{"/some/other/dir"}, "/", dir)
	if res.err == "" {
		t.Error("binary in untrusted dir should be denied")
	}
}

// A shadow binary earlier in PATH is found first and (being untrusted) denies.
func TestResolveShadow(t *testing.T) {
	shadow := t.TempDir()
	real := t.TempDir()
	mkExe(t, shadow, "git")
	mkExe(t, real, "git")
	path := shadow + string(os.PathListSeparator) + real
	res := resolveCommand("git", []string{real}, "/", path) // only `real` trusted
	if res.err == "" {
		t.Errorf("shadowed git should deny, got %+v", res)
	}
}

func TestResolveNotFound(t *testing.T) {
	res := resolveCommand("definitely-not-a-real-binary-xyz", nil, "/", t.TempDir())
	if res.err == "" {
		t.Error("missing binary should deny")
	}
}

func TestResolveSlashRelative(t *testing.T) {
	cwd := t.TempDir()
	mkExe(t, cwd, "script.sh")
	// cwd not trusted -> deny
	res := resolveCommand("./script.sh", nil, cwd, "")
	if res.err == "" {
		t.Error("./script.sh in untrusted cwd should deny")
	}
	// cwd trusted -> allow
	res = resolveCommand("./script.sh", []string{cwd}, cwd, "")
	if res.err != "" {
		t.Errorf("./script.sh in trusted cwd should resolve, got %+v", res)
	}
}
