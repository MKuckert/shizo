package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"mvdan.cc/sh/v3/syntax"
)

type config struct {
	trusted      []string
	returnMode   string // "decision" (default) or "automode"
	sandboxMode  string // "nono" or "dangerously-none" — required in the global tier, no default
	stdinDefault string // "allow" (default), "ask", or "deny"
	allowRules   []rule
	denyRules    []rule
	askRules     []rule
	ignored      []string // ignored project-tier directive lines (logged)
	denyAll      bool
	denyReason   string
}

func denyAllConfig(reason string) *config {
	return &config{returnMode: "decision", denyAll: true, denyReason: reason}
}

// euidFn is the effective-UID source, overridable in tests.
var euidFn = syscall.Geteuid

// rootBlocksNono returns a deny reason when euid == 0 under sandbox nono.
func rootBlocksNono(euid int, mode string) string {
	if mode == "nono" && euid == 0 {
		return "sandbox nono requires a non-root user (euid=0); use 'sandbox dangerously-none' for root environments"
	}
	return ""
}

// loadConfig reads the global tier (required) and project tier (optional).
// Any load/parse failure yields a deny-all config with a reason (§2 step 2).
// A tier that fails attestation (unattested, changed since attest, or a live
// PATH that no longer matches the attested trust-dir snapshot) also yields
// deny-all — see attest_verify.go and plans/drafts/PLAN-rule-attestation.md.
func loadConfig(globalPath, projectPath, root string) *config {
	cfg := &config{returnMode: "decision"}

	globalData, err := os.ReadFile(globalPath)
	if err != nil {
		return denyAllConfig("global rules unreadable (" + globalPath + "): " + err.Error())
	}
	if reason, ok := parseTier(cfg, string(globalData), false); !ok {
		return denyAllConfig(reason)
	}
	// sandbox is required in the global tier — absence fails closed (§3).
	if cfg.sandboxMode == "" {
		return denyAllConfig("global tier must declare a sandbox directive (nono or dangerously-none)")
	}
	if reason := rootBlocksNono(euidFn(), cfg.sandboxMode); reason != "" {
		return denyAllConfig(reason)
	}

	var projectData []byte
	projectPresent := false
	if projectPath != "" {
		data, err := os.ReadFile(projectPath)
		if err == nil {
			projectPresent = true
			projectData = data
			if reason, ok := parseTier(cfg, string(data), true); !ok {
				return denyAllConfig(reason)
			}
		} else if !os.IsNotExist(err) {
			return denyAllConfig("project rules unreadable (" + projectPath + "): " + err.Error())
		}
	}

	if cfg.stdinDefault == "" {
		cfg.stdinDefault = "allow"
	}

	// dangerously-none: attestation adds latency for zero security value —
	// without a sandbox the agent can rewrite both rules and records.
	if cfg.sandboxMode != "dangerously-none" {
		if reason := verifyAttestation(globalData, projectPath, projectData, projectPresent, root); reason != "" {
			return denyAllConfig(reason)
		}
	}

	return cfg
}

// verifyAttestation checks the loaded tiers against operator-recorded
// attestations. Trusted iff the global record matches and, when a project
// tier is present, its record also matches and the live normalized-PATH still
// matches the global record's snapshot. No project file -> nothing to verify
// for it (§ of the plan: "No project file ⇒ nothing to attest").
func verifyAttestation(globalData []byte, projectPath string, projectData []byte, projectPresent bool, root string) string {
	// Probe store writability before trusting any records. Only meaningful
	// inside the sandbox — outside it (tests, operator commands) everything
	// is writable regardless of the profile.
	if os.Getenv("NONO_CAP_FILE") != "" {
		attestDir := filepath.Join(root, "attestations")
		if ok, detail := probeStoreReadOnly(attestDir); !ok {
			return "attestation store is writable in-sandbox: " + detail +
				" — records are forgeable, re-check the sandbox profile"
		}
	}

	gNorm, reason, ok := normalizeTier(string(globalData), false)
	if !ok {
		return "global rules failed to normalize for attestation: " + reason
	}
	gRecord, err := readAttestRecord(globalAttestPath(root))
	if err != nil {
		return "global rules not attested (" + err.Error() + "); run `shizo attest --global`"
	}
	if reason := checkRecord("global", gNorm, gRecord); reason != "" {
		return reason
	}
	if live := normalizedPath(os.Getenv("PATH"), gNorm.trusted); gRecord["normalized-path"] != live {
		return "PATH (trust-dir subset) changed since global attestation; re-attest with `shizo attest --global`"
	}

	if !projectPresent {
		return ""
	}
	abs, err := filepath.Abs(projectPath)
	if err != nil {
		return "project rules path: " + err.Error()
	}
	pNorm, reason, ok := normalizeTier(string(projectData), true)
	if !ok {
		return "project rules failed to normalize for attestation: " + reason
	}
	pRecord, err := readAttestRecord(projectAttestPath(root, abs))
	if err != nil {
		return "project rules not attested (" + err.Error() + "); run `shizo attest`"
	}
	return checkRecord("project", pNorm, pRecord)
}

// tierState carries the per-file "seen" flags across parseLine calls: rules must
// follow directives, and returntype/sandbox/stdin may appear at most once.
type tierState struct {
	sawRule       bool
	sawReturntype bool
	sawSandbox    bool
	sawStdin      bool
}

// parseTier folds one rules file into cfg. Returns (reason, false) on the first
// malformed line (fail-closed §2 step 2). project restricts loosening directives.
func parseTier(cfg *config, content string, project bool) (string, bool) {
	var st tierState
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if reason, ok := parseLine(cfg, trimmed, project, &st); !ok {
			return reason, false
		}
	}
	return "", true
}

// parseLine folds one non-blank, non-comment rule line into cfg, updating st.
// Returns (reason, false) on a malformed or misplaced line. Pure over cfg/st —
// both the fail-closed loader (parseTier) and the validator (validateTier) drive
// it, so its verdict is identical in both.
func parseLine(cfg *config, trimmed string, project bool, st *tierState) (string, bool) {
	toks, err := parseRuleWords(trimmed)
	if err != nil {
		return "malformed rule line: " + trimmed + " (" + err.Error() + ")", false
	}
	if len(toks) == 0 {
		return "", true
	}

	head := toks[0]
	if head.kind != tokLiteral {
		return "malformed rule line: " + trimmed, false
	}

	switch head.text {
	case "trust":
		if project {
			cfg.ignored = append(cfg.ignored, trimmed)
			return "", true
		}
		if st.sawRule {
			return "directive after rules: " + trimmed, false
		}
		if len(toks) != 2 || toks[1].kind != tokLiteral || !filepath.IsAbs(toks[1].text) {
			return "trust needs one absolute directory: " + trimmed, false
		}
		cfg.trusted = append(cfg.trusted, toks[1].text)

	case "returntype":
		if project {
			cfg.ignored = append(cfg.ignored, trimmed)
			return "", true
		}
		if st.sawRule {
			return "directive after rules: " + trimmed, false
		}
		if st.sawReturntype {
			return "duplicate returntype: " + trimmed, false
		}
		if len(toks) != 2 || (toks[1].text != "decision" && toks[1].text != "automode") {
			return "returntype must be decision or automode: " + trimmed, false
		}
		cfg.returnMode = toks[1].text
		st.sawReturntype = true

	case "sandbox":
		if project {
			cfg.ignored = append(cfg.ignored, trimmed)
			return "", true
		}
		if st.sawRule {
			return "directive after rules: " + trimmed, false
		}
		if st.sawSandbox {
			return "duplicate sandbox: " + trimmed, false
		}
		if len(toks) != 2 || toks[1].kind != tokLiteral ||
			(toks[1].text != "nono" && toks[1].text != "dangerously-none") {
			return "sandbox must be nono or dangerously-none: " + trimmed, false
		}
		cfg.sandboxMode = toks[1].text
		st.sawSandbox = true

	case "stdin":
		if project {
			cfg.ignored = append(cfg.ignored, trimmed)
			return "", true
		}
		if st.sawRule {
			return "directive after rules: " + trimmed, false
		}
		if st.sawStdin {
			return "duplicate stdin: " + trimmed, false
		}
		if len(toks) != 2 || toks[1].kind != tokLiteral ||
			(toks[1].text != "allow" && toks[1].text != "ask" && toks[1].text != "deny") {
			return "stdin must be allow, ask, or deny: " + trimmed, false
		}
		cfg.stdinDefault = toks[1].text
		st.sawStdin = true

	case "allow":
		r, reason, ok := buildRule("allow", toks[1:], trimmed)
		if !ok {
			return reason, false
		}
		cfg.allowRules = append(cfg.allowRules, r)
		st.sawRule = true

	case "deny":
		r, reason, ok := buildRule("deny", toks[1:], trimmed)
		if !ok {
			return reason, false
		}
		cfg.denyRules = append(cfg.denyRules, r)
		st.sawRule = true

	case "ask":
		r, reason, ok := buildRule("ask", toks[1:], trimmed)
		if !ok {
			return reason, false
		}
		cfg.askRules = append(cfg.askRules, r)
		st.sawRule = true

	default:
		return "unknown directive: " + trimmed, false
	}

	return "", true
}

// buildRule validates the token stream of an allow/deny/ask line (§4.1).
func buildRule(action string, toks []ruleToken, raw string) (rule, string, bool) {
	if len(toks) == 0 {
		return rule{}, "rule needs at least one token: " + raw, false
	}

	stdinOnly := false
	if toks[0].kind == tokSTDIN {
		stdinOnly = true
		toks = toks[1:]
		if len(toks) == 0 {
			return rule{}, "STDIN needs a command after it: " + raw, false
		}
	}

	anyCount, hasCount, hasIdx := 0, 0, -1
	for i, t := range toks {
		switch t.kind {
		case tokANY:
			anyCount++
		case tokHAS:
			hasCount++
			hasIdx = i
		case tokSTDIN:
			return rule{}, "STDIN must be the first token: " + raw, false
		}
	}

	// The first literal is the command token — bare name or absolute path.
	// A relative path like ./deploy.sh can never match a resolved absolute path.
	if toks[0].kind == tokLiteral && strings.ContainsRune(toks[0].text, '/') && !filepath.IsAbs(toks[0].text) {
		return rule{}, "rule command path must be absolute or a bare name: " + raw, false
	}

	if anyCount > 0 && hasCount > 0 {
		return rule{}, "ANY and HAS may not appear in the same rule: " + raw, false
	}
	if anyCount > 1 {
		return rule{}, "at most one ANY per rule: " + raw, false
	}
	if hasCount > 1 {
		return rule{}, "at most one HAS per rule: " + raw, false
	}
	if hasCount == 1 {
		// prefix... HAS flag : >=1 prefix token, exactly one literal flag after.
		if hasIdx == 0 {
			return rule{}, "HAS needs a command before it: " + raw, false
		}
		if hasIdx != len(toks)-2 {
			return rule{}, "HAS must be followed by exactly one token: " + raw, false
		}
		if toks[hasIdx+1].kind != tokLiteral {
			return rule{}, "HAS token must be a literal: " + raw, false
		}
	}

	return rule{action: action, tokens: toks, raw: raw, stdinOnly: stdinOnly}, "", true
}

// parseRuleWords splits one rule line into tokens using the shell parser,
// so config quoting matches command quoting. A bare literal ANY/HAS is the
// keyword; a quoted 'ANY'/'HAS' is an ordinary literal argument.
func parseRuleWords(line string) ([]ruleToken, error) {
	p := syntax.NewParser()
	var toks []ruleToken
	for w, err := range p.WordsSeq(strings.NewReader(line)) {
		if err != nil {
			return nil, err
		}
		if kw, ok := keyword(w); ok {
			toks = append(toks, ruleToken{kind: kw})
			continue
		}
		text, kind := resolveWord(w, true)
		if kind != kindLiteral {
			return nil, fmt.Errorf("non-literal token in rule")
		}
		toks = append(toks, ruleToken{text: text, kind: tokLiteral})
	}
	return toks, nil
}

// keyword returns the keyword kind if w is a bare (unquoted) ANY, HAS, or STDIN literal.
func keyword(w *syntax.Word) (tokKind, bool) {
	if len(w.Parts) != 1 {
		return 0, false
	}
	lit, ok := w.Parts[0].(*syntax.Lit)
	if !ok {
		return 0, false
	}
	switch lit.Value {
	case "ANY":
		return tokANY, true
	case "HAS":
		return tokHAS, true
	case "STDIN":
		return tokSTDIN, true
	}
	return 0, false
}
