package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// Every registered harness must name itself the way its registry key does —
// the CLI dispatches on that key, so a mismatch would be invisible until an
// operator typed it.
func TestHarnessRegistry(t *testing.T) {
	if _, ok := harnessByName("pretooluse"); !ok {
		t.Fatal("registry is missing the pretooluse (claude) harness")
	}
	for name, h := range harnesses {
		if h.Name() != name {
			t.Errorf("harness %q: Name() = %q, want the registry key", name, h.Name())
		}
	}
}

func TestParseSnakeInput(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantCmd string
		wantErr string // substring of the deny reason; "" = no error
	}{
		{"valid", `{"tool_name":"Bash","tool_input":{"command":"git status"},"cwd":"/w","session_id":"s1"}`, "git status", ""},
		{"malformed json", `{not json`, "", "malformed hook input"},
		{"missing command", `{"tool_name":"Bash","tool_input":{}}`, "", "empty command"},
		{"blank command", `{"tool_name":"Bash","tool_input":{"command":"  "}}`, "", "empty command"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in, err := parseSnakeInput([]byte(c.raw))
			if (err != "") != (c.wantErr != "") {
				t.Fatalf("err = %q, want presence of %q", err, c.wantErr)
			}
			if c.wantErr != "" && !strings.Contains(err, c.wantErr) {
				t.Fatalf("err = %q, want substring %q", err, c.wantErr)
			}
			if err == "" && in.ToolInput.Command != c.wantCmd {
				t.Errorf("command = %q, want %q", in.ToolInput.Command, c.wantCmd)
			}
		})
	}
}

// emitEnvelope decodes a Claude/Codex-style hookSpecificOutput envelope.
func emitEnvelope(t *testing.T, body string) map[string]any {
	t.Helper()
	var obj struct {
		HookSpecificOutput map[string]any `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(body), &obj); err != nil {
		t.Fatalf("envelope is not JSON: %v\n%s", err, body)
	}
	if obj.HookSpecificOutput == nil {
		t.Fatalf("envelope has no hookSpecificOutput: %s", body)
	}
	return obj.HookSpecificOutput
}

// claude Emit: allow/ask exit 0, deny exits 2; automode defers only the
// unmatched fallthrough (kindUndecided), never an explicit ask rule.
func TestClaudeEmit(t *testing.T) {
	cases := []struct {
		name         string
		cfg          *config
		outcome      Outcome
		wantExit     int
		wantDecision string // "" = no envelope (defer sentinel)
		wantDefer    bool
	}{
		{"allow", &config{returnMode: "decision"}, Outcome{kindAllow, "rule: allow git status ANY"}, 0, "allow", false},
		{"ask rule", &config{returnMode: "decision"}, Outcome{kindAsk, "ask rule matched"}, 0, "ask", false},
		{"fallthrough", &config{returnMode: "decision"}, Outcome{kindUndecided, "no rule matches: git log"}, 0, "ask", false},
		{"deny", &config{returnMode: "decision"}, Outcome{kindDeny, "rule: deny git push ANY"}, 2, "deny", false},
		{"automode fallthrough defers", &config{returnMode: "automode"}, Outcome{kindUndecided, "no rule matches: git log"}, 0, "", true},
		{"automode ask rule does not defer", &config{returnMode: "automode"}, Outcome{kindAsk, "ask rule matched"}, 0, "ask", false},
		{"automode deny still denies", &config{returnMode: "automode"}, Outcome{kindDeny, "rule: deny git push ANY"}, 2, "deny", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			rc := claudeHarness{}.Emit(&out, hookInput{}, c.cfg, c.outcome)
			if rc != c.wantExit {
				t.Fatalf("exit = %d, want %d (body %q)", rc, c.wantExit, out.String())
			}
			if c.wantDefer {
				if out.String() != "defer" {
					t.Fatalf("body = %q, want the defer sentinel", out.String())
				}
				return
			}
			env := emitEnvelope(t, out.String())
			if env["hookEventName"] != "PreToolUse" {
				t.Errorf("hookEventName = %v, want PreToolUse", env["hookEventName"])
			}
			if env["permissionDecision"] != c.wantDecision {
				t.Errorf("permissionDecision = %v, want %q", env["permissionDecision"], c.wantDecision)
			}
			if env["permissionDecisionReason"] != c.outcome.Reason {
				t.Errorf("reason = %v, want %q", env["permissionDecisionReason"], c.outcome.Reason)
			}
		})
	}
}

// codex Emit: allow re-emits the command as updatedInput (the documented
// no-block rewrite shape); ask and the unmatched fallthrough downgrade to
// deny because Codex proceeds on both ("ask" is parsed-but-unsupported, empty
// output continues). Codex always exits 0 — the decision rides in the JSON,
// and every non-zero exit without a stderr reason fails open.
func TestCodexEmit(t *testing.T) {
	in := hookInput{}
	in.ToolInput.Command = "git push"
	cases := []struct {
		name         string
		outcome      Outcome
		wantDecision string
		wantReason   string // "" = no reason field expected; "<updated>" = updatedInput check
		wantUpdate   bool
	}{
		{"allow", Outcome{kindAllow, "rule: allow git push ANY"}, "allow", "", true},
		{"deny", Outcome{kindDeny, "rule: deny git push HAS --force"}, "deny", "rule: deny git push HAS --force", false},
		{"ask downgrades to deny", Outcome{kindAsk, "ask rule matched"}, "deny", "shizo asks for confirmation, but Codex has no ask — denied instead: ask rule matched", false},
		{"fallthrough downgrades to deny", Outcome{kindUndecided, "no rule matches: git push"}, "deny", "no allow rule matched, and Codex has no ask — denied instead: no rule matches: git push", false},
		{"empty deny reason gets a fallback", Outcome{kindDeny, ""}, "deny", "denied by shizo", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			rc := codexHarness{}.Emit(&out, in, nil, c.outcome)
			if rc != 0 {
				t.Fatalf("exit = %d, want 0 (Codex blocks only on exit-0 JSON or exit-2+stderr)", rc)
			}
			env := emitEnvelope(t, out.String())
			if env["hookEventName"] != "PreToolUse" {
				t.Errorf("hookEventName = %v, want PreToolUse", env["hookEventName"])
			}
			if env["permissionDecision"] != c.wantDecision {
				t.Errorf("permissionDecision = %v, want %q", env["permissionDecision"], c.wantDecision)
			}
			if c.wantUpdate {
				upd, ok := env["updatedInput"].(map[string]any)
				if !ok || upd["command"] != "git push" {
					t.Errorf("updatedInput = %v, want {command: git push}", env["updatedInput"])
				}
			} else if _, present := env["updatedInput"]; present {
				t.Errorf("updatedInput present on a non-allow: %v", env["updatedInput"])
			}
			if c.wantReason != "" {
				if env["permissionDecisionReason"] != c.wantReason {
					t.Errorf("reason = %v, want %q", env["permissionDecisionReason"], c.wantReason)
				}
			}
		})
	}
}

// An unknown or missing hook event must error (exit 2) without reading stdin.
func TestDispatchHookUnknownEvent(t *testing.T) {
	for _, args := range [][]string{{"hook"}, {"hook", "bogus"}} {
		var out, errb bytes.Buffer
		stdin := strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"git status"}}`)
		rc := dispatch(args, stdin, &out, &errb)
		if rc != 2 {
			t.Errorf("dispatch %v: rc = %d, want 2", args, rc)
		}
		if !strings.Contains(errb.String(), "hook requires a supported event") {
			t.Errorf("dispatch %v: stderr = %q, want the usage error", args, errb.String())
		}
		if out.Len() != 0 {
			t.Errorf("dispatch %v leaked stdout: %q", args, out.String())
		}
	}
}

// copilot Parse accepts both payload shapes (native camelCase, PascalCase
// PreToolUse snake_case) and three tool-args encodings (object, JSON string
// of an object, bare command string). A non-bash tool parses cleanly with an
// empty command so the pipeline defers instead of denying.
func TestCopilotParse(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantTool string
		wantCmd  string
		wantErr  string // substring of the deny reason; "" = no error
	}{
		{"camelCase object args", `{"toolName":"bash","toolArgs":{"command":"git status"},"cwd":"/w","sessionId":"s1"}`, "bash", "git status", ""},
		{"snake_case object args", `{"tool_name":"Bash","tool_input":{"command":"git status"},"cwd":"/w","session_id":"s1"}`, "Bash", "git status", ""},
		{"camelCase JSON-string args", `{"toolName":"bash","toolArgs":"{\"command\":\"git status\"}"}`, "bash", "git status", ""},
		{"camelCase bare-string args", `{"toolName":"bash","toolArgs":"git status"}`, "bash", "git status", ""},
		{"snake_case JSON-string args", `{"tool_name":"Bash","tool_input":"{\"command\":\"git status\"}"}`, "Bash", "git status", ""},
		{"non-bash tool defers", `{"toolName":"view","toolArgs":{"file_path":"/x"}}`, "view", "", ""},
		{"malformed json", `{not json`, "", "", "malformed hook input"},
		{"missing tool name", `{"toolArgs":{"command":"x"}}`, "", "", "missing tool name"},
		{"bash with absent args", `{"toolName":"bash"}`, "bash", "", "empty command"},
		{"bash with null args", `{"toolName":"bash","toolArgs":null}`, "bash", "", "empty command"},
		{"bash with blank command", `{"toolName":"bash","toolArgs":{"command":"  "}}`, "bash", "", "empty command"},
		{"bash with malformed args", `{"toolName":"bash","toolArgs":123}`, "bash", "", "malformed tool args"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in, err := copilotHarness{}.Parse([]byte(c.raw))
			if (err != "") != (c.wantErr != "") {
				t.Fatalf("err = %q, want presence of %q", err, c.wantErr)
			}
			if c.wantErr != "" && !strings.Contains(err, c.wantErr) {
				t.Fatalf("err = %q, want substring %q", err, c.wantErr)
			}
			if err == "" {
				if in.ToolName != c.wantTool {
					t.Errorf("tool = %q, want %q", in.ToolName, c.wantTool)
				}
				if in.ToolInput.Command != c.wantCmd {
					t.Errorf("command = %q, want %q", in.ToolInput.Command, c.wantCmd)
				}
			}
		})
	}
}

// copilot Handles gates only the shell tool, in either spelling.
func TestCopilotHandles(t *testing.T) {
	for _, c := range []struct {
		name string
		want bool
	}{
		{"bash", true},
		{"Bash", true},
		{"view", false},
		{"powershell", false},
		{"", false},
	} {
		in := hookInput{}
		in.ToolName = c.name
		if got := (copilotHarness{}).Handles(in); got != c.want {
			t.Errorf("Handles(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

// copilotEmitBody decodes Copilot's bare top-level decision object.
func copilotEmitBody(t *testing.T, body string) map[string]any {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal([]byte(body), &obj); err != nil {
		t.Fatalf("envelope is not JSON: %v\n%s", err, body)
	}
	return obj
}

// copilot Emit: allow and deferrals exit 0; deny exits 2 (Copilot merges the
// stdout JSON with the exit-2 deny, so the reason survives). ask and the bash
// fallthrough downgrade to deny in decision mode (auto-approval bug
// github/copilot-cli#3590); automode defers only the fallthrough; a non-bash
// tool always defers with empty output.
func TestCopilotEmit(t *testing.T) {
	bashIn := hookInput{}
	bashIn.ToolName = "bash"
	bashIn.ToolInput.Command = "git push"
	nonBashIn := hookInput{}
	nonBashIn.ToolName = "view"
	decision := &config{returnMode: "decision"}
	auto := &config{returnMode: "automode"}
	cases := []struct {
		name       string
		in         hookInput
		cfg        *config
		outcome    Outcome
		wantExit   int
		wantBody   string // exact body; "" = empty output (defer)
		wantReason string // checked when wantBody is unset
	}{
		{"allow", bashIn, decision, Outcome{kindAllow, "rule: allow git push ANY"}, 0, `{"permissionDecision":"allow"}`, ""},
		{"deny", bashIn, decision, Outcome{kindDeny, "denied by rule: deny git push HAS --force"}, 2, "", "denied by rule: deny git push HAS --force"},
		{"ask downgrades to deny", bashIn, decision, Outcome{kindAsk, "ask rule matched"}, 2, "", "shizo asks for confirmation, but Copilot auto-approves ask (github/copilot-cli#3590) — denied instead: ask rule matched"},
		{"fallthrough downgrades to deny", bashIn, decision, Outcome{kindUndecided, "no rule matches: git push"}, 2, "", "no allow rule matched, and Copilot auto-approves ask (github/copilot-cli#3590) — denied instead: no rule matches: git push"},
		{"automode fallthrough defers", bashIn, auto, Outcome{kindUndecided, "no rule matches: git push"}, 0, "", ""},
		{"automode ask rule does not defer", bashIn, auto, Outcome{kindAsk, "ask rule matched"}, 2, "", "shizo asks for confirmation, but Copilot auto-approves ask (github/copilot-cli#3590) — denied instead: ask rule matched"},
		{"non-bash tool defers", nonBashIn, decision, Outcome{kindUndecided, "no shizo policy for tool: view"}, 0, "", ""},
		{"empty deny reason gets a fallback", bashIn, decision, Outcome{kindDeny, ""}, 2, "", "denied by shizo"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			rc := copilotHarness{}.Emit(&out, c.in, c.cfg, c.outcome)
			if rc != c.wantExit {
				t.Fatalf("exit = %d, want %d (body %q)", rc, c.wantExit, out.String())
			}
			if c.wantBody != "" {
				if out.String() != c.wantBody {
					t.Fatalf("body = %q, want %q", out.String(), c.wantBody)
				}
				return
			}
			if c.wantReason == "" {
				if out.String() != "" {
					t.Fatalf("body = %q, want empty (defer)", out.String())
				}
				return
			}
			obj := copilotEmitBody(t, out.String())
			if obj["permissionDecision"] != "deny" {
				t.Errorf("permissionDecision = %v, want deny", obj["permissionDecision"])
			}
			if obj["permissionDecisionReason"] != c.wantReason {
				t.Errorf("reason = %v, want %q", obj["permissionDecisionReason"], c.wantReason)
			}
		})
	}
}
