// shizo permission plugin for the OpenCode agent (docs/OPENCODE.md).
// Routes every shell tool call through the shizo.sh hook wrapper: a deny is
// enforced by failing the call; allow, ask, and "no decision" fall through to
// OpenCode's own permission flow, which prompts by default. Fail-closed: any
// wrapper failure or unparseable output blocks the call.
//
// One file serves both OpenCode plugin generations: 2.x (V2) loads the
// default export's setup() and registers a tool hook, 1.x (V1) loads the
// default export's server() and receives a hook table. Both block by throwing
// from the before-hook.
"use strict";

const { spawn } = require("node:child_process");
const path = require("node:path");

// `shizo install opencode-plugin` substitutes the wrapper path it materialized
// for __SHIZO_WRAPPER__ (docs/OPENCODE.md); SHIZO_HOOK still overrides at runtime.
const HOOK = process.env.SHIZO_HOOK ?? "__SHIZO_WRAPPER__";

// Mirrors the hook contract (docs/CLAUDE_CODE_HOOKS.md): exit 0 + decision JSON
// (allow/ask), exit 2 + deny JSON, exit 0 + empty stdout (defer). Else deny.
function runHook(command, cwd, sessionId) {
  return new Promise((resolve) => {
    const fail = (reason) => resolve({ kind: "deny", reason });
    const child = spawn(HOOK, [], { stdio: ["pipe", "pipe", "ignore"] });
    let out = "";
    child.stdout.on("data", (d) => (out += d));
    child.on("error", (e) => fail(`shizo hook failed to start: ${e.message}`));
    child.on("close", (code) => {
      if (code === 0 && out.trim() === "") return resolve({ kind: "none", reason: "no decision" });
      try {
        const h = JSON.parse(out).hookSpecificOutput;
        const kind = h?.permissionDecision;
        const reason = String(h?.permissionDecisionReason ?? "");
        if (code === 0 && (kind === "allow" || kind === "ask")) return resolve({ kind, reason });
        if (code === 2 && kind === "deny") return resolve({ kind, reason });
      } catch {}
      fail(`shizo hook gave unusable output (exit ${code})`);
    });
    child.stdin.on("error", () => {}); // surfaced via close
    child.stdin.end(
      JSON.stringify({
        hook_event_name: "PreToolUse",
        tool_name: "Bash",
        tool_input: { command },
        cwd,
        session_id: sessionId,
      }),
    );
  });
}

// OpenCode has no confirm prompt in this hook: only a deny blocks here. allow,
// ask, and "no decision" all fall through to OpenCode's own permission flow,
// which prompts for each new command pattern unless the user configured it to
// auto-approve (docs/OPENCODE.md). A missing or blank command is left to
// OpenCode's own input validation.
async function decide(command, cwd, sessionId) {
  if (typeof command !== "string" || command.trim() === "") return;
  const v = await runHook(command, cwd, sessionId);
  if (v.kind === "deny") throw new Error(`shizo: ${v.reason}`);
}

module.exports = {
  id: "shizo",

  // OpenCode 2.x (V2) plugin: setup registers a tool hook; throwing in
  // execute.before fails that call alone. The shell tool is named "shell".
  async setup(ctx) {
    const reg = await ctx.tool.hook("execute.before", async (event) => {
      if (event.tool !== "shell") return;
      const input = event.input || {};
      const base = String(ctx.location.directory);
      const cwd = input.workdir ? path.resolve(base, String(input.workdir)) : base;
      await decide(input.command, cwd, String(event.sessionID ?? ""));
    });
    return () => reg.dispose();
  },

  // OpenCode 1.x (V1) plugin: server returns the hook table; throwing in
  // tool.execute.before fails the call. The shell tool is named "bash"
  // (renamed to "shell" in 2.0).
  async server(input) {
    return {
      "tool.execute.before": async (hookInput, output) => {
        if (hookInput.tool !== "bash") return;
        const args = output && output.args ? output.args : {};
        const base = String(input.directory);
        const cwd = args.workdir ? path.resolve(base, String(args.workdir)) : base;
        await decide(args.command, cwd, String(hookInput.sessionID ?? ""));
      },
    };
  },
};
