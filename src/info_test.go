package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunInfo(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SHIZO_DIR", root)

	// No args: every key present as "key<TAB>value".
	var out, errb bytes.Buffer
	if code := runInfo(nil, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, errb.String())
	}
	for _, k := range []string{
		"install-root", "global-rules-path", "project-rules-path",
		"attestation-dir", "log-dir", "engine-dir", "hook-command", "version",
	} {
		if !strings.Contains(out.String(), k+"\t") {
			t.Errorf("missing key %q in:\n%s", k, out.String())
		}
	}

	// --KEY: bare value, no key prefix.
	for _, tc := range []struct{ flag, want string }{
		{"--global-rules-path", globalRulesPath()},
		{"--hook-command", filepath.Join(root, "engine", "shizo.sh")},
		{"--attestation-dir", filepath.Join(root, "attestations")},
		{"--version", version},
	} {
		var o, e bytes.Buffer
		if code := runInfo([]string{tc.flag}, &o, &e); code != 0 {
			t.Fatalf("%s: exit = %d (stderr: %s)", tc.flag, code, e.String())
		}
		if got := o.String(); got != tc.want+"\n" {
			t.Errorf("%s = %q, want %q", tc.flag, got, tc.want+"\n")
		}
	}

	// --cwd picks the project tier.
	var o, e bytes.Buffer
	runInfo([]string{"--cwd", "/tmp/proj", "--project-rules-path"}, &o, &e)
	if got := o.String(); got != filepath.Join("/tmp/proj", ".shizo")+"\n" {
		t.Errorf("--project-rules-path = %q", got)
	}

	// Unknown flag and stray positional both exit 2.
	for _, args := range [][]string{{"--nope"}, {"stray"}} {
		var o, e bytes.Buffer
		if code := runInfo(args, &o, &e); code != 2 {
			t.Errorf("runInfo(%v) exit = %d, want 2", args, code)
		}
	}

	// --help prints usage on stdout, exit 0 (matches the other subcommands).
	for _, flag := range []string{"-h", "--help"} {
		var o, e bytes.Buffer
		if code := runInfo([]string{flag}, &o, &e); code != 0 {
			t.Errorf("runInfo(%s) exit = %d, want 0", flag, code)
		}
		if !strings.Contains(o.String(), "usage: shizo info") {
			t.Errorf("runInfo(%s) stdout = %q", flag, o.String())
		}
	}
}
