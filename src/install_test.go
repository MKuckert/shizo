package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeWrapper installs a stub shizo.sh in a temp dir and points installExe at
// a sibling binary path, so runInstall resolves a real wrapper.
func fakeWrapper(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	wrapper := filepath.Join(dir, "shizo.sh")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := installExe
	installExe = func() (string, error) { return filepath.Join(dir, "shizo"), nil }
	t.Cleanup(func() { installExe = old })
	return wrapper
}

func runInstallIn(t *testing.T, cwd string, args ...string) (int, string, string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	var out, errb bytes.Buffer
	code := runInstall(args, &out, &errb)
	return code, out.String(), errb.String()
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	return m
}

// bashCommands returns the command strings registered under the Bash matcher.
func bashCommands(t *testing.T, m map[string]any) []string {
	t.Helper()
	hooks, _ := m["hooks"].(map[string]any)
	pre, _ := hooks["PreToolUse"].([]any)
	var got []string
	for _, e := range pre {
		em, _ := e.(map[string]any)
		if em["matcher"] != "Bash" {
			continue
		}
		list, _ := em["hooks"].([]any)
		for _, h := range list {
			hm, _ := h.(map[string]any)
			s, _ := hm["command"].(string)
			got = append(got, s)
		}
	}
	return got
}

func TestInstallClaudeHook(t *testing.T) {
	cases := []struct {
		name     string
		existing string // settings.json content; "" = no file
		args     []string
		wantCode int
		wantCmds int    // shizo.sh entries under the Bash matcher
		wantOut  string // substring of stdout
	}{
		{name: "fresh file", args: []string{"claude-hook"}, wantCmds: 1, wantOut: "installed hook"},
		{
			name:     "preserves unrelated keys",
			existing: `{"model":"opus","hooks":{"PostToolUse":[]}}`,
			args:     []string{"claude-hook"},
			wantCmds: 1, wantOut: "installed hook",
		},
		{
			name:     "appends to existing Bash matcher",
			existing: `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"/other.sh"}]}]}}`,
			args:     []string{"claude-hook"},
			wantCmds: 1, wantOut: "installed hook",
		},
		{
			name:     "idempotent",
			existing: `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"{{WRAPPER}}"}]}]}}`,
			args:     []string{"claude-hook"},
			wantCmds: 1, wantOut: "already installed",
		},
		{
			name:     "stale wrapper path updated in place",
			existing: `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"/old/root/shizo.sh"}]}]}}`,
			args:     []string{"claude-hook"},
			wantCmds: 1, wantOut: "installed hook",
		},
		{
			name:     "non-Bash matcher left alone",
			existing: `{"hooks":{"PreToolUse":[{"matcher":"Write","hooks":[{"type":"command","command":"/w.sh"}]}]}}`,
			args:     []string{"claude-hook"},
			wantCmds: 1, wantOut: "installed hook",
		},
		{
			name:     "non-object hooks refused",
			existing: `{"hooks":"disabled"}`,
			args:     []string{"claude-hook"},
			wantCode: 1,
		},
		{
			name:     "non-array PreToolUse refused",
			existing: `{"hooks":{"PreToolUse":{"matcher":"Bash"}}}`,
			args:     []string{"claude-hook"},
			wantCode: 1,
		},
		{
			name:     "non-array matcher hooks refused",
			existing: `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":"nope"}]}}`,
			args:     []string{"claude-hook"},
			wantCode: 1,
		},
		{
			name:     "malformed JSON refused",
			existing: `{not json`,
			args:     []string{"claude-hook"},
			wantCode: 1,
		},
		{name: "missing target", args: nil, wantCode: 2},
		{name: "wrong target", args: []string{"nope"}, wantCode: 2},
		{name: "extra positional", args: []string{"claude-hook", "extra"}, wantCode: 2},
		{name: "mutually exclusive", args: []string{"claude-hook", "--local", "--global"}, wantCode: 2},
		{name: "unknown flag", args: []string{"claude-hook", "--nope"}, wantCode: 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wrapper := fakeWrapper(t)
			cwd := t.TempDir()
			path := filepath.Join(cwd, ".claude", "settings.json")
			existing := strings.ReplaceAll(tc.existing, "{{WRAPPER}}", wrapper)
			if existing != "" {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			code, out, _ := runInstallIn(t, cwd, tc.args...)
			if code != tc.wantCode {
				t.Fatalf("exit = %d, want %d (out=%q)", code, tc.wantCode, out)
			}
			if tc.wantCode != 0 {
				if existing != "" {
					b, _ := os.ReadFile(path)
					if string(b) != existing {
						t.Errorf("settings file was modified on failure")
					}
				}
				return
			}
			if !strings.Contains(out, tc.wantOut) {
				t.Errorf("stdout = %q, want substring %q", out, tc.wantOut)
			}

			m := readJSON(t, path)
			var n int
			for _, c := range bashCommands(t, m) {
				if strings.HasSuffix(c, "shizo.sh") {
					n++
				}
			}
			if n != tc.wantCmds {
				t.Errorf("shizo.sh hooks = %d, want %d (%v)", n, tc.wantCmds, bashCommands(t, m))
			}

			switch tc.name {
			case "preserves unrelated keys":
				if m["model"] != "opus" {
					t.Errorf("unrelated key lost: %v", m)
				}
			case "appends to existing Bash matcher":
				if got := bashCommands(t, m); len(got) != 2 || got[0] != "/other.sh" || got[1] != wrapper {
					t.Errorf("Bash hooks = %v", got)
				}
			case "stale wrapper path updated in place":
				if got := bashCommands(t, m); len(got) != 1 || got[0] != wrapper {
					t.Errorf("Bash hooks = %v, want only the fresh wrapper path", got)
				}
			case "non-Bash matcher left alone":
				hooks, _ := m["hooks"].(map[string]any)
				pre, _ := hooks["PreToolUse"].([]any)
				if len(pre) != 2 {
					t.Fatalf("PreToolUse = %v, want the Write entry plus a new Bash entry", pre)
				}
				if first, _ := pre[0].(map[string]any); first["matcher"] != "Write" {
					t.Errorf("Write matcher clobbered: %v", pre[0])
				}
			}
		})
	}
}

func TestInstallTargetPaths(t *testing.T) {
	for _, tc := range []struct {
		flag, rel string
	}{
		{"--project", ".claude/settings.json"},
		{"--local", ".claude/settings.local.json"},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			fakeWrapper(t)
			cwd := t.TempDir()
			if code, _, errs := runInstallIn(t, cwd, "claude-hook", tc.flag); code != 0 {
				t.Fatalf("exit = %d: %s", code, errs)
			}
			if _, err := os.Stat(filepath.Join(cwd, filepath.FromSlash(tc.rel))); err != nil {
				t.Errorf("%s not written: %v", tc.rel, err)
			}
		})
	}

	t.Run("--global", func(t *testing.T) {
		fakeWrapper(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		if code, _, errs := runInstallIn(t, t.TempDir(), "claude-hook", "--global"); code != 0 {
			t.Fatalf("exit = %d: %s", code, errs)
		}
		if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); err != nil {
			t.Errorf("global settings not written: %v", err)
		}
	})
}

// A missing wrapper must fail rather than register a dangling hook command.
func TestInstallMissingWrapper(t *testing.T) {
	dir := t.TempDir()
	old := installExe
	installExe = func() (string, error) { return filepath.Join(dir, "shizo"), nil }
	t.Cleanup(func() { installExe = old })

	cwd := t.TempDir()
	code, _, errs := runInstallIn(t, cwd, "claude-hook")
	if code != 1 || !strings.Contains(errs, "wrapper not found") {
		t.Fatalf("exit = %d, stderr = %q", code, errs)
	}
	if _, err := os.Stat(filepath.Join(cwd, ".claude", "settings.json")); err == nil {
		t.Error("settings file written despite missing wrapper")
	}
}

func TestInstallHelp(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runInstall([]string{"--help"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out.String(), "shizo install claude-hook") {
		t.Errorf("stdout = %q", out.String())
	}
}
