package main

import (
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
)

// A Harness adapts shizo's harness-agnostic decision pipeline to one coding
// agent: Parse maps the agent's raw hook input to shizo's canonical form, and
// Emit writes the agent's decision envelope from the outcome. decide() never
// learns about a harness; adapters wrap it (docs/ARCHITECTURE.md).
type Harness interface {
	// Name is the CLI event name (shizo hook <name>).
	Name() string
	// Parse maps raw hook input to canonical form. A non-empty reason is a
	// malformed-input deny; decide() is not run for it.
	Parse(raw []byte) (hookInput, string)
	// Handles reports whether shizo has a policy for this input. A harness
	// that only gates bash commands reports false for other tools, and the
	// pipeline defers to the agent's own permission flow.
	Handles(in hookInput) bool
	// Emit writes the harness-specific decision envelope for outcome to w and
	// returns the process exit code (0 = allow/ask, 2 = deny). cfg is nil when
	// the rule store could not be loaded.
	Emit(w io.Writer, in hookInput, cfg *config, outcome Outcome) int
}

// harnesses maps CLI event names to adapters. Adding a harness means adding
// an adapter file and an entry here (docs/ARCHITECTURE.md).
var harnesses = map[string]Harness{
	"pretooluse": claudeHarness{},
	"codex":      codexHarness{},
}

func harnessByName(name string) (Harness, bool) {
	h, ok := harnesses[name]
	return h, ok
}

// parseSnakeInput reads the Claude-format hook JSON (§2 step 1): tool_name,
// tool_input.command, cwd, session_id. Codex's PreToolUse payload and
// copilot's PascalCase (VS Code) payload share this shape. Returns a deny
// reason for malformed JSON or a missing/empty command.
func parseSnakeInput(raw []byte) (hookInput, string) {
	var in hookInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return in, "malformed hook input: " + err.Error()
	}
	if strings.TrimSpace(in.ToolInput.Command) == "" {
		return in, "empty command"
	}
	return in, ""
}

// runHarness is the shared hook pipeline: read stdin, parse with the adapter,
// evaluate, log, emit. This is the only mode that logs (see $SHIZO_DIR/logs).
func runHarness(h Harness, stdin io.Reader, stdout io.Writer) int {
	raw, _ := io.ReadAll(stdin)
	in, inErr := h.Parse(raw)

	root := hookRootFn()
	if root == "" {
		h.Emit(stdout, in, nil, Outcome{kindDeny, "cannot determine home directory for rules"})
		return 2
	}
	cfg := loadConfig(filepath.Join(root, "global", "rules"), projectRulesPathFor(in.Cwd), root)

	var (
		outcome Outcome
		logs    []cmdLog
	)
	switch {
	case inErr != "":
		outcome = Outcome{kindDeny, inErr}
	case !h.Handles(in):
		outcome = Outcome{kindUndecided, "no shizo policy for tool: " + in.ToolName}
	default:
		outcome, logs = decide(in.ToolInput.Command, in.Cwd, cfg, currentEnviron())
	}

	logDecision(in, outcome, logs) // best effort; never changes the decision
	return h.Emit(stdout, in, cfg, outcome)
}
