# PreToolUse-Style Tool Denial in Coding Agent Harnesses

**Research date:** 2026-07
**Question:** Claude Code's PreToolUse hooks let users run a command that can intercept, approve, or **deny** tool calls (especially bash commands) before execution. Do equivalent features exist in: **Opencode**, **Pi**, **GitHub Copilot CLI**, and **Codex (OpenAI)**?

**Short answer: Yes — all four have it.** Codex and Copilot CLI adopted the same JSON hook contract as Claude Code; opencode and pi express the same capability through their own plugin/extension systems (declarative deny patterns + programmatic interception).

## Comparison matrix

| Harness | Equivalent feature | Deny mechanism | Custom program decides? |
|---|---|---|---|
| **Opencode** | `permission` config (declarative) + plugin events (programmatic) | `"deny"` patterns per tool/command; throw in `tool.execute.before` | Yes, via JS/TS plugin (can shell out) |
| **Pi** (pi-coding-agent) | `tool_call` extension event | `return { block: true, reason }` in a TS extension; mutable input allows rewriting args | Yes — in-process TS code by design; no declarative config hooks |
| **Copilot CLI** | `preToolUse` hook (plus `permissionRequest`) + `--allow-tool`/`--deny-tool` | JSON stdout: `{"permissionDecision":"deny",...}`; fail-closed on non-zero exit | Yes — command/HTTP hooks; also reads `.claude/settings.json` directly |
| **Codex** (OpenAI) | `PreToolUse` hook in `hooks.json`/`config.toml` + sandbox/approval policies + experimental `prefix_rule`s | Same JSON contract as Claude Code (explicitly modeled on it); OS-level sandbox as hard boundary | Yes — command hooks, same stdin/stdout JSON contract |

---

## 1. Opencode (sst/opencode)

**Feature exists: yes — in two complementary forms.**

### 1.1 Declarative permissions (`permission` in `opencode.json`)
- Every tool call resolves to `allow` / `ask` / `deny`, including per-command-pattern rules for bash.
- Bash rules match **parsed commands** with glob wildcards (`*`, `?`); **last matching rule wins**.
- `ask` prompts in the TUI (once / always / reject); `--auto` auto-approves everything not explicitly denied (explicit denies still enforced).
- Also covers `edit`/`read` by file path, `webfetch` by URL, `task` by subagent type, plus safety guards `external_directory` and `doom_loop`.
- Per-agent overrides (agent rules take precedence over global).

```jsonc
// opencode.json — deny rm, allow git/npm, ask everything else
{
  "$schema": "https://opencode.ai/config.json",
  "permission": {
    "bash": {
      "*": "ask",
      "git *": "allow",
      "npm *": "allow",
      "rm *": "deny"
    },
    "edit": {
      "*": "deny",
      "packages/web/src/content/docs/*.mdx": "allow"
    }
  }
}
```

```jsonc
// Per-agent override
{
  "permission": { "bash": { "*": "ask", "git push *": "deny" } },
  "agent": {
    "build": { "permission": { "bash": { "*": "ask", "git commit *": "ask" } } }
  }
}
```

### 1.2 Plugins (programmatic — the true PreToolUse analog)
- Drop a JS/TS file in `.opencode/plugins/` (project) or `~/.config/opencode/plugins/` (global), or list an npm package under `"plugin"`.
- The `tool.execute.before` event receives tool name + args; **throwing an Error blocks execution**; `output.args` is mutable.
- Since it's full JS, you can run an external decision program (e.g., exec a policy binary and inspect its exit code) — functionally identical to Claude Code's hook command, just in-process.

```js
// .opencode/plugins/block-rm.js
export const BlockRm = async ({ $ }) => {
  return {
    "tool.execute.before": async (input, output) => {
      if (input.tool === "bash" && /\brm\s+(-\w+\s+)*-rf/.test(output.args.command)) {
        throw new Error("DENIED: rm -rf is not allowed");
      }
    },
  };
};
```

### 1.3 Differences from Claude Code PreToolUse
- No config-level declarative "run this command as a decision hook" — programmatic decisions require a plugin (but declarative pattern denies need no code at all).
- No `updatedInput`-style JSON protocol; mutation is via the JS object.
- Community plugin **opencode-agent-hooks** (github.com/monks1975/opencode-agent-hooks) runs `.claude/settings.json` hook configs unmodified (same stdin JSON schema).

**Docs:** https://opencode.ai/docs/permissions/ · https://opencode.ai/docs/plugins/ · https://opencode.ai/docs/policies/ · https://opencode.ai/docs/config/

---

## 2. Pi (pi-coding-agent / pi-web)

**Feature exists: yes — the first-class `tool_call` extension event.**

### 2.1 How it works
- The **`tool_call`** extension event fires after `tool_execution_start`, before the tool executes, and **can block (and modify)** the call.
- Placement: `~/.pi/agent/extensions/*.ts` (global), `.pi/extensions/*.ts` (project), or a pi package; test with `pi -e ./ext.ts`; hot-reloadable via `/reload`.
- Handler receives `event` with `toolName`, `toolCallId`, and a **mutable** `input` (rewrite arguments before execution — more than PreToolUse offers).
- Return `{ block: true, reason?: string, terminate?: boolean }` to **deny**. `reason` is fed back to the model; `terminate: true` stops the agent early.
- `ctx.ui.confirm/select/input` give interactive approval prompts; `ctx.hasUI` lets you hard-deny in non-interactive modes.
- Typed narrowing via `isToolCallEventType("bash", event)` so `event.input` is typed `{ command: string; timeout?: number }`.
- Related events: `tool_result` (modify results post-execution), `user_bash` (intercepts user `!cmd`), `tool_execution_start/update/end` (lifecycle, non-blocking).

### 2.2 No built-in permission system (by design)
- Pi's philosophy: *"No permission popups — run in a container, or build your own confirmation flow with extensions."*
- Coarser controls: `--tools`/`--exclude-tools` CLI flags (tool allow/deny lists), project-level **trust** (`/trust`, `defaultProjectTrust`) gating loading of project extensions/settings — but not individual tool calls.

### 2.3 Concrete example (shipped with pi: `examples/extensions/permission-gate.ts`)

```typescript
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

export default function (pi: ExtensionAPI) {
  const dangerousPatterns = [/\brm\s+(-rf?|--recursive)/i, /\bsudo\b/i, /\b(chmod|chown)\b.*777/i];

  pi.on("tool_call", async (event, ctx) => {
    if (event.toolName !== "bash") return undefined;
    const command = event.input.command as string;
    if (!dangerousPatterns.some((p) => p.test(command))) return undefined;

    if (!ctx.hasUI) {
      // Non-interactive mode: deny by default
      return { block: true, reason: "Dangerous command blocked (no UI for confirmation)" };
    }
    const choice = await ctx.ui.select(`⚠️ Dangerous command:\n\n  ${command}\n\nAllow?`, ["Yes", "No"]);
    if (choice !== "Yes") return { block: true, reason: "Blocked by user" };
    return undefined; // allow
  });
}
```

Other shipped examples: `protected-paths.ts` (block writes to `.env`, etc.), `tool-override.ts` (replace built-in tools), `confirm-destructive.ts`, `timed-confirm.ts`.

### 2.4 Differences from Claude Code PreToolUse
| Aspect | Claude Code PreToolUse | Pi `tool_call` event |
|---|---|---|
| Mechanism | Config-file hook that runs an external **command** | TypeScript extension handler `pi.on("tool_call", ...)` |
| Deny | `permissionDecision: "deny"` / exit 2 | `return { block: true, reason }` |
| Ask prompt | `"ask"` decision → Claude Code prompts user | Extension calls `ctx.ui.confirm/select` itself (fully custom UX) |
| Modify args | Limited (`updatedInput`) | First-class: `event.input` is mutable |
| Post-execution | `PostToolUse` | `tool_result` + `tool_execution_*` |
| Setup effort | Declarative, no code (simple cases) | Requires a TS extension file (shareable as a pi package) |

**Docs/examples (local):** `.../pi-coding-agent/docs/extensions.md` (Tool Events → `tool_call`) · `README.md` (Extensions, Philosophy) · `examples/extensions/permission-gate.ts`, `confirm-destructive.ts`, `protected-paths.ts`, `tool-override.ts`

---

## 3. GitHub Copilot CLI

**Feature exists: yes — very close to Claude Code's PreToolUse, with explicit compatibility.**

### 3.1 Hooks (`preToolUse`, `permissionRequest`, …)
- **Config load order:** policy → user → project → plugins:
  - Policy (machine-wide, admin): `/etc/github-copilot/policy.d/*.json` (POSIX) or `C:\ProgramData\GitHub\Copilot\policy.d\*.json` / Windows Registry — cannot be disabled by users
  - Repo: `.github/hooks/*.json` (committed)
  - User: `~/.copilot/hooks/*.json` (or `$COPILOT_HOME/hooks/`)
  - Inline `hooks` block in `.github/copilot/settings.json` / `~/.copilot/settings.json`; **`.claude/settings.json` and `.claude/settings.local.json` are also read**
  - Installed plugins contribute their own `hooks.json`
- **`preToolUse`** fires before each tool executes. Input JSON: `{sessionId, timestamp, cwd, toolName, toolArgs}` (shell = `toolName: "bash"` / `"powershell"`). Output JSON on stdout controls the call:
  - `permissionDecision`: `"allow" | "deny" | "ask"` (deny requires `permissionDecisionReason`)
  - `modifiedArgs`: substitute tool arguments (rewrite a command before it runs)
  - `matcher`: regex on `toolName` (e.g. `"bash|edit"`)
- **Fail behavior:** command hooks are **fail-closed** for `preToolUse` — a crash or non-zero exit (incl. exit 2) **denies** the call even if stdout said "allow"; timeouts are fail-open. HTTP hooks are fail-open.
- **`permissionRequest`** (CLI-only) fires even earlier — before the rules engine / session approvals / user prompt — and returns `behavior: "allow"|"deny"` to short-circuit the permission flow. Sandbox-escape requests cannot be pre-approved by a hook `allow` (only interactive user confirmation); a `deny` does propagate.
- Hook types: `command` (bash/powershell/exec+args), `http` (POST payload to URL; must be https for preToolUse), `prompt` (sessionStart only).
- Other events: `sessionStart/End`, `userPromptSubmitted`, `postToolUse` (can modify result / inject context), `agentStop`, `subagentStart/Stop`, `errorOccurred`, `notification`, `preCompact`.

**Example — deny `rm -rf` in bash:**
```json
{
  "version": 1,
  "hooks": {
    "preToolUse": [
      {
        "type": "command",
        "matcher": "bash|powershell",
        "bash": "./scripts/block-destructive.sh",
        "timeoutSec": 10
      }
    ]
  }
}
```
```bash
#!/bin/bash
INPUT=$(cat)   # {"toolName":"bash","toolArgs":{"command":"rm -rf /tmp/x"},...}
CMD=$(echo "$INPUT" | jq -r '.toolArgs.command // empty')
if echo "$CMD" | grep -Eq '\brm\s+(-[a-zA-Z]*r[a-zA-Z]*f|-[a-zA-Z]*f[a-zA-Z]*r)\b'; then
  echo '{"permissionDecision":"deny","permissionDecisionReason":"Destructive rm blocked by policy"}'
else
  echo '{"permissionDecision":"allow"}'
fi
```

### 3.2 Permission system (declarative)
- Read-only ops auto-approved; mutating tools prompt (one-time or for-session).
- `--allow-tool='shell(git commit)'`, `--allow-tool='shell(git:*)'` (subcommand patterns), `--deny-tool=write`; deny always beats allow.
- `--available-tools` / `--excluded-tools` restrict which tools the model can see.
- Permissive modes: `--allow-all-tools`, `--allow-all` / `--yolo` (also `/allow-all`, `/yolo` mid-session); `/reset-allowed-tools` revokes.
- Persistence: approvals in `~/.copilot/permissions-config.json`; URL allowlist in `~/.copilot/settings.json` (`allowedUrls`).
- Sandboxing (local/cloud) as an alternative control layer.

### 3.3 Differences from Claude Code PreToolUse
- Tool names: native `bash`, `powershell`, `create`, `edit`, `view`, `grep`; PascalCase config uses **Claude tool names** (`Bash`, `Edit`) with Claude matcher semantics.
- Extra `permissionRequest` hook; sandbox-escape can't be hook-allowed.
- Fail-closed on any non-zero/crash (deny); timeouts fail-open.
- Enterprise layer: policy hooks in system dirs/registry that users can't disable; `disableAllHooks` (can't affect policy hooks).
- Windows: first-class `powershell` key per hook; `exec`/`args` for shell-free execution.
- Hooks can emit `{"type":"progress","message":...}` lines to the timeline while deciding.
- **Known issue:** GitHub issue #3590 — a `preToolUse` hook returning `"ask"` gets auto-approved by the TUI (since v1.0.53); under cloud agent, `"ask"` is treated as `"deny"`.

**Docs:** https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/use-hooks · https://docs.github.com/en/copilot/reference/hooks-reference · https://docs.github.com/en/copilot/how-tos/copilot-cli/use-copilot-cli/allowing-tools · https://docs.github.com/en/copilot/concepts/agents/hooks · https://docs.github.com/en/copilot/how-tos/copilot-sdk/hooks/pre-tool-use

---

## 4. Codex CLI (OpenAI)

**Feature exists: yes — a full Hooks framework explicitly modeled on Claude Code's, layered on sandbox/approval and per-command rules.**

### 4.1 Hooks (`PreToolUse`, `PostToolUse`, `PermissionRequest`, …)
- Configured in `hooks.json` or inline `[[hooks.*]]` tables in `config.toml`; discovered at `~/.codex/hooks.json`, `~/.codex/config.toml`, `<repo>/.codex/hooks.json`, etc.
- Events: `PreToolUse`, `PostToolUse`, `PermissionRequest`, `UserPromptSubmit`, `SessionStart/End`, `PreCompact`/`PostCompact`, `Stop`, `SubagentStop`, `Interrupt`.
- A **matcher** (regex on tool name) selects tools — `Bash` for shell, `apply_patch`/`Edit`/`Write` for file edits, `mcp__server__tool` for MCP.
- The hook command receives one JSON object on **stdin** (`session_id`, `cwd`, `tool_name`, `tool_input` (`.command` for Bash), `permission_mode`, …) and returns JSON on **stdout**.
- **Deny** (exact analog of Claude Code PreToolUse deny):

```json
{"hookSpecificOutput": {
  "hookEventName": "PreToolUse",
  "permissionDecision": "deny",
  "permissionDecisionReason": "Destructive command blocked by hook."
}}
```
  Legacy `{"decision":"block","reason":...}` and **exit code 2 with stderr** also block.
- **Allow + rewrite:** `permissionDecision: "allow"` with `updatedInput` rewrites the command before execution (known bug: rewritten input may not execute under named permission profiles in app-server/Desktop — github.com/openai/codex#32544).
- `permissionDecision: "ask"` is parsed but **not yet supported** (hook run marked failed, tool call continues) — use the `PermissionRequest` hook for approval decisions instead (returns `decision.behavior: allow | deny`; any deny wins).
- Trust model: non-managed hooks must be **reviewed and trusted** via `/hooks` (hash-pinned; changed hooks re-require review). Managed/enterprise hooks via `requirements.toml` are trusted by policy; `allow_managed_hooks_only = true` skips all other hook sources.
- Multiple matching hooks run **concurrently**. `async: true` runs background hooks, but those **cannot block**.
- Docs warn: "Treat tool hooks as a useful guardrail, not a complete enforcement boundary" (some specialized tool paths can opt out; hosted tools like WebSearch aren't covered).

```json
// ~/.codex/hooks.json — deny bad bash via a policy script
{"hooks": {"PreToolUse": [
  {"matcher": "Bash", "hooks": [
    {"type": "command",
     "command": "/usr/bin/python3 \"$(git rev-parse --show-toplevel)/.codex/hooks/pre_tool_use_policy.py\"",
     "timeout": 30, "statusMessage": "Checking Bash command"}
  ]}
]}}
```

### 4.2 Sandbox modes + approval policies (built-in, pre-dates hooks)
- **Sandbox modes** (`sandbox_mode` / `--sandbox`): `read-only`, `workspace-write` (default; writes limited to workspace + `/tmp`, network off by default, `.git`/`.codex`/`.agents` protected read-only), `danger-full-access`. OS-level enforcement: Seatbelt/`sandbox-exec` (macOS), `bwrap`+seccomp (Linux), native Windows sandbox or WSL2.
- **Approval policies** (`approval_policy` / `--ask-for-approval`): `on-request`, `never`, plus a granular policy object (`{ granular = { sandbox_approval, rules, mcp_elicitations, request_permissions, skill_approval } }`). Older `untrusted` and `on-failure` values are **retired** — `untrusted` behavior is now `[projects."/path"] trust_level = "untrusted"`.
- **Auto-review** (`approvals_reviewer = "auto_review"`): routes approval requests to a reviewer agent (guardian) — an LLM-based gate.
- Network: off by default; `sandbox_workspace_write.network_access = true` enables it; optional `network_proxy` feature with domain allow/deny rules.

### 4.3 Rules / execpolicy (per-command prefix rules, experimental)
- `.rules` files (Starlark) under `rules/` next to a config layer, e.g. `~/.codex/rules/default.rules`:

```python
prefix_rule(
    pattern = ["rm"],           # or ["gh", "pr", "view"]
    decision = "forbidden",     # allow | prompt | forbidden (most restrictive wins)
    justification = "Use git rm instead.",
    match   = ["rm -rf /"],
    not_match = ["rmdir build"],
)
```

- Closest thing to a **static command allowlist/denylist**: `allow` (run outside sandbox without prompt), `prompt`, `forbidden` (block without prompting).
- Smart shell-splitting: linear `&&`/`;`/`|` chains in `bash -lc "a && b"` are parsed (tree-sitter) and each command evaluated separately, so `git add . && rm -rf /` can't be auto-allowed by a `git add` rule; complex scripts are conservatively treated as one opaque invocation.
- Testable: `codex execpolicy check --rules ~/.codex/rules/default.rules -- <cmd>`.
- Supersedes the older `shell_command_prefix` config key.

### 4.4 Differences from Claude Code PreToolUse
| Aspect | Claude Code PreToolUse | Codex |
|---|---|---|
| Deny | `permissionDecision: deny` / exit 2 | Same shapes + legacy `decision:block`; **near-identical contract** |
| Rewrite input | `updatedInput` on allow | Supported, but bug under named permission profiles (#32544) |
| Force interactive prompt | `permissionDecision: "ask"` supported | **Not supported yet** — use `PermissionRequest` hook or approval policy |
| Static per-command rules | None (rules live in hooks) | **Extra**: `prefix_rule` allow/prompt/forbidden + execpolicy checker with shell-chain splitting |
| Hard boundary | Hooks are the main control | OS-level sandbox is a separate hard boundary; hooks explicitly "a guardrail" |
| Trust/management | Permissioned in settings | Hash-pinned trust review via `/hooks`; enterprise `requirements.toml`, `allow_managed_hooks_only` |
| Concurrency | Varies | Matching hooks run concurrently; async hooks can't block |
| Coverage | All tools incl. WebSearch-style | Hosted tools (WebSearch) **not** covered; some tool paths can opt out |

**Docs:** https://learn.chatgpt.com/docs/hooks · https://learn.chatgpt.com/docs/agent-approvals-security · https://learn.chatgpt.com/docs/sandboxing · https://learn.chatgpt.com/docs/agent-configuration/rules · https://learn.chatgpt.com/docs/config-file/config-reference · source: `codex-rs/hooks/` in github.com/openai/codex

---

## Takeaway

Claude Code's PreToolUse pattern has become an industry norm:

- **Codex and Copilot CLI adopted the exact same hook contract** (JSON on stdout, `permissionDecision`, matchers) — Copilot CLI even reads your `.claude/settings.json` directly and accepts PascalCase `PreToolUse` with Claude matcher semantics.
- **Opencode and pi express the same capability through their own extension/plugin systems**: declarative deny patterns (opencode) plus in-process code interception (`tool.execute.before` / `tool_call`), rather than spawning an external decision command.
- **Pi deliberately ships no built-in permission system** — its philosophy is "no permission popups; run in a container or build your own with extensions" (a ready-made `permission-gate.ts` example ships with it).
- Notable gaps: Codex lacks the `"ask"` decision and treats hooks as a guardrail (sandbox is the hard boundary); Copilot CLI has an open issue where `"ask"` auto-approves in the TUI; opencode/pi require code for programmatic decisions but offer first-class argument mutation.

## Source links (all accessed 2026-07)

| Harness | Key docs |
|---|---|
| Claude Code (reference) | https://code.claude.com/docs/en/hooks#pretooluse-decision-control |
| Opencode | https://opencode.ai/docs/permissions/ · https://opencode.ai/docs/plugins/ · https://opencode.ai/docs/policies/ |
| Pi | `docs/extensions.md`, `README.md`, `examples/extensions/*` in the pi-coding-agent package |
| Copilot CLI | https://docs.github.com/en/copilot/reference/hooks-reference · https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/use-hooks · https://docs.github.com/en/copilot/how-tos/copilot-cli/use-copilot-cli/allowing-tools |
| Codex | https://learn.chatgpt.com/docs/hooks · https://learn.chatgpt.com/docs/agent-approvals-security · https://learn.chatgpt.com/docs/agent-configuration/rules |
