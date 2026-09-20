package main

import (
	"os"
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

type Outcome string

const (
	outAllow Outcome = "allow"
	outDeny  Outcome = "deny"
	outAsk   Outcome = "ask"
)

type environ struct {
	path      string   // inherited PATH
	tmpdirs   []string // TMPDIR (if set) and /tmp
	sandboxed bool     // NONO_CAP_FILE present
}

// decide runs the evaluation pipeline §2 steps 2-13 and returns the aggregated
// outcome, a specific reason, per-command log records, and whether an ask
// result came from an explicit ask rule (vs. unmatched fallthrough).
func decide(command, cwd string, cfg *config, env environ) (Outcome, string, []cmdLog, bool) {
	if cfg.denyAll { // step 2 result
		return outDeny, cfg.denyReason, nil, false
	}

	// step 3 — sandbox gate. Fail closed: only the explicit `dangerously-none`
	// escape hatch skips the gate; any other mode (incl. empty) denies.
	if cfg.sandboxMode != "dangerously-none" && !env.sandboxed {
		return outDeny, "sandbox gate: rules require 'sandbox nono' but NONO_CAP_FILE is unset", nil, false
	}

	// step 4 — parse
	f, err := syntax.NewParser().Parse(strings.NewReader(command), "command")
	if err != nil {
		return outDeny, "parse error: " + err.Error(), nil, false
	}

	// steps 5-8 — extract
	ex := extract(f)
	if ex.hasFuncDecl {
		return outDeny, "function declarations are not allowed (a function may shadow a trusted name)", nil, false
	}

	outcome := outAllow
	reason := "all commands allowed"
	askExplicit := false // distinguishes explicit ask rules from unmatched fallthrough
	setDeny := func(r string) {
		if outcome != outDeny {
			outcome = outDeny
			reason = r
		}
	}
	setAsk := func(r string, explicit bool) {
		if outcome == outAllow {
			outcome = outAsk
			reason = r
			askExplicit = explicit
		} else if outcome == outAsk && explicit && !askExplicit {
			reason = r
			askExplicit = true
		}
	}

	var logs []cmdLog
	cdPresent := false

	for _, cmd := range ex.commands {
		cl := cmdLog{Argv: displayArgv(cmd.argv), StdinFed: cmd.stdinFed}
		disp := strings.Join(cl.Argv, " ")

		switch {
		case cmd.stdinCompound:
			cl.Decision = string(outDeny)
			setDeny("stdin feeds a compound command; the receiving command is not statically identifiable")
		case cmd.unresolvable: // step 7
			cl.Decision = string(outDeny)
			setDeny("unresolvable word (ParamExp/ArithmExp) in: " + disp)
		case cmd.hasAssign: // step 8
			cl.Decision = string(outDeny)
			setDeny("assignment prefix or bare assignment is not allowed: " + disp)
		case len(cmd.argv) == 0:
			// empty CallExpr (bare assignment already handled above)
		default:
			decideCommand(cmd, cfg, cwd, env, &cl, &cdPresent, setDeny, setAsk)
		}
		if cl.Decision != "" || len(cmd.argv) > 0 {
			logs = append(logs, cl)
		}
	}

	// step 12 — redirect containment
	for _, w := range ex.writes {
		switch {
		case cdPresent:
			setDeny("write redirect denied because 'cd' changes the containment base")
		case w.unverifiable:
			setDeny("write redirect target is not statically resolvable")
		case !contained(w.target, cwd, env.tmpdirs):
			setDeny("write redirect escapes the allowed directories: " + w.target)
		}
	}

	return outcome, reason, logs, askExplicit
}

func decideCommand(cmd extractedCmd, cfg *config, cwd string, env environ,
	cl *cmdLog, cdPresent *bool, setDeny func(string), setAsk func(string, bool)) {

	name := cmd.argv[0]
	if name == substToken || name == varToken {
		cl.Decision = string(outDeny)
		setDeny("command name is not statically resolvable")
		return
	}
	base := filepath.Base(name)

	// step 9 — uses raw basename, not resolved path: runs before resolution so
	// an unresolvable or untrusted `eval` still denies.
	if uncondDeny[base] {
		cl.Decision = string(outDeny)
		setDeny("command '" + base + "' is unconditionally denied")
		return
	}

	res := resolveCommand(name, cfg.trusted, cwd, env.path) // step 10
	if res.builtin {
		if base == "cd" {
			*cdPresent = true
		}
		cl.Decision = string(outAllow)
		cl.Resolved = "builtin"
		return
	}
	if res.err != "" {
		cl.Decision = string(outDeny)
		setDeny(res.err)
		return
	}
	cl.Resolved = res.path

	// step 11 — deny → ask → allow rules. Match against the resolved path so
	// both bare names and absolute spellings hit the same rules.
	if r, ok := matchAny(cmd.argv, res.path, cmd.stdinFed, cfg.denyRules); ok {
		cl.Decision = string(outDeny)
		cl.Rule = r.raw
		setDeny("denied by rule: " + r.raw)
		return
	}
	if r, ok := matchAny(cmd.argv, res.path, cmd.stdinFed, cfg.askRules); ok {
		cl.Decision = string(outAsk)
		cl.Rule = r.raw
		setAsk("ask rule: "+r.raw, true)
		return
	}
	if r, ok := matchAny(cmd.argv, res.path, cmd.stdinFed, cfg.allowRules); ok {
		// stdin downgrade: an allowed stdin-fed command is subject to the stdin default
		if cmd.stdinFed {
			switch cfg.stdinDefault {
			case "deny":
				cl.Decision = string(outDeny)
				cl.Rule = r.raw
				setDeny("stdin-fed command downgraded by stdin directive: " + strings.Join(cl.Argv, " "))
				return
			case "ask":
				cl.Decision = string(outAsk)
				cl.Rule = r.raw
				setAsk("stdin-fed command downgraded by stdin directive: "+strings.Join(cl.Argv, " "), true)
				return
			}
		}
		cl.Decision = string(outAllow)
		cl.Rule = r.raw
		return
	}
	cl.Decision = string(outAsk)
	setAsk("no rule matches: "+strings.Join(cl.Argv, " "), false)
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
