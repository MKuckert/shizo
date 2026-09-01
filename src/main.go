// Command bash-gate is a Claude Code PreToolUse hook that allowlists Bash
// commands at subcommand granularity. See PLAN-bash-allowlist.md.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
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

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Println("bash-gate " + version)
		return
	}
	os.Exit(run(os.Stdin, os.Stdout))
}

func run(stdin io.Reader, stdout io.Writer) int {
	raw, _ := io.ReadAll(stdin)
	in, inErr := parseInput(raw)

	cfg := loadConfig(globalRulesPath(), projectRulesPathFor(in.Cwd))

	var (
		outcome Outcome
		reason  string
		logs    []cmdLog
	)
	if inErr != "" {
		outcome, reason = outDeny, inErr
	} else {
		outcome, reason, logs = decide(in.ToolInput.Command, in.Cwd, cfg, currentEnviron())
	}

	logDecision(in, outcome, reason, logs) // best effort; never changes the decision

	switch outcome {
	case outDeny:
		emitDecision(stdout, outDeny, reason)
		return 2
	default: // allow / ask
		if cfg.returnMode == "automode" {
			fmt.Fprint(stdout, "defer") // §2.1: no decision, defer to Claude Code
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
	if d := os.Getenv("CLAUDE_BASH_RULES_DIR"); d != "" {
		return d
	}
	return filepath.Join(os.Getenv("HOME"), ".claude-bash-rules")
}

func globalRulesPath() string { return filepath.Join(installRoot(), "global", "rules") }

func projectRulesPathFor(cwd string) string {
	if cwd == "" {
		return ""
	}
	return filepath.Join(cwd, ".claude-bash-rules")
}

func logDir() string {
	return filepath.Join(os.Getenv("HOME"), ".claude-bash-rules", "logs")
}

// --- logging (§4.3) ---

type cmdLog struct {
	Argv     []string `json:"argv"`
	Resolved string   `json:"resolved,omitempty"`
	Decision string   `json:"decision"`
	Rule     string   `json:"rule,omitempty"`
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
