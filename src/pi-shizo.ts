// shizo permission extension for the pi coding agent (docs/PI.md).
// Routes every bash tool call through the shizo.sh hook wrapper: allow and deny
// are enforced; ask and "no decision" fall back to a user confirm. Fail-closed:
// any wrapper failure, unparseable output, or confirm-less (headless) ask blocks.
import { spawn } from "node:child_process";
import { homedir } from "node:os";
import { join } from "node:path";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { isToolCallEventType } from "@earendil-works/pi-coding-agent";

const HOOK = process.env.SHIZO_HOOK ?? join(homedir(), ".shizo", "engine", "shizo.sh");

type Verdict = { kind: "allow" | "deny" | "ask" | "none"; reason: string };

// Mirrors the hook contract (docs/CLAUDE_CODE_HOOKS.md): exit 0 + decision JSON
// (allow/ask), exit 2 + deny JSON, exit 0 + empty stdout (defer). Else deny.
function runHook(command: string, cwd: string, sessionId: string): Promise<Verdict> {
  return new Promise((resolve) => {
    const fail = (reason: string) => resolve({ kind: "deny", reason });
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

export default function (pi: ExtensionAPI) {
  pi.on("tool_call", async (event, ctx) => {
    if (!isToolCallEventType("bash", event)) return;

    const v = await runHook(event.input.command, ctx.cwd, ctx.sessionManager.getSessionId());
    if (v.kind === "allow") return;
    if (v.kind === "deny") return { block: true, reason: `shizo: ${v.reason}` };

    // ask / no decision → user confirm; headless has nobody to ask.
    if (!ctx.hasUI) return { block: true, reason: `shizo: needs confirmation (${v.reason}); no UI` };
    const ok = await ctx.ui.confirm(
      "shizo: allow this command?",
      `${event.input.command}\n\n${v.reason}`,
    );
    if (!ok) return { block: true, reason: "shizo: declined by user" };
  });
}
