import * as fs from "fs";
import * as path from "path";
import * as vscode from "vscode";
import { showDashboard, updateDashboardIfOpen } from "./dashboard";
import {
  openXiTTerminal,
  promptRunCommand,
  promptRunWithAutoCompression,
} from "./runner";
import type { LatestRun, XiTStatus } from "./types";
import {
  appendOutput,
  clearOutput,
  fetchStatus,
  openLatestRawLog,
  readCurrentRunState,
  readLatestRawLogMeta,
  readLatestRun,
  resolveWorkspaceCwd,
  showOutput,
  writeTerminalEvent,
} from "./xit";
import {
  buildAgentTurnView,
  buildVerifyRoutingReport,
  buildDiagnoseReport,
  computeWorkflowHealth,
  getAdapterHookConnectivity,
  getTokenMetricsForRun,
  formatSavedTokensForRun,
  installWorkspaceAiRules,
} from "./workflow";

let statusBarItem: vscode.StatusBarItem | undefined;
let refreshTimer: NodeJS.Timeout | undefined;
let liveState:
  | "idle"
  | "guarding"
  | "running"
  | "success"
  | "waiting"
  | "missed"
  | "no-binary" = "idle";
let liveStateTimer: NodeJS.Timeout | undefined;
let waitingStateTimer: NodeJS.Timeout | undefined;
let lastObservedRunSignature: string | undefined;
let terminalListenerDisposable: vscode.Disposable | undefined;
let activeRunStartedAt: number | undefined;
let activeRunRawLogPath: string | undefined;
let currentRunStateSignature: string | undefined;

function getRefreshIntervalMs(): number {
  const cfg = vscode.workspace.getConfiguration("xit");
  const seconds = cfg.get<number>("refreshInterval", 5);
  return Math.max(3, seconds) * 1000;
}

function isEnabled(): boolean {
  const cfg = vscode.workspace.getConfiguration("xit");
  return cfg.get<boolean>("enableStatusBar", true);
}

function isTerminalListenerEnabled(): boolean {
  const cfg = vscode.workspace.getConfiguration("xit");
  return cfg.get<boolean>("enableTerminalListener", true);
}

function getWorkspacePath(): string | undefined {
  const folders = vscode.workspace.workspaceFolders;
  return folders && folders.length > 0 ? folders[0].uri.fsPath : undefined;
}

function getStatusBarTextFromRun(run: LatestRun | undefined): string {
  if (!run) {
    return "吸T神功 · 准备就绪";
  }
  const savedBytes = Math.max(0, run.raw_bytes - run.summary_bytes);
  if (savedBytes <= 0) {
    return "吸T神功 · 无需发功";
  }
  return `吸T完成 · 省${formatSavedTokensForRun(run)}`;
}

function markActiveRun(startedAt = Date.now(), rawLogPath?: string): void {
  activeRunStartedAt = startedAt;
  if (rawLogPath) {
    activeRunRawLogPath = path.resolve(rawLogPath);
  }
}

function clearActiveRun(): void {
  activeRunStartedAt = undefined;
  activeRunRawLogPath = undefined;
}

function parseIsoTimeMs(iso: string | undefined): number | undefined {
  if (!iso) {
    return undefined;
  }
  const ms = Date.parse(iso);
  return Number.isNaN(ms) ? undefined : ms;
}

function isCurrentRunCompletion(run: LatestRun | undefined): boolean {
  if (!run || activeRunStartedAt === undefined) {
    return false;
  }
  const finishedAt = parseIsoTimeMs(run.timestamp);
  if (finishedAt === undefined || finishedAt < activeRunStartedAt) {
    return false;
  }
  if (activeRunRawLogPath) {
    const runRawLogPath = run.raw_log ? path.resolve(run.raw_log) : "";
    if (runRawLogPath !== activeRunRawLogPath) {
      return false;
    }
  }
  return true;
}

function setLiveState(state: typeof liveState, durationMs = 0): void {
  liveState = state;
  if (liveStateTimer) {
    clearTimeout(liveStateTimer);
    liveStateTimer = undefined;
  }
  if (waitingStateTimer) {
    clearTimeout(waitingStateTimer);
    waitingStateTimer = undefined;
  }
  void updateStatusBarLive();
  if (durationMs > 0) {
    liveStateTimer = setTimeout(() => {
      if (state === "success" || state === "missed") {
        clearActiveRun();
        liveState = "waiting";
        void updateStatusBarLive();
        waitingStateTimer = setTimeout(() => {
          liveState = "idle";
          void updateStatusBar();
        }, 20000);
        return;
      }
      liveState = "idle";
      void updateStatusBar();
    }, durationMs);
  }
}

function getRunSignature(run: LatestRun | undefined): string | undefined {
  if (!run) {
    return undefined;
  }
  return `${run.timestamp}|${run.command}|${run.raw_log}|${run.raw_bytes}|${run.summary_bytes}`;
}

function getCurrentRunStateSignature(): string | undefined {
  const state = readCurrentRunState();
  if (!state) {
    return undefined;
  }
  return [
    state.status,
    state.command || "",
    state.raw_log || "",
    state.started_at || "",
    state.completed_at || state.finished_at || "",
    state.raw_bytes ?? "",
    state.summary_bytes ?? "",
  ].join("|");
}

function isFreshRunningState(): boolean {
  const state = readCurrentRunState();
  if (!state || state.status !== "running") {
    return false;
  }
  const heartbeatAt = parseIsoTimeMs(state.heartbeat_at || state.started_at);
  if (heartbeatAt === undefined) {
    return false;
  }
  return Date.now() - heartbeatAt <= 15000;
}

function getCompletedRunFromStateOrHistory(): LatestRun | undefined {
  const state = readCurrentRunState();
  const latestRun = readLatestRun();
  if (!state || (state.status !== "completed" && state.status !== "failed")) {
    return latestRun;
  }
  // If the state raw_log matches history's latest, use history (it has richer fields).
  if (
    latestRun?.raw_log &&
    state.raw_log &&
    path.resolve(latestRun.raw_log) === path.resolve(state.raw_log)
  ) {
    return latestRun;
  }
  if (!state.completed_at && !state.finished_at) {
    return latestRun;
  }
  // If history has a newer entry than state's completion time, history is the source of truth.
  const stateCompletedMs = parseIsoTimeMs(state.completed_at || state.finished_at);
  const historyTs = parseIsoTimeMs(latestRun?.timestamp);
  if (
    stateCompletedMs !== undefined &&
    historyTs !== undefined &&
    historyTs > stateCompletedMs
  ) {
    return latestRun;
  }
  return {
    timestamp:
      state.completed_at || state.finished_at || new Date().toISOString(),
    command: state.command || latestRun?.command || "",
    exit_code: state.exit_code ?? latestRun?.exit_code ?? 0,
    raw_bytes: state.raw_bytes ?? latestRun?.raw_bytes ?? 0,
    summary_bytes: state.summary_bytes ?? latestRun?.summary_bytes ?? 0,
    saved_tokens: state.saved_tokens,
    saved_tokens_display: state.saved_tokens_display,
    estimated_reduction:
      state.estimated_reduction ?? latestRun?.estimated_reduction ?? 0,
    duration_ms: latestRun?.duration_ms ?? 0,
    filter: latestRun?.filter ?? "auto",
    confidence: latestRun?.confidence ?? "high",
    policy: latestRun?.policy ?? "should_compress",
    raw_log: state.raw_log || latestRun?.raw_log || "",
  };
}

function detectActiveRawLog(): string | undefined {
  const latestRawLog = readLatestRawLogMeta();
  if (!latestRawLog) {
    return undefined;
  }

  const latestRun = readLatestRun();
  const latestRunLog = latestRun?.raw_log
    ? path.resolve(latestRun.raw_log)
    : undefined;
  const rawLogPath = path.resolve(latestRawLog.path);
  const ageMs = Date.now() - latestRawLog.mtimeMs;

  if (ageMs > 15000) {
    return undefined;
  }

  if (!latestRunLog || latestRunLog !== rawLogPath) {
    return rawLogPath;
  }

  try {
    const historyMtime = fs.statSync(
      path.join(resolveWorkspaceCwd(), ".xit", "history.jsonl"),
    ).mtimeMs;
    if (latestRawLog.mtimeMs > historyMtime) {
      return rawLogPath;
    }
  } catch {
    return rawLogPath;
  }

  return undefined;
}

async function updateStatusBar(): Promise<void> {
  if (!statusBarItem) {
    return;
  }

  const status = await fetchStatus();
  const latestRun = getCompletedRunFromStateOrHistory();
  const latestRunSignature = getRunSignature(latestRun);
  const stateSignature = getCurrentRunStateSignature();
  const state = readCurrentRunState();
  const activeRawLog = detectActiveRawLog();
  const health = computeWorkflowHealth(status, latestRun);

  if (!status.available && status.state === "binary-not-found") {
    liveState = "no-binary";
    statusBarItem.text = "吸T神功 · 未找到 XiT";
    statusBarItem.tooltip = [
      "吸T神功尚未找到本地 XiT。",
      status.cwd ? `当前工作区：${status.cwd}` : "",
      status.attempts && status.attempts.length > 0
        ? `已尝试：${status.attempts.join(", ")}`
        : "",
      "点击打开 XiT Dashboard",
    ]
      .filter(Boolean)
      .join("\n");
    updateDashboardIfOpen(status);
    return;
  }

  if (isFreshRunningState() || activeRawLog) {
    const rawLogPath = state?.raw_log || activeRawLog;
    markActiveRun(parseIsoTimeMs(state?.started_at) || Date.now(), rawLogPath);
    setLiveState("running");
  } else if (
    state &&
    (state.status === "completed" || state.status === "failed") &&
    stateSignature &&
    stateSignature !== currentRunStateSignature
  ) {
    currentRunStateSignature = stateSignature;
    lastObservedRunSignature = latestRunSignature;
    const savedBytes = Math.max(
      0,
      (latestRun?.raw_bytes || 0) - (latestRun?.summary_bytes || 0),
    );
    setLiveState(savedBytes > 0 ? "success" : "missed", 25000);
  } else if (
    latestRunSignature &&
    latestRunSignature !== lastObservedRunSignature
  ) {
    lastObservedRunSignature = latestRunSignature;
    const savedBytes = Math.max(
      0,
      (latestRun?.raw_bytes || 0) - (latestRun?.summary_bytes || 0),
    );
    setLiveState(savedBytes > 0 ? "success" : "missed", 25000);
  } else if (liveState === "idle" && health.workspaceRulesInstalled) {
    liveState = "guarding";
  }

  if (liveState === "running") {
    statusBarItem.text = "吸T神功 · 正在吸T中";
  } else if (liveState === "success") {
    statusBarItem.text = getStatusBarTextFromRun(latestRun);
  } else if (liveState === "missed") {
    statusBarItem.text = "吸T神功 · 无需发功";
  } else if (liveState === "waiting") {
    statusBarItem.text = "吸T神功 · 等待下轮发功";
  } else if (liveState === "guarding") {
    statusBarItem.text = "吸T神功 · 守护你的T";
  } else {
    statusBarItem.text = "吸T神功 · 准备就绪";
  }

  const workspaceRoot = getWorkspacePath() || "unknown";
  const watchedStatePath = `${workspaceRoot}/.xit/state/current-run.json`;
  const watchedHistoryPath = `${workspaceRoot}/.xit/history.jsonl`;
  const currentRunStatus = readCurrentRunState()?.status || "none";
  const lastUpdateStr = status.refreshedAt
    ? status.refreshedAt.toLocaleTimeString()
    : "—";

  // Build turn-aware tooltip lines
  const agentTurn = buildAgentTurnView();
  const turnStatusMap: Record<string, string> = {
    working: "AI 正在工作",
    xit_running: "正在吸T中",
    completed: "本轮已完成",
    stopped: "已停止",
    idle: "空闲",
    unknown: "未知",
  };
  const turnLines =
    agentTurn.status !== "idle" || agentTurn.commandsObserved > 0
      ? [
          `当前对话：${agentTurn.adapter === "unknown" ? "未知" : agentTurn.adapter}`,
          `Turn 状态：${turnStatusMap[agentTurn.status] || agentTurn.status}`,
          `本轮命令：${agentTurn.commandsObserved} 个，路由 XiT：${agentTurn.routedThroughXit}`,
          agentTurn.savedTokensThisTurn > 0 ? `本轮节省：${agentTurn.savedTokensDisplay}` : "",
        ].filter(Boolean)
      : [];

  statusBarItem.tooltip = [
    ...(liveState === "running"
      ? ["正在吸T中", "完成后显示实际节省"]
      : (() => {
          const metrics = getTokenMetricsForRun(latestRun);
          if (!metrics || !latestRun) {
            return [
              health.workspaceRulesInstalled
                ? "吸T神功正在守护当前工作区"
                : "吸T神功已准备好，随时出手",
            ];
          }
          return [
            "本次吸T",
            `原始输出：${metrics.rawTokens >= 1000 ? `~${(metrics.rawTokens / 1000).toFixed(1)}k Token` : `${metrics.rawTokens} Token`}`,
            `吸后摘要：${metrics.summaryTokens >= 1000 ? `~${(metrics.summaryTokens / 1000).toFixed(1)}k Token` : `${metrics.summaryTokens} Token`}`,
            `本次节省：${metrics.savedDisplay}`,
            `降噪率：${Math.round(metrics.reductionPct)}%`,
          ];
        })()),
    ...(turnLines.length > 0 ? ["─".repeat(20), ...turnLines] : []),
    `Workspace: ${workspaceRoot}`,
    `State: ${watchedStatePath}`,
    `Current run: ${currentRunStatus}`,
    latestRun?.timestamp ? `Latest run: ${new Date(latestRun.timestamp).toLocaleString()}` : "Latest run: none",
    `Rules: ${health.workspaceRulesInstalled ? "installed" : "not installed"}`,
    latestRun?.raw_log ? `raw log：${latestRun.raw_log}` : "",
    status.binary ? `XiT 本体：${status.binary}` : "",
    `Last update: ${lastUpdateStr}`,
    "本地处理，无遥测，无网络请求",
    "点击打开 XiT Dashboard",
  ]
    .filter(Boolean)
    .join("\n");

  updateDashboardIfOpen(status);
}

async function updateStatusBarLive(): Promise<void> {
  if (!statusBarItem) {
    return;
  }

  if (liveState === "no-binary") {
    statusBarItem.text = "吸T神功 · 未找到 XiT";
    return;
  }
  if (liveState === "running") {
    statusBarItem.text = "吸T神功 · 正在吸T中";
    return;
  }
  if (liveState === "missed") {
    statusBarItem.text = "吸T神功 · 无需发功";
    return;
  }
  if (liveState === "success") {
    statusBarItem.text = getStatusBarTextFromRun(
      getCompletedRunFromStateOrHistory(),
    );
    return;
  }
  if (liveState === "waiting") {
    statusBarItem.text = "吸T神功 · 等待下轮发功";
    return;
  }
  if (liveState === "guarding") {
    statusBarItem.text = "吸T神功 · 守护你的T";
    return;
  }
  statusBarItem.text = computeWorkflowHealth(
    await fetchStatus(),
    readLatestRun(),
  ).workspaceRulesInstalled
    ? "吸T神功 · 守护你的T"
    : "吸T神功 · 准备就绪";
}

function startRefresh(): void {
  if (refreshTimer) {
    clearInterval(refreshTimer);
  }
  if (!isEnabled()) {
    return;
  }
  void updateStatusBar();
  refreshTimer = setInterval(() => {
    void updateStatusBar();
  }, getRefreshIntervalMs());
}

function stopRefresh(): void {
  if (refreshTimer) {
    clearInterval(refreshTimer);
    refreshTimer = undefined;
  }
}

function registerWorkspaceWatchers(context: vscode.ExtensionContext): void {
  const workspacePath = getWorkspacePath();
  if (!workspacePath) {
    return;
  }

  const historyPattern = new vscode.RelativePattern(
    workspacePath,
    ".xit/history.jsonl",
  );
  const statePattern = new vscode.RelativePattern(
    workspacePath,
    ".xit/state/current-run.json",
  );
  const legacyStatePattern = new vscode.RelativePattern(
    workspacePath,
    ".xit/state/current.json",
  );
  const rawLogPattern = new vscode.RelativePattern(
    workspacePath,
    ".xit/runs/*.raw.log",
  );

  const historyWatcher =
    vscode.workspace.createFileSystemWatcher(historyPattern);
  const stateWatcher = vscode.workspace.createFileSystemWatcher(statePattern);
  const legacyStateWatcher =
    vscode.workspace.createFileSystemWatcher(legacyStatePattern);
  const rawLogWatcher = vscode.workspace.createFileSystemWatcher(rawLogPattern);

  const onHistoryChange = async (): Promise<void> => {
    const latestRun = readLatestRun();
    const signature = getRunSignature(latestRun);
    if (signature && signature !== lastObservedRunSignature) {
      lastObservedRunSignature = signature;
      const savedBytes = Math.max(
        0,
        (latestRun?.raw_bytes || 0) - (latestRun?.summary_bytes || 0),
      );
      setLiveState(savedBytes > 0 ? "success" : "missed", 25000);
    }
    if (statusBarItem) {
      const status = await fetchStatus();
      updateDashboardIfOpen(status);
    }
    await updateStatusBar();
  };

  const onRawLogChange = async (): Promise<void> => {
    const active = detectActiveRawLog();
    if (active) {
      const rawLogMeta = readLatestRawLogMeta();
      markActiveRun(rawLogMeta?.mtimeMs || Date.now(), active);
      setLiveState("running");
    }
    await updateStatusBar();
  };

  const onStateChange = async (): Promise<void> => {
    const state = readCurrentRunState();
    const signature = getCurrentRunStateSignature();
    if (state?.status === "running" && isFreshRunningState()) {
      markActiveRun(
        parseIsoTimeMs(state.started_at) || Date.now(),
        state.raw_log,
      );
      setLiveState("running");
    } else if (
      state &&
      (state.status === "completed" || state.status === "failed") &&
      signature &&
      signature !== currentRunStateSignature
    ) {
      currentRunStateSignature = signature;
      const latestRun = getCompletedRunFromStateOrHistory();
      lastObservedRunSignature = getRunSignature(latestRun);
      const savedBytes = Math.max(
        0,
        (latestRun?.raw_bytes || 0) - (latestRun?.summary_bytes || 0),
      );
      setLiveState(savedBytes > 0 ? "success" : "missed", 25000);
    }
    if (statusBarItem) {
      const status = await fetchStatus();
      updateDashboardIfOpen(status);
    }
    await updateStatusBar();
  };

  historyWatcher.onDidChange(onHistoryChange, null, context.subscriptions);
  historyWatcher.onDidCreate(onHistoryChange, null, context.subscriptions);
  stateWatcher.onDidChange(onStateChange, null, context.subscriptions);
  stateWatcher.onDidCreate(onStateChange, null, context.subscriptions);
  legacyStateWatcher.onDidChange(onStateChange, null, context.subscriptions);
  legacyStateWatcher.onDidCreate(onStateChange, null, context.subscriptions);
  rawLogWatcher.onDidChange(onRawLogChange, null, context.subscriptions);
  rawLogWatcher.onDidCreate(onRawLogChange, null, context.subscriptions);

  context.subscriptions.push(
    historyWatcher,
    stateWatcher,
    legacyStateWatcher,
    rawLogWatcher,
  );
}

function registerTerminalListeners(context: vscode.ExtensionContext): void {
  terminalListenerDisposable?.dispose();
  terminalListenerDisposable = undefined;

  if (!isTerminalListenerEnabled()) {
    return;
  }

  try {
    const listener = (vscode.window as any).onDidStartTerminalShellExecution?.(
      (event: any) => {
        const commandLine = event.execution?.commandLine?.value || "";
        const confidence = event.execution?.commandLine?.confidence ?? 0;
        const terminalName = event.terminal?.name || "unknown";
        const cwd = event.execution?.cwd?.fsPath;
        if (!commandLine) {
          return;
        }
        writeTerminalEvent({ commandLine, confidence, terminalName, cwd });
        if (/\bxit\s+auto\b/.test(commandLine)) {
          markActiveRun(Date.now());
          setLiveState("running");
        }
        void updateStatusBar();
      },
    );
    if (listener) {
      terminalListenerDisposable = listener;
      context.subscriptions.push(listener);
    }
  } catch {
    // ignore API absence
  }
}

async function runDiagnose(): Promise<void> {
  const status = await fetchStatus();
  const latestRun = readLatestRun();
  const report = await buildDiagnoseReport(status, latestRun);
  const hookConnectivity = getAdapterHookConnectivity();
  const agentTurn = buildAgentTurnView();

  const hookLines = Object.entries(hookConnectivity).map(([adapter, info]) => {
    const hookType = info.hasTurnLifecycle
      ? "turn lifecycle (UserPromptSubmit/Stop)"
      : info.connected
        ? "command routing only (PreToolUse)"
        : "not connected";
    const detail = info.connected && info.latestEventTime
      ? `${info.eventCount} events, last ${info.latestEventTime}`
      : "no events";
    return `  ${adapter.padEnd(10)}: ${hookType} — ${detail}`;
  });

  const cannotReadChatNote = [
    "  NOTE: VS Code extension cannot read Claude/Codex/Gemini chat content.",
    "  Only local hook metadata (command routing, turn lifecycle) is used.",
    "  Claude Code panel activity requires Claude hooks to record local turn metadata.",
  ];

  const lines = [
    "XiT: Diagnose AI Workflow",
    "─".repeat(50),
    `VS Code workspace root:   ${report.workspacePath}`,
    `Watched XiT state path:   ${report.watchedStatePath}`,
    `Watched XiT history path: ${report.watchedHistoryPath}`,
    `Watched runs dir:         ${report.watchedRunsDir}`,
    `state file exists:        ${report.stateFileExists ? "yes" : "no"}`,
    `history file exists:      ${report.historyFileExists ? "yes" : "no"}`,
    `AGENTS.md detected:       ${report.agentsMdDetected ? "yes" : "no"}`,
    `CLAUDE.md detected:       ${report.claudeMdDetected ? "yes" : "no"}`,
    "─".repeat(50),
    `binary_path:              ${report.binaryPath || "missing"}`,
    `cli_version:              ${report.cliVersion || "unknown"}`,
    `has_runs_dir:             ${report.hasRunsDir ? "yes" : "no"}`,
    `latest_current_run_status:${report.currentRunState || "none"}`,
    `latest_history_timestamp: ${report.latestHistoryTimestamp || "none"}`,
    `latest_saved_bytes:       ${report.latestSavedBytes ?? "none"}`,
    `latest_saved_display:     ${report.latestSavedDisplay || "none"}`,
    `latest_raw_log:           ${report.latestRawLogPath || "none"}`,
    "─".repeat(50),
    `recent_agent_events:      ${report.recentHighNoiseCommands} high-noise command(s)`,
    `recent_xit_auto_runs:     ${report.recentHighNoiseRouted} routed through xit auto`,
    `routing_hit_rate:         ${(report.routingHitRate * 100).toFixed(1)}%`,
    `workspace_rules_installed:${report.workspaceRulesInstalled ? "yes" : "no"}`,
    `workspace_rule_files:     ${report.workspaceRuleFiles.length > 0 ? report.workspaceRuleFiles.join(", ") : "none"}`,
    "─".repeat(50),
    "Agent conversation hooks:",
    ...hookLines,
    ...cannotReadChatNote,
    "─".repeat(50),
    "Latest agent turn:",
    `  adapter:            ${agentTurn.adapter}`,
    `  turn status:        ${agentTurn.status}`,
    `  latest event:       ${agentTurn.latestEvent || "none"}`,
    `  commands observed:  ${agentTurn.commandsObserved}`,
    `  routed through XiT: ${agentTurn.routedThroughXit}`,
    `  saved this turn:    ${agentTurn.savedTokensDisplay}`,
    `  evidence:`,
    ...agentTurn.evidence.map(e => `    ${e}`),
    "─".repeat(50),
    `recommendation: ${report.recommendation || "none"}`,
  ];
  clearOutput();
  appendOutput(lines.join("\n"));
  showOutput();
}

async function runVerifyRouting(): Promise<void> {
  const report = buildVerifyRoutingReport();
  const lines = [
    "XiT Verify AI Agent Routing",
    `workspace: ${report.workspacePath}`,
    `current_run_state: ${report.currentRunState}`,
    `rules_files_installed: ${report.rulesFilesInstalled.length > 0 ? report.rulesFilesInstalled.join(", ") : "none"}`,
    `latest_run_time: ${report.latestRunTime || "none"}`,
    `latest_run_raw_log: ${report.latestRunRawLog || "none"}`,
    `latest_high_noise_commands: ${report.latestHighNoiseCommands.length > 0 ? report.latestHighNoiseCommands.join(" | ") : "none"}`,
    `latest_xit_auto_commands: ${report.latestXiTAutoCommands.length > 0 ? report.latestXiTAutoCommands.join(" | ") : "none"}`,
    `recent_high_noise_commands_routed_through_xit: ${report.recentHighNoiseRouted}/${report.recentHighNoiseCommands}`,
    `Codex: ${report.codex.status} | ${report.codex.evidence}`,
    `Claude: ${report.claude.status} | ${report.claude.evidence}`,
    `Gemini: ${report.gemini.status} | ${report.gemini.evidence}`,
    `Cursor: ${report.cursor.status} | ${report.cursor.evidence}`,
    `recommendation: ${report.recommendation}`,
  ];
  clearOutput();
  appendOutput(lines.join("\n"));
  showOutput();
}

export function activate(context: vscode.ExtensionContext): void {
  if (isEnabled()) {
    statusBarItem = vscode.window.createStatusBarItem(
      vscode.StatusBarAlignment.Right,
      100,
    );
    statusBarItem.command = "xit.openDashboard";
    statusBarItem.text = "吸T神功 · 准备就绪";
    statusBarItem.show();
    context.subscriptions.push(statusBarItem);
  }

  lastObservedRunSignature = getRunSignature(readLatestRun());
  currentRunStateSignature = getCurrentRunStateSignature();
  startRefresh();
  registerWorkspaceWatchers(context);
  registerTerminalListeners(context);

  context.subscriptions.push(
    vscode.commands.registerCommand("xit.openDashboard", async () => {
      const status = await fetchStatus();
      showDashboard(context, status);
    }),
    vscode.commands.registerCommand("xit.refresh", async () => {
      await updateStatusBar();
      vscode.window.showInformationMessage("XiT status refreshed");
    }),
    vscode.commands.registerCommand("xit.showGain", async () => {
      const status = await fetchStatus();
      if (!status.available || !status.gain) {
        vscode.window.showWarningMessage(
          `XiT: ${status.error || "No gain data available."}`,
        );
        return;
      }
      const g = status.gain;
      vscode.window.showInformationMessage(
        `Commands condensed: ${g.total_commands_condensed} | Saved tokens: ${g.saved_tokens_display} | Estimated reduction: ${(g.estimated_reduction * 100).toFixed(1)}% | Saved bytes: ${g.saved_bytes}`,
      );
    }),
    vscode.commands.registerCommand("xit.openLatestRawLog", openLatestRawLog),
    vscode.commands.registerCommand("xit.showOutput", showOutput),
    vscode.commands.registerCommand("xit.runCommand", async () => {
      setLiveState("running");
      await promptRunCommand();
    }),
    vscode.commands.registerCommand("xit.runWithAutoCompression", async () => {
      setLiveState("running");
      await promptRunWithAutoCompression();
    }),
    vscode.commands.registerCommand("xit.openXiTTerminal", () => {
      openXiTTerminal();
    }),
    vscode.commands.registerCommand("xit.installWorkspaceAiRules", async () => {
      const result = installWorkspaceAiRules();
      await updateStatusBar();
      const createdSummary =
        result.created.length > 0
          ? ` Created: ${result.created.join(", ")}`
          : "";
      vscode.window.showInformationMessage(
        `XiT workspace AI rules updated in ${result.files.length} file(s).${createdSummary}`,
      );
    }),
    vscode.commands.registerCommand("xit.diagnoseAiWorkflow", async () => {
      await runDiagnose();
    }),
    vscode.commands.registerCommand("xit.verifyAiAgentRouting", async () => {
      await runVerifyRouting();
    }),
    vscode.workspace.onDidChangeConfiguration((e) => {
      if (e.affectsConfiguration("xit.enableStatusBar")) {
        if (isEnabled()) {
          if (!statusBarItem) {
            statusBarItem = vscode.window.createStatusBarItem(
              vscode.StatusBarAlignment.Right,
              100,
            );
            statusBarItem.command = "xit.openDashboard";
            context.subscriptions.push(statusBarItem);
          }
          statusBarItem.show();
          statusBarItem.text = "吸T神功 · 准备就绪";
          startRefresh();
        } else {
          stopRefresh();
          statusBarItem?.hide();
        }
      }
      if (e.affectsConfiguration("xit.refreshInterval")) {
        startRefresh();
      }
      if (e.affectsConfiguration("xit.enableTerminalListener")) {
        registerTerminalListeners(context);
      }
    }),
  );
}

export function deactivate(): void {
  stopRefresh();
  terminalListenerDisposable?.dispose();
}
