import * as vscode from "vscode";
import * as fs from "fs";
import * as path from "path";
import { execFile } from "child_process";
import type {
  AdapterEvent,
  AdapterHealthItem,
  AdapterHookInfo,
  AgentTurnView,
  AgentTurnStatus,
  DiagnoseReport,
  LatestActivityInfo,
  LatestRun,
  CurrentRunState,
  StaleTurnRecord,
  TerminalEventRecord,
  TokenImpactStats,
  TokenMetrics,
  TurnState,
  VerifyRoutingReport,
  WorkflowHealth,
  XiTStatus,
} from "./types";
import {
  isHighOutputCommand,
  readRecentEvents,
  readTerminalEvents,
  readTurnState,
  readWorkspaceHistory,
  resolveAvailableBinary,
  readCurrentRunState,
  resolveWorkspaceCwd,
} from "./xit";

const RULES_START = "<!-- XIT_AI_RULES_START -->";
const RULES_END = "<!-- XIT_AI_RULES_END -->";

const RULES_SECTION = `${RULES_START}
## XiT command-output rule

For high-noise commands, prefer running them through XiT:

\`\`\`bash
./xit auto <command>
\`\`\`

Use XiT for:

* \`go test -v ./...\`
* \`npm test\`
* \`docker logs ...\`
* \`tsc --noEmit\`
* \`eslint .\`
* large \`git diff\`
* long \`rg\` / \`find\` searches

Do not use XiT for short commands:

* \`pwd\`
* \`git status\`
* \`go version\`
* \`node --version\`
* short \`ls\`
* small \`cat\`

XiT compresses noisy terminal output locally and preserves raw logs for audit. It does not read chat content.
${RULES_END}`;

interface RuleInstallResult {
  files: string[];
  created: string[];
  updated: string[];
}

interface WorkflowEvent {
  time: string;
  command: string;
  routedThroughXiT: boolean;
  source?: string;
}

function isWorkspaceAvailable(): boolean {
  return !!(
    vscode.workspace.workspaceFolders &&
    vscode.workspace.workspaceFolders.length > 0
  );
}

function getWorkspaceRoot(): string | undefined {
  const folders = vscode.workspace.workspaceFolders;
  return folders && folders.length > 0 ? folders[0].uri.fsPath : undefined;
}

function normalizeLineEndings(text: string): string {
  return text.replace(/\r\n/g, "\n");
}

function upsertMarkedSection(existingContent: string, section: string): string {
  const content = normalizeLineEndings(existingContent);
  const startIndex = content.indexOf(RULES_START);
  const endIndex = content.indexOf(RULES_END);

  if (startIndex !== -1 && endIndex !== -1 && endIndex > startIndex) {
    const afterEnd = endIndex + RULES_END.length;
    const before = content.slice(0, startIndex).replace(/\s*$/, "");
    const after = content.slice(afterEnd).replace(/^\s*/, "");
    return `${before}\n\n${section}${after ? `\n\n${after}` : ""}\n`;
  }

  const trimmed = content.trimEnd();
  if (!trimmed) {
    return `${section}\n`;
  }
  return `${trimmed}\n\n${section}\n`;
}

function collectExistingRuleTargets(root: string): string[] {
  const targets: string[] = [];
  const directFiles = ["AGENTS.md", "CLAUDE.md"];
  for (const file of directFiles) {
    const fullPath = path.join(root, file);
    if (fs.existsSync(fullPath) && fs.statSync(fullPath).isFile()) {
      targets.push(fullPath);
    }
  }

  const codexDir = path.join(root, ".codex");
  if (fs.existsSync(codexDir) && fs.statSync(codexDir).isDirectory()) {
    for (const entry of fs.readdirSync(codexDir, { withFileTypes: true })) {
      if (entry.isFile() && /\.(md|markdown|txt|mdc)$/i.test(entry.name)) {
        targets.push(path.join(codexDir, entry.name));
      }
    }
  }

  const cursorRulesDir = path.join(root, ".cursor", "rules");
  if (
    fs.existsSync(cursorRulesDir) &&
    fs.statSync(cursorRulesDir).isDirectory()
  ) {
    for (const entry of fs.readdirSync(cursorRulesDir, {
      withFileTypes: true,
    })) {
      if (entry.isFile() && /\.(md|markdown|txt|mdc)$/i.test(entry.name)) {
        targets.push(path.join(cursorRulesDir, entry.name));
      }
    }
  }

  return [...new Set(targets)];
}

export function getExistingRuleTargets(): string[] {
  const root = getWorkspaceRoot();
  return root ? collectExistingRuleTargets(root) : [];
}

function getWorkspaceHistoryPath(): string | undefined {
  const root = getWorkspaceRoot();
  return root ? path.join(root, ".xit", "history.jsonl") : undefined;
}

function parseIsoTimeMs(iso: string | undefined): number | undefined {
  if (!iso) {
    return undefined;
  }
  const ms = Date.parse(iso);
  return Number.isNaN(ms) ? undefined : ms;
}

export function readAllWorkspaceRuns(): LatestRun[] {
  const historyPath = getWorkspaceHistoryPath();
  if (!historyPath || !fs.existsSync(historyPath)) {
    return [];
  }
  try {
    const content = fs.readFileSync(historyPath, "utf-8");
    return content
      .split("\n")
      .filter((line) => line.trim().length > 0)
      .map((line) => {
        try {
          return JSON.parse(line) as LatestRun;
        } catch {
          return undefined;
        }
      })
      .filter((run): run is LatestRun => run !== undefined);
  } catch {
    return [];
  }
}

export function installWorkspaceAiRules(): RuleInstallResult {
  const root = getWorkspaceRoot();
  if (!root) {
    throw new Error("No workspace folder is open.");
  }

  const existingTargets = collectExistingRuleTargets(root);
  const targets =
    existingTargets.length > 0
      ? existingTargets
      : [path.join(root, "AGENTS.md")];
  const created: string[] = [];
  const updated: string[] = [];

  for (const target of targets) {
    const alreadyExists = fs.existsSync(target);
    const current = alreadyExists ? fs.readFileSync(target, "utf-8") : "";
    const next = upsertMarkedSection(current, RULES_SECTION);
    if (!alreadyExists) {
      fs.mkdirSync(path.dirname(target), { recursive: true });
      created.push(target);
    }
    if (current !== next) {
      fs.writeFileSync(target, next, "utf-8");
      updated.push(target);
    }
  }

  return { files: targets, created, updated };
}

export function getWorkspaceRuleStatus(): {
  installed: boolean;
  files: string[];
} {
  const root = getWorkspaceRoot();
  if (!root) {
    return { installed: false, files: [] };
  }

  const targets = collectExistingRuleTargets(root);
  const installedFiles = targets.filter((target) => {
    try {
      const content = fs.readFileSync(target, "utf-8");
      return content.includes(RULES_START) && content.includes(RULES_END);
    } catch {
      return false;
    }
  });

  const fallbackAgents = path.join(root, "AGENTS.md");
  if (installedFiles.length === 0 && fs.existsSync(fallbackAgents)) {
    try {
      const content = fs.readFileSync(fallbackAgents, "utf-8");
      if (content.includes(RULES_START) && content.includes(RULES_END)) {
        installedFiles.push(fallbackAgents);
      }
    } catch {
      // ignore
    }
  }

  return {
    installed: installedFiles.length > 0,
    files: [...new Set(installedFiles)],
  };
}

function hasRuleMarker(filePath: string): boolean {
  try {
    const content = fs.readFileSync(filePath, "utf-8");
    return content.includes(RULES_START) && content.includes(RULES_END);
  } catch {
    return false;
  }
}

function getAdapterHookRouting(adapter: "codex" | "claude" | "cursor"): {
  highNoiseObserved: number;
  highNoiseRouted: number;
} {
  const events = readRecentEvents(adapter, 20);
  let highNoiseObserved = 0;
  let highNoiseRouted = 0;
  for (const event of events) {
    const command = (event.original_command || "").trim();
    if (!command) {
      continue;
    }
    const normalized = command.replace(/\bxit\s+auto\s+/, "").trim();
    if (!isHighOutputCommand(normalized)) {
      continue;
    }
    highNoiseObserved += 1;
    if (/\bxit\s+auto\b/.test(command)) {
      highNoiseRouted += 1;
    }
  }
  return { highNoiseObserved, highNoiseRouted };
}

function getAdapterRuleCandidates(
  root: string,
): Record<"Codex" | "Claude" | "Gemini" | "Cursor", string[]> {
  const existingTargets = collectExistingRuleTargets(root);
  return {
    Codex: [
      path.join(root, "AGENTS.md"),
      ...existingTargets.filter((p) =>
        p.includes(`${path.sep}.codex${path.sep}`),
      ),
    ],
    Claude: [path.join(root, "CLAUDE.md")],
    Gemini: [
      path.join(root, "GEMINI.md"),
      path.join(root, ".gemini", "rules.md"),
    ],
    Cursor: existingTargets.filter((p) =>
      p.includes(`${path.sep}.cursor${path.sep}rules${path.sep}`),
    ),
  };
}

export function getAiAdapterHealth(): AdapterHealthItem[] {
  const root = getWorkspaceRoot();
  if (!root) {
    return [
      {
        adapter: "Codex",
        status: "unknown",
        evidence: "No workspace folder open.",
        ruleFiles: [],
      },
      {
        adapter: "Claude",
        status: "unknown",
        evidence: "No workspace folder open.",
        ruleFiles: [],
      },
      {
        adapter: "Gemini",
        status: "unknown",
        evidence: "No workspace folder open.",
        ruleFiles: [],
      },
      {
        adapter: "Cursor",
        status: "unknown",
        evidence: "No workspace folder open.",
        ruleFiles: [],
      },
    ];
  }

  const candidates = getAdapterRuleCandidates(root);
  const codexFiles = [...new Set(candidates.Codex)].filter(hasRuleMarker);
  const claudeFiles = [...new Set(candidates.Claude)].filter(hasRuleMarker);
  const geminiFiles = [...new Set(candidates.Gemini)].filter(hasRuleMarker);
  const cursorFiles = [...new Set(candidates.Cursor)].filter(hasRuleMarker);
  const codexRouting = getAdapterHookRouting("codex");
  const claudeRouting = getAdapterHookRouting("claude");
  const cursorRouting = getAdapterHookRouting("cursor");

  return [
    {
      adapter: "Codex",
      status:
        codexFiles.length > 0
          ? codexRouting.highNoiseRouted > 0
            ? "verified"
            : "rules installed"
          : "not verified",
      evidence:
        codexFiles.length > 0
          ? `${path.basename(codexFiles[0])} contains XIT_AI_RULES section; Codex routed ${codexRouting.highNoiseRouted}/${codexRouting.highNoiseObserved} recent high-noise commands through XiT`
          : "No AGENTS.md or .codex rule file with XIT_AI_RULES detected.",
      ruleFiles: codexFiles,
      routedCount: codexRouting.highNoiseRouted,
      observedCount: codexRouting.highNoiseObserved,
    },
    {
      adapter: "Claude",
      status:
        claudeFiles.length > 0
          ? claudeRouting.highNoiseRouted > 0
            ? "verified"
            : "rules installed"
          : "not verified",
      evidence:
        claudeFiles.length > 0
          ? `${path.basename(claudeFiles[0])} contains XIT_AI_RULES section; Claude routed ${claudeRouting.highNoiseRouted}/${claudeRouting.highNoiseObserved} recent high-noise commands through XiT`
          : "No CLAUDE.md with XIT_AI_RULES detected.",
      ruleFiles: claudeFiles,
      routedCount: claudeRouting.highNoiseRouted,
      observedCount: claudeRouting.highNoiseObserved,
    },
    {
      adapter: "Gemini",
      status: geminiFiles.length > 0 ? "rules installed" : "unknown",
      evidence:
        geminiFiles.length > 0
          ? `${path.basename(geminiFiles[0])} contains XIT_AI_RULES section`
          : "No known workspace rule file detected for Gemini. Manual verification needed.",
      ruleFiles: geminiFiles,
    },
    {
      adapter: "Cursor",
      status:
        cursorFiles.length > 0
          ? cursorRouting.highNoiseRouted > 0
            ? "verified"
            : "rules installed"
          : "not verified",
      evidence:
        cursorFiles.length > 0
          ? `${path.basename(cursorFiles[0])} contains XIT_AI_RULES section; Cursor routed ${cursorRouting.highNoiseRouted}/${cursorRouting.highNoiseObserved} recent high-noise commands through XiT`
          : "No .cursor/rules file with XIT_AI_RULES detected.",
      ruleFiles: cursorFiles,
      routedCount: cursorRouting.highNoiseRouted,
      observedCount: cursorRouting.highNoiseObserved,
    },
  ];
}

function mapAdapterEvent(event: AdapterEvent): WorkflowEvent | undefined {
  const command = (event.original_command || "").trim();
  if (!command) {
    return undefined;
  }

  const recommended = (event.recommended_command || "").trim();
  const routedThroughXiT =
    /\bxit\s+auto\b/.test(command) || /\bxit\s+auto\b/.test(recommended);

  return {
    time: event.time || "",
    command,
    routedThroughXiT,
    source: event.adapter,
  };
}

function mapTerminalEvent(event: TerminalEventRecord): WorkflowEvent {
  return {
    time: event.time,
    command: event.commandLine,
    routedThroughXiT: /\bxit\s+auto\b/.test(event.commandLine),
    source: "vscode-terminal",
  };
}

function getMergedWorkflowEvents(limit = 20): WorkflowEvent[] {
  const terminalEvents = readTerminalEvents(limit).map(mapTerminalEvent);
  const adapterSources = ["codex", "claude", "cursor", "kimi"]
    .flatMap((adapter) => readRecentEvents(adapter, limit))
    .map(mapAdapterEvent)
    .filter((event): event is WorkflowEvent => event !== undefined);
  const workspaceHistory = readWorkspaceHistory(limit)
    .map(mapAdapterEvent)
    .filter((event): event is WorkflowEvent => event !== undefined);

  return terminalEvents
    .concat(adapterSources)
    .concat(workspaceHistory)
    .sort((a, b) => b.time.localeCompare(a.time))
    .slice(0, limit);
}

export function getRecentWorkflowRoutingStats(limit = 20): {
  recentHighNoiseCommands: number;
  recentHighNoiseRouted: number;
  routingHitRate: number;
} {
  const merged = getMergedWorkflowEvents(limit);

  let recentHighNoiseCommands = 0;
  let recentHighNoiseRouted = 0;
  const seen = new Set<string>();

  for (const event of merged) {
    const key = `${event.time}|${event.command}`;
    if (seen.has(key)) {
      continue;
    }
    seen.add(key);

    const normalized = event.command.replace(/\bxit\s+auto\s+/, "").trim();
    if (!isHighOutputCommand(normalized)) {
      continue;
    }
    recentHighNoiseCommands += 1;
    if (event.routedThroughXiT) {
      recentHighNoiseRouted += 1;
    }
  }

  return {
    recentHighNoiseCommands,
    recentHighNoiseRouted,
    routingHitRate:
      recentHighNoiseCommands > 0
        ? recentHighNoiseRouted / recentHighNoiseCommands
        : 0,
  };
}

export function computeWorkflowHealth(
  status: XiTStatus,
  latestRun: LatestRun | undefined,
): WorkflowHealth {
  const rules = getWorkspaceRuleStatus();
  const routing = getRecentWorkflowRoutingStats(20);
  const latestSavedBytes = latestRun
    ? Math.max(0, latestRun.raw_bytes - latestRun.summary_bytes)
    : 0;

  let recommendation = "XiT is active for this workspace";
  if (!rules.installed) {
    recommendation = "Run XiT: Install Workspace AI Rules";
  } else if (
    routing.recentHighNoiseCommands > 0 &&
    routing.recentHighNoiseRouted === 0
  ) {
    recommendation = "High-noise commands are not routed through XiT yet";
  }

  return {
    cliStatus:
      status.available || status.state === "gain-json-failed"
        ? "found"
        : "missing",
    latestRunStatus: latestRun ? "success" : "none",
    latestSavedBytes,
    latestSavedDisplay: formatSavedTokensFromBytes(latestSavedBytes),
    workspaceRulesInstalled: rules.installed,
    workspaceRuleFiles: rules.files,
    recentHighNoiseCommands: routing.recentHighNoiseCommands,
    recentHighNoiseRouted: routing.recentHighNoiseRouted,
    routingHitRate: routing.routingHitRate,
    recommendation,
  };
}

function execFilePromise(
  file: string,
  args: string[],
  cwd: string,
  timeoutMs = 5000,
): Promise<string> {
  return new Promise((resolve, reject) => {
    execFile(
      file,
      args,
      { cwd, timeout: timeoutMs, encoding: "utf-8" },
      (error, stdout, stderr) => {
        if (error) {
          reject(new Error(stderr || error.message));
          return;
        }
        resolve(stdout);
      },
    );
  });
}

export async function buildDiagnoseReport(
  status: XiTStatus,
  latestRun: LatestRun | undefined,
): Promise<DiagnoseReport> {
  const workspacePath = resolveWorkspaceCwd();
  const rules = getWorkspaceRuleStatus();
  const routing = getRecentWorkflowRoutingStats(20);
  const binaryPath = resolveAvailableBinary();
  let cliVersion: string | undefined;

  if (binaryPath) {
    try {
      cliVersion = (
        await execFilePromise(binaryPath, ["--version"], workspacePath)
      ).trim();
    } catch {
      cliVersion = undefined;
    }
  }

  const runsDir = path.join(workspacePath, ".xit", "runs");
  const watchedStatePath = path.join(workspacePath, ".xit", "state", "current-run.json");
  const watchedHistoryPath = path.join(workspacePath, ".xit", "history.jsonl");
  const stateFileExists = fs.existsSync(watchedStatePath);
  const historyFileExists = fs.existsSync(watchedHistoryPath);
  const agentsMdDetected = fs.existsSync(path.join(workspacePath, "AGENTS.md"));
  const claudeMdDetected = fs.existsSync(path.join(workspacePath, "CLAUDE.md"));
  const currentRunState = readCurrentRunState()?.status || "none";
  const latestSavedBytes = latestRun
    ? Math.max(0, latestRun.raw_bytes - latestRun.summary_bytes)
    : undefined;

  // Build recommendation — if no xit data in this workspace, suggest mismatch
  let recommendation: string;
  if (!historyFileExists && !stateFileExists && !fs.existsSync(runsDir)) {
    recommendation = `This VS Code window is watching ${workspacePath}. No XiT state found here. To monitor a different project, open that folder as the workspace or run XiT inside the current workspace.`;
  } else if (!rules.installed) {
    recommendation = "Run XiT: Install Workspace AI Rules";
  } else if (routing.recentHighNoiseCommands > 0 && routing.recentHighNoiseRouted === 0) {
    recommendation = "High-noise commands are not routed through XiT yet";
  } else {
    recommendation = "XiT is active for this workspace";
  }

  return {
    binaryPath: status.binary || binaryPath,
    cliVersion,
    workspacePath,
    watchedStatePath,
    watchedHistoryPath,
    watchedRunsDir: runsDir,
    stateFileExists,
    historyFileExists,
    agentsMdDetected,
    claudeMdDetected,
    hasRunsDir: fs.existsSync(runsDir),
    currentRunState,
    latestRunTime: latestRun?.timestamp,
    latestHistoryTimestamp: latestRun?.timestamp,
    latestSavedBytes,
    latestSavedDisplay:
      latestSavedBytes !== undefined
        ? formatSavedTokensFromBytes(latestSavedBytes)
        : undefined,
    latestRawLogPath: latestRun?.raw_log,
    recentHighNoiseCommands: routing.recentHighNoiseCommands,
    recentHighNoiseRouted: routing.recentHighNoiseRouted,
    routingHitRate: routing.routingHitRate,
    workspaceRulesInstalled: rules.installed,
    workspaceRuleFiles: rules.files,
    recommendation,
  };
}

export function formatSavedBytes(bytes: number): string {
  if (bytes >= 1000 * 1000) {
    return `~${Math.round(bytes / (1000 * 1000))}MB`;
  }
  if (bytes >= 1000) {
    return `~${Math.round(bytes / 1000)}KB`;
  }
  return `${bytes}B`;
}

export function estimateTokensFromBytes(bytes: number): number {
  return Math.max(0, Math.round(bytes / 4));
}

export function formatTokenCount(tokens: number): string {
  if (tokens >= 1000000) {
    return `~${Math.round(tokens / 100000) / 10}M Token`;
  }
  if (tokens >= 1000) {
    return `~${Math.round(tokens / 1000)}k Token`;
  }
  return `${tokens} Token`;
}

export function formatSavedTokensFromBytes(bytes: number): string {
  return formatTokenCount(estimateTokensFromBytes(bytes));
}

export function formatSavedTokensForRun(run: LatestRun | undefined): string {
  if (!run) {
    return "0 Token";
  }
  if (run.saved_tokens_display) {
    return run.saved_tokens_display.includes("Token")
      ? run.saved_tokens_display
      : `${run.saved_tokens_display} Token`;
  }
  if (typeof run.saved_tokens === "number") {
    return formatTokenCount(run.saved_tokens);
  }
  return formatSavedTokensFromBytes(
    Math.max(0, run.raw_bytes - run.summary_bytes),
  );
}

export function getTokenMetricsForRun(
  run: LatestRun | undefined,
): TokenMetrics | undefined {
  if (!run) {
    return undefined;
  }
  const rawTokens = estimateTokensFromBytes(Math.max(0, run.raw_bytes));
  const summaryTokens = estimateTokensFromBytes(Math.max(0, run.summary_bytes));
  const savedTokens =
    typeof run.saved_tokens === "number"
      ? Math.max(0, run.saved_tokens)
      : Math.max(0, rawTokens - summaryTokens);
  const savedDisplay = run.saved_tokens_display
    ? run.saved_tokens_display.includes("Token")
      ? run.saved_tokens_display
      : `${run.saved_tokens_display} Token`
    : formatTokenCount(savedTokens);
  const reductionPct = rawTokens > 0 ? (savedTokens / rawTokens) * 100 : 0;

  return {
    rawTokens,
    summaryTokens,
    savedTokens,
    savedDisplay,
    reductionPct,
  };
}

function getStartOfTodayMs(): number {
  const now = new Date();
  return new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
}

export function getTokenImpactStats(
  latestRun: LatestRun | undefined,
): TokenImpactStats {
  const latest = getTokenMetricsForRun(latestRun);
  const runs = readAllWorkspaceRuns();
  const startOfTodayMs = getStartOfTodayMs();
  let todaySavedTokens = 0;
  let workspaceTotalSavedTokens = 0;
  const byCommand = new Map<
    string,
    {
      runs: number;
      savedTokens: number;
      rawTokens: number;
      summaryTokens: number;
    }
  >();

  for (const run of runs) {
    const metrics = getTokenMetricsForRun(run);
    if (!metrics) {
      continue;
    }
    workspaceTotalSavedTokens += metrics.savedTokens;
    const ts = parseIsoTimeMs(run.timestamp);
    if (ts !== undefined && ts >= startOfTodayMs) {
      todaySavedTokens += metrics.savedTokens;
    }
    const entry = byCommand.get(run.command) || {
      runs: 0,
      savedTokens: 0,
      rawTokens: 0,
      summaryTokens: 0,
    };
    entry.runs += 1;
    entry.savedTokens += metrics.savedTokens;
    entry.rawTokens += metrics.rawTokens;
    entry.summaryTokens += metrics.summaryTokens;
    byCommand.set(run.command, entry);
  }

  const topTokenHeavyCommands = [...byCommand.entries()]
    .sort((a, b) => b[1].savedTokens - a[1].savedTokens)
    .slice(0, 10)
    .map(([command, entry]) => ({
      command,
      runs: entry.runs,
      savedTokens: entry.savedTokens,
      savedDisplay: formatTokenCount(entry.savedTokens),
      rawTokens: entry.rawTokens,
      summaryTokens: entry.summaryTokens,
    }));

  return {
    latest,
    todaySavedTokens,
    todaySavedDisplay: formatTokenCount(todaySavedTokens),
    workspaceTotalSavedTokens,
    workspaceTotalSavedDisplay: formatTokenCount(workspaceTotalSavedTokens),
    topTokenHeavyCommands,
  };
}

export function buildVerifyRoutingReport(): VerifyRoutingReport {
  const workspacePath = resolveWorkspaceCwd();
  const rules = getWorkspaceRuleStatus();
  const routing = getRecentWorkflowRoutingStats(20);
  const events = getMergedWorkflowEvents(20);
  const latestRun = readAllWorkspaceRuns().slice(-1)[0];
  const currentRunState = readCurrentRunState()?.status || "none";
  const latestHighNoiseCommands: string[] = [];
  const latestXiTAutoCommands: string[] = [];

  for (const event of events) {
    const normalized = event.command.replace(/\bxit\s+auto\s+/, "").trim();
    if (
      isHighOutputCommand(normalized) &&
      !latestHighNoiseCommands.includes(event.command)
    ) {
      latestHighNoiseCommands.push(event.command);
    }
    if (
      /\bxit\s+auto\b/.test(event.command) &&
      !latestXiTAutoCommands.includes(event.command)
    ) {
      latestXiTAutoCommands.push(event.command);
    }
  }

  const adapterHealth = getAiAdapterHealth();
  const codex = adapterHealth.find((item) => item.adapter === "Codex")!;
  const claude = adapterHealth.find((item) => item.adapter === "Claude")!;
  const gemini = adapterHealth.find((item) => item.adapter === "Gemini")!;
  const cursor = adapterHealth.find((item) => item.adapter === "Cursor")!;
  const codexRouting = getAdapterHookRouting("codex");
  const claudeRouting = getAdapterHookRouting("claude");
  const cursorRouting = getAdapterHookRouting("cursor");

  let recommendation = "XiT is active for this workspace";
  if (
    codexRouting.highNoiseObserved > 0 &&
    codexRouting.highNoiseRouted === 0
  ) {
    recommendation = "High-noise commands are not routed through XiT yet.";
  } else if (
    claudeRouting.highNoiseObserved > 0 &&
    claudeRouting.highNoiseRouted === 0
  ) {
    recommendation = "High-noise commands are not routed through XiT yet.";
  } else if (
    cursorRouting.highNoiseObserved > 0 &&
    cursorRouting.highNoiseRouted === 0
  ) {
    recommendation = "High-noise commands are not routed through XiT yet.";
  } else if (
    rules.installed &&
    codexRouting.highNoiseObserved === 0 &&
    claudeRouting.highNoiseObserved === 0 &&
    cursorRouting.highNoiseObserved === 0
  ) {
    recommendation =
      "Rules installed, waiting for agent to run a high-noise command through XiT.";
  } else if (!rules.installed) {
    recommendation = "Run XiT: Install Workspace AI Rules";
  }

  return {
    workspacePath,
    rulesFilesInstalled: rules.files,
    currentRunState,
    latestRunTime: latestRun?.timestamp,
    latestRunRawLog: latestRun?.raw_log,
    latestHighNoiseCommands: latestHighNoiseCommands.slice(0, 5),
    latestXiTAutoCommands: latestXiTAutoCommands.slice(0, 5),
    recentHighNoiseCommands: routing.recentHighNoiseCommands,
    recentHighNoiseRouted: routing.recentHighNoiseRouted,
    codex,
    claude,
    gemini,
    cursor,
    recommendation,
  };
}

// === Agent Turn Awareness ===

const ACTIVE_TURN_STALE_MS = 2 * 60 * 1000;  // active turn: no event for 2min → stale
const SUCCESS_HOLD_MS = 25 * 1000;             // completed turn shown for 25s after stop
const RECENT_ACTIVITY_MS = 10 * 60 * 1000;    // command routing mode window

function resolveActivityReason(event: AdapterEvent): string {
  const cmd = event.original_command || "";
  if (event.action === "reroute" || /\bxit\s+auto\b/.test(event.recommended_command || "") || /\bxit\s+auto\b/.test(cmd)) {
    return "routed";
  }
  if (event.event && !cmd) {
    return "lifecycle event";
  }
  if (cmd) {
    const normalized = cmd.replace(/\bxit\s+auto\s+/, "").trim();
    if (isHighOutputCommand(normalized)) return "high-noise / not routed";
    return "low-noise";
  }
  return "unknown";
}

export function buildAgentTurnView(): AgentTurnView {
  const workspacePath = resolveWorkspaceCwd();
  const turnState = readTurnState();
  const allRuns = readAllWorkspaceRuns();
  const ADAPTERS = ["claude", "codex", "kimi", "cursor"] as const;

  const adapterEvents = new Map<string, AdapterEvent[]>();
  for (const adapter of ADAPTERS) {
    adapterEvents.set(adapter, readRecentEvents(adapter, 50));
  }

  // === Stale turn detection ===
  const ignoredStaleTurns: StaleTurnRecord[] = [];
  let freshKimiTurnState: TurnState | undefined;

  if (turnState) {
    const finishedMs = parseIsoTimeMs(turnState.finished_at);
    const startedMs = parseIsoTimeMs(turnState.started_at);

    if (finishedMs !== undefined) {
      // Turn has a finished_at — check how long ago it stopped
      const ageMs = Date.now() - finishedMs;
      if (ageMs > SUCCESS_HOLD_MS) {
        ignoredStaleTurns.push({
          adapter: "kimi",
          stoppedAt: turnState.finished_at,
          ageHours: Math.round((ageMs / 3600000) * 10) / 10,
          reason: `turn stopped ${Math.round(ageMs / 3600000 * 10) / 10}h ago, exceeds hold window (${SUCCESS_HOLD_MS / 1000}s)`,
        });
      } else {
        freshKimiTurnState = turnState;
      }
    } else if (startedMs !== undefined) {
      // No finished_at — turn started but never recorded a stop
      const ageMs = Date.now() - startedMs;
      if (ageMs > ACTIVE_TURN_STALE_MS) {
        ignoredStaleTurns.push({
          adapter: "kimi",
          stoppedAt: turnState.started_at,
          ageHours: Math.round((ageMs / 3600000) * 10) / 10,
          reason: `turn active since ${Math.round(ageMs / 60000)}min ago with no finished_at, exceeds active stale threshold (${ACTIVE_TURN_STALE_MS / 1000}s)`,
        });
      } else {
        freshKimiTurnState = turnState;
      }
    }
  }

  // === Collect latest activity across all adapters (workspace-filtered) ===
  let latestActivity: LatestActivityInfo | undefined;
  let latestActivityMs: number | undefined;

  for (const [adapter, events] of adapterEvents) {
    for (const event of events) {
      if (!event.time) continue;
      if (event.cwd && path.resolve(event.cwd) !== path.resolve(workspacePath)) continue;
      const eventMs = parseIsoTimeMs(event.time);
      if (eventMs === undefined) continue;
      if (latestActivityMs === undefined || eventMs > latestActivityMs) {
        latestActivityMs = eventMs;
        const command = event.original_command || event.event || event.action || "";
        const isRouted = event.action === "reroute" ||
          /\bxit\s+auto\b/.test(event.recommended_command || "") ||
          /\bxit\s+auto\b/.test(event.original_command || "");
        latestActivity = {
          adapter,
          timestamp: event.time,
          eventType: event.event || event.action || event.policy || "command",
          command: (event.original_command || event.event) || undefined,
          cwd: event.cwd,
          routedThroughXit: isRouted,
          reason: resolveActivityReason(event),
          sourceFile: `~/.xit/${adapter}-hooks/events.jsonl`,
        };
      }
    }
  }

  // === Determine current turn (fresh lifecycle only) ===
  let primaryAdapter: AgentTurnView["adapter"] = "unknown";
  let turnStartMs: number | undefined;
  let turnStatus: AgentTurnStatus = "idle";
  let latestEvent: string | undefined;
  let turnStartedAt: string | undefined;
  let turnUpdatedAt: string | undefined;
  let isFreshActive = false;
  let staleTurnReason: string | undefined;
  let selectedTurnSource: string | undefined;

  if (freshKimiTurnState) {
    primaryAdapter = "kimi";
    latestEvent = freshKimiTurnState.event;
    turnStartedAt = freshKimiTurnState.started_at;
    turnStartMs = parseIsoTimeMs(freshKimiTurnState.started_at);
    turnUpdatedAt = freshKimiTurnState.finished_at || freshKimiTurnState.started_at;
    isFreshActive = true;

    if (freshKimiTurnState.finished_at) {
      turnStatus = "completed";
    } else if (freshKimiTurnState.status === "thinking" || freshKimiTurnState.status === "active" ||
               freshKimiTurnState.event === "UserPromptSubmit") {
      turnStatus = "working";
    } else {
      turnStatus = "completed";
    }
    selectedTurnSource = `turn.json (kimi, status=${freshKimiTurnState.status}, started=${freshKimiTurnState.started_at})`;
  } else if (ignoredStaleTurns.length > 0) {
    staleTurnReason = ignoredStaleTurns[0].reason;
    isFreshActive = false;
    selectedTurnSource = "none (stale turn ignored)";
  }

  // Override with xit_running if xit auto is actively running
  const currentRun = readCurrentRunState();
  if (currentRun?.status === "running") {
    const heartbeatMs = parseIsoTimeMs(currentRun.heartbeat_at || currentRun.started_at);
    if (heartbeatMs !== undefined && Date.now() - heartbeatMs <= 15000) {
      turnStatus = "xit_running";
      isFreshActive = true;
    }
  }

  // === Count commands in turn window, filtered by workspace cwd ===
  const evidence: string[] = [];
  let commandsObserved = 0;
  let routedThroughXit = 0;

  for (const [adapter, events] of adapterEvents) {
    let countForAdapter = 0;
    for (const event of events) {
      if (!event.time || !event.original_command) continue;
      if (event.cwd && path.resolve(event.cwd) !== path.resolve(workspacePath)) continue;
      const eventMs = parseIsoTimeMs(event.time);
      if (eventMs === undefined) continue;
      if (turnStartMs !== undefined && eventMs < turnStartMs) continue;

      commandsObserved++;
      countForAdapter++;
      if (
        event.action === "reroute" ||
        /\bxit\s+auto\b/.test(event.recommended_command || "") ||
        /\bxit\s+auto\b/.test(event.original_command || "")
      ) {
        routedThroughXit++;
      }
    }
    const latestForAdapter = events.find(e => e.time && (!e.cwd || path.resolve(e.cwd) === path.resolve(workspacePath)));
    if (countForAdapter > 0 || latestForAdapter?.time) {
      evidence.push(`${adapter}: ${countForAdapter} cmd(s)${latestForAdapter?.time ? `, last ${latestForAdapter.time}` : ""}`);
    }
  }

  // === Saved tokens: only from credible fresh turn window ===
  let savedTokensThisTurn = 0;
  const hasCreditWindow = isFreshActive && turnStartMs !== undefined;
  if (hasCreditWindow) {
    for (const run of allRuns) {
      const runMs = parseIsoTimeMs(run.timestamp);
      if (runMs === undefined || runMs < turnStartMs!) continue;
      const metrics = getTokenMetricsForRun(run);
      if (metrics) savedTokensThisTurn += metrics.savedTokens;
    }
  }

  const savedTokensDisplay = hasCreditWindow && savedTokensThisTurn > 0
    ? formatTokenCount(savedTokensThisTurn)
    : "—";

  return {
    adapter: primaryAdapter,
    status: turnStatus,
    latestEvent,
    startedAt: turnStartedAt,
    updatedAt: turnUpdatedAt,
    commandsObserved,
    routedThroughXit,
    savedTokensThisTurn,
    savedTokensDisplay,
    evidence,
    isFreshActive,
    staleTurnReason,
    latestActivity,
    selectedTurnSource,
    selectedActivitySource: latestActivity
      ? `${latestActivity.adapter} events (last=${latestActivity.timestamp})`
      : "none",
    ignoredStaleTurns,
  };
}

export function getAdapterHookConnectivity(): Record<string, AdapterHookInfo> {
  const ADAPTERS = ["claude", "codex", "kimi", "cursor"] as const;
  const result: Record<string, AdapterHookInfo> = {};

  for (const adapter of ADAPTERS) {
    const events = readRecentEvents(adapter, 5);
    const hasTurnLifecycle = adapter === "kimi" && events.some(
      e => e.event === "UserPromptSubmit" || e.event === "Stop" || e.event === "SessionStart"
    );
    result[adapter] = {
      connected: events.length > 0,
      hasTurnLifecycle,
      latestEventTime: events[0]?.time,
      eventCount: events.length,
    };
  }

  return result;
}
