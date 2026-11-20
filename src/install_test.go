package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// fakeEngine points installExe at a binary path in an empty temp dir, so the
// install materializes its files there. Returns the engine dir.
func fakeEngine(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := installExe
	installExe = func() (string, error) { return filepath.Join(dir, "shizo"), nil }
	t.Cleanup(func() { installExe = old })
	return dir
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
			engine := fakeEngine(t)
			wrapper := filepath.Join(engine, "shizo.sh")
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
			fakeEngine(t)
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
		fakeEngine(t)
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

// A missing wrapper is written from the embedded source (executable) before
// the hook is registered — no source file on disk is required.
func TestInstallMaterializesWrapper(t *testing.T) {
	engine := fakeEngine(t)
	wrapper := filepath.Join(engine, "shizo.sh")
	cwd := t.TempDir()

	code, out, errb := runInstallIn(t, cwd, "claude-hook")
	if code != 0 || !strings.Contains(out, "installed hook") {
		t.Fatalf("exit = %d out=%q err=%q", code, out, errb)
	}
	got, err := os.ReadFile(wrapper)
	if err != nil {
		t.Fatalf("wrapper not written: %v", err)
	}
	if !bytes.Equal(got, embeddedWrapper) {
		t.Errorf("wrapper content differs from the embedded source (%d bytes)", len(got))
	}
	if fi, err := os.Stat(wrapper); err != nil || fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("wrapper mode = %v, want executable", fi.Mode())
	}

	m := readJSON(t, filepath.Join(cwd, ".claude", "settings.json"))
	if got := bashCommands(t, m); len(got) != 1 || got[0] != wrapper {
		t.Errorf("Bash hooks = %v, want [%s]", got, wrapper)
	}
}

// A restrictive umask must not strip the exec bit from the materialized
// wrapper: WriteFile's mode is umask-masked, so install chmods it back.
func TestInstallMaterializesWrapperUmask(t *testing.T) {
	umask := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(umask) })

	engine := fakeEngine(t)
	wrapper := filepath.Join(engine, "shizo.sh")
	code, out, errb := runInstallIn(t, t.TempDir(), "claude-hook")
	if code != 0 || !strings.Contains(out, "installed hook") {
		t.Fatalf("exit = %d out=%q err=%q", code, out, errb)
	}
	if fi, err := os.Stat(wrapper); err != nil || fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("wrapper mode = %v, %v; want executable despite umask 077", fi.Mode(), err)
	}
}

// A wrapper that differs from the embedded source is refused, never
// overwritten: an operator may have patched it.
func TestInstallDivergentWrapperRefused(t *testing.T) {
	engine := fakeEngine(t)
	wrapper := filepath.Join(engine, "shizo.sh")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\n# patched\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()

	code, _, errb := runInstallIn(t, cwd, "claude-hook")
	if code != 1 || !strings.Contains(errb, "differs from the wrapper") {
		t.Fatalf("exit = %d, stderr = %q", code, errb)
	}
	if b, _ := os.ReadFile(wrapper); string(b) != "#!/bin/sh\n# patched\n" {
		t.Errorf("divergent wrapper was modified: %q", b)
	}
	if _, err := os.Stat(filepath.Join(cwd, ".claude", "settings.json")); err == nil {
		t.Error("settings file written despite divergent wrapper")
	}
}

// The extension is copied as a regular file (never a symlink) with the
// rendered content (the materialized wrapper path written in); re-running is
// a no-op.
func TestInstallPiExtension(t *testing.T) {
	engine := fakeEngine(t)
	want, err := renderExtension(embeddedPiExtension, filepath.Join(engine, "shizo.sh"))
	if err != nil {
		t.Fatal(err)
	}
	proj, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)

	cases := []struct {
		name, target string
		args         []string
	}{
		{"project default", filepath.Join(proj, ".pi", "extensions", "shizo.ts"), []string{"pi-extension"}},
		{"project flag", filepath.Join(proj, ".pi", "extensions", "shizo.ts"), []string{"pi-extension", "--project"}},
		{"global", filepath.Join(home, ".pi", "agent", "extensions", "shizo.ts"), []string{"pi-extension", "--global"}},
	}
	for _, c := range cases {
		os.RemoveAll(filepath.Join(proj, ".pi"))
		os.RemoveAll(filepath.Join(home, ".pi"))
		code, out, errb := runInstallIn(t, proj, c.args...)
		if code != 0 || !strings.Contains(out, "installed pi extension") {
			t.Fatalf("%s: code=%d out=%q err=%q", c.name, code, out, errb)
		}
		fi, err := os.Lstat(c.target)
		if err != nil || fi.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("%s: target = %v, %v; want a regular file", c.name, fi.Mode(), err)
		}
		if fi.Mode().Perm() != 0o644 {
			t.Errorf("%s: mode = %v, want 0644", c.name, fi.Mode())
		}
		if got, err := os.ReadFile(c.target); err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s: content differs from the rendered extension: %v", c.name, err)
		}
		code, out, _ = runInstallIn(t, proj, c.args...)
		if code != 0 || !strings.Contains(out, "already installed") {
			t.Errorf("%s rerun: code=%d out=%q", c.name, code, out)
		}
	}
}

// The extension spawns the wrapper on every bash call, so a pi install
// materializes it next to the binary — including when the extension itself is
// already installed (a bare release binary must self-install completely).
func TestInstallPiExtensionMaterializesWrapper(t *testing.T) {
	engine := fakeEngine(t)
	wrapper := filepath.Join(engine, "shizo.sh")
	proj := t.TempDir()
	t.Setenv("HOME", t.TempDir())

	code, out, errb := runInstallIn(t, proj, "pi-extension")
	if code != 0 || !strings.Contains(out, "installed pi extension") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errb)
	}
	assertWrapper(t, wrapper)

	// Re-run: the extension is already installed, but the wrapper must still
	// be ensured.
	if err := os.Remove(wrapper); err != nil {
		t.Fatal(err)
	}
	code, out, _ = runInstallIn(t, proj, "pi-extension")
	if code != 0 || !strings.Contains(out, "already installed") {
		t.Fatalf("rerun: code=%d out=%q", code, out)
	}
	assertWrapper(t, wrapper)
}

// assertWrapper checks the materialized wrapper matches the embedded source
// and is executable.
func assertWrapper(t *testing.T, path string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("wrapper not written: %v", err)
	}
	if !bytes.Equal(got, embeddedWrapper) {
		t.Errorf("wrapper content differs from the embedded source (%d bytes)", len(got))
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("wrapper mode = %v, %v; want executable", fi.Mode(), err)
	}
}

// A divergent wrapper blocks the pi install too (ensureWrapper runs first):
// exit 1 and no extension written.
func TestInstallPiExtensionDivergentWrapperRefused(t *testing.T) {
	engine := fakeEngine(t)
	wrapper := filepath.Join(engine, "shizo.sh")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\n# patched\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	proj := t.TempDir()
	t.Setenv("HOME", t.TempDir())

	code, _, errb := runInstallIn(t, proj, "pi-extension")
	if code != 1 || !strings.Contains(errb, "differs from the wrapper") {
		t.Fatalf("exit = %d, stderr = %q", code, errb)
	}
	if _, err := os.Stat(filepath.Join(proj, ".pi", "extensions", "shizo.ts")); err == nil {
		t.Error("extension written despite divergent wrapper")
	}
}

// If the binary moved since a previous install, the installed extension's
// wrapper path is updated in place rather than refused; a hand edit is not a
// previous render and is still refused.
func TestInstallPiExtensionUpdatesMovedBinary(t *testing.T) {
	fakeEngine(t) // first root
	proj := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	target := filepath.Join(proj, ".pi", "extensions", "shizo.ts")

	code, out, errb := runInstallIn(t, proj, "pi-extension")
	if code != 0 || !strings.Contains(out, "installed pi extension") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errb)
	}

	engineB := fakeEngine(t) // installExe now points at a second root
	want, err := renderExtension(embeddedPiExtension, filepath.Join(engineB, "shizo.sh"))
	if err != nil {
		t.Fatal(err)
	}

	code, out, errb = runInstallIn(t, proj, "pi-extension")
	if code != 0 || !strings.Contains(out, "installed pi extension") {
		t.Fatalf("rerun: code=%d out=%q err=%q", code, out, errb)
	}
	if got, err := os.ReadFile(target); err != nil || !bytes.Equal(got, want) {
		t.Errorf("content not updated to the new wrapper path: %v", err)
	}

	// A hand edit is not a previous render: refused, never overwritten.
	got, _ := os.ReadFile(target)
	got[len(got)-1] = 'x'
	if err := os.WriteFile(target, got, 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errb = runInstallIn(t, proj, "pi-extension")
	if code != 1 || !strings.Contains(errb, "different content") {
		t.Errorf("hand edit: code=%d err=%q", code, errb)
	}
	if b, _ := os.ReadFile(target); !bytes.Equal(b, got) {
		t.Error("hand-edited extension was overwritten")
	}
}

// A previous render behind a symlink (an older install left the symlink in
// place, the binary moved) is updated in place: the rename replaces the
// symlink with a regular file, leaving its upstream untouched.
func TestInstallPiExtensionUpdatesSymlinkedRender(t *testing.T) {
	engineA := fakeEngine(t)
	proj := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	target := filepath.Join(proj, ".pi", "extensions", "shizo.ts")

	wantA, err := renderExtension(embeddedPiExtension, filepath.Join(engineA, "shizo.sh"))
	if err != nil {
		t.Fatal(err)
	}
	upstream := t.TempDir() + "/shizo.ts"
	if err := os.WriteFile(upstream, wantA, 0o644); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Dir(target), 0o755)
	if err := os.Symlink(upstream, target); err != nil {
		t.Fatal(err)
	}

	engineB := fakeEngine(t) // the binary moved to a second root
	wantB, err := renderExtension(embeddedPiExtension, filepath.Join(engineB, "shizo.sh"))
	if err != nil {
		t.Fatal(err)
	}

	code, out, errb := runInstallIn(t, proj, "pi-extension")
	if code != 0 || !strings.Contains(out, "installed pi extension") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errb)
	}
	fi, err := os.Lstat(target)
	if err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("target = %v, %v; want a regular file", fi.Mode(), err)
	}
	if got, err := os.ReadFile(target); err != nil || !bytes.Equal(got, wantB) {
		t.Errorf("content not updated to the new wrapper path: %v", err)
	}
	if b, err := os.ReadFile(upstream); err != nil || !bytes.Equal(b, wantA) {
		t.Errorf("symlink upstream was modified: %v", err)
	}
}

// renderExtension and renderedWrapperPath are exact inverses for every
// embedded extension, including paths with characters that need JSON
// escaping; the SHIZO_HOOK override survives the render. An unrendered source
// copy (the placeholder still in place) is recognized as a render, so
// re-installing a hand-copied source file fixes it rather than refusing.
func TestRenderExtensionRoundTrip(t *testing.T) {
	sources := []struct {
		name string
		src  []byte
	}{
		{"pi", embeddedPiExtension},
		{"opencode", embeddedOpencodePlugin},
	}
	for _, s := range sources {
		t.Run(s.name, func(t *testing.T) {
			for _, p := range []string{
				"/home/u/.shizo/engine/shizo.sh",
				`/tmp/we"ird\path/shizo.sh`,
			} {
				rendered, err := renderExtension(s.src, p)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(rendered), "process.env.SHIZO_HOOK ??") {
					t.Error("SHIZO_HOOK override lost in rendered extension")
				}
				got, ok := renderedWrapperPath(s.src, rendered)
				if !ok || got != p {
					t.Errorf("round-trip %q = %q, ok=%v", p, got, ok)
				}
			}
			if got, ok := renderedWrapperPath(s.src, s.src); !ok || got != "__SHIZO_WRAPPER__" {
				t.Errorf("unrendered source: %q, ok=%v; want recognized as a render", got, ok)
			}
			if _, ok := renderedWrapperPath(s.src, append([]byte("x"), s.src...)); ok {
				t.Error("modified source recognized as a render")
			}
		})
	}
}

// A stale symlink from an older install pointing at identical content is
// migrated to a regular file so it cannot dangle once engine/shizo-pi.ts is
// gone.
func TestInstallPiExtensionMigratesStaleSymlink(t *testing.T) {
	engine := fakeEngine(t)
	want, err := renderExtension(embeddedPiExtension, filepath.Join(engine, "shizo.sh"))
	if err != nil {
		t.Fatal(err)
	}
	proj := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	target := filepath.Join(proj, ".pi", "extensions", "shizo.ts")
	upstream := t.TempDir() + "/old.shizo.ts"
	if err := os.WriteFile(upstream, want, 0o644); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Dir(target), 0o755)
	if err := os.Symlink(upstream, target); err != nil {
		t.Fatal(err)
	}

	code, out, errb := runInstallIn(t, proj, "pi-extension")
	if code != 0 || !strings.Contains(out, "installed pi extension") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errb)
	}
	fi, err := os.Lstat(target)
	if err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("target = %v, %v; want a regular file", fi.Mode(), err)
	}
	if got, err := os.ReadFile(target); err != nil || !bytes.Equal(got, want) {
		t.Errorf("content differs from the rendered extension: %v", err)
	}
	// The temp-file rename must leave no residue next to the extension.
	if entries, err := os.ReadDir(filepath.Dir(target)); err != nil || len(entries) != 1 || entries[0].Name() != "shizo.ts" {
		t.Errorf("extension dir = %v, %v; want only shizo.ts", entries, err)
	}
}

// A broken symlink (its upstream engine/shizo-pi.ts is gone) is replaced by
// the regular copy.
func TestInstallPiExtensionReplacesDanglingSymlink(t *testing.T) {
	engine := fakeEngine(t)
	want, err := renderExtension(embeddedPiExtension, filepath.Join(engine, "shizo.sh"))
	if err != nil {
		t.Fatal(err)
	}
	proj := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	target := filepath.Join(proj, ".pi", "extensions", "shizo.ts")
	os.MkdirAll(filepath.Dir(target), 0o755)
	if err := os.Symlink("/nonexistent/engine/shizo-pi.ts", target); err != nil {
		t.Fatal(err)
	}

	code, out, errb := runInstallIn(t, proj, "pi-extension")
	if code != 0 || !strings.Contains(out, "installed pi extension") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errb)
	}
	fi, err := os.Lstat(target)
	if err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("target = %v, %v; want a regular file", fi.Mode(), err)
	}
	if got, err := os.ReadFile(target); err != nil || !bytes.Equal(got, want) {
		t.Errorf("content differs from the rendered extension: %v", err)
	}
}

func TestInstallPiExtensionRefusals(t *testing.T) {
	fakeEngine(t)
	proj := t.TempDir()
	t.Setenv("HOME", t.TempDir())

	if code, _, _ := runInstallIn(t, proj, "pi-extension", "--local"); code != 2 {
		t.Errorf("--local: code=%d, want 2", code)
	}

	target := filepath.Join(proj, ".pi", "extensions", "shizo.ts")
	os.MkdirAll(filepath.Dir(target), 0o755)
	os.WriteFile(target, []byte("mine"), 0o644)
	code, _, errb := runInstallIn(t, proj, "pi-extension")
	if code != 1 || !strings.Contains(errb, "already exists with different content") {
		t.Errorf("existing file: code=%d err=%q", code, errb)
	}
	if b, _ := os.ReadFile(target); string(b) != "mine" {
		t.Errorf("existing file was overwritten: %q", b)
	}

	// A directory at the target is refused, never written into.
	proj2 := t.TempDir()
	dirTarget := filepath.Join(proj2, ".pi", "extensions", "shizo.ts")
	if err := os.MkdirAll(dirTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	code, _, errb = runInstallIn(t, proj2, "pi-extension")
	if code != 1 || !strings.Contains(errb, "already exists") {
		t.Errorf("directory target: code=%d err=%q", code, errb)
	}
	if entries, err := os.ReadDir(dirTarget); err != nil || len(entries) != 0 {
		t.Errorf("directory target modified: %v, %v", entries, err)
	}
}

// The plugin is copied as a regular file (never a symlink) with the rendered
// content (the materialized wrapper path written in); re-running is a no-op.
func TestInstallOpencodePlugin(t *testing.T) {
	engine := fakeEngine(t)
	want, err := renderExtension(embeddedOpencodePlugin, filepath.Join(engine, "shizo.sh"))
	if err != nil {
		t.Fatal(err)
	}
	proj, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "") // pin the $HOME/.config fallback

	cases := []struct {
		name, target string
		args         []string
	}{
		{"project default", filepath.Join(proj, ".opencode", "plugins", "shizo.js"), []string{"opencode-plugin"}},
		{"project flag", filepath.Join(proj, ".opencode", "plugins", "shizo.js"), []string{"opencode-plugin", "--project"}},
		{"global", filepath.Join(home, ".config", "opencode", "plugins", "shizo.js"), []string{"opencode-plugin", "--global"}},
	}
	for _, c := range cases {
		os.RemoveAll(filepath.Join(proj, ".opencode"))
		os.RemoveAll(filepath.Join(home, ".config", "opencode"))
		code, out, errb := runInstallIn(t, proj, c.args...)
		if code != 0 || !strings.Contains(out, "installed opencode plugin") {
			t.Fatalf("%s: code=%d out=%q err=%q", c.name, code, out, errb)
		}
		fi, err := os.Lstat(c.target)
		if err != nil || fi.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("%s: target = %v, %v; want a regular file", c.name, fi.Mode(), err)
		}
		if fi.Mode().Perm() != 0o644 {
			t.Errorf("%s: mode = %v, want 0644", c.name, fi.Mode())
		}
		if got, err := os.ReadFile(c.target); err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s: content differs from the rendered plugin: %v", c.name, err)
		}
		code, out, _ = runInstallIn(t, proj, c.args...)
		if code != 0 || !strings.Contains(out, "already installed") {
			t.Errorf("%s rerun: code=%d out=%q", c.name, code, out)
		}
	}
}

// The plugin spawns the wrapper on every shell call, so an opencode install
// materializes it next to the binary — including when the plugin itself is
// already installed (a bare release binary must self-install completely).
// OpenCode resolves its global config root from $XDG_CONFIG_HOME when set;
// installing under $HOME/.config there would leave the plugin undiscovered
// and the gate silently inactive (packages/util/src/global-roots.ts).
func TestInstallOpencodePluginXDGConfigHome(t *testing.T) {
	fakeEngine(t)
	proj, xdg := t.TempDir(), t.TempDir()
	t.Setenv("HOME", t.TempDir()) // must not be consulted for the global path
	t.Setenv("XDG_CONFIG_HOME", xdg)

	target := filepath.Join(xdg, "opencode", "plugins", "shizo.js")
	code, out, errb := runInstallIn(t, proj, "opencode-plugin", "--global")
	if code != 0 || !strings.Contains(out, "installed opencode plugin") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errb)
	}
	if fi, err := os.Lstat(target); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("target = %v, %v; want a regular file under XDG_CONFIG_HOME", fi.Mode(), err)
	}
	if _, err := os.Lstat(filepath.Join(os.Getenv("HOME"), ".config", "opencode", "plugins", "shizo.js")); !os.IsNotExist(err) {
		t.Errorf("also installed under $HOME/.config: %v", err)
	}
}

func TestInstallOpencodePluginMaterializesWrapper(t *testing.T) {
	engine := fakeEngine(t)
	wrapper := filepath.Join(engine, "shizo.sh")
	proj := t.TempDir()
	t.Setenv("HOME", t.TempDir())

	code, out, errb := runInstallIn(t, proj, "opencode-plugin")
	if code != 0 || !strings.Contains(out, "installed opencode plugin") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errb)
	}
	assertWrapper(t, wrapper)

	// Re-run: the plugin is already installed, but the wrapper must still be
	// ensured.
	if err := os.Remove(wrapper); err != nil {
		t.Fatal(err)
	}
	code, out, _ = runInstallIn(t, proj, "opencode-plugin")
	if code != 0 || !strings.Contains(out, "already installed") {
		t.Fatalf("rerun: code=%d out=%q", code, out)
	}
	assertWrapper(t, wrapper)
}

// If the binary moved since a previous opencode install, the installed
// plugin's wrapper path is updated in place rather than refused; a hand edit
// is not a previous render and is still refused.
func TestInstallOpencodePluginUpdatesMovedBinary(t *testing.T) {
	fakeEngine(t) // first root
	proj := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	target := filepath.Join(proj, ".opencode", "plugins", "shizo.js")

	code, out, errb := runInstallIn(t, proj, "opencode-plugin")
	if code != 0 || !strings.Contains(out, "installed opencode plugin") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errb)
	}

	engineB := fakeEngine(t) // installExe now points at a second root
	want, err := renderExtension(embeddedOpencodePlugin, filepath.Join(engineB, "shizo.sh"))
	if err != nil {
		t.Fatal(err)
	}

	code, out, errb = runInstallIn(t, proj, "opencode-plugin")
	if code != 0 || !strings.Contains(out, "installed opencode plugin") {
		t.Fatalf("rerun: code=%d out=%q err=%q", code, out, errb)
	}
	if got, err := os.ReadFile(target); err != nil || !bytes.Equal(got, want) {
		t.Errorf("content not updated to the new wrapper path: %v", err)
	}

	// A hand edit is not a previous render: refused, never overwritten.
	got, _ := os.ReadFile(target)
	got[len(got)-1] = 'x'
	if err := os.WriteFile(target, got, 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errb = runInstallIn(t, proj, "opencode-plugin")
	if code != 1 || !strings.Contains(errb, "different content") {
		t.Errorf("hand edit: code=%d err=%q", code, errb)
	}
	if b, _ := os.ReadFile(target); !bytes.Equal(b, got) {
		t.Error("hand-edited plugin was overwritten")
	}
}

func TestInstallOpencodePluginRefusals(t *testing.T) {
	fakeEngine(t)
	proj := t.TempDir()
	t.Setenv("HOME", t.TempDir())

	if code, _, _ := runInstallIn(t, proj, "opencode-plugin", "--local"); code != 2 {
		t.Errorf("--local: code=%d, want 2", code)
	}

	target := filepath.Join(proj, ".opencode", "plugins", "shizo.js")
	os.MkdirAll(filepath.Dir(target), 0o755)
	os.WriteFile(target, []byte("mine"), 0o644)
	code, _, errb := runInstallIn(t, proj, "opencode-plugin")
	if code != 1 || !strings.Contains(errb, "already exists with different content") {
		t.Errorf("existing file: code=%d err=%q", code, errb)
	}
	if b, _ := os.ReadFile(target); string(b) != "mine" {
		t.Errorf("existing file was overwritten: %q", b)
	}

	// A directory at the target is refused, never written into.
	proj2 := t.TempDir()
	dirTarget := filepath.Join(proj2, ".opencode", "plugins", "shizo.js")
	if err := os.MkdirAll(dirTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	code, _, errb = runInstallIn(t, proj2, "opencode-plugin")
	if code != 1 || !strings.Contains(errb, "already exists") {
		t.Errorf("directory target: code=%d err=%q", code, errb)
	}
	if entries, err := os.ReadDir(dirTarget); err != nil || len(entries) != 0 {
		t.Errorf("directory target modified: %v, %v", entries, err)
	}
}

func TestInstallHelp(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runInstall([]string{"--help"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out.String(), "shizo install claude-hook") ||
		!strings.Contains(out.String(), "opencode-plugin") {
		t.Errorf("stdout = %q", out.String())
	}
}
