package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The `attest` subcommand is operator-only, like validate/test/selfcheck: it
// never touches the hook pipeline. It records approval of a rule tier's
// current content so loadConfig (src/rules.go) can recognise tampering later —
// see plans/drafts/PLAN-rule-attestation.md and docs/RULES.md.

const attestUsage = `bash-gate attest — record operator approval of a rule tier.

usage: bash-gate attest --global
       bash-gate attest [FILE]

--global attests the resolved global tier ($BASH_GATE_DIR/global/rules),
snapshotting the live PATH intersected with the trust-dir set alongside the
rules hash. With no --global, attests the project tier at FILE (default:
<cwd>/.bash-gate).

Run this outside the sandbox, after reviewing the rules. The hook recomputes
the hash on every command and denies everything on any divergence. Writes to
$BASH_GATE_DIR/attestations/. Never touches the hook pipeline.`

func runAttest(args []string, stdout, stderr io.Writer) int {
	global := false
	var file string
	for _, a := range args {
		switch {
		case a == "--global":
			global = true
		case a == "-h" || a == "--help":
			fmt.Fprintln(stdout, attestUsage)
			return 0
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, "attest: unknown flag %q\n\n%s\n", a, attestUsage)
			return 2
		case file == "":
			file = a
		default:
			fmt.Fprintf(stderr, "attest: too many arguments\n\n%s\n", attestUsage)
			return 2
		}
	}
	if global && file != "" {
		fmt.Fprintf(stderr, "attest: FILE is not allowed with --global\n\n%s\n", attestUsage)
		return 2
	}

	// attest writes to $BASH_GATE_DIR/attestations/, outside the sandbox's
	// writable set — the raw os error is opaque, so bail early with a hint.
	if os.Getenv("NONO_CAP_FILE") != "" {
		fmt.Fprintln(stderr, "attest: this command must be run outside the sandbox")
		return 1
	}

	path := file
	if global {
		path = globalRulesPath()
	} else if path == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "attest: %s\n", err)
			return 1
		}
		path = projectRulesPathFor(cwd)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "attest: unreadable: %s: %s\n", path, err)
		return 1
	}

	norm, reason, ok := normalizeTier(string(data), !global)
	if !ok {
		fmt.Fprintf(stderr, "attest: %s\n", reason)
		return 1
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		fmt.Fprintf(stderr, "attest: %s\n", err)
		return 1
	}

	tier := "project"
	root := installRoot()
	recordPath := projectAttestPath(root, abs)
	fields := map[string]string{}
	if global {
		tier = "global"
		recordPath = globalAttestPath(root)
		fields["normalized-path"] = normalizedPath(os.Getenv("PATH"), norm.trusted)
	}
	fields["schema"] = attestSchema
	fields["tier"] = tier
	fields["norm-version"] = normVersion
	fields["rules-path"] = abs
	fields["rules-hash"] = hashCanonical(norm.canonical)
	fields["attested-at"] = time.Now().UTC().Format(time.RFC3339)
	fields["attested-by"] = attestedBy()

	if err := writeAttestRecord(recordPath, fields, norm.canonical); err != nil {
		fmt.Fprintf(stderr, "attest: %s\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "attested %s tier %s -> %s\n", tier, path, recordPath)
	return 0
}

func attestedBy() string {
	user := os.Getenv("USER")
	if user == "" {
		user = "unknown"
	}
	if host, err := os.Hostname(); err == nil && host != "" {
		return user + "@" + host
	}
	return user
}

// attestFieldOrder controls header key order in written records for readable
// diffs; unset fields are skipped.
var attestFieldOrder = []string{
	"schema", "tier", "norm-version", "rules-path", "rules-hash",
	"attested-at", "attested-by", "normalized-path",
}

// writeAttestRecord writes one record: an ordered `key value` header, a `rules`
// marker, then the (advisory-only) canonical rules block.
func writeAttestRecord(path string, fields map[string]string, rulesBlock string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	for _, k := range attestFieldOrder {
		if v, ok := fields[k]; ok {
			fmt.Fprintf(&b, "%s %s\n", k, v)
		}
	}
	b.WriteString("rules\n")
	if rulesBlock != "" {
		b.WriteString(rulesBlock)
		b.WriteString("\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// readAttestRecord reads and parses one record's header fields.
func readAttestRecord(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseAttestFields(string(data)), nil
}
