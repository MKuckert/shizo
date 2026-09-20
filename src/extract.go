package main

import (
	"mvdan.cc/sh/v3/syntax"
)

// Placeholders for words whose runtime value is not statically knowable.
// NUL-prefixed so they can never collide with a real literal token.
const (
	substToken = "\x00subst" // CmdSubst / ProcSubst present in the word
	varToken   = "\x00var"   // ParamExp / ArithmExp present in the word
)

// displayToken maps the internal placeholders to human-readable forms for logs.
func displayToken(t string) string {
	switch t {
	case substToken:
		return "<subst>"
	case varToken:
		return "<var>"
	default:
		return t
	}
}

func displayArgv(argv []string) []string {
	out := make([]string, len(argv))
	for i, t := range argv {
		out[i] = displayToken(t)
	}
	return out
}

// extractedCmd is one simple command (CallExpr) found anywhere in the tree.
type extractedCmd struct {
	argv           []string // resolved tokens; opaque->substToken, unresolvable->varToken
	unresolvable   bool     // §2 step 7: ParamExp/ArithmExp in an argv word
	hasAssign      bool     // §2 step 8: assignment prefix or bare assignment
	stdinFed       bool     // stdin redirect (<, <<, <<<, <>) or pipe RHS
	stdinCompound  bool     // pipe/redirect feeds a compound (non-CallExpr) sink
}

// writeRedirect is a write-target redirection collected for containment (§2 step 12).
type writeRedirect struct {
	target       string // resolved literal target ("" if unverifiable)
	unverifiable bool   // ParamExp/CmdSubst in the target -> deny
}

type extraction struct {
	commands    []extractedCmd
	writes      []writeRedirect
	hasFuncDecl bool // §2 step 5: a FuncDecl anywhere -> deny
}

// extract walks the whole AST (into FuncDecl bodies, subshells, loops, if/case,
// CmdSubst, ProcSubst) collecting every CallExpr and every write Redirect.
func extract(f *syntax.File) extraction {
	var ex extraction
	stdinSet := map[*syntax.CallExpr]bool{}     // CallExprs that receive stdin
	compoundSink := map[*syntax.CallExpr]bool{} // CallExprs inside a compound stdin sink

	// Pre-walk: mark stdin-fed commands from pipes and redirects.
	syntax.Walk(f, func(n syntax.Node) bool {
		switch x := n.(type) {
		case *syntax.BinaryCmd:
			if x.Op == syntax.Pipe || x.Op == syntax.PipeAll {
				markStdinSink(x.Y, stdinSet, compoundSink)
			}
		case *syntax.Stmt:
			for _, r := range x.Redirs {
				if isStdinRedirect(r) && x.Cmd != nil {
					markStdinSinkCmd(x.Cmd, stdinSet, compoundSink)
				}
			}
		}
		return true
	})

	syntax.Walk(f, func(n syntax.Node) bool {
		switch x := n.(type) {
		case *syntax.FuncDecl:
			ex.hasFuncDecl = true
		case *syntax.CallExpr:
			ex.commands = append(ex.commands, buildCmd(x, stdinSet, compoundSink))
		case *syntax.Redirect:
			if w, ok := classifyRedirect(x); ok {
				ex.writes = append(ex.writes, w)
			}
		}
		return true
	})
	return ex
}

func buildCmd(c *syntax.CallExpr, stdinSet, compoundSink map[*syntax.CallExpr]bool) extractedCmd {
	cmd := extractedCmd{
		hasAssign:     len(c.Assigns) > 0,
		stdinFed:      stdinSet[c],
		stdinCompound: compoundSink[c],
	}
	for _, w := range c.Args {
		text, kind := resolveWord(w)
		switch kind {
		case kindUnresolvable:
			cmd.unresolvable = true
			cmd.argv = append(cmd.argv, varToken)
		case kindOpaque:
			cmd.argv = append(cmd.argv, substToken)
		default:
			cmd.argv = append(cmd.argv, text)
		}
	}
	return cmd
}

// isStdinRedirect reports whether r redirects fd 0 into the command.
func isStdinRedirect(r *syntax.Redirect) bool {
	switch r.Op {
	case syntax.RdrIn, syntax.Hdoc, syntax.DashHdoc, syntax.WordHdoc, syntax.RdrInOut:
		// fd 0 when N is nil (implicit) or explicitly "0"
		return r.N == nil || r.N.Value == "0"
	}
	return false
}

// markStdinSink marks the right-hand side of a pipe as stdin-fed. If the sink
// is a compound command (not a CallExpr), all CallExprs inside it are marked
// as stdinCompound.
func markStdinSink(stmt *syntax.Stmt, stdinSet, compoundSink map[*syntax.CallExpr]bool) {
	if stmt == nil || stmt.Cmd == nil {
		return
	}
	markStdinSinkCmd(stmt.Cmd, stdinSet, compoundSink)
}

func markStdinSinkCmd(cmd syntax.Command, stdinSet, compoundSink map[*syntax.CallExpr]bool) {
	if ce, ok := cmd.(*syntax.CallExpr); ok {
		stdinSet[ce] = true
		return
	}
	// Compound sink: subshell, block, while, if, etc. — mark all inner
	// CallExprs so decide() can deny them.
	syntax.Walk(cmd, func(n syntax.Node) bool {
		if ce, ok := n.(*syntax.CallExpr); ok {
			compoundSink[ce] = true
		}
		return true
	})
}

func classifyRedirect(r *syntax.Redirect) (writeRedirect, bool) {
	if !isWriteOp(r.Op) {
		return writeRedirect{}, false
	}
	text, kind := resolveWord(r.Word)
	if kind != kindLiteral {
		// ParamExp/CmdSubst target: cannot verify containment -> deny.
		return writeRedirect{unverifiable: true}, true
	}
	return writeRedirect{target: text}, true
}

// isWriteOp reports whether the operator creates/truncates/appends a file target.
// Fd duplication (<& >&) and heredocs/reads carry no containable file target.
func isWriteOp(op syntax.RedirOperator) bool {
	switch op {
	case syntax.RdrOut, syntax.AppOut, syntax.RdrClob, syntax.AppClob,
		syntax.RdrAll, syntax.RdrAllClob, syntax.AppAll, syntax.AppAllClob,
		syntax.RdrInOut:
		return true
	}
	return false
}

type wordKind int

const (
	kindLiteral      wordKind = iota // fully static text
	kindOpaque                       // CmdSubst/ProcSubst present (inner cmds checked separately)
	kindUnresolvable                 // ParamExp/ArithmExp present -> not knowable at parse time
)

func maxKind(a, b wordKind) wordKind {
	if b > a {
		return b
	}
	return a
}

// resolveWord flattens a Word to literal text (§2 step 6), reporting how
// resolvable it is. Unresolvable dominates opaque dominates literal.
func resolveWord(w *syntax.Word) (string, wordKind) {
	var text string
	kind := kindLiteral
	for _, p := range w.Parts {
		t, k := resolvePart(p)
		text += t
		kind = maxKind(kind, k)
	}
	return text, kind
}

func resolvePart(p syntax.WordPart) (string, wordKind) {
	switch x := p.(type) {
	case *syntax.Lit:
		return x.Value, kindLiteral
	case *syntax.SglQuoted:
		return x.Value, kindLiteral
	case *syntax.DblQuoted:
		// Only literals inside keep the text matchable (e.g. -m "fix && cleanup").
		var text string
		kind := kindLiteral
		for _, ip := range x.Parts {
			t, k := resolvePart(ip)
			text += t
			kind = maxKind(kind, k)
		}
		return text, kind
	case *syntax.ParamExp, *syntax.ArithmExp:
		return "", kindUnresolvable
	case *syntax.CmdSubst, *syntax.ProcSubst:
		return "", kindOpaque
	default:
		// BraceExp, ExtGlob, and anything else: not statically a single literal.
		// Treat as opaque (safe: won't match specific rules, argv[0] fails to resolve).
		return "", kindOpaque
	}
}
