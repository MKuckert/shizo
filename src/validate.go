package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The `validate` subcommand is an operator-facing linter for the rule tiers. It
// is entirely separate from the hook evaluation pipeline (§2): no sandbox gate,
// no logging, no deny decision. Unlike loadConfig (which fails closed on the
// first bad line), it reports *every* syntax error plus semantic warnings, so
// rules can be checked before install.

const validateUsage = `usage: bash-gate validate [--project] [FILE...]

With no FILE, validates the resolved global tier and the project tier in the
current directory. FILE arguments are validated as a global tier unless
--project is given. Exit 0 when no errors (warnings allowed), 1 on errors,
2 on a usage problem.`

type level int

const (
	levelError level = iota
	levelWarn
)

func (l level) String() string {
	if l == levelError {
		return "error"
	}
	return "warning"
}

// finding is one diagnostic. line 0 means the whole file (no specific line).
type finding struct {
	file  string
	line  int
	level level
	msg   string
}

// runValidate implements `bash-gate validate`. It never reads stdin or emits
// hook JSON; it prints human-readable diagnostics and returns a process code.
func runValidate(args []string, stdout, stderr io.Writer) int {
	project := false
	var paths []string
	for _, a := range args {
		switch {
		case a == "--project":
			project = true
		case a == "-h" || a == "--help":
			fmt.Fprintln(stdout, validateUsage)
			return 0
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, "validate: unknown flag %q\n\n%s\n", a, validateUsage)
			return 2
		default:
			paths = append(paths, a)
		}
	}

	type target struct {
		file     string
		project  bool
		optional bool // absent file is not an error (the project tier is optional, §2 step 2)
	}
	var targets []target
	if len(paths) == 0 {
		targets = append(targets, target{globalRulesPath(), false, false})
		if cwd, err := os.Getwd(); err == nil {
			targets = append(targets, target{projectRulesPathFor(cwd), true, true})
		}
	} else {
		for _, p := range paths {
			targets = append(targets, target{p, project, false})
		}
	}

	var findings []finding
	for _, t := range targets {
		data, err := os.ReadFile(t.file)
		if err != nil {
			if t.optional && os.IsNotExist(err) {
				fmt.Fprintf(stdout, "%s: not present, skipped\n", t.file)
				continue
			}
			findings = append(findings, finding{t.file, 0, levelError, "unreadable: " + err.Error()})
			continue
		}
		findings = append(findings, validateFile(t.file, string(data), t.project)...)
	}

	return report(stdout, findings)
}

// validateFile parses one tier collecting all syntax errors (rather than
// stopping at the first, as parseTier does), then runs the semantic lint pass.
func validateFile(file, content string, project bool) []finding {
	cfg := &config{returnMode: "decision"}
	var st tierState
	var out []finding

	lineByRaw := map[string]int{} // trimmed rule/directive line -> line number
	trustLine := map[string]int{} // trusted dir -> line number
	seenRule := map[string]int{}  // duplicate allow/deny detection: raw -> first line

	for i, line := range strings.Split(content, "\n") {
		lineNo := i + 1
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		beforeTrust := len(cfg.trusted)
		reason, ok := parseLine(cfg, trimmed, project, &st)
		if !ok {
			out = append(out, finding{file, lineNo, levelError, reason})
			continue
		}
		lineByRaw[trimmed] = lineNo
		if len(cfg.trusted) > beforeTrust {
			trustLine[cfg.trusted[beforeTrust]] = lineNo
		}
		if strings.HasPrefix(trimmed, "allow ") || strings.HasPrefix(trimmed, "deny ") {
			if first, dup := seenRule[trimmed]; dup {
				out = append(out, finding{file, lineNo, levelWarn,
					fmt.Sprintf("duplicate rule (first seen on line %d): %s", first, trimmed)})
			} else {
				seenRule[trimmed] = lineNo
			}
		}
	}

	// sandbox is required in the global tier — mirror loadConfig's fail-closed rule (§3).
	if !project && !st.sawSandbox {
		out = append(out, finding{file, 0, levelError,
			"global tier must declare a sandbox directive (nono or dangerously-none)"})
	}

	out = append(out, lintDeadRules(cfg, file, lineByRaw)...)
	out = append(out, lintShadowedAllows(cfg, file, lineByRaw)...)
	out = append(out, lintIgnoredDirectives(cfg, file, lineByRaw)...)
	out = append(out, lintTrustDirs(cfg, file, trustLine)...)

	sort.SliceStable(out, func(i, j int) bool { return out[i].line < out[j].line })
	return out
}

// lintDeadRules flags rules whose command can never reach rule matching:
// unconditionally-denied names (§2 step 9 fires first) and safe builtins
// (§3 short-circuits before rules).
func lintDeadRules(cfg *config, file string, lineOf map[string]int) []finding {
	var out []finding
	check := func(r rule) {
		cmd, ok := ruleCommand(r)
		if !ok {
			return
		}
		base := filepath.Base(cmd)
		switch {
		case uncondDeny[base]:
			out = append(out, finding{file, lineOf[r.raw], levelWarn,
				fmt.Sprintf("rule has no effect: %q is unconditionally denied: %s", base, r.raw)})
		case safeBuiltins[base]:
			out = append(out, finding{file, lineOf[r.raw], levelWarn,
				fmt.Sprintf("rule has no effect: %q is a safe builtin (always allowed before rules): %s", base, r.raw)})
		}
	}
	for _, r := range cfg.allowRules {
		check(r)
	}
	for _, r := range cfg.denyRules {
		check(r)
	}
	return out
}

// lintShadowedAllows flags allow rules fully overridden by a deny (deny wins,
// §4.1 precedence). Conservative and sound: only reports when every command the
// allow could match is provably denied.
func lintShadowedAllows(cfg *config, file string, lineOf map[string]int) []finding {
	var out []finding
	for _, a := range cfg.allowRules {
		for _, d := range cfg.denyRules {
			if denyShadowsAllow(a, d) {
				out = append(out, finding{file, lineOf[a.raw], levelWarn,
					fmt.Sprintf("allow rule is fully shadowed by deny rule %q (deny always wins): %s", d.raw, a.raw)})
				break
			}
		}
	}
	return out
}

// lintIgnoredDirectives flags project-tier trust/sandbox/returntype lines, which
// are ignored (§4.2) — they can only loosen, so the project tier drops them.
func lintIgnoredDirectives(cfg *config, file string, lineOf map[string]int) []finding {
	var out []finding
	for _, ln := range cfg.ignored {
		out = append(out, finding{file, lineOf[ln], levelWarn,
			"directive ignored in project tier (global-tier only): " + ln})
	}
	return out
}

// lintTrustDirs flags trust directories that do not exist or are not directories.
func lintTrustDirs(cfg *config, file string, lineOf map[string]int) []finding {
	var out []finding
	for _, dir := range cfg.trusted {
		if msg, ok := checkTrustDir(dir); !ok {
			out = append(out, finding{file, lineOf[dir], levelWarn, msg})
		}
	}
	return out
}

// checkTrustDir stats one trusted directory. ok=false with a message when it is
// missing or not a directory.
func checkTrustDir(dir string) (string, bool) {
	fi, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "trusted directory does not exist: " + dir, false
		}
		return "trusted directory not readable: " + dir + " (" + err.Error() + ")", false
	}
	if !fi.IsDir() {
		return "trusted path is not a directory: " + dir, false
	}
	return "", true
}

// ruleCommand returns the rule's command word (first token) when it is a literal.
func ruleCommand(r rule) (string, bool) {
	if len(r.tokens) == 0 || r.tokens[0].kind != tokLiteral {
		return "", false
	}
	return r.tokens[0].text, true
}

// denyShadowsAllow reports whether deny rule d overrides every command allow
// rule a could match. Sound (no false positives): d must be a literal prefix,
// optionally ending in a trailing ANY, with no HAS; a's leading tokens must be
// literals equal to that prefix.
func denyShadowsAllow(a, d rule) bool {
	var prefix []string
	trailingAny := false
	for i, t := range d.tokens {
		switch t.kind {
		case tokLiteral:
			if trailingAny { // literal after ANY — not a simple prefix, bail
				return false
			}
			prefix = append(prefix, t.text)
		case tokANY:
			if i != len(d.tokens)-1 { // ANY must be last for this sound check
				return false
			}
			trailingAny = true
		case tokHAS:
			return false // HAS matches one flag anywhere; not a prefix relation
		}
	}

	if len(a.tokens) < len(prefix) {
		return false
	}
	for i := range prefix {
		if a.tokens[i].kind != tokLiteral || a.tokens[i].text != prefix[i] {
			return false
		}
	}
	if trailingAny {
		// d matches any argv starting with prefix; a only produces such argvs.
		return true
	}
	// d is a closed literal: it matches exactly one argv, so a must too — a must
	// be exactly the prefix with nothing more.
	return len(a.tokens) == len(prefix)
}

// report prints findings grouped in encounter order and returns the exit code:
// 1 if any error, else 0.
func report(w io.Writer, findings []finding) int {
	errors, warnings := 0, 0
	for _, f := range findings {
		switch f.level {
		case levelError:
			errors++
		case levelWarn:
			warnings++
		}
		if f.line > 0 {
			fmt.Fprintf(w, "%s:%d: %s: %s\n", f.file, f.line, f.level, f.msg)
		} else {
			fmt.Fprintf(w, "%s: %s: %s\n", f.file, f.level, f.msg)
		}
	}
	fmt.Fprintf(w, "%d error(s), %d warning(s)\n", errors, warnings)
	if errors > 0 {
		return 1
	}
	return 0
}
