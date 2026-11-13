package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeEditor writes an executable stub editor script to dir whose body runs
// with the edited path as $1, and returns its path.
func writeEditor(t *testing.T, dir, body string) string {
	t.Helper()
	script := filepath.Join(dir, "editor.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

// checkAttestRecord verifies the record at path matches a freshly computed
// attestation of content for the given tier, so tests assert on the hash
// rather than on loadConfig (whose root gate differs per environment).
func checkAttestRecord(t *testing.T, path, content string, project bool) map[string]string {
	t.Helper()
	norm, reason, ok := normalizeTier(content, project)
	if !ok {
		t.Fatalf("checkAttestRecord: %s", reason)
	}
	rec, err := readAttestRecord(path)
	if err != nil {
		t.Fatalf("checkAttestRecord: no record at %s: %s", path, err)
	}
	if rec["rules-hash"] != hashCanonical(norm.canonical) {
		t.Errorf("record hash does not match content: %q", rec["rules-hash"])
	}
	wantTier := "project"
	if !project {
		wantTier = "global"
	}
	if rec["tier"] != wantTier {
		t.Errorf("record tier = %q, want %q", rec["tier"], wantTier)
	}
	return rec
}

func TestRunEditProjectHappyPath(t *testing.T) {
	root := t.TempDir()
	projDir := t.TempDir()
	t.Chdir(projDir)
	t.Setenv("SHIZO_DIR", root)
	t.Setenv("NONO_CAP_FILE", "")
	t.Setenv("VISUAL", "")
	// The stub editor appends a rule — the attestation must reflect the
	// post-edit content.
	t.Setenv("EDITOR", writeEditor(t, root, `echo 'allow ls ANY' >> "$1"`))

	writeFile(t, projDir, ".shizo", "deny git push ANY\n")

	var out, errb bytes.Buffer
	rc := runEdit(nil, &out, &errb)
	if rc != 0 {
		t.Fatalf("edit: rc=%d err=%s", rc, errb.String())
	}
	if !strings.Contains(out.String(), "attested project tier") {
		t.Errorf("missing success line: %q", out.String())
	}

	body, err := os.ReadFile(filepath.Join(projDir, ".shizo"))
	if err != nil {
		t.Fatal(err)
	}
	checkAttestRecord(t, projectAttestPath(root, filepath.Join(projDir, ".shizo")), string(body), true)
}

func TestRunEditGlobalHappyPathUnchanged(t *testing.T) {
	root := t.TempDir()
	bin := t.TempDir()
	t.Setenv("SHIZO_DIR", root)
	t.Setenv("NONO_CAP_FILE", "")
	t.Setenv("PATH", bin)
	t.Setenv("VISUAL", "")
	// No-op editor: an unchanged file is attested anyway (idempotent).
	t.Setenv("EDITOR", writeEditor(t, root, `exit 0`))

	body := "sandbox nono\ntrust " + bin + "\nallow git status ANY\n"
	writeFile(t, root, "global/rules", body)

	var out, errb bytes.Buffer
	rc := runEdit([]string{"--global"}, &out, &errb)
	if rc != 0 {
		t.Fatalf("edit --global: rc=%d err=%s", rc, errb.String())
	}
	if !strings.Contains(out.String(), "attested global tier") {
		t.Errorf("missing success line: %q", out.String())
	}
	rec := checkAttestRecord(t, globalAttestPath(root), body, false)
	if rec["normalized-path"] != bin {
		t.Errorf("normalized-path = %q, want %q", rec["normalized-path"], bin)
	}
}

func TestRunEditCancelSkipsAttestation(t *testing.T) {
	root := t.TempDir()
	projDir := t.TempDir()
	t.Chdir(projDir)
	t.Setenv("SHIZO_DIR", root)
	t.Setenv("NONO_CAP_FILE", "")
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", writeEditor(t, root, `exit 3`))

	writeFile(t, projDir, ".shizo", "deny git push ANY\n")
	abs := filepath.Join(projDir, ".shizo")

	var out, errb bytes.Buffer
	rc := runEdit(nil, &out, &errb)
	if rc != 0 {
		t.Fatalf("cancelled editor must exit 0: rc=%d err=%s", rc, errb.String())
	}
	if !strings.Contains(out.String(), "attestation skipped") {
		t.Errorf("missing cancel note: %q", out.String())
	}
	if _, err := os.Stat(projectAttestPath(root, abs)); !os.IsNotExist(err) {
		t.Errorf("cancelled edit must not write a record: %v", err)
	}
}

func TestRunEditNoEditorFails(t *testing.T) {
	t.Setenv("NONO_CAP_FILE", "")
	// A whitespace-only $EDITOR is treated as unset, not a binary name.
	for _, editor := range []string{"", "   "} {
		t.Setenv("EDITOR", editor)
		t.Setenv("VISUAL", "")
		var out, errb bytes.Buffer
		if rc := runEdit(nil, &out, &errb); rc != 1 {
			t.Errorf("EDITOR=%q: rc=%d, want 1", editor, rc)
		}
		if !strings.Contains(errb.String(), "neither $EDITOR nor $VISUAL") {
			t.Errorf("EDITOR=%q: missing editor hint: %q", editor, errb.String())
		}
	}
}

func TestRunEditVisualFallback(t *testing.T) {
	root := t.TempDir()
	projDir := t.TempDir()
	t.Chdir(projDir)
	t.Setenv("SHIZO_DIR", root)
	t.Setenv("NONO_CAP_FILE", "")
	t.Setenv("EDITOR", "")
	t.Setenv("VISUAL", writeEditor(t, root, `exit 0`))

	writeFile(t, projDir, ".shizo", "deny git push ANY\n")

	var out, errb bytes.Buffer
	if rc := runEdit(nil, &out, &errb); rc != 0 {
		t.Fatalf("VISUAL fallback: rc=%d err=%s", rc, errb.String())
	}
	checkAttestRecord(t, projectAttestPath(root, filepath.Join(projDir, ".shizo")), "deny git push ANY\n", true)
}

func TestRunEditInsideSandboxRefuses(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "editor-ran")
	t.Setenv("NONO_CAP_FILE", "/tmp/fake-cap")
	t.Setenv("VISUAL", "")
	// The stub would leave a marker if it ran — the sandbox bail must come
	// before the editor is spawned.
	t.Setenv("EDITOR", writeEditor(t, root, `touch "`+marker+`"`))

	var out, errb bytes.Buffer
	if rc := runEdit([]string{"--global"}, &out, &errb); rc != 1 {
		t.Errorf("edit inside sandbox: rc=%d, want 1", rc)
	}
	if !strings.Contains(errb.String(), "must be run outside the sandbox") {
		t.Errorf("missing sandbox hint: %q", errb.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Error("editor must not run inside the sandbox")
	}
}

func TestRunEditEditorCannotStart(t *testing.T) {
	root := t.TempDir()
	projDir := t.TempDir()
	t.Chdir(projDir)
	t.Setenv("SHIZO_DIR", root)
	t.Setenv("NONO_CAP_FILE", "")
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", filepath.Join(root, "no-such-editor"))

	writeFile(t, projDir, ".shizo", "deny git push ANY\n")

	var out, errb bytes.Buffer
	if rc := runEdit(nil, &out, &errb); rc != 1 {
		t.Errorf("unstartable editor: rc=%d, want 1", rc)
	}
	if !strings.Contains(errb.String(), "edit:") {
		t.Errorf("missing error: %q", errb.String())
	}
	if _, err := os.Stat(projectAttestPath(root, filepath.Join(projDir, ".shizo"))); !os.IsNotExist(err) {
		t.Error("unstartable editor must not write a record")
	}
}

func TestRunEditEditorWithFlags(t *testing.T) {
	root := t.TempDir()
	projDir := t.TempDir()
	t.Chdir(projDir)
	t.Setenv("SHIZO_DIR", root)
	t.Setenv("NONO_CAP_FILE", "")
	t.Setenv("VISUAL", "")
	// The stub dumps its argv, so the test pins how a $EDITOR that carries
	// its own flags is split: flag and path arrive as separate arguments.
	argsFile := filepath.Join(root, "editor-args")
	script := writeEditor(t, root, `printf '%s\n' "$@" > "`+argsFile+`"`)
	t.Setenv("EDITOR", script+" --flag")

	writeFile(t, projDir, ".shizo", "deny git push ANY\n")

	var out, errb bytes.Buffer
	if rc := runEdit(nil, &out, &errb); rc != 0 {
		t.Fatalf("edit with editor flags: rc=%d err=%s", rc, errb.String())
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	want := "--flag\n" + filepath.Join(projDir, ".shizo") + "\n"
	if string(args) != want {
		t.Errorf("editor argv = %q, want %q", args, want)
	}
}

func TestRunEditPostEditUnreadable(t *testing.T) {
	root := t.TempDir()
	projDir := t.TempDir()
	t.Chdir(projDir)
	t.Setenv("SHIZO_DIR", root)
	t.Setenv("NONO_CAP_FILE", "")
	t.Setenv("VISUAL", "")
	// The editor deletes the tier: a clean exit, but nothing left to attest.
	t.Setenv("EDITOR", writeEditor(t, root, `rm -f "$1"`))

	writeFile(t, projDir, ".shizo", "deny git push ANY\n")
	abs := filepath.Join(projDir, ".shizo")

	var out, errb bytes.Buffer
	if rc := runEdit(nil, &out, &errb); rc != 1 {
		t.Fatalf("deleted tier: rc=%d, want 1 (err=%s)", rc, errb.String())
	}
	if !strings.Contains(errb.String(), "unreadable") {
		t.Errorf("missing unreadable error: %q", errb.String())
	}
	if _, err := os.Stat(projectAttestPath(root, abs)); !os.IsNotExist(err) {
		t.Error("failed attestation must not write a record")
	}
}

func TestRunEditArgErrors(t *testing.T) {
	t.Setenv("NONO_CAP_FILE", "")
	cases := []struct {
		args    []string
		wantRC  int
		wantErr string // substring of stderr
		wantOut string // substring of stdout
	}{
		{[]string{"--bogus"}, 2, "unknown flag", ""},
		{[]string{"FILE"}, 2, "too many arguments", ""},
		{[]string{"--global", "FILE"}, 2, "too many arguments", ""},
		{[]string{"-h"}, 0, "", "usage: shizo edit"},
	}
	for _, c := range cases {
		var out, errb bytes.Buffer
		rc := runEdit(c.args, &out, &errb)
		if rc != c.wantRC {
			t.Errorf("edit %v: rc=%d, want %d (err=%s)", c.args, rc, c.wantRC, errb.String())
		}
		if !strings.Contains(errb.String(), c.wantErr) {
			t.Errorf("edit %v: stderr %q missing %q", c.args, errb.String(), c.wantErr)
		}
		if !strings.Contains(out.String(), c.wantOut) {
			t.Errorf("edit %v: stdout %q missing %q", c.args, out.String(), c.wantOut)
		}
	}
}
