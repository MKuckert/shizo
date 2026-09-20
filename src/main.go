// Command shizo is a Claude Code PreToolUse hook that allowlists Bash
// commands at subcommand granularity. See PLAN-bash-allowlist.md.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"
)

type hookInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
	Cwd       string `json:"cwd"`
	SessionID string `json:"session_id"`
}

// version is the build version, stamped at build time via
// -ldflags "-X main.version=...". Defaults to "dev" for plain `go build`.
var version = "dev"

// rootUsage is the top-level overview printed by `help`/`--help`/`-h`/`-?`.
// It indexes the operator subcommands; each owns its own detailed usage const.
const rootUsage = `shizo — a Claude Code PreToolUse hook that allowlists Bash
commands at subcommand granularity. "claude-hook" is the hook entrypoint: it
reads hook JSON on stdin and emits a decision. Every other subcommand is
operator-only and never touches the hook pipeline; with no arguments this
prints usage and exits (it never reads stdin).

usage: shizo [command] [args]

Commands:
  claude-hook hook mode: read hook JSON on stdin, emit an allow/ask/deny decision
  validate    lint rule files (all errors + semantic warnings)
  attest      record operator approval of a rule tier (required before it is trusted)
  test        dry-run a command through the evaluation pipeline
  selfcheck   verify the sandbox enforces the install's read/write shape
  help [cmd]  show this overview, or detailed usage for a command
              (aliases: --help, -h, -?)
  --version   print the version (alias: -v)
  (none)      → help

Rules live in $SHIZO_DIR (default ~/.shizo): the global
tier under global/rules, an optional per-project tier at <cwd>/.shizo.
See docs/RULES.md. Run "shizo help <command>" for per-command usage.`

func main() {
	os.Exit(dispatch(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// dispatch routes operator subcommands; "claude-hook" is the hook entrypoint
// (what shizo.sh invokes). Arg-less prints usage and exits — it never reads
// stdin. An unrecognised first arg is a mistyped subcommand, not hook input,
// so it errors rather than blocking on stdin.
func dispatch(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "claude-hook":
			return runClaudeHook(stdin, stdout)
		case "--version", "-v":
			fmt.Fprintln(stdout, "shizo "+version)
			return 0
		case "help", "--help", "-h", "-?":
			return runHelp(args[1:], stdout)
		case "validate":
			return runValidate(args[1:], stdout, stderr)
		case "attest":
			return runAttest(args[1:], stdout, stderr)
		case "test":
			return runTest(args[1:], stdin, stdout, stderr)
		case "selfcheck":
			return runSelfcheck(args[1:], stdout, stderr)
		default:
			fmt.Fprintf(stderr, "shizo: unknown command %q\n\n%s\n", args[0], rootUsage)
			return 2
		}
	}
	return runHelp(nil, stdout)
}

// runHelp prints the top-level overview, or a named command's own usage const.
// Unknown or empty command falls back to rootUsage. Always exit 0; never reads
// stdin or emits a hook decision.
func runHelp(args []string, stdout io.Writer) int {
	usage := rootUsage
	if len(args) > 0 {
		switch args[0] {
		case "claude-hook":
			usage = claudeHookUsage
		case "validate":
			usage = validateUsage
		case "attest":
			usage = attestUsage
		case "test":
			usage = testUsage
		case "selfcheck":
			usage = selfcheckUsage
		}
	}
	fmt.Fprintln(stdout, usage)
	return 0
}

// claudeHookUsage documents the hook entrypoint invoked by shizo.sh. This is
// the only subcommand that reads stdin and logs decisions.
const claudeHookUsage = `shizo claude-hook — the Claude Code PreToolUse hook entrypoint.

usage: shizo claude-hook

Reads hook JSON on stdin (tool_name, tool_input.command, cwd, session_id),
evaluates the command against the rule tiers, and emits a decision object on
stdout. Exit 0 for allow/ask, exit 2 for deny. The only mode that logs
(see $SHIZO_DIR/logs).`

// hookRootFn resolves the install root for hook mode via the password
// database (getpwuid), not env vars. An agent that can set $HOME or
// $SHIZO_DIR cannot redirect the hook to a forged rule store.
// Var so tests can override.
var hookRootFn = defaultHookRoot

func defaultHookRoot() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	return filepath.Join(u.HomeDir, ".shizo")
}

func runClaudeHook(stdin io.Reader, stdout io.Writer) int {
	raw, _ := io.ReadAll(stdin)
	in, inErr := parseInput(raw)

	root := hookRootFn()
	if root == "" {
		emitDecision(stdout, outDeny, "cannot determine home directory for rules")
		return 2
	}
	gPath := filepath.Join(root, "global", "rules")
	cfg := loadConfig(gPath, projectRulesPathFor(in.Cwd), root)

	var (
		outcome     Outcome
		reason      string
		logs        []cmdLog
		askExplicit bool
	)
	if inErr != "" {
		outcome, reason = outDeny, inErr
	} else {
		outcome, reason, logs, askExplicit = decide(in.ToolInput.Command, in.Cwd, cfg, currentEnviron())
	}

	logDecision(in, outcome, reason, logs) // best effort; never changes the decision

	switch outcome {
	case outDeny:
		emitDecision(stdout, outDeny, reason)
		return 2
	default: // allow / ask
		// §2.1: defer only unmatched fallthrough, not explicit ask rules
		if cfg.returnMode == "automode" && outcome == outAsk && !askExplicit {
			fmt.Fprint(stdout, "defer")
			return 0
		}
		emitDecision(stdout, outcome, reason)
		return 0
	}
}

// parseInput reads the hook JSON (§2 step 1). Returns a deny reason on
// malformed JSON or an absent/empty command.
func parseInput(raw []byte) (hookInput, string) {
	var in hookInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return in, "malformed hook input: " + err.Error()
	}
	if strings.TrimSpace(in.ToolInput.Command) == "" {
		return in, "empty command"
	}
	return in, ""
}

func emitDecision(w io.Writer, outcome Outcome, reason string) {
	obj := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       string(outcome),
			"permissionDecisionReason": reason,
		},
	}
	b, _ := json.Marshal(obj)
	w.Write(b)
}

func currentEnviron() environ {
	tmp := []string{}
	if t := os.Getenv("TMPDIR"); t != "" {
		tmp = append(tmp, t)
	}
	tmp = append(tmp, "/tmp")
	return environ{
		path:      os.Getenv("PATH"),
		tmpdirs:   tmp,
		sandboxed: os.Getenv("NONO_CAP_FILE") != "",
	}
}

// --- paths (§0) ---

func installRoot() string {
	if d := os.Getenv("SHIZO_DIR"); d != "" {
		return d
	}
	return filepath.Join(os.Getenv("HOME"), ".shizo")
}

func globalRulesPath() string { return filepath.Join(installRoot(), "global", "rules") }

func projectRulesPathFor(cwd string) string {
	if cwd == "" {
		return ""
	}
	return filepath.Join(cwd, ".shizo")
}

func logDir() string {
	return filepath.Join(os.Getenv("HOME"), ".shizo", "logs")
}

// --- logging (§4.3) ---

type cmdLog struct {
	Argv     []string `json:"argv"`
	Resolved string   `json:"resolved,omitempty"`
	Decision string   `json:"decision"`
	Rule     string   `json:"rule,omitempty"`
	StdinFed bool     `json:"stdin_fed,omitempty"`
}

type logLine struct {
	Ts        string   `json:"ts"`
	SessionID string   `json:"session_id"`
	Command   string   `json:"command"`
	Commands  []cmdLog `json:"commands"`
	Decision  string   `json:"decision"`
	Reason    string   `json:"reason"`
}

func logDecision(in hookInput, outcome Outcome, reason string, cmds []cmdLog) {
	dir := logDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	if cmds == nil {
		cmds = []cmdLog{}
	}
	ll := logLine{
		Ts:        time.Now().Format(time.RFC3339),
		SessionID: in.SessionID,
		Command:   in.ToolInput.Command,
		Commands:  cmds,
		Decision:  string(outcome),
		Reason:    reason,
	}
	b, err := json.Marshal(ll)
	if err != nil {
		return
	}
	path := filepath.Join(dir, time.Now().Format("2006-01-02")+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(b, '\n'))
}
