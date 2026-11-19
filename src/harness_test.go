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
