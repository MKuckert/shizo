package main

import (
	"os"
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// OutcomeKind is ordered weakest to strongest; aggregation keeps the strongest.
// kindUndecided is an unmatched fallthrough, kindAsk an explicit ask rule; both
// serialize as "ask" on the wire (§2.1 only defers the fallthrough).
type OutcomeKind int

const (
	kindAllow OutcomeKind = iota
	kindUndecided
	kindAsk
	kindDeny
)

func (k OutcomeKind) String() string {
	switch k {
	case kindDeny:
		return "deny"
	case kindAsk, kindUndecided:
		return "ask"
	default:
		return "allow"
	}
}

type Outcome struct {
	Kind   OutcomeKind
	Reason string
}

// escalate keeps the stronger of the two outcomes (first one wins on a tie).
func escalate(cur, cand Outcome) Outcome {
	if cand.Kind > cur.Kind {
		return cand
	}
	return cur
}

type environ struct {
	path      string   // inherited PATH
	tmpdirs   []string // TMPDIR (if set) and /tmp
	sandboxed bool     // NONO_CAP_FILE present
}

// decide runs the evaluation pipeline §2 steps 2-13 and returns the aggregated
// outcome plus per-command log records.
func decide(command, cwd string, cfg *config, env environ) (Outcome, []cmdLog) {
	if cfg.denyAll { // step 2 result
		return Outcome{kindDeny, cfg.denyReason}, nil
	}

	// step 3 — sandbox gate. Fail closed: only the explicit `dangerously-none`
	// escape hatch skips the gate; any other mode (incl. empty) denies.
	if cfg.sandboxMode != "dangerously-none" && !env.sandboxed {
		return Outcome{kindDeny, "sandbox gate: rules require 'sandbox nono' but NONO_CAP_FILE is unset"}, nil
	}

	// step 4 — parse
	f, err := syntax.NewParser().Parse(strings.NewReader(command), "command")
	if err != nil {
		return Outcome{kindDeny, "parse error: " + err.Error()}, nil
	}

	// steps 5-8 — extract
	ex := extract(f)
	if ex.hasFuncDecl {
		return Outcome{kindDeny, "function declarations are not allowed (a function may shadow a trusted name)"}, nil
	}

	out := Outcome{kindAllow, "all commands allowed"}

	var logs []cmdLog
	cdPresent := false

	for _, cmd := range ex.commands {
		cl := cmdLog{Argv: displayArgv(cmd.argv), StdinFed: cmd.stdinFed}
		disp := strings.Join(cl.Argv, " ")

		switch {
		case cmd.stdinCompound:
			cl.Decision = kindDeny.String()
			out = escalate(out, Outcome{kindDeny, "stdin feeds a compound command; the receiving command is not statically identifiable"})
		case cmd.unresolvable: // step 7
			cl.Decision = kindDeny.String()
			out = escalate(out, Outcome{kindDeny, "unresolvable word (ParamExp/ArithmExp) in: " + disp})
		case cmd.hasAssign: // step 8
			cl.Decision = kindDeny.String()
			out = escalate(out, Outcome{kindDeny, "assignment prefix or bare assignment is not allowed: " + disp})
		case len(cmd.argv) == 0:
			// empty CallExpr (bare assignment already handled above)
		default:
			decideCommand(cmd, cfg, cwd, env, &cl, &cdPresent, &out)
		}
		if cl.Decision != "" || len(cmd.argv) > 0 {
			logs = append(logs, cl)
		}
	}

	// step 12 — redirect containment
	for _, w := range ex.writes {
		switch {
		case cdPresent:
			out = escalate(out, Outcome{kindDeny, "write redirect denied because 'cd' changes the containment base"})
		case w.unverifiable:
			out = escalate(out, Outcome{kindDeny, "write redirect target is not statically resolvable"})
		case !contained(w.target, cwd, env.tmpdirs):
			out = escalate(out, Outcome{kindDeny, "write redirect escapes the allowed directories: " + w.target})
		}
	}

	return out, logs
}

func decideCommand(cmd extractedCmd, cfg *config, cwd string, env environ,
	cl *cmdLog, cdPresent *bool, out *Outcome) {

	name := cmd.argv[0]
	if name == substToken || name == varToken {
		cl.Decision = kindDeny.String()
		*out = escalate(*out, Outcome{kindDeny, "command name is not statically resolvable"})
		return
	}
	base := filepath.Base(name)

	// step 9 — uses raw basename, not resolved path: runs before resolution so
	// an unresolvable or untrusted `eval` still denies.
	if uncondDeny[base] {
		cl.Decision = kindDeny.String()
		*out = escalate(*out, Outcome{kindDeny, "command '" + base + "' is unconditionally denied"})
		return
	}

	res := resolveCommand(name, cfg.trusted, cwd, env.path) // step 10
	if res.builtin {
		if base == "cd" {
			*cdPresent = true
		}
		cl.Decision = kindAllow.String()
		cl.Resolved = "builtin"
		return
	}
	if res.err != "" {
		cl.Decision = kindDeny.String()
		*out = escalate(*out, Outcome{kindDeny, res.err})
		return
	}
	cl.Resolved = res.path

	// step 11 — deny → ask → allow rules. Match against the resolved path so
	// both bare names and absolute spellings hit the same rules.
	if r, ok := matchAny(cmd.argv, res.path, cmd.stdinFed, cfg.denyRules); ok {
		cl.Decision = kindDeny.String()
		cl.Rule = r.raw
		*out = escalate(*out, Outcome{kindDeny, "denied by rule: " + r.raw})
		return
	}
	if r, ok := matchAny(cmd.argv, res.path, cmd.stdinFed, cfg.askRules); ok {
		cl.Decision = kindAsk.String()
		cl.Rule = r.raw
		*out = escalate(*out, Outcome{kindAsk, "ask rule: " + r.raw})
		return
	}
	if r, ok := matchAny(cmd.argv, res.path, cmd.stdinFed, cfg.allowRules); ok {
		// stdin downgrade: an allowed stdin-fed command is subject to the stdin default
		if cmd.stdinFed {
			switch cfg.stdinDefault {
			case "deny":
				cl.Decision = kindDeny.String()
				cl.Rule = r.raw
				*out = escalate(*out, Outcome{kindDeny, "stdin-fed command downgraded by stdin directive: " + strings.Join(cl.Argv, " ")})
				return
			case "ask":
				cl.Decision = kindAsk.String()
				cl.Rule = r.raw
				*out = escalate(*out, Outcome{kindAsk, "stdin-fed command downgraded by stdin directive: " + strings.Join(cl.Argv, " ")})
				return
			}
		}
		cl.Decision = kindAllow.String()
		cl.Rule = r.raw
		return
	}
	cl.Decision = kindUndecided.String()
	*out = escalate(*out, Outcome{kindUndecided, "no rule matches: " + strings.Join(cl.Argv, " ")})
}

func matchAny(argv []string, resolved string, stdinFed bool, rules []rule) (rule, bool) {
	for _, r := range rules {
		if matchRule(argv, resolved, stdinFed, r) {
			return r, true
		}
	}
	return rule{}, false
}

func contained(target, cwd string, tmpdirs []string) bool {
	switch target {
	case "/dev/null", "/dev/stdout", "/dev/stderr":
		return true
	}
	abs := target
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(cwd, abs)
	}
	abs = filepath.Clean(abs)
	for _, b := range append([]string{cwd}, tmpdirs...) {
		if within(abs, b) {
			return true
		}
	}
	return false
}

func within(path, base string) bool {
	if base == "" {
		return false
	}
	base = filepath.Clean(base)
	return path == base || strings.HasPrefix(path, base+string(os.PathSeparator))
}
