package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Rule attestation (docs/RULES.md "Attestation", plans/drafts/PLAN-rule-attestation.md).
// Pure normalize/hash/compare helpers — no I/O — so they're unit-testable
// independent of the record store and the loadConfig wiring in rules.go.

const (
	attestSchema = "1"
	normVersion  = "1"
)

// tierNorm is one tier's canonical (order/comment/whitespace-independent)
// serialization, ready to hash. trusted is only meaningful for the global tier
// (used to compute the normalized-PATH component).
type tierNorm struct {
	canonical string
	trusted   []string
}

// normalizeTier parses tier content in isolation (independent of any other
// tier) and returns its canonical serialization. Reuses parseTier/parseLine so
// the attestation hash always reflects exactly what the loader would accept.
func normalizeTier(content string, project bool) (tierNorm, string, bool) {
	cfg := &config{returnMode: "decision"}
	if reason, ok := parseTier(cfg, content, project); !ok {
		return tierNorm{}, reason, false
	}
	if !project && cfg.sandboxMode == "" {
		return tierNorm{}, "global tier must declare a sandbox directive (nono or dangerously-none)", false
	}
	return tierNorm{canonical: canonicalizeTier(cfg, project), trusted: cfg.trusted}, "", true
}

// canonicalizeTier serializes a parsed tier deterministically: directives
// (global only) then allow/deny rules, each group sorted, so comment/
// whitespace/rule-order edits don't change the hash. Sound only because
// evaluation is order-independent (deny > allow > ask, RULES.md §4) — a rule's
// effect never depends on its position relative to other rules of the same
// action.
func canonicalizeTier(cfg *config, project bool) string {
	var lines []string
	if !project {
		lines = append(lines, "returntype "+cfg.returnMode)
		lines = append(lines, "sandbox "+cfg.sandboxMode)
		trusted := append([]string{}, cfg.trusted...)
		sort.Strings(trusted)
		for _, t := range trusted {
			lines = append(lines, "trust "+t)
		}
	}
	lines = append(lines, sortedRuleLines(cfg.allowRules)...)
	lines = append(lines, sortedRuleLines(cfg.denyRules)...)
	return strings.Join(lines, "\n")
}

func sortedRuleLines(rules []rule) []string {
	lines := make([]string, len(rules))
	for i, r := range rules {
		lines[i] = serializeRule(r)
	}
	sort.Strings(lines)
	return lines
}

// serializeRule reconstructs a rule's canonical text from its tokens (not its
// raw source line), so cosmetic spacing differences don't affect the hash.
// Every literal is strconv.Quote'd and keywords stay bare, so the encoding is
// injective: `allow foo 'a b'` (one arg) and `allow foo a b` (two args) — and a
// literal argument spelled ANY/HAS vs the keyword — never collide. Without this
// a tamper that changes token boundaries would hash identically and pass.
func serializeRule(r rule) string {
	parts := []string{r.action}
	for _, t := range r.tokens {
		switch t.kind {
		case tokANY:
			parts = append(parts, "ANY")
		case tokHAS:
			parts = append(parts, "HAS")
		default:
			parts = append(parts, strconv.Quote(t.text))
		}
	}
	return strings.Join(parts, " ")
}

func hashCanonical(canonical string) string {
	sum := sha256.Sum256([]byte(canonical))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// normalizedPath is the live PATH restricted to the trusted-dir set, in PATH
// order — the only PATH-derived surface a resolved command's trust actually
// depends on (RULES.md §6).
func normalizedPath(pathEnv string, trusted []string) string {
	trustSet := map[string]bool{}
	for _, t := range trusted {
		trustSet[filepath.Clean(t)] = true
	}
	var kept []string
	for _, dir := range strings.Split(pathEnv, string(os.PathListSeparator)) {
		// Store the cleaned form so cosmetic differences (a trailing slash at
		// attest-time vs none at run-time) don't spuriously fail the drift check.
		if clean := filepath.Clean(dir); dir != "" && trustSet[clean] {
			kept = append(kept, clean)
		}
	}
	return strings.Join(kept, string(os.PathListSeparator))
}

// checkRecord verifies a live-computed norm against a parsed attestation
// record. Returns "" if trusted, else a deny reason. schema/norm-version
// mismatches are treated as a mismatch, never a silent wrong verdict.
func checkRecord(tier string, norm tierNorm, record map[string]string) string {
	if record["schema"] != attestSchema || record["norm-version"] != normVersion {
		return tier + " attestation schema/norm-version mismatch — re-attest"
	}
	if record["rules-hash"] != hashCanonical(norm.canonical) {
		return tier + " rules changed since attestation — re-attest"
	}
	return ""
}

// parseAttestFields reads the bare `key value` header of a record, stopping at
// the `rules` marker. The embedded rules block after it is advisory only
// (never trusted for the verdict), so it is intentionally not parsed here.
func parseAttestFields(content string) map[string]string {
	fields := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if trimmed == "rules" {
			break
		}
		if k, v, ok := strings.Cut(trimmed, " "); ok {
			fields[k] = v
		}
	}
	return fields
}

// globalAttestPath and projectAttestPath locate a tier's record under the
// attestation store (plan: "one file per project ... plus global.attest").
func globalAttestPath(root string) string {
	return filepath.Join(root, "attestations", "global.attest")
}

func projectAttestPath(root, absRulesPath string) string {
	sum := sha256.Sum256([]byte(absRulesPath))
	base := filepath.Base(filepath.Dir(absRulesPath))
	return filepath.Join(root, "attestations", fmt.Sprintf("%x-%s.attest", sum, base))
}
