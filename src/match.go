package main

import "strings"

type tokKind int

const (
	tokLiteral tokKind = iota
	tokANY
	tokHAS
)

type ruleToken struct {
	text string
	kind tokKind
}

type rule struct {
	action string // "allow" or "deny"
	tokens []ruleToken
	raw    string // original line, for logs and deny reasons
}

// matchRule reports whether argv satisfies rule r (§4.1). Pure, no I/O.
func matchRule(argv []string, r rule) bool {
	hasIdx, anyIdx := -1, -1
	for i, t := range r.tokens {
		switch t.kind {
		case tokHAS:
			hasIdx = i
		case tokANY:
			anyIdx = i
		}
	}

	switch {
	case hasIdx >= 0:
		// prefix... HAS flag  — validation guarantees HAS is second-to-last
		// with exactly one literal flag after it and >=1 prefix token.
		prefix := r.tokens[:hasIdx]
		flag := r.tokens[hasIdx+1].text
		if len(argv) < len(prefix) {
			return false
		}
		for i, t := range prefix {
			if argv[i] != t.text {
				return false
			}
		}
		return hasFlag(argv[len(prefix):], flag)

	case anyIdx >= 0:
		head := r.tokens[:anyIdx]
		tail := r.tokens[anyIdx+1:]
		if len(argv) < len(head)+len(tail) {
			return false
		}
		for i, t := range head {
			if argv[i] != t.text {
				return false
			}
		}
		for i, t := range tail {
			if argv[len(argv)-len(tail)+i] != t.text {
				return false
			}
		}
		return true

	default:
		if len(argv) != len(r.tokens) {
			return false
		}
		for i, t := range r.tokens {
			if argv[i] != t.text {
				return false
			}
		}
		return true
	}
}

// hasFlag reports whether flag appears anywhere in rest, applying =-splitting
// (--output=/tmp/x tests as --output) and single-dash bundle matching (-f in -rf).
func hasFlag(rest []string, flag string) bool {
	shortBundle := len(flag) == 2 && flag[0] == '-' && flag[1] != '-'
	for _, tok := range rest {
		base := tok
		if i := strings.IndexByte(tok, '='); i >= 0 {
			base = tok[:i]
		}
		if base == flag {
			return true
		}
		if shortBundle && len(base) >= 2 && base[0] == '-' && base[1] != '-' {
			if strings.IndexByte(base[1:], flag[1]) >= 0 {
				return true
			}
		}
	}
	return false
}
