import * as vscode from "vscode";
import * as child_process from "child_process";
import * as path from "path";
import * as fs from "fs";
import * as os from "os";
import type {
  GainData,
  AdapterEvent,
  GlobalActivity,
  XiTStatus,
  LatestRun,
  LatestRawLogMeta,
  CurrentRunState,
  TurnState,
} from "./types";

const OUTPUT_CHANNEL = vscode.window.createOutputChannel("XiT Status");

function getConfig(): vscode.WorkspaceConfiguration {
  return vscode.workspace.getConfiguration("xit");
}

function log(message: string): void {
  OUTPUT_CHANNEL.appendLine(message);
}

export function appendOutput(message: string): void {
  OUTPUT_CHANNEL.appendLine(message);
}

export function clearOutput(): void {
  OUTPUT_CHANNEL.clear();
}

function showOutput(): void {
  OUTPUT_CHANNEL.show(true);
}

function hasXitData(dir: string): boolean {
  try {
    return (
      fs.existsSync(path.join(dir, ".xit", "history.jsonl")) ||
      fs.existsSync(path.join(dir, ".xit", "state", "current-run.json"))
    );
  } catch {
    return false;
  }
}

function parseXitCwdsFromHookCommands(): string[] {
  const home = resolveXiTHome();
  const hookFiles = [
    path.join(home, "claude-hooks", "events.jsonl"),
    path.join(home, "codex-hooks", "events.jsonl"),
    path.join(home, "cursor-hooks", "events.jsonl"),
    path.join(home, "kimi-hooks", "events.jsonl"),
  ];
  const cutoffMs = Date.now() - 10 * 60 * 1000;
  const found: string[] = [];

  for (const hookFile of hookFiles) {
    try {
      if (!fs.existsSync(hookFile)) {
        continue;
      }
      const content = fs.readFileSync(hookFile, "utf-8");
      const lines = content.trim().split("\n").filter(Boolean).slice(-30).reverse();
      for (const line of lines) {
        try {
          const event = JSON.parse(line) as {
            time?: string;
            original_command?: string;
            cwd?: string;
          };
          if (event.time) {
            const ms = Date.parse(event.time);
            if (!Number.isNaN(ms) && ms < cutoffMs) {
              continue;
            }
          }
          if (event.original_command) {
            // Extract path from "cd /some/path &&" or "; cd /some/path"
            const m = event.original_command.match(
              /(?:^|;|\s&&)\s*cd\s+([^\s;&|"'`\\]+)/,
            );
            if (m && m[1] && !found.includes(m[1])) {
              found.push(m[1]);
            }
          }
          if (event.cwd && !found.includes(event.cwd)) {
            found.push(event.cwd);
          }
        } catch {
          // skip
        }
      }
    } catch {
      // skip
    }
  }
  return found;
}

export function resolveActiveXitWorkspace(): string {
  const folders = vscode.workspace.workspaceFolders;

  // 1. VS Code workspace root if it has .xit data
  if (folders && folders.length > 0) {
    const wsRoot = folders[0].uri.fsPath;
    if (hasXitData(wsRoot)) {
      return wsRoot;
    }
  }

  // 2. Scan recent hook commands/cwds for paths with .xit data
  for (const candidate of parseXitCwdsFromHookCommands()) {
    const resolved = candidate.startsWith("~/")
      ? path.join(os.homedir(), candidate.slice(2))
      : candidate === "~"
        ? os.homedir()
        : candidate;
    try {
      if (hasXitData(resolved)) {
        return resolved;
      }
    } catch {
      // skip
    }
  }

  // 3. Fall back to VS Code workspace root even if no .xit data there
  if (folders && folders.length > 0) {
    return folders[0].uri.fsPath;
  }

  return os.homedir();
}

export function resolveWorkspaceCwd(): string {
  return resolveActiveXitWorkspace();
}

function resolveXiTHome(): string {
  const cfg = getConfig();
  const configured = cfg.get<string>("home", "");
  if (configured) {
    return configured;
  }
  return path.join(os.homedir(), ".xit");
}

function expandHome(p: string): string {
  if (p === "~") {
    return os.homedir();
  }
  if (p.startsWith("~/")) {
    return path.join(os.homedir(), p.slice(2));
  }
  return p;
}

function addCandidate(
  candidates: string[],
  candidate: string | undefined,
): void {
  if (!candidate) {
    return;
  }
  const normalized = expandHome(candidate);
  if (!candidates.includes(normalized)) {
    candidates.push(normalized);
  }
}

function resolveBinaryCandidates(): string[] {
  const cfg = getConfig();
  const candidates: string[] = [];
  const configured = cfg.get<string>("binaryPath", "");
  addCandidate(candidates, configured);

  // Try PATH
  addCandidate(candidates, tryWhich("xit"));

  // Try ~/.local/bin/xit
  addCandidate(candidates, "~/.local/bin/xit");

  // Try workspace ./xit
  const workspaceFolders = vscode.workspace.workspaceFolders;
  if (workspaceFolders && workspaceFolders.length > 0) {
    addCandidate(candidates, path.join(workspaceFolders[0].uri.fsPath, "xit"));
  }

  addCandidate(candidates, "xit");
  return candidates;
}

function tryWhich(command: string): string | undefined {
  try {
    const envPath = process.env.PATH || "";
    const paths = envPath.split(path.delimiter);
    for (const p of paths) {
      const candidate = path.join(p, command);
      if (fs.existsSync(candidate)) {
        return candidate;
      }
      if (process.platform === "win32") {
        const candidateExe = candidate + ".exe";
        if (fs.existsSync(candidateExe)) {
          return candidateExe;
        }
      }
    }
  } catch {
    // ignore
  }
  return undefined;
}

function execFilePromise(
  file: string,
  args: string[],
  cwd: string,
  timeoutMs = 5000,
): Promise<{ stdout: string; stderr: string }> {
  return new Promise((resolve, reject) => {
    child_process.execFile(
      file,
      args,
      { cwd, timeout: timeoutMs, encoding: "utf-8" },
      (error, stdout, stderr) => {
        if (error) {
          const wrapped = error as Error & { stderr?: string; stdout?: string };
          wrapped.stderr = stderr as string;
          wrapped.stdout = stdout as string;
          reject(wrapped);
        } else {
          resolve({ stdout: stdout as string, stderr: stderr as string });
        }
      },
    );
  });
}

function isExecutableCandidate(candidate: string): boolean {
  if (candidate === "xit") {
    return true;
  }
  try {
    return fs.existsSync(candidate);
  } catch {
    return false;
  }
}

function previewText(text: string, max = 500): string {
  const trimmed = text.trim();
  if (trimmed.length <= max) {
    return trimmed;
  }
  return trimmed.slice(0, max) + "...";
}

export function readGlobalActivity(): GlobalActivity {
  const adapters = ["cursor", "codex", "claude", "kimi"];
  const adapterCounts: Record<string, number> = {};
  let latestAdapter: string | undefined;
  let latestTime = "";
  let latestCommand: string | undefined;
  let latestPolicy: string | undefined;
  let eventCount = 0;

  for (const adapter of adapters) {
    const events = readRecentEvents(adapter, 50);
    if (events.length > 0) {
      adapterCounts[adapter] = events.length;
      eventCount += events.length;
      // events[0] is most recent (readRecentEvents reverses)
      const latest = events[0];
      if (!latestTime || (latest.time && latest.time > latestTime)) {
        latestTime = latest.time || "";
        latestAdapter = adapter;
        latestCommand = latest.original_command;
        latestPolicy = latest.policy;
      }
    }
  }

  return {
    latestAdapter,
    latestTime: latestTime || undefined,
    latestCommand,
    latestPolicy,
    eventCount,
    adapterCounts,
  };
}

export async function fetchStatus(): Promise<XiTStatus> {
  const cwd = resolveWorkspaceCwd();
  const candidates = resolveBinaryCandidates();
  const attempts: string[] = [];
  let sawRunnableBinary = false;
  let lastError = "";

  log(`[${new Date().toISOString()}] fetchStatus cwd=${cwd}`);
  log(`binary candidates: ${candidates.join(", ")}`);

  for (const binary of candidates) {
    attempts.push(binary);
    if (!isExecutableCandidate(binary)) {
      const message = `not found: ${binary}`;
      lastError = message;
      log(message);
      continue;
    }

    sawRunnableBinary = true;
    try {
      const { stdout, stderr } = await execFilePromise(
        binary,
        ["gain", "--json"],
        cwd,
      );
      if (stderr.trim()) {
        log(`stderr from ${binary}: ${previewText(stderr)}`);
      }
      if (!stdout.trim()) {
        lastError = `${binary}: empty stdout from gain --json`;
        log(lastError);
        continue;
      }
      try {
        const data = JSON.parse(stdout) as GainData;
        const activity = readGlobalActivity();
        const state = data.total_commands_condensed > 0 ? "ok" : "no-data";
        log(
          `activity: eventCount=${activity.eventCount} latestAdapter=${activity.latestAdapter || "none"}`,
        );
        return {
          available: true,
          state,
          gain: data,
          activity,
          binary,
          cwd,
          attempts,
          refreshedAt: new Date(),
        };
      } catch (parseErr) {
        lastError = `${binary}: JSON parse error: ${parseErr}; stdout=${previewText(stdout)}`;
        log(lastError);
      }
    } catch (err) {
      const e = err as Error & {
        code?: string;
        stderr?: string;
        stdout?: string;
      };
      const details = [
        `${binary}: execFile failed: ${e.message}`,
        e.code ? `code=${e.code}` : "",
        e.stderr ? `stderr=${previewText(e.stderr)}` : "",
        e.stdout ? `stdout=${previewText(e.stdout)}` : "",
      ]
        .filter(Boolean)
        .join(" | ");
      lastError = details;
      log(details);
    }
  }

  const activity = readGlobalActivity();

  if (!sawRunnableBinary) {
    return {
      available: false,
      state: "binary-not-found",
      activity,
      error: `XiT binary not found. Attempted: ${attempts.join(", ")}`,
      cwd,
      attempts,
      refreshedAt: new Date(),
    };
  }

  return {
    available: false,
    state: "gain-json-failed",
    activity,
    error: lastError || "xit gain --json failed for all binary candidates",
    cwd,
    attempts,
    refreshedAt: new Date(),
  };
}

export function readRecentEvents(
  adapter: string,
  maxLines = 20,
): AdapterEvent[] {
  const home = resolveXiTHome();
  const eventPaths: Record<string, string[]> = {
    cursor: [path.join(home, "cursor-hooks", "events.jsonl")],
    codex: [path.join(home, "codex-hooks", "events.jsonl")],
    claude: [path.join(home, "claude-hooks", "events.jsonl")],
    kimi: [
      path.join(home, "kimi-hooks", "turn-events.jsonl"),
      path.join(home, "kimi-hooks", "events.jsonl"),
    ],
  };

  const paths = eventPaths[adapter] || [];
  const events: AdapterEvent[] = [];

  for (const p of paths) {
    try {
      if (!fs.existsSync(p)) {
        continue;
      }
      const content = fs.readFileSync(p, "utf-8");
      const lines = content.split("\n").filter((l) => l.trim().length > 0);
      const tail = lines.slice(-maxLines);
      for (const line of tail) {
        try {
          const obj = JSON.parse(line) as AdapterEvent;
          obj.adapter = adapter;
          obj.source_file = p;
          events.push(obj);
        } catch {
          // skip malformed lines
        }
      }
    } catch {
      // skip unreadable files
    }
  }

  return events.reverse();
}

export function readWorkspaceHistory(maxLines = 20): AdapterEvent[] {
  const historyPath = path.join(resolveActiveXitWorkspace(), ".xit", "history.jsonl");
  try {
    if (!fs.existsSync(historyPath)) {
      return [];
    }
    const content = fs.readFileSync(historyPath, "utf-8");
    const lines = content.split("\n").filter((l) => l.trim().length > 0);
    const tail = lines.slice(-maxLines);
    return tail
      .map((line) => {
        try {
          return JSON.parse(line) as AdapterEvent;
        } catch {
          return undefined;
        }
      })
      .filter((e): e is AdapterEvent => e !== undefined)
      .reverse();
  } catch {
    return [];
  }
}

export function findLatestRawLog(): string | undefined {
  const runsDir = path.join(resolveActiveXitWorkspace(), ".xit", "runs");
  try {
    if (!fs.existsSync(runsDir)) {
      return undefined;
    }
    const files = fs.readdirSync(runsDir).filter((f) => f.endsWith(".raw.log"));
    if (files.length === 0) {
      return undefined;
    }
    let latest = files[0];
    let latestMtime = fs.statSync(path.join(runsDir, latest)).mtimeMs;
    for (const f of files.slice(1)) {
      const mtime = fs.statSync(path.join(runsDir, f)).mtimeMs;
      if (mtime > latestMtime) {
        latest = f;
        latestMtime = mtime;
      }
    }
    return path.join(runsDir, latest);
  } catch {
    return undefined;
  }
}

export function readLatestRawLogMeta(): LatestRawLogMeta | undefined {
  const latestPath = findLatestRawLog();
  if (!latestPath) {
    return undefined;
  }
  try {
    const stats = fs.statSync(latestPath);
    return {
      path: latestPath,
      mtimeMs: stats.mtimeMs,
      size: stats.size,
    };
  } catch {
    return undefined;
  }
}

function getCurrentRunStateCandidates(): string[] {
  const root = resolveActiveXitWorkspace();
  return [
    path.join(root, ".xit", "state", "current-run.json"),
    path.join(root, ".xit", "state", "current.json"),
  ];
}

export function readCurrentRunState(): CurrentRunState | undefined {
  let best: CurrentRunState | undefined;
  let bestMs = -1;

  for (const candidate of getCurrentRunStateCandidates()) {
    try {
      if (!fs.existsSync(candidate)) {
        continue;
      }
      const state = JSON.parse(
        fs.readFileSync(candidate, "utf-8"),
      ) as CurrentRunState;
      // Pick the freshest state across both files. Running states use
      // heartbeat_at; completed states use completed_at / finished_at.
      const ts =
        state.heartbeat_at ||
        state.completed_at ||
        state.finished_at ||
        state.started_at ||
        "";
      const ms = ts ? new Date(ts).getTime() : 0;
      if (!best || ms > bestMs) {
        best = state;
        bestMs = ms;
      }
    } catch {
      continue;
    }
  }
  return best;
}

export function readTurnState(): TurnState | undefined {
  const workspacePath = resolveActiveXitWorkspace();
  const turnPath = path.join(workspacePath, ".xit", "state", "turn.json");
  try {
    if (!fs.existsSync(turnPath)) {
      return undefined;
    }
    const obj = JSON.parse(fs.readFileSync(turnPath, "utf-8")) as TurnState;
    // Only return if the turn belongs to this workspace
    if (obj.cwd && path.resolve(obj.cwd) !== path.resolve(workspacePath)) {
      return undefined;
    }
    return obj;
  } catch {
    return undefined;
  }
}

export async function openLatestRawLog(): Promise<void> {
  const logPath = findLatestRawLog();
  if (!logPath) {
    vscode.window.showInformationMessage(
      "XiT: No raw log found in workspace .xit/runs/",
    );
    return;
  }
  const doc = await vscode.workspace.openTextDocument(logPath);
  await vscode.window.showTextDocument(doc);
}

function resolveTerminalEventsPath(): string {
  const home = resolveXiTHome();
  return path.join(home, "vscode-terminal", "events.jsonl");
}

export function writeTerminalEvent(event: {
  commandLine: string;
  confidence: number;
  terminalName: string;
  cwd?: string;
}): void {
  const eventsPath = resolveTerminalEventsPath();
  try {
    fs.mkdirSync(path.dirname(eventsPath), { recursive: true });
    const record = {
      source: "vscode-terminal",
      time: new Date().toISOString(),
      commandLine: event.commandLine,
      confidence: event.confidence,
      terminalName: event.terminalName,
      cwd: event.cwd,
    };
    const line = JSON.stringify(record) + "\n";
    fs.appendFileSync(eventsPath, line, "utf-8");
  } catch (err) {
    log(`writeTerminalEvent failed: ${err}`);
  }
}

export function readTerminalEvents(
  maxLines = 20,
): { time: string; commandLine: string; terminalName: string; cwd?: string }[] {
  const eventsPath = resolveTerminalEventsPath();
  try {
    if (!fs.existsSync(eventsPath)) {
      return [];
    }
    const content = fs.readFileSync(eventsPath, "utf-8");
    const lines = content.split("\n").filter((l) => l.trim().length > 0);
    const tail = lines.slice(-maxLines);
    const events = tail
      .map((line) => {
        try {
          return JSON.parse(line) as {
            time: string;
            commandLine: string;
            terminalName: string;
            cwd?: string;
          };
        } catch {
          return undefined;
        }
      })
      .filter(
        (
          e,
        ): e is {
          time: string;
          commandLine: string;
          terminalName: string;
          cwd?: string;
        } => e !== undefined,
      );
    return events.reverse();
  } catch {
    return [];
  }
}

export function resolveAvailableBinary(): string | undefined {
  const candidates = resolveBinaryCandidates();
  for (const candidate of candidates) {
    if (isExecutableCandidate(candidate)) {
      return candidate;
    }
  }
  return undefined;
}

export function isHighOutputCommand(cmd: string): boolean {
  const trimmed = cmd.trim().toLowerCase();
  if (!trimmed) {
    return false;
  }

  // Explicit passthrough flags
  if (/\b(--json|--porcelain|-q|--quiet)\b/.test(trimmed)) {
    return false;
  }

  // Explicit passthrough commands (short output)
  const passthroughPatterns = [
    /^git\s+status(\s|$)/,
    /^git\s+branch(\s|$)/,
    /^git\s+log\s+--oneline/,
    /^git\s+show\s+--stat/,
    /^pwd(\s|$)/,
    /^whoami(\s|$)/,
    /^go\s+version(\s|$)/,
    /^node\s+--version(\s|$)/,
    /^npm\s+--version(\s|$)/,
    /^ls(\s|$)/,
    /^cat\s+\S+$/,
  ];
  for (const p of passthroughPatterns) {
    if (p.test(trimmed)) {
      return false;
    }
  }

  // High-output commands
  const highOutputPatterns = [
    /^go\s+test/,
    /^npm\s+test/,
    /^pnpm\s+test/,
    /^yarn\s+test/,
    /^pytest/,
    /^cargo\s+test/,
    /^git\s+diff/,
    /^git\s+log\s+--stat/,
    /^git\s+log\s+-p/,
    /^(grep|rg|find|tree)\s/,
    /^docker\s+logs/,
    /^kubectl\s+logs/,
    /^tsc(\s|$)/,
    /^eslint/,
    /^webpack/,
    /^vite\s+build/,
    /^npm\s+run\s+build/,
    /^npm\s+run\s+lint/,
    /^pnpm\s+run\s+build/,
    /^yarn\s+run\s+build/,
    /^docker\s+build/,
    /^docker\s+compose\s+up/,
    /^make(\s|$)/,
    /^cmake\s+--build/,
  ];
  for (const p of highOutputPatterns) {
    if (p.test(trimmed)) {
      return true;
    }
  }

  return false;
}

export function readLatestRun(): LatestRun | undefined {
  const historyPath = path.join(resolveActiveXitWorkspace(), ".xit", "history.jsonl");
  try {
    if (!fs.existsSync(historyPath)) {
      return undefined;
    }
    const content = fs.readFileSync(historyPath, "utf-8");
    const lines = content.split("\n").filter((l) => l.trim().length > 0);
    if (lines.length === 0) {
      return undefined;
    }
    const latest = JSON.parse(lines[lines.length - 1]) as LatestRun;
    return latest;
  } catch {
    return undefined;
  }
}

export { showOutput, log };
