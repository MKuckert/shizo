package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

type config struct {
	trusted     []string
	returnMode  string // "decision" (default) or "automode"
	sandboxMode string // "nono" (default) or "dangerously-none"
	allowRules  []rule
	denyRules   []rule
	ignored     []string // ignored project-tier directive lines (logged)
	denyAll     bool
	denyReason  string
}

func denyAllConfig(reason string) *config {
	return &config{returnMode: "decision", sandboxMode: "nono", denyAll: true, denyReason: reason}
}

// loadConfig reads the global tier (required) and project tier (optional).
// Any load/parse failure yields a deny-all config with a reason (§2 step 2).
func loadConfig(globalPath, projectPath string) *config {
	cfg := &config{returnMode: "decision", sandboxMode: "nono"}

	data, err := os.ReadFile(globalPath)
	if err != nil {
		return denyAllConfig("global rules unreadable (" + globalPath + "): " + err.Error())
	}
	if reason, ok := parseTier(cfg, string(data), false); !ok {
		return denyAllConfig(reason)
	}

	if projectPath != "" {
		data, err := os.ReadFile(projectPath)
		if err == nil {
			if reason, ok := parseTier(cfg, string(data), true); !ok {
				return denyAllConfig(reason)
			}
		} else if !os.IsNotExist(err) {
			return denyAllConfig("project rules unreadable (" + projectPath + "): " + err.Error())
		}
	}

	return cfg
}

// parseTier folds one rules file into cfg. Returns (reason, false) on a
// malformed line. project restricts loosening directives.
func parseTier(cfg *config, content string, project bool) (string, bool) {
	sawRule := false
	sawReturntype := false
	sawSandbox := false

	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		toks, err := parseRuleWords(trimmed)
		if err != nil {
			return "malformed rule line: " + trimmed + " (" + err.Error() + ")", false
		}
		if len(toks) == 0 {
			continue
		}

		head := toks[0]
		if head.kind != tokLiteral {
			return "malformed rule line: " + trimmed, false
		}

		switch head.text {
		case "trust":
			if project {
				cfg.ignored = append(cfg.ignored, trimmed)
				continue
			}
			if sawRule {
				return "directive after rules: " + trimmed, false
			}
			if len(toks) != 2 || toks[1].kind != tokLiteral || !filepath.IsAbs(toks[1].text) {
				return "trust needs one absolute directory: " + trimmed, false
			}
			cfg.trusted = append(cfg.trusted, toks[1].text)

		case "returntype":
			if project {
				cfg.ignored = append(cfg.ignored, trimmed)
				continue
			}
			if sawRule {
				return "directive after rules: " + trimmed, false
			}
			if sawReturntype {
				return "duplicate returntype: " + trimmed, false
			}
			if len(toks) != 2 || (toks[1].text != "decision" && toks[1].text != "automode") {
				return "returntype must be decision or automode: " + trimmed, false
			}
			cfg.returnMode = toks[1].text
			sawReturntype = true

		case "sandbox":
			if project {
				cfg.ignored = append(cfg.ignored, trimmed)
				continue
			}
			if sawRule {
				return "directive after rules: " + trimmed, false
			}
			if sawSandbox {
				return "duplicate sandbox: " + trimmed, false
			}
			if len(toks) != 2 || toks[1].kind != tokLiteral ||
				(toks[1].text != "nono" && toks[1].text != "dangerously-none") {
				return "sandbox must be nono or dangerously-none: " + trimmed, false
			}
			cfg.sandboxMode = toks[1].text
			sawSandbox = true

		case "allow":
			r, reason, ok := buildRule("allow", toks[1:], trimmed)
			if !ok {
				return reason, false
			}
			cfg.allowRules = append(cfg.allowRules, r)
			sawRule = true

		case "deny":
			r, reason, ok := buildRule("deny", toks[1:], trimmed)
			if !ok {
				return reason, false
			}
			cfg.denyRules = append(cfg.denyRules, r)
			sawRule = true

		default:
			return "unknown directive: " + trimmed, false
		}
	}

	return "", true
}

// buildRule validates the token stream of an allow/deny line (§4.1).
func buildRule(action string, toks []ruleToken, raw string) (rule, string, bool) {
	if len(toks) == 0 {
		return rule{}, "rule needs at least one token: " + raw, false
	}

	anyCount, hasCount, hasIdx := 0, 0, -1
	for i, t := range toks {
		switch t.kind {
		case tokANY:
			anyCount++
		case tokHAS:
			hasCount++
			hasIdx = i
		}
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

	return rule{action: action, tokens: toks, raw: raw}, "", true
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
		text, kind := resolveWord(w)
		if kind != kindLiteral {
			return nil, fmt.Errorf("non-literal token in rule")
		}
		toks = append(toks, ruleToken{text: text, kind: tokLiteral})
	}
	return toks, nil
}

// keyword returns the keyword kind if w is a bare (unquoted) ANY or HAS literal.
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
	}
	return 0, false
}
