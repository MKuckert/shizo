package main

import (
	"path/filepath"
	"strings"
)

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

// matchCmdToken checks the rule's first token against the resolved path (§4.1).
// A bare name matches filepath.Base(resolved); a slash-containing token matches
// resolved exactly.
func matchCmdToken(tok, resolved string) bool {
	if strings.ContainsRune(tok, '/') {
		return tok == resolved
	}
	return tok == filepath.Base(resolved)
}

// matchTok checks one rule token against the argv at position i. Index 0 is
// the command token and uses resolved-path canonicalization; all others are literal.
func matchTok(i int, tok, argv, resolved string) bool {
	if i == 0 {
		return matchCmdToken(tok, resolved)
	}
	return tok == argv
}

// matchPrefix checks a literal token slice against argv, using matchTok.
func matchPrefix(toks []ruleToken, argv []string, resolved string) bool {
	for i, t := range toks {
		if !matchTok(i, t.text, argv[i], resolved) {
			return false
		}
	}
	return true
}

// matchRule reports whether argv satisfies rule r (§4.1). resolved is the
// absolute path from resolveCommand, used to canonicalize the command token.
func matchRule(argv []string, resolved string, r rule) bool {
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
		if !matchPrefix(prefix, argv, resolved) {
			return false
		}
		return hasFlag(argv[len(prefix):], flag)

	case anyIdx >= 0:
		head := r.tokens[:anyIdx]
		tail := r.tokens[anyIdx+1:]
		if len(argv) < len(head)+len(tail) {
			return false
		}
		if !matchPrefix(head, argv, resolved) {
			return false
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
		return matchPrefix(r.tokens, argv, resolved)
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
