package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// The `test` subcommand dry-runs a raw command through the evaluation pipeline
// (§2) exactly as the hook would, but operator-facing: no stdin hook JSON is
// required, nothing is logged, and no hook decision JSON is emitted. It exists
// to answer "what would the gate do with this command?" without issuing it
// through Claude Code. See docs/ARCHITECTURE.md.
//
// The one wrinkle is the sandbox gate (§2 step 3): operators run `test` outside
// the sandbox, where decide() denies everything. --assume-sandboxed sets
// env.sandboxed=true so the rest of the pipeline is exercised faithfully; when a
// deny is sandbox-gate-only, we hint at that flag.

const testUsage = `usage: bash-gate test [flags] [COMMAND]

Dry-run COMMAND through the gate pipeline and print the decision. COMMAND may be
given as arguments, or read from stdin when omitted. Exit codes mirror the hook:
2 on deny, 0 on allow/ask.

Flags:
  --cwd DIR           working directory for resolution (default: current dir)
  --assume-sandboxed  treat the process as sandboxed (skip the sandbox gate)
  --json              emit the machine-readable log-line shape instead of a table
  --stdin-json        read a full hook JSON from stdin (command and cwd from it)`

func runTest(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	// Help only when it's the leading token — otherwise `-h`/`--help` belong to
	// the command under test (e.g. `test df -h`), which flag.Parse treats as a
	// positional. stdout + exit 0 to match validate.
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(stdout, testUsage)
		return 0
	}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintln(stderr, testUsage) }
	cwd := fs.String("cwd", "", "working directory for resolution")
	assumeSandboxed := fs.Bool("assume-sandboxed", false, "treat the process as sandboxed")
	asJSON := fs.Bool("json", false, "emit the machine-readable log-line shape")
	stdinJSON := fs.Bool("stdin-json", false, "read a full hook JSON from stdin")
	if err := fs.Parse(args); err != nil {
		return 2 // flag already printed the error + usage
	}

	command := strings.Join(fs.Args(), " ")

	if *stdinJSON {
		raw, _ := io.ReadAll(stdin)
		in, reason := parseInput(raw)
		if reason != "" {
			fmt.Fprintf(stderr, "test: %s\n", reason)
			return 2
		}
		command = in.ToolInput.Command
		if *cwd == "" {
			*cwd = in.Cwd
		}
	} else if command == "" {
		raw, _ := io.ReadAll(stdin)
		command = strings.TrimSpace(string(raw))
	}

	if strings.TrimSpace(command) == "" {
		fmt.Fprintf(stderr, "test: no command given\n\n%s\n", testUsage)
		return 2
	}

	if *cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			*cwd = wd
		}
	}

	cfg := loadConfig(globalRulesPath(), projectRulesPathFor(*cwd), installRoot())

	env := currentEnviron()
	if *assumeSandboxed {
		env.sandboxed = true
	}

	outcome, reason, logs := decide(command, *cwd, cfg, env)

	if *asJSON {
		if logs == nil {
			logs = []cmdLog{}
		}
		b, _ := json.MarshalIndent(logLine{
			Ts:       time.Now().Format(time.RFC3339),
			Command:  command,
			Commands: logs,
			Decision: string(outcome),
			Reason:   reason,
		}, "", "  ")
		fmt.Fprintln(stdout, string(b))
	} else {
		renderTest(stdout, outcome, reason, logs)
	}

	// Sandbox-gate-only denies are an operator footgun, not a real policy result.
	if outcome == outDeny && !env.sandboxed && strings.HasPrefix(reason, "sandbox gate:") {
		fmt.Fprintln(stderr, "hint: this deny is the sandbox gate; re-run with --assume-sandboxed to exercise the rules")
	}

	if outcome == outDeny {
		return 2
	}
	return 0
}

// renderTest prints the decision, reason, and a per-command breakdown.
func renderTest(w io.Writer, outcome Outcome, reason string, logs []cmdLog) {
	fmt.Fprintf(w, "decision: %s\n", outcome)
	fmt.Fprintf(w, "reason:   %s\n", reason)
	if len(logs) == 0 {
		return
	}
	fmt.Fprintln(w, "commands:")
	for _, cl := range logs {
		line := fmt.Sprintf("  %-5s %s", cl.Decision, strings.Join(cl.Argv, " "))
		if cl.Resolved != "" {
			line += "  (" + cl.Resolved + ")"
		}
		if cl.Rule != "" {
			line += "  [" + cl.Rule + "]"
		}
		fmt.Fprintln(w, line)
	}
}
