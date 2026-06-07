import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import * as vscode from "vscode";
import { showDashboard, updateDashboardIfOpen } from "./dashboard";
import {
  openXiTTerminal,
  promptRunCommand,
  promptRunWithAutoCompression,
} from "./runner";
import type { AdapterEvent, LatestRun, LiveStatusView, XiTStatus } from "./types";
import {
  appendOutput,
  clearOutput,
  fetchStatus,
  openLatestRawLog,
  readCurrentRunState,
  readLatestRawLogMeta,
  readLatestRun,
  readRecentEvents,
  resolveActiveXitWorkspace,
  resolveWorkspaceCwd,
  showOutput,
  writeTerminalEvent,
} from "./xit";
import {
  buildAgentTurnView,
  buildLiveStatusView,
  buildVerifyRoutingReport,
  buildDiagnoseReport,
  estimateHitRateLift,
  getAdapterHookConnectivity,
  getTokenMetricsForRun,
  formatSavedTokensForRun,
  installWorkspaceAiRules,
  readAllWorkspaceRuns,
} from "./workflow";

let statusBarItem: vscode.StatusBarItem | undefined;
let refreshTimer: NodeJS.Timeout | undefined;
let liveState:
  | "idle"
  | "guarding"
  | "turn_active"
  | "running"
  | "settling"
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
let runningVisibleUntil: number | undefined;
let activeRunPoller: NodeJS.Timeout | undefined;
const RUNNING_MIN_VISIBLE_MS = 2500;
const TURN_ACTIVE_TIMEOUT_MS = 45000;
const SETTLING_DURATION_MS = 4000;

let successRunDisplay: string | undefined;
let sessionRunCountAtSuccess = 0;
let turnActiveTimer: NodeJS.Timeout | undefined;

let liveStateWorkspace: string | undefined;
let turnActiveStartedAt: number | undefined;
let activeRunPollerWorkspace: string | undefined;
const TURN_ACTIVE_MAX_MS = 60000;
const RUNNING_MAX_MS = 120000;

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

function pickRotatingText(options: string[], intervalMs = 5000): string {
  if (!options.length) { return ""; }
  const idx = Math.floor(Date.now() / intervalMs) % options.length;
  return options[idx];
}

function ensureTildePrefix(display: string): string {
  if (!display || display === "0 Token" || display === "—") { return display; }
  return display.startsWith("~") ? display : `~${display}`;
}

function getSessionRunCount(): number {
  const startOfToday = new Date().setHours(0, 0, 0, 0);
  const count = readAllWorkspaceRuns().filter((r) => {
    const ms = r.timestamp ? Date.parse(r.timestamp) : 0;
    return ms >= startOfToday;
  }).length;
  return Math.max(1, count);
}

function getStatusBarTextFromLiveStatus(view: LiveStatusView): string {
  switch (view.kind) {
    case "xit_running":
      return "吸T神功 · 正在吸T中";
    case "agent_routed_pending_state":
    case "agent_observing":
      return "吸T神功 · 守护你的T";
    case "agent_not_routed":
      return "吸T神功 · 无需发功";
    case "xit_completed": {
      const savedDisplay = ensureTildePrefix(view.savedTokensDisplay || "");
      const opt1 = savedDisplay ? `吸T完成 · 本次省${savedDisplay}` : "吸T完成";
      const opt2 = `吸T神功 · 本轮共吸 ${getSessionRunCount()}次`;
      return pickRotatingText([opt1, opt2]);
    }
    case "missing":
      return "吸T神功 · 未接入";
    case "idle":
    default:
      return pickRotatingText(["吸T神功 · 准备就绪", "吸T神功 · 守护你的T"]);
  }
}

function getLiveStatusLabel(view: LiveStatusView): string {
  const labelMap: Partial<Record<string, string>> = {
    xit_running: "正在吸T中",
    agent_routed_pending_state: "守护你的T",
    agent_observing: "守护你的T",
    agent_not_routed: "无需发功",
    xit_completed: "吸T完成",
    missing: "未接入",
    idle: "守护你的T",
  };
  return labelMap[view.kind] ?? "守护你的T";
}

function markActiveRun(startedAt = Date.now(), rawLogPath?: string, workspace?: string): void {
  activeRunStartedAt = startedAt;
  if (rawLogPath) {
    activeRunRawLogPath = path.resolve(rawLogPath);
  }
  if (workspace) {
    liveStateWorkspace = workspace;
  }
}

function clearActiveRun(): void {
  activeRunStartedAt = undefined;
  activeRunRawLogPath = undefined;
}

function resetTurnActiveTimer(): void {
  if (turnActiveTimer) {
    clearTimeout(turnActiveTimer);
  }
  turnActiveTimer = setTimeout(() => {
    turnActiveTimer = undefined;
    if (liveState === "turn_active") {
      setLiveState("missed", 8000);
    }
  }, TURN_ACTIVE_TIMEOUT_MS);
}

function enterTurnActive(workspace?: string): void {
  // Don't downgrade from xit-active or post-run phases
  if (liveState === "running" || liveState === "settling" || liveState === "success") {
    resetTurnActiveTimer();
    return;
  }
  if (liveState !== "turn_active") {
    // Cancel any pending state timers before overriding
    if (liveStateTimer) {
      clearTimeout(liveStateTimer);
      liveStateTimer = undefined;
    }
    if (waitingStateTimer) {
      clearTimeout(waitingStateTimer);
      waitingStateTimer = undefined;
    }
    liveState = "turn_active";
    liveStateWorkspace = workspace || resolveActiveXitWorkspace();
    turnActiveStartedAt = Date.now();
    void updateStatusBarLive();
  }
  resetTurnActiveTimer();
}

function enterSuccessPhase(hasSavings: boolean, latestRun?: LatestRun, workspace?: string): void {
  if (liveState === "settling" || liveState === "success") {
    return;
  }
  if (!hasSavings) {
    setLiveState("missed", 8000, workspace);
    return;
  }

  const metrics = getTokenMetricsForRun(latestRun);
  const rawDisplay = metrics?.savedDisplay ||
    (latestRun?.saved_tokens_display
      ? (latestRun.saved_tokens_display.includes("Token")
        ? latestRun.saved_tokens_display
        : `${latestRun.saved_tokens_display} Token`)
      : undefined);
  successRunDisplay = rawDisplay ? ensureTildePrefix(rawDisplay) : undefined;
  sessionRunCountAtSuccess = getSessionRunCount();

  if (activeRunPoller) {
    clearInterval(activeRunPoller);
    activeRunPoller = undefined;
  }
  if (turnActiveTimer) {
    clearTimeout(turnActiveTimer);
    turnActiveTimer = undefined;
  }
  if (liveStateTimer) {
    clearTimeout(liveStateTimer);
    liveStateTimer = undefined;
  }
  if (waitingStateTimer) {
    clearTimeout(waitingStateTimer);
    waitingStateTimer = undefined;
  }

  const delay =
    liveState === "running" && runningVisibleUntil !== undefined && Date.now() < runningVisibleUntil
      ? runningVisibleUntil - Date.now()
      : 0;

  const doSettle = (): void => {
    if (liveState === "settling" || liveState === "success") {
      return;
    }
    liveState = "settling";
    if (workspace) {
      liveStateWorkspace = workspace;
    }
    runningVisibleUntil = undefined;
    void updateStatusBarLive();

    liveStateTimer = setTimeout(() => {
      liveStateTimer = undefined;
      if (liveState !== "settling") {
        return;
      }
      liveState = "success";
      if (workspace) {
        liveStateWorkspace = workspace;
      }
      void updateStatusBarLive();

      liveStateTimer = setTimeout(() => {
        liveStateTimer = undefined;
        clearActiveRun();
        liveState = "waiting";
        if (workspace) {
          liveStateWorkspace = workspace;
        }
        void updateStatusBarLive();
        waitingStateTimer = setTimeout(() => {
          waitingStateTimer = undefined;
          liveState = "idle";
          liveStateWorkspace = undefined;
          void updateStatusBar();
        }, 20000);
      }, 25000);
    }, SETTLING_DURATION_MS);
  };

  if (delay > 0) {
    setTimeout(doSettle, delay);
  } else {
    doSettle();
  }
}

function buildLiveStatusOverride(activeWorkspace?: string): LiveStatusView | undefined {
  if (
    activeWorkspace &&
    liveStateWorkspace &&
    path.resolve(liveStateWorkspace) !== path.resolve(activeWorkspace)
  ) {
    return undefined;
  }
  switch (liveState) {
    case "running":
      return {
        kind: "xit_running",
        label: "正在吸T中",
        reason: "xit auto running",
        source: "liveState",
      };
    case "turn_active":
      return {
        kind: "xit_running",
        label: "正在吸T中",
        reason: "turn active",
        source: "liveState",
      };
    case "settling":
    case "success":
      return {
        kind: "xit_completed",
        label: "吸T完成",
        savedTokensDisplay: successRunDisplay,
        reason: liveState === "settling" ? "post-run settling" : "success hold",
        source: "liveState",
      };
    case "missed":
      return {
        kind: "agent_not_routed",
        label: "无需发功",
        reason: "turn ended, no xit triggered",
        source: "liveState",
      };
    default:
      return undefined;
  }
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

function setLiveState(state: typeof liveState, durationMs = 0, workspace?: string): void {
  // Enforce minimum running visibility: if we're leaving "running" too soon, delay.
  if (
    liveState === "running" &&
    state !== "running" &&
    runningVisibleUntil !== undefined &&
    Date.now() < runningVisibleUntil
  ) {
    const delay = runningVisibleUntil - Date.now();
    setTimeout(() => setLiveState(state, durationMs, workspace), delay);
    return;
  }

  liveState = state;

  if (workspace) {
    liveStateWorkspace = workspace;
  } else if (state === "idle" || state === "no-binary" || state === "waiting") {
    liveStateWorkspace = undefined;
  }

  if (state === "running") {
    runningVisibleUntil = Date.now() + RUNNING_MIN_VISIBLE_MS;
  } else {
    runningVisibleUntil = undefined;
  }

  // Stop polling once we've left running state via success/missed/settling
  if (state === "success" || state === "settling" || state === "missed" || state === "idle" || state === "waiting") {
    if (activeRunPoller) {
      clearInterval(activeRunPoller);
      activeRunPoller = undefined;
    }
  }
  // Clear turn-active timer when entering a higher-priority or terminal state
  if (state === "running" || state === "settling" || state === "success" || state === "missed" || state === "idle" || state === "waiting") {
    if (turnActiveTimer) {
      clearTimeout(turnActiveTimer);
      turnActiveTimer = undefined;
    }
  }

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
        if (workspace) {
          liveStateWorkspace = workspace;
        }
        void updateStatusBarLive();
        waitingStateTimer = setTimeout(() => {
          liveState = "idle";
          liveStateWorkspace = undefined;
          void updateStatusBar();
        }, 20000);
        return;
      }
      liveState = "idle";
      liveStateWorkspace = undefined;
      void updateStatusBar();
    }, durationMs);
  }
}

function startActiveRunPoller(workspacePath?: string): void {
  if (activeRunPoller) {
    if (
      activeRunPollerWorkspace &&
      workspacePath &&
      path.resolve(activeRunPollerWorkspace) !== path.resolve(workspacePath)
    ) {
      clearInterval(activeRunPoller);
      activeRunPoller = undefined;
    } else {
      return;
    }
  }

  activeRunPollerWorkspace = workspacePath;
  let ticks = 0;
  const MAX_TICKS = 240; // 120s at 500ms
  activeRunPoller = setInterval(() => {
    ticks++;
    if (ticks > MAX_TICKS) {
      clearInterval(activeRunPoller);
      activeRunPoller = undefined;
      activeRunPollerWorkspace = undefined;
      return;
    }

    if (workspacePath) {
      const currentWorkspace = resolveActiveXitWorkspace();
      if (path.resolve(currentWorkspace) !== path.resolve(workspacePath)) {
        clearInterval(activeRunPoller);
        activeRunPoller = undefined;
        activeRunPollerWorkspace = undefined;
        return;
      }
    }

    const state = readCurrentRunState();
    if (!state) {
      return;
    }
    if (state.status === "completed" || state.status === "failed") {
      clearInterval(activeRunPoller);
      activeRunPoller = undefined;
      activeRunPollerWorkspace = undefined;
      const signature = getCurrentRunStateSignature();
      if (signature && signature !== currentRunStateSignature) {
        currentRunStateSignature = signature;
        const latestRun = getCompletedRunFromStateOrHistory();
        lastObservedRunSignature = getRunSignature(latestRun);
        const savedBytes = Math.max(
          0,
          (latestRun?.raw_bytes || 0) - (latestRun?.summary_bytes || 0),
        );
        enterSuccessPhase(savedBytes > 0, latestRun, workspacePath);
      }
      void updateStatusBar();
    } else if (state.status === "running" && isFreshRunningState()) {
      void updateStatusBarLive();
    }
  }, 500);
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
  if (heartbeatAt !== undefined) {
    return Date.now() - heartbeatAt <= 15000;
  }
  // Fallback for xit ≤0.2.43 which omits heartbeat_at / started_at:
  // treat the raw_log file's mtime as a proxy for liveness.
  if (state.raw_log) {
    try {
      const stats = fs.statSync(state.raw_log);
      return Date.now() - stats.mtimeMs <= 15000;
    } catch {
      // ignore
    }
  }
  return false;
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

  // Stuck safety net: auto-clear stale live states
  if (
    liveState === "turn_active" &&
    turnActiveStartedAt &&
    Date.now() - turnActiveStartedAt > TURN_ACTIVE_MAX_MS
  ) {
    setLiveState("idle");
  }
  if (
    liveState === "running" &&
    activeRunStartedAt &&
    Date.now() - activeRunStartedAt > RUNNING_MAX_MS
  ) {
    const currentState = readCurrentRunState();
    if (
      !currentState ||
      currentState.status !== "running" ||
      !isFreshRunningState()
    ) {
      setLiveState("idle");
    }
  }

  if (!status.available && status.state === "binary-not-found") {
    liveState = "no-binary";
    statusBarItem.text = "吸T神功 · 未接入";
    statusBarItem.tooltip = [
      "当前状态：未接入",
      "请安装本地 XiT CLI 以启用降噪功能",
      "─".repeat(20),
      "本地处理 · 不读取聊天内容 · 无遥测",
      "点击打开 XiT Dashboard",
    ].join("\n");
    updateDashboardIfOpen(status);
    return;
  }

  if (liveState === "turn_active" || liveState === "running") {
    // Safety net: file watchers may be registered against the wrong workspace path at
    // activation time (before any hook events arrive). On every periodic refresh, check
    // if the run actually completed and transition immediately if so.
    if (liveState === "running") {
      const currentState = readCurrentRunState();
      if (currentState?.status === "completed" || currentState?.status === "failed") {
        const latestRun = getCompletedRunFromStateOrHistory();
        const savedBytes = Math.max(
          0,
          (latestRun?.raw_bytes || 0) - (latestRun?.summary_bytes || 0),
        );
        enterSuccessPhase(savedBytes > 0, latestRun);
        updateDashboardIfOpen(status, buildLiveStatusOverride(resolveActiveXitWorkspace()));
        return;
      }
    }
    statusBarItem.text = "吸T神功 · 正在吸T中";
    statusBarItem.tooltip = [
      "当前状态：正在吸T中",
      "本次节省：—",
      "─".repeat(20),
      "本地处理 · 不读取聊天内容 · 无遥测",
      "点击打开 XiT Dashboard",
    ].join("\n");
    updateDashboardIfOpen(status, buildLiveStatusOverride(resolveActiveXitWorkspace()));
    return;
  }

  if (liveState === "settling") {
    statusBarItem.text = "吸T完成 · 神功正在收工";
    const metrics = getTokenMetricsForRun(latestRun);
    const reductionLabel = metrics && metrics.reductionPct > 0 ? `${Math.round(metrics.reductionPct)}%` : "--";
    const hitLift = metrics ? estimateHitRateLift(metrics.reductionPct, metrics.savedTokens) : 0;
    statusBarItem.tooltip = [
      "当前状态：吸T完成",
      successRunDisplay ? `本次节省：${successRunDisplay}` : "本次节省：—",
      `降噪率：${reductionLabel}`,
      hitLift > 0 ? `预计命中率提升：预计 +${hitLift}%` : "预计命中率提升：--",
      "─".repeat(20),
      "本地处理 · 不读取聊天内容 · 无遥测",
      "点击打开 XiT Dashboard",
    ].join("\n");
    updateDashboardIfOpen(status, buildLiveStatusOverride(resolveActiveXitWorkspace()));
    return;
  }

  if (liveState === "success") {
    const opt1 = successRunDisplay ? `吸T完成 · 本次省${successRunDisplay}` : "吸T完成";
    const opt2 = `吸T完成 · 本轮共吸 ${sessionRunCountAtSuccess}次`;
    statusBarItem.text = pickRotatingText([opt1, opt2]);
    const metrics = getTokenMetricsForRun(latestRun);
    const reductionLabel = metrics && metrics.reductionPct > 0 ? `${Math.round(metrics.reductionPct)}%` : "--";
    const hitLift = metrics ? estimateHitRateLift(metrics.reductionPct, metrics.savedTokens) : 0;
    statusBarItem.tooltip = [
      "当前状态：吸T完成",
      successRunDisplay ? `本次节省：${successRunDisplay}` : "本次节省：—",
      `降噪率：${reductionLabel}`,
      hitLift > 0 ? `预计命中率提升：预计 +${hitLift}%` : "预计命中率提升：--",
      "─".repeat(20),
      "本地处理 · 不读取聊天内容 · 无遥测",
      "点击打开 XiT Dashboard",
    ].join("\n");
    updateDashboardIfOpen(status, buildLiveStatusOverride(resolveActiveXitWorkspace()));
    return;
  }

  if (liveState === "missed") {
    statusBarItem.text = "吸T神功 · 无需发功";
    statusBarItem.tooltip = [
      "当前状态：无需发功",
      "本次节省：—",
      "─".repeat(20),
      "本地处理 · 不读取聊天内容 · 无遥测",
      "点击打开 XiT Dashboard",
    ].join("\n");
    updateDashboardIfOpen(status, buildLiveStatusOverride(resolveActiveXitWorkspace()));
    return;
  }

  if (liveState === "waiting") {
    statusBarItem.text = pickRotatingText(["吸T神功 · 等待下轮发功", "吸T神功 · 守护你的T"]);
    updateDashboardIfOpen(status);
    return;
  }

  const liveStatus = buildLiveStatusView();
  statusBarItem.text = getStatusBarTextFromLiveStatus(liveStatus);

  const metrics = getTokenMetricsForRun(latestRun);
  const savedDisplay = liveStatus.savedTokensDisplay || metrics?.savedDisplay;
  const reductionLabel = metrics && metrics.reductionPct > 0
    ? `${Math.round(metrics.reductionPct)}%`
    : "--";
  const hitLift = metrics ? estimateHitRateLift(metrics.reductionPct, metrics.savedTokens) : 0;
  const hitLiftLabel = hitLift > 0 ? `预计 +${hitLift}%` : "--";

  statusBarItem.tooltip = [
    `当前状态：${getLiveStatusLabel(liveStatus)}`,
    savedDisplay && metrics && metrics.savedTokens > 0
      ? `本次节省：${savedDisplay}`
      : "本次节省：—",
    `降噪率：${reductionLabel}`,
    `预计命中率提升：${hitLiftLabel}`,
    "─".repeat(20),
    "本地处理 · 不读取聊天内容 · 无遥测",
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
    statusBarItem.text = "吸T神功 · 未接入";
    return;
  }
  if (liveState === "turn_active" || liveState === "running") {
    statusBarItem.text = "吸T神功 · 正在吸T中";
    return;
  }
  if (liveState === "settling") {
    statusBarItem.text = "吸T完成 · 神功正在收工";
    return;
  }
  if (liveState === "success") {
    const opt1 = successRunDisplay ? `吸T完成 · 本次省${successRunDisplay}` : "吸T完成";
    const opt2 = `吸T完成 · 本轮共吸 ${sessionRunCountAtSuccess}次`;
    statusBarItem.text = pickRotatingText([opt1, opt2]);
    return;
  }
  if (liveState === "missed") {
    statusBarItem.text = "吸T神功 · 无需发功";
    return;
  }
  if (liveState === "waiting") {
    statusBarItem.text = pickRotatingText(["吸T神功 · 等待下轮发功", "吸T神功 · 守护你的T"]);
    return;
  }
  statusBarItem.text = getStatusBarTextFromLiveStatus(buildLiveStatusView());
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
  // Use resolveActiveXitWorkspace so watchers target the real xit project,
  // not just the VS Code window root which may differ from where xit auto runs.
  const workspacePath = resolveActiveXitWorkspace();

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
      enterSuccessPhase(savedBytes > 0, latestRun, workspacePath);
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
      markActiveRun(rawLogMeta?.mtimeMs || Date.now(), active, workspacePath);
      setLiveState("running", 0, workspacePath);
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
        workspacePath,
      );
      setLiveState("running", 0, workspacePath);
      startActiveRunPoller(workspacePath);
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
      enterSuccessPhase(savedBytes > 0, latestRun, workspacePath);
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

function resolveXiTHome(): string {
  const configured = vscode.workspace.getConfiguration("xit").get<string>("home", "");
  if (configured) {
    if (configured === "~") {
      return os.homedir();
    }
    if (configured.startsWith("~/")) {
      return path.join(os.homedir(), configured.slice(2));
    }
    return configured;
  }
  return path.join(os.homedir(), ".xit");
}

function eventBelongsToWorkspace(
  event: AdapterEvent,
  workspacePath: string,
): boolean {
  const resolvedWorkspace = path.resolve(workspacePath);

  if (event.cwd) {
    const resolvedCwd = path.resolve(event.cwd);
    if (
      resolvedCwd === resolvedWorkspace ||
      resolvedWorkspace.startsWith(resolvedCwd + path.sep)
    ) {
      return true;
    }
  }

  if (event.original_command) {
    const m = event.original_command.match(
      /(?:^|;|\s&&)\s*cd\s+([^\s;&|"'`\\]+)/,
    );
    if (m && m[1]) {
      const cdPath = m[1].startsWith("~/")
        ? path.join(os.homedir(), m[1].slice(2))
        : m[1] === "~"
          ? os.homedir()
          : m[1];
      const resolvedCd = path.resolve(cdPath);
      if (
        resolvedCd === resolvedWorkspace ||
        resolvedWorkspace.startsWith(resolvedCd + path.sep)
      ) {
        return true;
      }
    }
  }

  return false;
}

function getLatestHookCommand(adapter: string): string | undefined {
  const events = readRecentEvents(adapter, 3);
  return events[0]?.original_command;
}

function checkHookEventForXitAuto(workspacePath?: string): boolean {
  for (const adapter of ["claude", "codex", "cursor", "kimi"]) {
    const events = readRecentEvents(adapter, 10);
    for (const event of events) {
      if (workspacePath && !eventBelongsToWorkspace(event, workspacePath)) {
        continue;
      }
      if (/\bxit\s+auto\b/.test(event.original_command || "")) {
        return true;
      }
    }
  }
  return false;
}

function registerAdapterHookWatchers(context: vscode.ExtensionContext): void {
  const home = resolveXiTHome();
  const hookFiles = [
    path.join(home, "claude-hooks", "events.jsonl"),
    path.join(home, "codex-hooks", "events.jsonl"),
    path.join(home, "kimi-hooks", "events.jsonl"),
    path.join(home, "cursor-hooks", "events.jsonl"),
    path.join(home, "kimi-hooks", "turn-events.jsonl"),
  ];

  let pendingRefresh: NodeJS.Timeout | undefined;
  const scheduleRefresh = (): void => {
    if (pendingRefresh) {
      clearTimeout(pendingRefresh);
    }
    pendingRefresh = setTimeout(() => {
      pendingRefresh = undefined;
      const activeWorkspace = resolveActiveXitWorkspace();

      let hasWorkspaceEvent = false;
      let hasWorkspaceXitAuto = false;

      for (const adapter of ["claude", "codex", "cursor", "kimi"]) {
        const events = readRecentEvents(adapter, 10);
        for (const event of events) {
          if (eventBelongsToWorkspace(event, activeWorkspace)) {
            hasWorkspaceEvent = true;
            if (/\bxit\s+auto\b/.test(event.original_command || "")) {
              hasWorkspaceXitAuto = true;
              break;
            }
          }
        }
        if (hasWorkspaceXitAuto) {
          break;
        }
      }

      if (hasWorkspaceEvent) {
        enterTurnActive(activeWorkspace);
        if (hasWorkspaceXitAuto) {
          markActiveRun(Date.now(), undefined, activeWorkspace);
          setLiveState("running", 0, activeWorkspace);
          startActiveRunPoller(activeWorkspace);
        }
      }
      void updateStatusBar();
    }, 100);
  };

  for (const hookFile of hookFiles) {
    const watcher = vscode.workspace.createFileSystemWatcher(
      new vscode.RelativePattern(path.dirname(hookFile), path.basename(hookFile)),
    );
    watcher.onDidChange(scheduleRefresh, null, context.subscriptions);
    watcher.onDidCreate(scheduleRefresh, null, context.subscriptions);
    context.subscriptions.push(watcher);
  }

  context.subscriptions.push({
    dispose: () => {
      if (pendingRefresh) {
        clearTimeout(pendingRefresh);
        pendingRefresh = undefined;
      }
    },
  });
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
          const ws = cwd || resolveActiveXitWorkspace();
          markActiveRun(Date.now(), undefined, ws);
          setLiveState("running", 0, ws);
          startActiveRunPoller(ws);
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
    "Selected current turn:",
    `  adapter:   ${agentTurn.isFreshActive ? agentTurn.adapter : "none"}`,
    `  status:    ${agentTurn.isFreshActive ? agentTurn.status : "—"}`,
    `  source:    ${agentTurn.selectedTurnSource || "none"}`,
    `  freshness: ${agentTurn.isFreshActive ? "fresh" : "stale/none"}`,
    `  reason:    ${agentTurn.staleTurnReason || (agentTurn.isFreshActive ? "active lifecycle" : "no turn state")}`,
    "",
    "Selected latest activity:",
    `  adapter:   ${agentTurn.latestActivity?.adapter || "none"}`,
    `  timestamp: ${agentTurn.latestActivity?.timestamp || "none"}`,
    `  cwd:       ${agentTurn.latestActivity?.cwd || "(no cwd in event)"}`,
    `  command:   ${agentTurn.latestActivity?.command || agentTurn.latestActivity?.eventType || "none"}`,
    `  source:    ${agentTurn.selectedActivitySource || "none"}`,
    "",
    `Ignored stale turns: ${agentTurn.ignoredStaleTurns.length}`,
    ...agentTurn.ignoredStaleTurns.map(t =>
      `  adapter: ${t.adapter}  stopped_at: ${t.stoppedAt || "unknown"}  age: ${t.ageHours}h  reason: ${t.reason}`
    ),
    "",
    "Selected latest xit run:",
    `  command:     ${report.latestHistoryTimestamp ? latestRun?.command || "—" : "none"}`,
    `  completed_at:${report.latestHistoryTimestamp || "none"}`,
    `  saved:       ${report.latestSavedDisplay || "none"}`,
    `  source:      ${report.historyFileExists ? "history.jsonl" : "none"}`,
    "",
    "Current turn detail:",
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
  registerAdapterHookWatchers(context);
  registerTerminalListeners(context);

  context.subscriptions.push(
    vscode.commands.registerCommand("xit.openDashboard", async () => {
      const status = await fetchStatus();
      showDashboard(context, status, buildLiveStatusOverride(resolveActiveXitWorkspace()));
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
      const ws = resolveActiveXitWorkspace();
      setLiveState("running", 0, ws);
      await promptRunCommand();
    }),
    vscode.commands.registerCommand("xit.runWithAutoCompression", async () => {
      const ws = resolveActiveXitWorkspace();
      setLiveState("running", 0, ws);
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
