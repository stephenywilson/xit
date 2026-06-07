package opencodehook

// PluginSource is the TypeScript source for the XiT OpenCode plugin.
// It is written to .opencode/plugins/xit.ts during installation.
const PluginSource = `import type { Plugin } from "@opencode-ai/plugin";

function shouldCompress(cmd: string): boolean {
  const c = cmd.trim();
  const parts = c.split(/\s+/);
  if (parts.length === 0) return false;

  const tuple = parts.slice(0, 2).join(" ");
  switch (tuple) {
    case "go test":
    case "cargo test":
    case "npm test":
    case "pnpm test":
    case "pytest test":
    case "git diff":
    case "git log":
    case "docker logs":
      return true;
    case "git status":
    case "git branch":
    case "docker ps":
      return false;
  }

  switch (parts[0]) {
    case "rg":
    case "grep":
    case "find":
    case "cat":
    case "head":
    case "tail":
    case "tsc":
    case "eslint":
    case "jq":
      return true;
    case "ls":
      return false;
    default:
      return false;
  }
}

function logEvent(home: string, record: Record<string, any>): void {
  try {
    const fs = require("fs");
    const path = require("path");
    const dir = path.join(home, "opencode-hooks");
    fs.mkdirSync(dir, { recursive: true });
    const line = JSON.stringify(record) + "\n";
    fs.appendFileSync(path.join(dir, "events.jsonl"), line);
  } catch {
    // fail-open: silently drop logging errors
  }
}

export default (async ({ directory, worktree }) => {
  const home = process.env.HOME || process.env.USERPROFILE || "/tmp";

  return {
    "tool.execute.before": async (input, output) => {
      if (input.tool !== "bash" && input.tool !== "Bash") return;
      const cmd = (output.args?.command ?? output.args?.cmd ?? "").toString();
      const alreadyWrapped =
        cmd.trim().startsWith("xit auto ") ||
        cmd.trim().startsWith("./xit auto ");

      let action = "observe";
      let reason = "low_noise";
      let finalCmd = cmd;

      if (alreadyWrapped) {
        action = "observe";
        reason = "already_xit_auto";
      } else if (shouldCompress(cmd)) {
        action = "reroute";
        reason = "should_compress";
        finalCmd = "xit auto " + cmd;
        if (output.args && typeof output.args === "object") {
          output.args.command = finalCmd;
        }
      }

      logEvent(home, {
        timestamp: new Date().toISOString(),
        adapter: "opencode",
        cwd: directory || worktree || process.cwd(),
        tool: input.tool,
        original_command: cmd,
        final_command: finalCmd,
        action,
        reason,
        sessionID: input.sessionID,
        callID: input.callID,
        stage: "before",
      });
    },

    "tool.execute.after": async (input, output) => {
      if (input.tool !== "bash" && input.tool !== "Bash") return;
      const cmd = (input.args?.command ?? input.args?.cmd ?? "").toString();

      logEvent(home, {
        timestamp: new Date().toISOString(),
        adapter: "opencode",
        cwd: directory || worktree || process.cwd(),
        tool: input.tool,
        original_command: cmd,
        final_command: output.output?.toString().slice(0, 200) ?? "",
        action: "observe",
        reason: "after_execution",
        sessionID: input.sessionID,
        callID: input.callID,
        stage: "after",
        title: output.title ?? "",
      });
    },
  };
}) satisfies Plugin;
`