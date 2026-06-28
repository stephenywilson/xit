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
function buildFinalCommand(cmd) {
  const xitCmd = "xit auto ";
  const c = cmd.trim();

  const shellMatch = c.match(/^(bash|sh)((?:\s+-[a-z]+)*)\s+["'](.+)["']$/i);
  if (shellMatch) {
    const inner = buildFinalCommand(shellMatch[3]);
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

function splitLastAndOr(c) {
  const lastAnd = c.lastIndexOf("&&");
  const lastOr = c.lastIndexOf("||");
  const splitAt = Math.max(lastAnd, lastOr);
  if (splitAt <= 0) return null;
  return {
    prefix: c.slice(0, splitAt + 2),
    suffix: c.slice(splitAt + 2).trim(),
  };
}

function isXitAutoCommand(cmd) {
  const core = extractCoreCommand(cmd);
  return core.startsWith("xit auto ") || core.startsWith("./xit auto ");
}

function injectEnvIntoXitAuto(cmd) {
  const c = cmd.trim();

  const shellMatch = c.match(/^(bash|sh)((?:\s+-[a-z]+)*)\s+["'](.+)["']$/i);
  if (shellMatch) {
    const inner = injectEnvIntoXitAuto(shellMatch[3]);
    return shellMatch[1] + shellMatch[2] + ' "' + inner + '"';
  }

  const split = splitLastAndOr(c);
  if (split) {
    return split.prefix + " " + injectEnvIntoXitAuto(split.suffix);
  }

  let target = c;
  if (target.startsWith("command ")) {
    target = target.slice(8).trim();
  }
  target = stripLeadingOpenCodeAdapterEnv(target);
  return target;
}

function sha256Hex(s) {
  try {
    const crypto = require("crypto");
    return crypto.createHash("sha256").update(String(s || "")).digest("hex");
  } catch {
    return "";
  }
}

function opencodeTurnKey(sessionID, userMessageID) {
  if (!sessionID || !userMessageID) return "";
  const hex = sha256Hex(String(sessionID) + "\x00" + String(userMessageID));
  return hex ? hex.slice(0, 24) : "";
}

function stripLeadingOpenCodeAdapterEnv(cmd) {
  return cmd.trim()
    .replace(/^XIT_ADAPTER=opencode\s+/, "")
    .replace(/^XIT_OPENCODE_TURN_KEY=(?:'[^']*'|"[^"]*"|\S+)\s+/, "");
}

function eventTypeOf(input) {
  const ev = input && input.event ? input.event : input;
  return (ev && ev.type ? ev.type : (input && input.type ? input.type : "")).toString();
}

function eventMessageOf(input) {
  const ev = input && input.event ? input.event : input;
  const props = ev && ev.properties ? ev.properties : {};
  return props.info || props.message || (ev && ev.message) || (input && input.message) || {};
}

function eventSessionIDOf(input, msg) {
  const ev = input && input.event ? input.event : input;
  const props = ev && ev.properties ? ev.properties : {};
  const session = (ev && ev.session) || (input && input.session) || {};
  return (input && input.sessionID) ||
    (ev && ev.sessionID) ||
    props.sessionID ||
    (input && input.sessionId) ||
    (ev && ev.sessionId) ||
    props.sessionId ||
    session.id ||
    (msg && msg.sessionID) ||
    (msg && msg.sessionId) ||
    "";
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
  const activeTurnBySession = new Map();

  function activateUserTurn(sessionID, userMessageID, source, allowReplace) {
    if (!sessionID || !userMessageID) return "";
    const current = activeTurnBySession.get(sessionID);
    if (current && current.turnOpen && !allowReplace) {
      return current.turnKey || "";
    }
    const turnKey = opencodeTurnKey(sessionID, userMessageID);
    if (!turnKey) return "";
    const sameTurn = current && current.userMessageID === userMessageID && current.turnKey === turnKey;
    activeTurnBySession.set(sessionID, {
      turnKey,
      userMessageID,
      turnOpen: true,
    });
    if (!sameTurn) {
      logEvent(home, {
        timestamp: new Date().toISOString(),
        adapter: "opencode",
        cwd: directory || worktree || process.cwd(),
        action: "observe",
        reason: "user_message_turn_start",
        turnKey,
        source: source || "",
        stage: "turn_activate",
      });
    }
    return turnKey;
  }

  function clearActiveTurn(sessionID, source) {
    if (!sessionID) return;
    activeTurnBySession.delete(sessionID);
    logEvent(home, {
      timestamp: new Date().toISOString(),
      adapter: "opencode",
      cwd: directory || worktree || process.cwd(),
      action: "observe",
      reason: "session_idle_clear_active_turn",
      source: source || "",
      stage: "turn_clear",
    });
  }

  // Diagnostic: plugin initialized
  logDebug(home, {
    timestamp: new Date().toISOString(),
    adapter: "opencode",
    stage: "plugin_initialized",
    directory: directory || "",
    worktree: worktree || "",
  });

  const hooks = {
    "chat.message": async (input, output) => {
      const msg = output && output.message ? output.message : {};
      if (input && input.sessionID && msg && msg.role === "user" && msg.id) {
        activateUserTurn(input.sessionID, msg.id, "chat.message", true);
      }
    },

    "event": async (input) => {
      const type = eventTypeOf(input);
      const msg = eventMessageOf(input);
      const sessionID = eventSessionIDOf(input, msg);

      if (type === "message.updated" && msg && msg.role === "user" && msg.id && sessionID) {
        activateUserTurn(sessionID, msg.id, "message.updated", false);
        return;
      }

      if (type === "session.idle" && sessionID) {
        clearActiveTurn(sessionID, "session.idle");
      }
    },

    "tool.execute.before": async (input, output) => {
      logDebug(home, {
        timestamp: new Date().toISOString(),
        adapter: "opencode",
        stage: "tool_execute_before_entered",
        tool: input.tool,
        hasCallID: !!input.callID,
        cwd: directory || worktree || process.cwd(),
      });

      if (input.tool !== "bash" && input.tool !== "Bash") return;
      const cmd = (output.args && output.args.command ? output.args.command : (output.args && output.args.cmd ? output.args.cmd : "")).toString();
      const alreadyWrapped = isXitAutoCommand(cmd);

      let action = "observe";
      let reason = "low_noise";
      let finalCmd = cmd;

      const coreCmd = extractCoreCommand(cmd);
      const compressDecision = shouldCompress(cmd);
      const activeTurn = activeTurnBySession.get(input.sessionID);
      const turnKey = activeTurn && activeTurn.turnOpen ? activeTurn.turnKey : "";

      logDebug(home, {
        timestamp: new Date().toISOString(),
        adapter: "opencode",
        stage: "classify",
        original_command: cmd,
        extracted_core: coreCmd,
        shouldCompress: compressDecision,
        alreadyWrapped,
      });

      if (alreadyWrapped) {
        // AI wrote "xit auto ..." itself — normalize visible command without double-wrapping.
        finalCmd = injectEnvIntoXitAuto(cmd);
        if (finalCmd !== cmd) {
          action = "reroute";
          reason = "already_xit_auto";
        } else {
          action = "observe";
          reason = "already_xit_auto";
        }
        if (output.args && typeof output.args === "object") {
          output.args.command = finalCmd;
        }
      } else if (compressDecision) {
        action = "reroute";
        reason = "should_compress";
        finalCmd = buildFinalCommand(cmd);
        if (output.args && typeof output.args === "object") {
          output.args.command = finalCmd;
        }
      }

      callState.set(input.callID, { original: cmd, final: finalCmd, turnKey, action });

      logEvent(home, {
        timestamp: new Date().toISOString(),
        adapter: "opencode",
        cwd: directory || worktree || process.cwd(),
        tool: input.tool,
        original_command: cmd,
        final_command: finalCmd,
        action,
        reason,
        turnKey,
        hasCallID: !!input.callID,
        stage: "before",
      });
    },

    "shell.env": async (input, output) => {
      const state = input && input.callID ? callState.get(input.callID) : null;
      const activeTurn = input && input.sessionID ? activeTurnBySession.get(input.sessionID) : null;
      const turnKey = (state && state.turnKey) || (activeTurn && activeTurn.turnOpen ? activeTurn.turnKey : "");
      const shouldInject = !!turnKey || (state && (state.action === "reroute" || state.final !== state.original));
      if (!shouldInject) return;
      if (!output.env || typeof output.env !== "object") output.env = {};
      output.env.XIT_ADAPTER = "opencode";
      if (turnKey) {
        output.env.XIT_OPENCODE_TURN_KEY = turnKey;
      }
    },

    "tool.execute.after": async (input, output) => {
      logDebug(home, {
        timestamp: new Date().toISOString(),
        adapter: "opencode",
        stage: "tool_execute_after_entered",
        tool: input.tool,
        hasCallID: !!input.callID,
        cwd: directory || worktree || process.cwd(),
      });

      if (input.tool !== "bash" && input.tool !== "Bash") return;
      const cmd = (input.args && input.args.command ? input.args.command : (input.args && input.args.cmd ? output.args.cmd : "")).toString();
      const state = callState.get(input.callID);
      const finalCmd = state ? state.final : cmd;
      const activeTurn = activeTurnBySession.get(input.sessionID);
      const turnKey = state ? state.turnKey : (activeTurn && activeTurn.turnOpen ? activeTurn.turnKey : "");

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
        turnKey,
        hasCallID: !!input.callID,
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
    hasShellEnv: "shell.env" in hooks,
    hasEvent: "event" in hooks,
  });

  return hooks;
};

export default XiTPlugin;
`
