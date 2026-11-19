package main

import (
	"encoding/json"
	"fmt"
	"io"
)

// claudeHarness adapts the pipeline to Claude Code's PreToolUse hook
// (docs/CLAUDE_CODE_HOOKS.md). Claude is the reference harness: its envelope
// shape is what shizo.sh and the pi/opencode plugins speak.
type claudeHarness struct{}

func (claudeHarness) Name() string { return "pretooluse" }

func (claudeHarness) Parse(raw []byte) (hookInput, string) {
	return parseSnakeInput(raw)
}

// Handles is always true: Claude registration is matcher-scoped to Bash, so
// any input that reached the hook is in shizo's policy. A non-Bash tool with
// no command is a malformed-input deny (fail-closed), not a deferral.
func (claudeHarness) Handles(hookInput) bool { return true }

// Emit writes Claude's decision envelope: exit 0 for allow/ask, exit 2 for
// deny. In automode an unmatched fallthrough emits the `defer` sentinel
// (exit 0) instead of a decision — shizo.sh maps it to empty output.
func (claudeHarness) Emit(w io.Writer, in hookInput, cfg *config, outcome Outcome) int {
	if cfg != nil && cfg.returnMode == "automode" && outcome.Kind == kindUndecided {
		fmt.Fprint(w, "defer")
		return 0
	}
	obj := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       outcome.Kind.String(),
			"permissionDecisionReason": outcome.Reason,
		},
	}
	b, _ := json.Marshal(obj)
	w.Write(b)
	if outcome.Kind == kindDeny {
		return 2
	}
	return 0
}
