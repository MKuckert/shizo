package main

import (
	"encoding/json"
	"io"
	"strings"
)

// codexHarness adapts the pipeline to Codex's PreToolUse hook (docs/CODEX.md).
// The input payload shares Claude's snake_case shape (tool_name,
// tool_input.command, cwd, session_id). Codex has no "ask":
// permissionDecision "ask" is parsed but unsupported — the hook run is marked
// failed and the tool call proceeds, and exit 0 with no output also proceeds.
// shizo therefore maps ask and the unmatched fallthrough to deny: fail-closed
// over fail-open.
type codexHarness struct{}

func (codexHarness) Name() string { return "codex" }

func (codexHarness) Parse(raw []byte) (hookInput, string) {
	return parseSnakeInput(raw)
}

// Handles is always true: registration is matcher-scoped to Bash, so any input
// that reached the hook is in shizo's policy. A non-Bash tool with no command
// is a malformed-input deny (fail-closed), not a deferral.
func (codexHarness) Handles(hookInput) bool { return true }

// Emit writes Codex's decision envelope using only the fields Codex supports
// (permissionDecision, permissionDecisionReason, updatedInput): unsupported
// fields fail the hook run and let the tool call proceed. allow re-emits the
// original command as updatedInput — the documented "rewrite without blocking"
// shape, a no-op rewrite that skips Codex's approval prompt.
//
// Exit code: always 0, decision carried in the JSON. Codex blocks only on a
// parsed deny (exit 0 + JSON) or exit 2 with a non-empty stderr reason; every
// other shape — "ask", empty output, exit 2 without stderr, any other non-zero
// exit — marks the run failed and lets the tool call proceed (fail-open). A Go
// panic exits 2 with its stack trace on stderr, which Codex does block.
func (codexHarness) Emit(w io.Writer, in hookInput, cfg *config, outcome Outcome) int {
	hs := map[string]any{"hookEventName": "PreToolUse"}
	switch outcome.Kind {
	case kindAllow:
		hs["permissionDecision"] = "allow"
		hs["updatedInput"] = map[string]any{"command": in.ToolInput.Command}
	default: // deny, ask, undecided — all map to deny (docs/CODEX.md)
		hs["permissionDecision"] = "deny"
		reason := codexDenyReason(outcome)
		// A deny without a non-empty reason is an invalid output for Codex:
		// the run fails and the tool call proceeds (fail-open).
		if strings.TrimSpace(reason) == "" {
			reason = "denied by shizo"
		}
		hs["permissionDecisionReason"] = reason
	}
	b, _ := json.Marshal(map[string]any{"hookSpecificOutput": hs})
	w.Write(b)
	return 0
}

// codexDenyReason explains the downgrade when shizo's own verdict was not a
// deny: Codex would have proceeded, so the agent must see why it was blocked.
func codexDenyReason(outcome Outcome) string {
	switch outcome.Kind {
	case kindAsk:
		return "shizo asks for confirmation, but Codex has no ask — denied instead: " + outcome.Reason
	case kindUndecided:
		return "no allow rule matched, and Codex has no ask — denied instead: " + outcome.Reason
	default:
		return outcome.Reason
	}
}
