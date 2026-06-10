package opencodehook

// PluginSource is the TypeScript source for the XiT OpenCode plugin.
// It is written to .opencode/plugins/xit.ts during installation.
const PluginSource = `function extractCoreCommand(cmd) {
  let c = cmd.trim();

  // Strip common shell wrappers: bash -lc "..." / sh -c "..."
  const shellWrapper = /^(?:bash|sh)\s+(?:-[a-z]+\s+)*["'](.+)["']$/i;
  const shellMatch = c.match(shellWrapper);
  if (shellMatch) {
    c = shellMatch[1].trim();
  }

  // Take the last segment after "&&" or "||" so that
  // export PATH="..." && go test ...  resolves to  go test ...
  const lastSegment = c.split(/\s*&&\s*|\s*\|\|\s*/).pop();
  if (lastSegment) {
    c = lastSegment.trim();
  }

  // Strip leading "command " prefix
  if (c.startsWith("command ")) {
    c = c.slice(8).trim();
  }

  return c;
}

function shouldCompress(cmd) {
  const core = extractCoreCommand(cmd);
  const parts = core.split(/\s+/);
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

// buildFinalCommand rewrites cmd to run via xit auto.
// envPrefix is prepended immediately before "xit auto" so that callers can
// inject per-invocation env vars (e.g. XIT_ADAPTER=opencode).
function buildFinalCommand(cmd, envPrefix) {
  const xitCmd = (envPrefix || "") + "xit auto ";
  const c = cmd.trim();

  const shellMatch = c.match(/^(bash|sh)((?:\s+-[a-z]+)*)\s+["'](.+)["']$/i);
  if (shellMatch) {
    const inner = buildFinalCommand(shellMatch[3], envPrefix);
    return shellMatch[1] + shellMatch[2] + ' "' + inner + '"';
  }

  const lastAnd = c.lastIndexOf("&&");
  const lastOr = c.lastIndexOf("||");
  const splitAt = Math.max(lastAnd, lastOr);

  if (splitAt > 0) {
    const prefix = c.slice(0, splitAt + 2);
    let suffix = c.slice(splitAt + 2).trim();
    if (suffix.startsWith("command ")) {
      suffix = suffix.slice(8).trim();
    }
    return prefix + " " + xitCmd + suffix;
  }

  if (c.startsWith("command ")) {
    return xitCmd + c.slice(8).trim();
  }

  return xitCmd + c;
}

function logEvent(home, record) {
  try {
    const fs = require("fs");
    const path = require("path");
    const dir = path.join(home, ".xit", "opencode-hooks");
    fs.mkdirSync(dir, { recursive: true });
    const line = JSON.stringify(record) + "\n";
    fs.appendFileSync(path.join(dir, "events.jsonl"), line);
  } catch {
    // fail-open: silently drop logging errors
  }
}

function logDebug(home, record) {
  if (process.env.XIT_OPENCODE_DEBUG !== "1") return;
  try {
    const fs = require("fs");
    const path = require("path");
    const dir = path.join(home, ".xit", "opencode-hooks");
    fs.mkdirSync(dir, { recursive: true });
    const line = JSON.stringify(record) + "\n";
    fs.appendFileSync(path.join(dir, "debug.jsonl"), line);
  } catch {
    // fail-open
  }
}

export const XiTPlugin = async ({ directory, worktree }) => {
  const home = process.env.HOME || process.env.USERPROFILE || "/tmp";
  const callState = new Map();

  // Diagnostic: plugin initialized
  logDebug(home, {
    timestamp: new Date().toISOString(),
    adapter: "opencode",
    stage: "plugin_initialized",
    directory: directory || "",
    worktree: worktree || "",
  });

  const hooks = {
    "tool.execute.before": async (input, output) => {
      logDebug(home, {
        timestamp: new Date().toISOString(),
        adapter: "opencode",
        stage: "tool_execute_before_entered",
        tool: input.tool,
        sessionID: input.sessionID,
        callID: input.callID,
        cwd: directory || worktree || process.cwd(),
      });

      if (input.tool !== "bash" && input.tool !== "Bash") return;
      const cmd = (output.args && output.args.command ? output.args.command : (output.args && output.args.cmd ? output.args.cmd : "")).toString();
      const alreadyWrapped =
        cmd.trim().startsWith("xit auto ") ||
        cmd.trim().startsWith("./xit auto ");

      let action = "observe";
      let reason = "low_noise";
      let finalCmd = cmd;

      const coreCmd = extractCoreCommand(cmd);
      const compressDecision = shouldCompress(cmd);

      logDebug(home, {
        timestamp: new Date().toISOString(),
        adapter: "opencode",
        stage: "classify",
        original_command: cmd,
        extracted_core: coreCmd,
        shouldCompress: compressDecision,
        alreadyWrapped,
      });

      const hasEnvPrefix = cmd.includes("XIT_ADAPTER=opencode");

      if (alreadyWrapped && !hasEnvPrefix) {
        // AI wrote "xit auto ..." itself — inject adapter env without double-wrapping
        action = "reroute";
        reason = "already_xit_auto_inject_env";
        finalCmd = "XIT_ADAPTER=opencode " + cmd.trim();
        if (output.args && typeof output.args === "object") {
          output.args.command = finalCmd;
        }
      } else if (alreadyWrapped && hasEnvPrefix) {
        action = "observe";
        reason = "already_xit_auto_with_env";
      } else if (compressDecision) {
        action = "reroute";
        reason = "should_compress";
        finalCmd = buildFinalCommand(cmd, "XIT_ADAPTER=opencode ");
        if (output.args && typeof output.args === "object") {
          output.args.command = finalCmd;
        }
      }

      callState.set(input.callID, { original: cmd, final: finalCmd });

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
      logDebug(home, {
        timestamp: new Date().toISOString(),
        adapter: "opencode",
        stage: "tool_execute_after_entered",
        tool: input.tool,
        sessionID: input.sessionID,
        callID: input.callID,
        cwd: directory || worktree || process.cwd(),
      });

      if (input.tool !== "bash" && input.tool !== "Bash") return;
      const cmd = (input.args && input.args.command ? input.args.command : (input.args && input.args.cmd ? output.args.cmd : "")).toString();
      const state = callState.get(input.callID);
      const finalCmd = state ? state.final : cmd;

      logEvent(home, {
        timestamp: new Date().toISOString(),
        adapter: "opencode",
        cwd: directory || worktree || process.cwd(),
        tool: input.tool,
        original_command: cmd,
        final_command: finalCmd,
        output_excerpt: (output.output ? output.output.toString().slice(0, 200) : ""),
        action: "observe",
        reason: "after_execution",
        sessionID: input.sessionID,
        callID: input.callID,
        stage: "after",
        title: output.title || "",
      });

      callState.delete(input.callID);
    },
  };

  logDebug(home, {
    timestamp: new Date().toISOString(),
    adapter: "opencode",
    stage: "hooks_registered",
    hasToolExecuteBefore: "tool.execute.before" in hooks,
    hasToolExecuteAfter: "tool.execute.after" in hooks,
  });

  return hooks;
};

export default XiTPlugin;
`
