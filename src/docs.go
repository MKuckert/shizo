package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"

	shizo "shizo"
)

// docsUsage documents the embedded doc viewer. Operator-only; never touches
// the hook pipeline.
const docsUsage = `shizo docs — view the embedded operator documentation.

usage: shizo docs [slug]

With no argument, lists the available slugs with their titles. With a slug,
prints that doc to stdout — through $PAGER (falling back to less) when
stdout is a terminal, raw otherwise. Exit 2 on an unknown slug.

The docs are compiled into the binary (docs/*.md), so this works from a bare
release install. Slugs are the doc filenames lowercased without .md.`

// docEntry pairs an embedded doc's slug with its file and first heading.
type docEntry struct {
	slug  string // lowercase filename without .md
	file  string // name inside docs/
	title string // first "# " heading, or the slug if none
}

// docsEntries lists every embedded doc, sorted by slug. Slugs are derived
// from filenames (lowercase, .md stripped), so adding a doc needs no code.
func docsEntries() []docEntry {
	entries, err := shizo.Docs.ReadDir("docs")
	if err != nil {
		return nil
	}
	var out []docEntry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := shizo.Docs.ReadFile("docs/" + e.Name())
		if err != nil {
			continue // embed guarantees presence; defensive only
		}
		slug := strings.ToLower(strings.TrimSuffix(e.Name(), ".md"))
		out = append(out, docEntry{slug: slug, file: e.Name(), title: firstHeading(string(data), slug)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].slug < out[j].slug })
	return out
}

// firstHeading returns a doc's first top-level "# " heading as a one-line
// description, falling back to the slug.
func firstHeading(doc, fallback string) string {
	for _, line := range strings.Split(doc, "\n") {
		if h, ok := strings.CutPrefix(strings.TrimSpace(line), "# "); ok {
			return strings.TrimSpace(h)
		}
	}
	return fallback
}

// docsContent returns the embedded doc for a slug, or false if unknown.
func docsContent(slug string) (string, bool) {
	for _, e := range docsEntries() {
		if e.slug == slug {
			data, err := shizo.Docs.ReadFile("docs/" + e.file)
			if err != nil {
				return "", false
			}
			return string(data), true
		}
	}
	return "", false
}

// runDocs lists the embedded docs (no arg) or prints one to stdout. Piped
// output is written raw; a terminal goes through the pager chain.
func runDocs(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(stdout, docsUsage)
		return 0
	}
	if len(args) > 1 {
		fmt.Fprintf(stderr, "shizo: docs takes at most one slug\n\n%s\n", docsUsage)
		return 2
	}
	if len(args) == 0 {
		entries := docsEntries()
		max := 0
		for _, e := range entries {
			if len(e.slug) > max {
				max = len(e.slug)
			}
		}
		for _, e := range entries {
			fmt.Fprintf(stdout, "%-*s  %s\n", max, e.slug, e.title)
		}
		return 0
	}
	doc, ok := docsContent(args[0])
	if !ok {
		fmt.Fprintf(stderr, "shizo: unknown doc %q — run `shizo docs` for slugs\n", args[0])
		return 2
	}
	if isTerminal(stdout) && runPager(doc, stdout, stderr) {
		return 0
	}
	fmt.Fprint(stdout, doc)
	return 0
}

// isTerminal reports whether w is an *os.File pointing at a character device.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// runPager pipes doc into $PAGER (falling back to less) and reports whether a
// pager ran. A missing binary or a nonzero exit (e.g., less cannot open the
// terminal) falls through to the next candidate; if none succeed, the caller
// writes raw. A pager that exits nonzero after a normal read re-dumps the doc
// raw — accepted, since losing the doc is worse.
func runPager(doc string, stdout, stderr io.Writer) bool {
	var candidates [][]string
	if p := os.Getenv("PAGER"); p != "" {
		candidates = append(candidates, strings.Fields(p))
	}
	candidates = append(candidates, []string{"less"})
	for _, argv := range candidates {
		if len(argv) == 0 {
			continue
		}
		bin, err := exec.LookPath(argv[0])
		if err != nil {
			continue
		}
		cmd := exec.Command(bin, argv[1:]...)
		cmd.Stdin = strings.NewReader(doc)
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		if err := cmd.Start(); err != nil {
			continue
		}
		if err := cmd.Wait(); err != nil {
			continue
		}
		return true
	}
	return false
}
