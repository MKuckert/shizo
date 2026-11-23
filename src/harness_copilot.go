package main

import (
	"encoding/json"
	"io"
	"strings"
)

// copilotHarness adapts the pipeline to GitHub Copilot CLI's preToolUse hook
// (docs/COPILOT.md). It accepts both payload shapes: the native camelCase
// (toolName/toolArgs) and the PascalCase PreToolUse snake_case
// (tool_name/tool_input, matching the VS Code and Claude formats).
//
// Only bash is gated: a non-bash tool defers to Copilot's own permission flow
// (empty output). In the default decision mode, ask and the unmatched
// fallthrough map to deny — since v1.0.53 the TUI auto-approves a hook "ask"
// without waiting for the user (github/copilot-cli#3590), so emitting one
// would fail open. In automode the fallthrough defers instead (empty output),
// like Claude's returntype directive.
type copilotHarness struct{}

func (copilotHarness) Name() string { return "copilot" }

// copilotRaw accepts both payload shapes at once; the snake_case fields win
// when present (the PascalCase PreToolUse payload uses them exclusively).
type copilotRaw struct {
	ToolName     string          `json:"toolName"`
	ToolArgs     json.RawMessage `json:"toolArgs"`
	ToolSnake    string          `json:"tool_name"`
	InputRaw     json.RawMessage `json:"tool_input"`
	Cwd          string          `json:"cwd"`
	SessionID    string          `json:"session_id"`
	SessionCamel string          `json:"sessionId"`
}

func (copilotHarness) Parse(raw []byte) (hookInput, string) {
	var r copilotRaw
	if err := json.Unmarshal(raw, &r); err != nil {
		return hookInput{}, "malformed hook input: " + err.Error()
	}
	in := hookInput{}
	in.Cwd = r.Cwd
	if r.SessionCamel != "" {
		in.SessionID = r.SessionCamel
	} else {
		in.SessionID = r.SessionID
	}
	if r.ToolSnake != "" {
		in.ToolName = r.ToolSnake
	} else {
		in.ToolName = r.ToolName
	}
	if in.ToolName == "" {
		return in, "missing tool name"
	}
	if !isBashTool(in.ToolName) {
		return in, "" // defer: Handles is false, Emit writes nothing
	}
	var args json.RawMessage
	if len(r.InputRaw) > 0 {
		args = r.InputRaw
	} else {
		args = r.ToolArgs
	}
	cmd, err := commandFromArgs(args)
	if err != "" {
		return in, err
	}
	in.ToolInput.Command = cmd
	if strings.TrimSpace(cmd) == "" {
		return in, "empty command"
	}
	return in, ""
}

// isBashTool reports whether name is Copilot's shell tool: "bash" in the
// native camelCase payload, "Bash" (the Claude tool name) in the PascalCase
// PreToolUse payload.
func isBashTool(name string) bool {
	return strings.EqualFold(name, "bash")
}

// commandFromArgs extracts the shell command from Copilot's tool args, which
// may be an object with a "command" field, a JSON string of that object, or a
// bare command string. A nil/absent args value yields an empty command; the
// caller denies it for bash.
func commandFromArgs(raw json.RawMessage) (string, string) {
	raw = trimJSONSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return "", ""
	}
	switch raw[0] {
	case '{':
		var args struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", "malformed tool args: " + err.Error()
		}
		return args.Command, ""
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", "malformed tool args: " + err.Error()
		}
		s = strings.TrimSpace(s)
		if strings.HasPrefix(s, "{") {
			var args struct {
				Command string `json:"command"`
			}
			if err := json.Unmarshal([]byte(s), &args); err != nil {
				return "", "malformed tool args: " + err.Error()
			}
			return args.Command, ""
		}
		return s, ""
	default:
		return "", "malformed tool args: expected an object or a string"
	}
}

func trimJSONSpace(raw json.RawMessage) json.RawMessage {
	return json.RawMessage(strings.TrimSpace(string(raw)))
}

// Handles reports whether shizo has a policy for this tool: only bash. Other
// tools defer to Copilot's own permission flow (Emit writes empty output).
func (copilotHarness) Handles(in hookInput) bool { return isBashTool(in.ToolName) }

// Emit writes Copilot's decision envelope: a bare top-level
// permissionDecision object. allow exits 0; deny exits 2 (Copilot merges any
// stdout JSON with the exit-2 deny, so the reason survives). A non-bash tool
// with an undecided outcome writes nothing: empty output defers to Copilot's
// default permission flow. In decision mode, ask and the unmatched fallthrough
// downgrade to deny (auto-approval bug, see above); in automode the
// fallthrough defers instead.
func (copilotHarness) Emit(w io.Writer, in hookInput, cfg *config, outcome Outcome) int {
	if outcome.Kind == kindUndecided && !isBashTool(in.ToolName) {
		return 0 // defer to Copilot's own permission flow
	}
	if cfg != nil && cfg.returnMode == "automode" && outcome.Kind == kindUndecided {
		return 0 // operator opted into deferring the fallthrough
	}
	switch outcome.Kind {
	case kindAllow:
		w.Write([]byte(`{"permissionDecision":"allow"}`))
		return 0
	default: // deny, ask, fallthrough — all map to deny (docs/COPILOT.md)
		reason := copilotDenyReason(outcome)
		// Copilot requires a reason on deny; without one the decision is
		// dropped and the default flow runs (fail-open).
		if strings.TrimSpace(reason) == "" {
			reason = "denied by shizo"
		}
		b, _ := json.Marshal(map[string]any{
			"permissionDecision":       "deny",
			"permissionDecisionReason": reason,
		})
		w.Write(b)
		return 2
	}
}

// copilotDenyReason explains the downgrade when shizo's own verdict was not a
// deny: Copilot would have auto-approved it, so the agent must see why it was
// blocked.
func copilotDenyReason(outcome Outcome) string {
	switch outcome.Kind {
	case kindAsk:
		return "shizo asks for confirmation, but Copilot auto-approves ask (github/copilot-cli#3590) — denied instead: " + outcome.Reason
	case kindUndecided:
		return "no allow rule matched, and Copilot auto-approves ask (github/copilot-cli#3590) — denied instead"
	default:
		return outcome.Reason
	}
}
