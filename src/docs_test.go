package main

import (
	"bytes"
	"strings"
	"testing"

	shizo "shizo"
)

func TestDocsEntries(t *testing.T) {
	entries := docsEntries()
	if len(entries) == 0 {
		t.Fatal("no embedded docs found")
	}
	bySlug := map[string]docEntry{}
	for _, e := range entries {
		if e.slug != strings.ToLower(strings.TrimSuffix(e.file, ".md")) {
			t.Errorf("slug %q not derived from file %q", e.slug, e.file)
		}
		if e.title == "" {
			t.Errorf("entry %q has empty title", e.slug)
		}
		bySlug[e.slug] = e
	}
	// Slug derivation must lowercase multi-word filenames, not hardcode a map.
	if _, ok := bySlug["claude_code_hooks"]; !ok {
		t.Errorf("missing derived slug claude_code_hooks: %v", bySlug)
	}
	// Sorted by slug.
	for i := 1; i < len(entries); i++ {
		if entries[i-1].slug >= entries[i].slug {
			t.Errorf("entries not sorted at %d: %v", i, entries)
		}
	}
}

func TestFirstHeading(t *testing.T) {
	for _, tc := range []struct{ doc, want string }{
		{"# Title\nbody\n", "Title"},
		{"\n  # Indented\n# Later\n", "Indented"},
		{"## not top-level\nbody\n", "fallback"},
		{"no heading at all\n", "fallback"},
	} {
		if got := firstHeading(tc.doc, "fallback"); got != tc.want {
			t.Errorf("firstHeading(%q) = %q, want %q", tc.doc, got, tc.want)
		}
	}
}

func TestRunDocsList(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runDocs(nil, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, errb.String())
	}
	for _, e := range docsEntries() {
		if !strings.Contains(out.String(), e.slug) {
			t.Errorf("list missing slug %q", e.slug)
		}
		if !strings.Contains(out.String(), e.title) {
			t.Errorf("list missing title %q", e.title)
		}
	}
}

func TestRunDocsShow(t *testing.T) {
	for _, e := range docsEntries() {
		t.Run(e.slug, func(t *testing.T) {
			want, err := shizo.Docs.ReadFile("docs/" + e.file)
			if err != nil {
				t.Fatal(err)
			}
			var out, errb bytes.Buffer
			if code := runDocs([]string{e.slug}, &out, &errb); code != 0 {
				t.Fatalf("exit = %d (stderr: %s)", code, errb.String())
			}
			if out.String() != string(want) {
				t.Errorf("content mismatch for %q", e.slug)
			}
		})
	}
}

func TestRunDocsErrors(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runDocs([]string{"nope"}, &out, &errb); code != 2 {
		t.Fatalf("unknown slug exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "nope") {
		t.Errorf("stderr = %q, want mention of the slug", errb.String())
	}

	errb.Reset()
	if code := runDocs([]string{"rules", "extra"}, &out, &errb); code != 2 {
		t.Fatalf("two args exit = %d, want 2", code)
	}
}

func TestRunDocsHelp(t *testing.T) {
	for _, flag := range []string{"-h", "--help"} {
		var out, errb bytes.Buffer
		if code := runDocs([]string{flag}, &out, &errb); code != 0 {
			t.Fatalf("%s exit = %d (stderr: %s)", flag, code, errb.String())
		}
		if !strings.Contains(out.String(), "usage: shizo docs") {
			t.Errorf("%s output missing usage:\n%s", flag, out.String())
		}
	}
}
