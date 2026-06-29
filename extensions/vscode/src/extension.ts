import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import * as vscode from "vscode";
import { showDashboard, updateDashboardIfOpen } from "./dashboard";
import {
  openXiTTerminal,
  promptRunCommand,
  promptRunWithAutoCompression,
  type VsCodeCommandRunRequest,
} from "./runner";
import { initializeHookCursor, readAppendedHookEvents, type HookFileCursor } from "./bridge-cursor";
import type {
  AdapterEvent,
  BridgeDiagnostics,
  CurrentRunState,
  LatestRun,
  LiveStatusView,
  VscodeAiBridgeEvent,
  XiTStatus,
} from "./types";
import {
  appendOutput,
  clearOutput,
  fetchStatus,
  openLatestRawLog,
  readCurrentRunState,
  readLatestRawLogMeta,
  readLatestRun,
  resolveActiveXitWorkspace,
  resolveWorkspaceCwd,
  showOutput,
  writeTerminalEvent,
} from "./xit";
import {
  buildAgentTurnView,
  buildVerifyRoutingReport,
  buildDiagnoseReport,
  getAdapterHookConnectivity,
  getTokenMetricsForRun,
  installWorkspaceAiRules,
} from "./workflow";
import {
  type ActiveVsCodeRun,
  bridgeEventIsFresh,
  bridgeEventToLiveStatus,
  bridgeWorkspaceHash,
  classifyBridgeEvent,
  formatSavedTokens,
  parseVscodeAiBridgeEvent,
  promoteSettlingToFinal,
  runRecordMatchesActiveTask,
} from "./logic";

let statusBarItem: vscode.StatusBarItem | undefined;
let refreshTimer: NodeJS.Timeout | undefined;
let liveState:
  | "idle"
  | "turn-active"
  | "running"
  | "settling"
  | "success"
  | "waiting"
  | "missed"
  | "failed"
  | "no-binary" = "idle";
let liveStateTimer: NodeJS.Timeout | undefined;
let waitingStateTimer: NodeJS.Timeout | undefined;
let lastObservedRunSignature: string | undefined;
let terminalListenerDisposable: vscode.Disposable | undefined;
let currentRunStateSignature: string | undefined;
let runningVisibleUntil: number | undefined;
let activeRunPoller: NodeJS.Timeout | undefined;
const RUNNING_MIN_VISIBLE_MS = 2500;
const SETTLING_DURATION_MS = 4000;

let successRunDisplay: string | undefined;
// Byte/reduction detail for the current VS Code session's local (non-bridge)
// run — carried on the live status override so Dashboard's "本次发功" never
// needs to fall back to .xit/history.jsonl for these numbers.
let lastLocalRunMetrics: { savedTokensDisplay?: string; reductionPct?: number; summaryBytes?: number } | undefined;
let liveStateWorkspace: string | undefined;
let activeRunPollerWorkspace: string | undefined;
let bridgeLiveStatus: LiveStatusView | undefined;
let bridgeLiveStatusWorkspace: string | undefined;
let activeBridgeRunId: string | undefined;
let bridgeActivatedAtMs = Date.now();
const RUNNING_MAX_MS = 120000;
const HOOK_EVENT_FRESH_MS = 30000;
const COMPLETED_RUN_FRESH_MS = 120000;
const RUN_START_SKEW_MS = 15000;

// ──────────────────────────────────────────────────────────────────
// BRIDGE TURN/SETTLING STATE — run.finished is "data ready", not "the AI is
// done talking". The Dashboard/status bar must show "收功中" (settling)
// after run.finished and only promote to the true final "本次省"/"执行失败"
// once a real turn.finished (Stop hook) event arrives — or, for an
// adapter/session where no turn-level signal was ever observed, a fallback
// timer. This is bridge-only: a LOCAL manual VS Code run (XiT: Run Command)
// has no AI "turn" at all, so its existing settling->success timer (above)
// is untouched.
// ──────────────────────────────────────────────────────────────────
let bridgeSettleTimer: NodeJS.Timeout | undefined;
let bridgeHoldTimer: NodeJS.Timeout | undefined;
// Real turn.finished/Stop always preempts this the instant it arrives —
// this is only how long we wait when no Stop hook fires at all (e.g. Claude
// today, where UserPromptSubmit/Stop are implemented but not installed by
// default; see docs/claude.md). Kept in the 6–8s range: long enough that a
// reasonably prompt Stop hook wins the race, short enough that the user
// isn't stuck looking at "收功中" for a noticeably long time.
const BRIDGE_SETTLE_FALLBACK_MS = 7000;
// How long the final "本次省"/"执行失败" result stays on the status bar
// before it reverts to "准备就绪". The Dashboard's "本轮发功" panel is NOT
// tied to this timer (see lastFinalizedBridgeResult below) — it keeps
// showing the final result well past this hold, until the next
// turn.started/run.started or a VS Code reload.
const BRIDGE_FINAL_RESULT_HOLD_MS = 20000;
// The Dashboard's own copy of the most recent finalized bridge result,
// decoupled from bridgeLiveStatus/liveState so it survives the status bar's
// hold timer expiring. Only cleared by a NEW turn.started/run.started (a
// genuinely new round starting) — never by the status bar reverting to
// idle, and never by a plain timeout.
let lastFinalizedBridgeResult: LiveStatusView | undefined;
let lastFinalizedBridgeResultWorkspace: string | undefined;

interface ActiveRunIdentity {
  workspace: string;
  detectedAt: number;
  startedAt?: number;
  command?: string;
  rawLog?: string;
}

let activeRunIdentity: ActiveRunIdentity | undefined;
let activeVsCodeRun: ActiveVsCodeRun | undefined;

// Hidden/internal diagnostics for the bridge watcher(s) — surfaced via "XiT:
// Diagnose AI Workflow", never via the status bar/Dashboard themselves. Only
// timestamps, counters, and already-hashed signals; see BridgeDiagnostics.
const bridgeDiagnostics: BridgeDiagnostics = {
  accepted_event_count: 0,
  dropped_event_count: 0,
  watcher_alive: false,
  event_file_path: [],
};

// The Go-side hook now dual-writes every bridge event into both the
// project-local file AND a global mirror file (see internal/vscodebridge
// MirrorHome()), so that an event survives even when the AI's cwd at the
// time differs from this workspace root. In the common case (AI never cd's
// elsewhere) BOTH files receive the very same event — this cap-bounded
// recent-keys set is what stops that dual-write from being processed twice.
const RECENT_BRIDGE_EVENT_KEY_CAP = 200;
const recentBridgeEventKeys: string[] = [];
const recentBridgeEventKeySet = new Set<string>();

function hasProcessedBridgeEvent(key: string): boolean {
  return recentBridgeEventKeySet.has(key);
}

function markBridgeEventProcessed(key: string): void {
  if (recentBridgeEventKeySet.has(key)) {
    return;
  }
  recentBridgeEventKeySet.add(key);
  recentBridgeEventKeys.push(key);
  if (recentBridgeEventKeys.length > RECENT_BRIDGE_EVENT_KEY_CAP) {
    const oldest = recentBridgeEventKeys.shift();
    if (oldest !== undefined) {
      recentBridgeEventKeySet.delete(oldest);
    }
  }
}

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

function ensureTildePrefix(display: string): string {
  // The "约" prefix from formatTokenCount already marks an approximate value, so
  // no extra "~" is added. Kept as a stable shim for existing call sites.
  return display;
}

function clearActiveRun(): void {
  activeRunIdentity = undefined;
  activeVsCodeRun = undefined;
  activeRunPollerWorkspace = undefined;
}

function beginVsCodeRun(request: VsCodeCommandRunRequest): void {
  const workspace = path.resolve(resolveActiveXitWorkspace());
  // New run starting — drop any previous session result so Dashboard
  // doesn't keep showing it once "running" takes over.
  lastLocalRunMetrics = undefined;
  activeVsCodeRun = {
    startedAt: Date.now(),
    originalCommand: request.originalCommand,
    normalizedCommand: request.finalCommand,
    terminalName: request.terminalName,
    mode: request.mode,
    state: request.mode === "auto" ? "running" : "finishing",
    workspacePath: workspace,
  };

  if (request.mode === "passthrough") {
    clearActiveRun();
    activeVsCodeRun = {
      startedAt: Date.now(),
      originalCommand: request.originalCommand,
      normalizedCommand: request.finalCommand,
      terminalName: request.terminalName,
      mode: "passthrough",
      state: "finishing",
      workspacePath: workspace,
    };
    lastLocalRunMetrics = { savedTokensDisplay: "0 Token", reductionPct: 0 };
    setLiveState("missed", 8000, workspace);
    return;
  }

  activeRunIdentity = {
    workspace,
    detectedAt: activeVsCodeRun.startedAt,
    command: request.originalCommand,
  };
  liveStateWorkspace = workspace;
  setLiveState("running", 0, workspace);
  startActiveRunPoller(workspace);
}

function enterSuccessPhase(hasSavings: boolean, latestRun?: LatestRun, workspace?: string): void {
  if (liveState === "settling" || liveState === "success") {
    return;
  }
  if (latestRun && latestRun.exit_code !== 0) {
    lastLocalRunMetrics = {
      savedTokensDisplay: "0 Token",
      reductionPct: 0,
      summaryBytes: latestRun.summary_bytes,
    };
    setLiveState("failed", 8000, workspace);
    return;
  }
  if (!hasSavings) {
    lastLocalRunMetrics = {
      savedTokensDisplay: "0 Token",
      reductionPct: 0,
      summaryBytes: latestRun?.summary_bytes,
    };
    setLiveState("missed", 8000, workspace);
    return;
  }

  const metrics = getTokenMetricsForRun(latestRun);
  lastLocalRunMetrics = {
    savedTokensDisplay: metrics?.savedDisplay,
    reductionPct: metrics?.reductionPct,
    summaryBytes: latestRun?.summary_bytes,
  };
  const rawDisplay = metrics?.savedDisplay;
  successRunDisplay = rawDisplay ? ensureTildePrefix(rawDisplay) : undefined;

  if (activeRunPoller) {
    clearInterval(activeRunPoller);
    activeRunPoller = undefined;
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
  if (
    bridgeLiveStatus &&
    activeWorkspace &&
    bridgeLiveStatusWorkspace &&
    path.resolve(bridgeLiveStatusWorkspace) === path.resolve(activeWorkspace)
  ) {
    return bridgeLiveStatus;
  }
  // The status bar's hold timer may have already reverted liveState to
  // "idle" (back to "准备就绪"), but the Dashboard's "本轮发功" panel keeps
  // showing the last finalized result until a new round actually starts —
  // see clearLastFinalizedBridgeResult's call sites.
  if (
    lastFinalizedBridgeResult &&
    activeWorkspace &&
    lastFinalizedBridgeResultWorkspace &&
    path.resolve(lastFinalizedBridgeResultWorkspace) === path.resolve(activeWorkspace)
  ) {
    return lastFinalizedBridgeResult;
  }
  switch (liveState) {
    case "running":
      return {
        kind: "xit_running",
        label: "正在吸T",
        reason: "xit auto running",
        source: "liveState",
      };
    case "settling":
    case "success":
      return {
        kind: "xit_completed",
        label: "本次省",
        savedTokensDisplay: successRunDisplay,
        reductionPct: lastLocalRunMetrics?.reductionPct,
        summaryBytes: lastLocalRunMetrics?.summaryBytes,
        exitCode: 0,
        reason: liveState === "settling" ? "post-run settling" : "success hold",
        source: "liveState",
      };
    case "missed":
      return {
        kind: "agent_not_routed",
        label: "本次无需发功",
        savedTokensDisplay: lastLocalRunMetrics?.savedTokensDisplay,
        reductionPct: lastLocalRunMetrics?.reductionPct,
        summaryBytes: lastLocalRunMetrics?.summaryBytes,
        reason: "current VS Code command did not need compression",
        source: "liveState",
      };
    case "failed":
      return {
        kind: "agent_not_routed",
        label: "执行失败",
        savedTokensDisplay: lastLocalRunMetrics?.savedTokensDisplay,
        reductionPct: lastLocalRunMetrics?.reductionPct,
        summaryBytes: lastLocalRunMetrics?.summaryBytes,
        exitCode: 1,
        reason: "current VS Code command failed",
        source: "liveState",
      };
    default:
      return undefined;
  }
}

function clearBridgeTimers(): void {
  if (bridgeSettleTimer) {
    clearTimeout(bridgeSettleTimer);
    bridgeSettleTimer = undefined;
  }
  if (bridgeHoldTimer) {
    clearTimeout(bridgeHoldTimer);
    bridgeHoldTimer = undefined;
  }
  // Defensive: liveState/liveStateTimer/waitingStateTimer are shared with
  // the LOCAL (non-bridge, manual VS Code run) state machine. If a local
  // run's settling/waiting timer is still pending when a bridge event
  // arrives, it must never fire later and stomp over the bridge's state.
  if (liveStateTimer) {
    clearTimeout(liveStateTimer);
    liveStateTimer = undefined;
  }
  if (waitingStateTimer) {
    clearTimeout(waitingStateTimer);
    waitingStateTimer = undefined;
  }
}

function clearBridgeLiveStatus(): void {
  clearBridgeTimers();
  bridgeLiveStatus = undefined;
  bridgeLiveStatusWorkspace = undefined;
  activeBridgeRunId = undefined;
}

// A genuinely new round is starting (new turn, or a new tool run for an
// adapter with no turn-tracking) — the Dashboard's persisted final result
// from the PREVIOUS round is now stale and must be dropped.
function clearLastFinalizedBridgeResult(): void {
  lastFinalizedBridgeResult = undefined;
  lastFinalizedBridgeResultWorkspace = undefined;
}

// turn.started: the AI is thinking about a NEW prompt. Always overrides
// immediately (clearing any still-visible result from a previous turn —
// once a new turn has begun, the old turn's result is stale).
function setBridgeTurnActive(event: VscodeAiBridgeEvent, workspacePath: string): void {
  clearBridgeTimers();
  activeBridgeRunId = undefined;
  clearLastFinalizedBridgeResult();
  bridgeLiveStatus = bridgeEventToLiveStatus(event);
  bridgeLiveStatusWorkspace = workspacePath;
  liveState = "turn-active";
  liveStateWorkspace = workspacePath;
  successRunDisplay = undefined;
  void updateStatusBarLive();
}

function setBridgeRunning(event: VscodeAiBridgeEvent, workspacePath: string): void {
  clearBridgeTimers();
  activeBridgeRunId = event.run_id;
  clearLastFinalizedBridgeResult();
  bridgeLiveStatus = bridgeEventToLiveStatus(event);
  bridgeLiveStatusWorkspace = workspacePath;
  liveState = "running";
  liveStateWorkspace = workspacePath;
  successRunDisplay = undefined;
  runningVisibleUntil = Date.now() + RUNNING_MIN_VISIBLE_MS;
  void updateStatusBarLive();
}

// run.finished: the tool call is done and its data is ready, but this is
// NOT "the turn is over" — the AI hasn't produced its final answer yet.
// Show "神功正在收工" immediately (status bar) / "收功中" (Dashboard),
// holding the real result data WITHOUT rendering it, and arm a fallback in
// case no real turn.finished ever arrives.
function setBridgeSettling(event: VscodeAiBridgeEvent, workspacePath: string): void {
  if (activeBridgeRunId && activeBridgeRunId !== event.run_id) {
    return;
  }
  clearBridgeTimers();
  activeBridgeRunId = event.run_id;
  bridgeLiveStatus = bridgeEventToLiveStatus(event);
  bridgeLiveStatusWorkspace = workspacePath;
  successRunDisplay = event.exit_code === 0
    ? formatSavedTokens(event.saved_tokens, event.saved_bytes)
    : undefined;
  liveState = "settling";
  liveStateWorkspace = workspacePath;
  runningVisibleUntil = undefined;
  void updateStatusBarLive();

  bridgeSettleTimer = setTimeout(() => {
    bridgeSettleTimer = undefined;
    finalizeBridgeTurn(workspacePath);
  }, BRIDGE_SETTLE_FALLBACK_MS);
}

// Promotes a held "收功中" (xit_settling) result to its true final display
// ("本次省"/"执行失败"). The status bar holds this for BRIDGE_FINAL_RESULT_
// HOLD_MS before reverting to "准备就绪" — but the Dashboard's copy
// (lastFinalizedBridgeResult) is saved separately and is NOT cleared by that
// timer; it stays until the next turn.started/run.started or a reload.
// Called by either a real turn.finished event or the fallback timer armed
// in setBridgeSettling — both paths converge here so the visible behavior
// is identical either way.
function finalizeBridgeTurn(workspacePath: string): void {
  if (bridgeSettleTimer) {
    clearTimeout(bridgeSettleTimer);
    bridgeSettleTimer = undefined;
  }
  if (
    !bridgeLiveStatus ||
    !bridgeLiveStatusWorkspace ||
    path.resolve(bridgeLiveStatusWorkspace) !== path.resolve(workspacePath)
  ) {
    return;
  }
  if (bridgeLiveStatus.kind === "xit_turn_active") {
    // The turn ended without ever running a tool — nothing to show.
    clearBridgeLiveStatus();
    liveState = "idle";
    liveStateWorkspace = undefined;
    void updateStatusBar();
    return;
  }
  if (bridgeLiveStatus.kind !== "xit_settling") {
    return;
  }
  bridgeLiveStatus = promoteSettlingToFinal(bridgeLiveStatus);
  lastFinalizedBridgeResult = bridgeLiveStatus;
  lastFinalizedBridgeResultWorkspace = workspacePath;
  liveState = bridgeLiveStatus.exitCode === 0 ? "success" : "failed";
  liveStateWorkspace = workspacePath;
  void updateStatusBarLive();
  bridgeHoldTimer = setTimeout(() => {
    bridgeHoldTimer = undefined;
    clearBridgeLiveStatus();
    liveState = "idle";
    liveStateWorkspace = undefined;
    void updateStatusBar();
  }, BRIDGE_FINAL_RESULT_HOLD_MS);
}

// turn.finished: the AI's final answer for this turn is done (Codex Stop
// hook confirmed the footer, or gave up after loop-prevention fail-open —
// either way the turn is genuinely over from XiT's perspective).
function setBridgeTurnFinished(_event: VscodeAiBridgeEvent, workspacePath: string): void {
  finalizeBridgeTurn(workspacePath);
}

function parseIsoTimeMs(iso: string | undefined): number | undefined {
  if (!iso) {
    return undefined;
  }
  const ms = Date.parse(iso);
  return Number.isNaN(ms) ? undefined : ms;
}

export function runStateMatchesIdentity(
  state: CurrentRunState | undefined,
  identity: ActiveRunIdentity | undefined,
  workspacePath: string,
  now = Date.now(),
): boolean {
  if (
    !state ||
    (state.status !== "completed" && state.status !== "failed") ||
    !identity ||
    path.resolve(workspacePath) !== identity.workspace
  ) {
    return false;
  }

  const completedAt = parseIsoTimeMs(state.completed_at || state.finished_at);
  const startedAt = parseIsoTimeMs(state.started_at);
  if (
    completedAt === undefined ||
    now - completedAt > COMPLETED_RUN_FRESH_MS ||
    completedAt < identity.detectedAt - RUN_START_SKEW_MS
  ) {
    return false;
  }
  if (
    startedAt !== undefined &&
    startedAt < identity.detectedAt - RUN_START_SKEW_MS
  ) {
    return false;
  }
  if (
    identity.startedAt !== undefined &&
    startedAt !== identity.startedAt
  ) {
    return false;
  }
  if (
    identity.rawLog &&
    (!state.raw_log || path.resolve(state.raw_log) !== identity.rawLog)
  ) {
    return false;
  }
  if (
    activeVsCodeRun &&
    !runRecordMatchesActiveTask(state, activeVsCodeRun, now, COMPLETED_RUN_FRESH_MS)
  ) {
    return false;
  }
  if (
    !activeVsCodeRun &&
    identity.command &&
    state.command &&
    state.command !== identity.command
  ) {
    return false;
  }
  return true;
}

function currentRunMatchesActiveIdentity(
  state: CurrentRunState | undefined,
  workspacePath: string,
): boolean {
  return runStateMatchesIdentity(
    state,
    activeRunIdentity,
    workspacePath,
  );
}

function historyRunMatchesActiveIdentity(
  run: LatestRun | undefined,
  workspacePath: string,
): boolean {
  if (
    !run ||
    !activeRunIdentity ||
    path.resolve(workspacePath) !== activeRunIdentity.workspace
  ) {
    return false;
  }
  const completedAt = parseIsoTimeMs(run.timestamp);
  if (
    completedAt === undefined ||
    Date.now() - completedAt > COMPLETED_RUN_FRESH_MS ||
    completedAt < activeRunIdentity.detectedAt - RUN_START_SKEW_MS
  ) {
    return false;
  }
  if (
    activeRunIdentity.rawLog &&
    path.resolve(run.raw_log) !== activeRunIdentity.rawLog
  ) {
    return false;
  }
  if (
    activeVsCodeRun &&
    !runRecordMatchesActiveTask(run, activeVsCodeRun, Date.now(), COMPLETED_RUN_FRESH_MS)
  ) {
    return false;
  }
  if (
    !activeVsCodeRun &&
    activeRunIdentity.command &&
    run.command &&
    run.command !== activeRunIdentity.command
  ) {
    return false;
  }
  return true;
}

function liveStateBelongsToWorkspace(workspacePath: string): boolean {
  return !!(
    liveStateWorkspace &&
    path.resolve(liveStateWorkspace) === path.resolve(workspacePath)
  );
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
  if (state === "success" || state === "settling" || state === "missed" || state === "failed" || state === "idle" || state === "waiting") {
    if (activeRunPoller) {
      clearInterval(activeRunPoller);
      activeRunPoller = undefined;
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
      if (state === "success" || state === "missed" || state === "failed") {
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

function startActiveRunPoller(workspacePath = resolveActiveXitWorkspace()): void {
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
      appendOutput(
        `VS Code run did not match a fresh XiT state/history record: ${activeVsCodeRun?.originalCommand || "unknown command"}`,
      );
      clearActiveRun();
      setLiveState("idle");
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

    const state = readCurrentRunState(workspacePath);
    if (!state) {
      return;
    }
    if (
      (state.status === "completed" || state.status === "failed") &&
      currentRunMatchesActiveIdentity(state, workspacePath)
    ) {
      clearInterval(activeRunPoller);
      activeRunPoller = undefined;
      activeRunPollerWorkspace = undefined;
      const signature = getCurrentRunStateSignature(workspacePath);
      currentRunStateSignature = signature;
      const latestRun = getCompletedRunFromStateOrHistory(workspacePath);
      lastObservedRunSignature = getRunSignature(latestRun);
      const savedBytes = Math.max(
        0,
        (latestRun?.raw_bytes || 0) - (latestRun?.summary_bytes || 0),
      );
      enterSuccessPhase(savedBytes > 0, latestRun, workspacePath);
      void updateStatusBar();
    } else if (state.status === "completed" || state.status === "failed") {
      clearInterval(activeRunPoller);
      activeRunPoller = undefined;
      appendOutput(
        `Ignored XiT state that did not match the active VS Code run: ${state.command || "unknown command"}`,
      );
      clearActiveRun();
      setLiveState("idle");
    } else if (state.status === "running" && isFreshRunningState(workspacePath)) {
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

function getCurrentRunStateSignature(
  workspacePath = resolveActiveXitWorkspace(),
): string | undefined {
  const state = readCurrentRunState(workspacePath);
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

function isFreshRunningState(
  workspacePath = resolveActiveXitWorkspace(),
): boolean {
  const state = readCurrentRunState(workspacePath);
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

function getCompletedRunFromStateOrHistory(
  workspacePath = resolveActiveXitWorkspace(),
): LatestRun | undefined {
  const state = readCurrentRunState(workspacePath);
  const latestRun = readLatestRun(workspacePath);
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
    estimated_reduction:
      state.estimated_reduction ?? latestRun?.estimated_reduction ?? 0,
    duration_ms: latestRun?.duration_ms ?? 0,
    filter: latestRun?.filter ?? "auto",
    confidence: latestRun?.confidence ?? "high",
    policy: latestRun?.policy ?? "should_compress",
    raw_log: state.raw_log || latestRun?.raw_log || "",
  };
}

function detectActiveRawLog(
  workspacePath = resolveActiveXitWorkspace(),
): string | undefined {
  const latestRawLog = readLatestRawLogMeta(workspacePath);
  if (!latestRawLog) {
    return undefined;
  }

  const latestRun = readLatestRun(workspacePath);
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
      path.join(workspacePath, ".xit", "history.jsonl"),
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
  // Defensive: nothing in this codebase should hide the status bar item
  // once created (other than the user disabling xit.enableStatusBar), but a
  // stuck-looking "准备就绪" has repeatedly turned out to be a status bar
  // that silently lost its visible state. Re-asserting .show() on every
  // update is cheap and rules that class of bug out entirely.
  if (isEnabled()) {
    statusBarItem.show();
  }

  const workspaceSnapshot = resolveActiveXitWorkspace();
  const status = await fetchStatus(workspaceSnapshot);
  const latestRun = getCompletedRunFromStateOrHistory(workspaceSnapshot);

  // Stuck safety net: auto-clear stale live states
  if (
    liveState === "running" &&
    activeRunIdentity &&
    Date.now() - activeRunIdentity.detectedAt > RUNNING_MAX_MS
  ) {
    const currentState = readCurrentRunState(activeRunIdentity.workspace);
    if (
      !currentState ||
      currentState.status !== "running" ||
      !isFreshRunningState(activeRunIdentity.workspace)
    ) {
      clearActiveRun();
      setLiveState("idle");
    }
  }

  if (!status.available && status.state === "binary-not-found") {
    liveState = "no-binary";
    liveStateWorkspace = workspaceSnapshot;
    statusBarItem.text = "吸T神功 · 未接入";
    statusBarItem.tooltip = [
      "当前状态：未接入",
      "请安装本地 XiT CLI 以启用降噪功能",
      "─".repeat(20),
      "本地处理 · 不读取聊天内容 · 无遥测",
      "点击打开 XiT Dashboard",
    ].join("\n");
    updateDashboardIfOpen(status, undefined, workspaceSnapshot);
    return;
  }

  const useLiveState = liveStateBelongsToWorkspace(workspaceSnapshot);

  if (useLiveState && liveState === "running") {
    // Safety net: file watchers may be registered against the wrong workspace path at
    // activation time (before any hook events arrive). On every periodic refresh, check
    // if the run actually completed and transition immediately if so.
    if (liveState === "running" && activeRunIdentity) {
      const currentState = readCurrentRunState(workspaceSnapshot);
      if (currentRunMatchesActiveIdentity(currentState, workspaceSnapshot)) {
        const latestRun = getCompletedRunFromStateOrHistory(workspaceSnapshot);
        const savedBytes = Math.max(
          0,
          (latestRun?.raw_bytes || 0) - (latestRun?.summary_bytes || 0),
        );
        enterSuccessPhase(savedBytes > 0, latestRun, workspaceSnapshot);
        updateDashboardIfOpen(
          status,
          buildLiveStatusOverride(workspaceSnapshot),
          workspaceSnapshot,
        );
        return;
      } else if (
        liveState === "running" &&
        currentState &&
        (currentState.status === "completed" || currentState.status === "failed")
      ) {
        clearActiveRun();
        setLiveState("idle");
        await updateStatusBar();
        return;
      }
    }
    statusBarItem.text = liveState === "running"
      ? "吸T神功 · 正在吸T"
      : "吸T神功 · 准备就绪";
    statusBarItem.tooltip = [
      "当前状态：正在吸T",
      "本次节省：—",
      "─".repeat(20),
      "本地处理 · 不读取聊天内容 · 无遥测",
      "点击打开 XiT Dashboard",
    ].join("\n");
    updateDashboardIfOpen(
      status,
      buildLiveStatusOverride(workspaceSnapshot),
      workspaceSnapshot,
    );
    return;
  }

  if (useLiveState && liveState === "settling") {
    statusBarItem.text = "吸T神功 · 神功正在收工";
    const metrics = getTokenMetricsForRun(latestRun);
    const reductionLabel = metrics && metrics.reductionPct > 0 ? `${Math.round(metrics.reductionPct)}%` : "--";
    statusBarItem.tooltip = [
      "当前状态：神功正在收工",
      successRunDisplay ? `本次节省：${successRunDisplay}` : "本次节省：—",
      `降噪率：${reductionLabel}`,
      "─".repeat(20),
      "本地处理 · 不读取聊天内容 · 无遥测",
      "点击打开 XiT Dashboard",
    ].join("\n");
    updateDashboardIfOpen(
      status,
      buildLiveStatusOverride(workspaceSnapshot),
      workspaceSnapshot,
    );
    return;
  }

  if (useLiveState && liveState === "success") {
    statusBarItem.text = successRunDisplay ? `吸T神功 · 本次省 ${successRunDisplay}` : "吸T神功 · 准备就绪";
    const metrics = getTokenMetricsForRun(latestRun);
    const reductionLabel = metrics && metrics.reductionPct > 0 ? `${Math.round(metrics.reductionPct)}%` : "--";
    statusBarItem.tooltip = [
      "当前状态：本次省",
      successRunDisplay ? `本次节省：${successRunDisplay}` : "本次节省：—",
      `降噪率：${reductionLabel}`,
      "─".repeat(20),
      "本地处理 · 不读取聊天内容 · 无遥测",
      "点击打开 XiT Dashboard",
    ].join("\n");
    updateDashboardIfOpen(
      status,
      buildLiveStatusOverride(workspaceSnapshot),
      workspaceSnapshot,
    );
    return;
  }

  if (useLiveState && liveState === "missed") {
    statusBarItem.text = "吸T神功 · 本次无需发功";
    statusBarItem.tooltip = [
      "当前状态：本次无需发功",
      "本次节省：—",
      "─".repeat(20),
      "本地处理 · 不读取聊天内容 · 无遥测",
      "点击打开 XiT Dashboard",
    ].join("\n");
    updateDashboardIfOpen(
      status,
      buildLiveStatusOverride(workspaceSnapshot),
      workspaceSnapshot,
    );
    return;
  }

  if (useLiveState && liveState === "failed") {
    statusBarItem.text = "吸T神功 · 执行失败";
    statusBarItem.tooltip = [
      "当前状态：执行失败",
      "本次节省：—",
      "详情请打开 Output Channel 或最新 Raw Log",
      "─".repeat(20),
      "本地处理 · 不读取聊天内容 · 无遥测",
      "点击打开 XiT Dashboard",
    ].join("\n");
    updateDashboardIfOpen(
      status,
      buildLiveStatusOverride(workspaceSnapshot),
      workspaceSnapshot,
    );
    return;
  }

  if (useLiveState && liveState === "waiting") {
    statusBarItem.text = "吸T神功 · 准备就绪";
    updateDashboardIfOpen(status, undefined, workspaceSnapshot);
    return;
  }

  const metrics = getTokenMetricsForRun(latestRun);
  const reductionLabel = metrics && metrics.reductionPct > 0
    ? `${Math.round(metrics.reductionPct)}%`
    : "--";
  statusBarItem.text = "吸T神功 · 准备就绪";

  statusBarItem.tooltip = [
    "当前状态：准备就绪",
    "本次节省：—",
    `降噪率：${reductionLabel}`,
    "─".repeat(20),
    "本地处理 · 不读取聊天内容 · 无遥测",
    "点击打开 XiT Dashboard",
  ]
    .filter(Boolean)
    .join("\n");

  updateDashboardIfOpen(status, undefined, workspaceSnapshot);
}

async function updateStatusBarLive(): Promise<void> {
  if (!statusBarItem) {
    return;
  }
  if (isEnabled()) {
    statusBarItem.show();
  }

  const workspaceSnapshot = resolveActiveXitWorkspace();
  if (!liveStateBelongsToWorkspace(workspaceSnapshot)) {
    statusBarItem.text = "吸T神功 · 准备就绪";
    return;
  }
  if (liveState === "no-binary") {
    statusBarItem.text = "吸T神功 · 未接入";
    return;
  }
  if (liveState === "turn-active") {
    statusBarItem.text = "吸T神功 · 正在守护";
    return;
  }
  if (liveState === "running") {
    statusBarItem.text = liveState === "running"
      ? "吸T神功 · 正在吸T"
      : "吸T神功 · 准备就绪";
    return;
  }
  if (liveState === "settling") {
    statusBarItem.text = "吸T神功 · 神功正在收工";
    return;
  }
  if (liveState === "success") {
    statusBarItem.text = successRunDisplay ? `吸T神功 · 本次省 ${successRunDisplay}` : "吸T神功 · 准备就绪";
    return;
  }
  if (liveState === "missed") {
    statusBarItem.text = "吸T神功 · 本次无需发功";
    return;
  }
  if (liveState === "failed") {
    statusBarItem.text = "吸T神功 · 执行失败";
    return;
  }
  if (liveState === "waiting") {
    statusBarItem.text = "吸T神功 · 准备就绪";
    return;
  }
  statusBarItem.text = "吸T神功 · 准备就绪";
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
    const latestRun = readLatestRun(workspacePath);
    const signature = getRunSignature(latestRun);
    if (
      signature &&
      signature !== lastObservedRunSignature &&
      historyRunMatchesActiveIdentity(latestRun, workspacePath)
    ) {
      lastObservedRunSignature = signature;
      const savedBytes = Math.max(
        0,
        (latestRun?.raw_bytes || 0) - (latestRun?.summary_bytes || 0),
      );
      enterSuccessPhase(savedBytes > 0, latestRun, workspacePath);
    }
    if (statusBarItem) {
      const status = await fetchStatus(workspacePath);
      updateDashboardIfOpen(status, undefined, workspacePath);
    }
    await updateStatusBar();
  };

  const onRawLogChange = async (): Promise<void> => {
    const active = detectActiveRawLog(workspacePath);
    if (
      active &&
      activeVsCodeRun?.mode === "auto" &&
      activeVsCodeRun.workspacePath &&
      path.resolve(activeVsCodeRun.workspacePath) === path.resolve(workspacePath)
    ) {
      if (activeRunIdentity) {
        activeRunIdentity.rawLog = path.resolve(active);
      }
      setLiveState("running", 0, workspacePath);
      startActiveRunPoller(workspacePath);
    }
    await updateStatusBar();
  };

  const onStateChange = async (): Promise<void> => {
    const state = readCurrentRunState(workspacePath);
    const signature = getCurrentRunStateSignature(workspacePath);
    const hasActiveVsCodeRun = !!(
      activeVsCodeRun?.mode === "auto" &&
      activeVsCodeRun.workspacePath &&
      path.resolve(activeVsCodeRun.workspacePath) === path.resolve(workspacePath)
    );
    if (hasActiveVsCodeRun && state?.status === "running" && isFreshRunningState(workspacePath)) {
      if (activeRunIdentity) {
        activeRunIdentity.startedAt = parseIsoTimeMs(state.started_at);
        activeRunIdentity.rawLog = state.raw_log ? path.resolve(state.raw_log) : activeRunIdentity.rawLog;
      }
      setLiveState("running", 0, workspacePath);
      startActiveRunPoller(workspacePath);
    } else if (
      hasActiveVsCodeRun &&
      state &&
      (state.status === "completed" || state.status === "failed") &&
      currentRunMatchesActiveIdentity(state, workspacePath) &&
      signature &&
      signature !== currentRunStateSignature
    ) {
      currentRunStateSignature = signature;
      const latestRun = getCompletedRunFromStateOrHistory(workspacePath);
      lastObservedRunSignature = getRunSignature(latestRun);
      const savedBytes = Math.max(
        0,
        (latestRun?.raw_bytes || 0) - (latestRun?.summary_bytes || 0),
      );
      enterSuccessPhase(savedBytes > 0, latestRun, workspacePath);
    }
    if (statusBarItem) {
      const status = await fetchStatus(workspacePath);
      updateDashboardIfOpen(status, undefined, workspacePath);
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

// Truncates a hex hash to its first 8 characters for diagnostics — never log
// full workspace paths, PIDs, or thread IDs in the Output Channel.
function shortHash(value: string | undefined): string {
  return value ? value.slice(0, 8) : "none";
}

function logBridgeDiagnostic(message: string): void {
  appendOutput(`[XiT Bridge] ${message}`);
}

// Mirrors internal/vscodebridge.MirrorHome() on the Go side: same env var,
// same default. The two sides must resolve to the same filesystem path for
// the mirror watcher (below) to ever observe what the Go-side hook wrote —
// this is plain os.homedir()/.xit in the overwhelming majority of setups,
// since both the VS Code extension host and the AI CLI's hook subprocess
// run as the same OS user on the same machine.
function resolveBridgeMirrorHome(): string {
  const configured = process.env.XIT_VSCODE_BRIDGE_HOME;
  if (configured) {
    return configured;
  }
  return path.join(os.homedir(), ".xit");
}

interface BridgeEventSource {
  label: "primary" | "mirror";
  bridgeFile: string;
  watcher: vscode.FileSystemWatcher;
  cursor: HookFileCursor;
}

function registerVscodeAiBridgeWatcher(context: vscode.ExtensionContext): void {
  const workspacePath = resolveActiveXitWorkspace();
  const primaryFile = path.join(workspacePath, ".xit", "events", "vscode-ai-bridge.jsonl");
  const mirrorHome = resolveBridgeMirrorHome();
  const mirrorFile = path.join(mirrorHome, "events", "vscode-ai-bridge.jsonl");

  // Base each watcher on a directory that always exists (the workspace root,
  // or the user's home directory), not on .xit/events itself (which may not
  // exist yet on a fresh workspace/machine — a watcher rooted at a
  // non-existent directory may never attach and silently miss the file's
  // later creation).
  const sources: BridgeEventSource[] = [
    {
      label: "primary",
      bridgeFile: primaryFile,
      cursor: initializeHookCursor(primaryFile),
      watcher: vscode.workspace.createFileSystemWatcher(
        new vscode.RelativePattern(workspacePath, ".xit/events/vscode-ai-bridge.jsonl"),
      ),
    },
  ];

  // The mirror file is everything the primary watcher can't see by
  // construction: when the AI's cwd at the time of a command differs from
  // this workspace root (e.g. it cd'd into a different project mid-session),
  // the Go-side hook's *primary* write lands in that OTHER project's .xit —
  // but its dual-write always also lands here, in the global mirror.
  if (path.resolve(mirrorFile) !== path.resolve(primaryFile)) {
    // mirrorHome (typically ~/.xit) may not exist yet on a fresh machine —
    // root the watcher at the home directory itself (which always exists)
    // rather than mirrorHome, the same reasoning as the primary watcher
    // above. Only falls back to mirrorHome directly for the advanced/test
    // case where XIT_VSCODE_BRIDGE_HOME points outside the home directory.
    const homeDir = os.homedir();
    const relativeToHome = path.relative(homeDir, mirrorHome);
    const mirrorWatchBase =
      relativeToHome === "" ||
      (!relativeToHome.startsWith(`..${path.sep}`) && relativeToHome !== ".." && !path.isAbsolute(relativeToHome))
        ? homeDir
        : mirrorHome;
    const mirrorWatchPattern = path.relative(mirrorWatchBase, mirrorFile);
    sources.push({
      label: "mirror",
      bridgeFile: mirrorFile,
      cursor: initializeHookCursor(mirrorFile),
      watcher: vscode.workspace.createFileSystemWatcher(
        new vscode.RelativePattern(mirrorWatchBase, mirrorWatchPattern),
      ),
    });
  }

  bridgeDiagnostics.event_file_path = sources.map((s) => s.bridgeFile);
  bridgeDiagnostics.watcher_alive = true;

  const handleBridgeChange = async (source: BridgeEventSource): Promise<void> => {
    const appended = readAppendedHookEvents(source.bridgeFile, source.cursor) as unknown[];
    let changed = false;
    for (const raw of appended) {
      const event = parseVscodeAiBridgeEvent(JSON.stringify(raw));
      if (!event) {
        continue;
      }
      const kind = event.event; // "turn.started" | "run.started" | "run.finished" | "turn.finished"
      const nowIso = new Date().toISOString();
      bridgeDiagnostics.last_event_at = nowIso;
      bridgeDiagnostics.last_event_workspace_hash = event.workspace_hash;
      bridgeDiagnostics.last_event_host_instance_hash = event.host_instance_hash;
      bridgeDiagnostics.last_event_source = source.label;
      bridgeDiagnostics.current_workspace_hash = bridgeWorkspaceHash(workspacePath);

      // The Go-side dual-write means the same logical event can arrive via
      // both sources; without this, every run would be processed twice.
      const dedupeKey = `${event.event}|${event.run_id}`;
      if (hasProcessedBridgeEvent(dedupeKey)) {
        continue;
      }
      markBridgeEventProcessed(dedupeKey);

      logBridgeDiagnostic(
        `event received: ${kind} run=${shortHash(event.run_id)} ws=${shortHash(event.workspace_hash)} host=${shortHash(event.host_instance_hash)} source=${source.label}`,
      );
      if (!bridgeEventIsFresh(event, bridgeActivatedAtMs)) {
        bridgeDiagnostics.dropped_event_count++;
        bridgeDiagnostics.last_dropped_event_at = nowIso;
        bridgeDiagnostics.last_drop_reason = "stale_or_pre_activation";
        logBridgeDiagnostic(`ignored: event predates this VS Code session (stale or pre-activation) run=${shortHash(event.run_id)}`);
        continue;
      }
      const decision = classifyBridgeEvent(event, workspacePath);
      if (!decision.accepted) {
        bridgeDiagnostics.dropped_event_count++;
        bridgeDiagnostics.last_dropped_event_at = nowIso;
        bridgeDiagnostics.last_drop_reason = decision.reason;
        logBridgeDiagnostic(
          `ignored: ${decision.reason} event=${shortHash(event.workspace_hash)} current=${shortHash(bridgeWorkspaceHash(workspacePath))}`,
        );
        continue;
      }
      bridgeDiagnostics.accepted_event_count++;
      bridgeDiagnostics.last_accepted_event_at = nowIso;
      if (decision.crossWorkspace) {
        logBridgeDiagnostic(`accepted: cross-workspace (host matches, workspace differs) run=${shortHash(event.run_id)} source=${source.label}`);
      } else if (decision.soft) {
        logBridgeDiagnostic(`soft accepted: host_instance_hash mismatch run=${shortHash(event.run_id)}`);
      } else {
        logBridgeDiagnostic(`accepted: ${kind} run=${shortHash(event.run_id)}`);
      }
      switch (event.event) {
        case "turn.started":
          setBridgeTurnActive(event, workspacePath);
          logBridgeDiagnostic(`liveOverride updated: status=turn-active run=${shortHash(event.run_id)}`);
          break;
        case "run.started":
          setBridgeRunning(event, workspacePath);
          logBridgeDiagnostic(`liveOverride updated: status=running run=${shortHash(event.run_id)}`);
          break;
        case "run.finished": {
          const savedTokens = typeof event.saved_tokens === "number" ? event.saved_tokens : undefined;
          logBridgeDiagnostic(`accepted: finished saved_tokens=${savedTokens ?? "unknown"}`);
          setBridgeSettling(event, workspacePath);
          // run.finished is "data ready", NOT "the AI is done talking" — the
          // status bar/Dashboard show "收功中" now and only promote to the
          // final 本次省/执行失败 once turn.finished (or the fallback timer)
          // confirms the turn is actually over.
          logBridgeDiagnostic(
            `liveOverride updated: status=settling saved=${formatSavedTokens(event.saved_tokens, event.saved_bytes) ?? "—"}`,
          );
          logBridgeDiagnostic("status bar: settling, waiting for turn.finished or fallback timer");
          break;
        }
        case "turn.finished":
          setBridgeTurnFinished(event, workspacePath);
          logBridgeDiagnostic("liveOverride finalized: turn.finished promoted settling result, visible 8s");
          break;
      }
      changed = true;
    }
    if (changed && statusBarItem) {
      statusBarItem.show();
      const status = await fetchStatus(workspacePath);
      updateDashboardIfOpen(status, buildLiveStatusOverride(workspacePath), workspacePath);
      logBridgeDiagnostic("dashboard refreshed");
      await updateStatusBar();
    }
  };

  for (const source of sources) {
    source.watcher.onDidChange(() => { void handleBridgeChange(source); }, null, context.subscriptions);
    source.watcher.onDidCreate(() => { void handleBridgeChange(source); }, null, context.subscriptions);
    context.subscriptions.push(source.watcher);
  }
  context.subscriptions.push({
    dispose: () => {
      bridgeDiagnostics.watcher_alive = false;
    },
  });
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

export function eventBelongsToWorkspace(
  event: AdapterEvent,
  workspacePath: string,
): boolean {
  const resolvedWorkspace = path.resolve(workspacePath);
  const home = path.resolve(os.homedir());

  const isWorkspaceOrChild = (candidate: string): boolean => {
    const resolvedCandidate = path.resolve(candidate);
    if (resolvedCandidate === home) return false;
    const relative = path.relative(resolvedWorkspace, resolvedCandidate);
    return relative === "" || (
      relative !== ".." &&
      !relative.startsWith(`..${path.sep}`) &&
      !path.isAbsolute(relative)
    );
  };

  if (event.cwd) {
    if (isWorkspaceOrChild(event.cwd)) {
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
          : path.isAbsolute(m[1])
            ? m[1]
            : event.cwd
              ? path.resolve(event.cwd, m[1])
              : "";
      if (cdPath && isWorkspaceOrChild(cdPath)) {
        return true;
      }
    }
  }

  return false;
}

export function hookEventIsFresh(event: AdapterEvent, now = Date.now()): boolean {
  const eventMs = parseIsoTimeMs(event.time);
  return eventMs !== undefined && Math.abs(now - eventMs) <= HOOK_EVENT_FRESH_MS;
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
  const cursors = new Map(
    hookFiles.map((hookFile) => [hookFile, initializeHookCursor(hookFile)]),
  );

  const pendingRefreshes = new Map<string, NodeJS.Timeout>();
  const scheduleRefresh = (hookFile: string): void => {
    const pending = pendingRefreshes.get(hookFile);
    if (pending) clearTimeout(pending);
    pendingRefreshes.set(hookFile, setTimeout(() => {
      pendingRefreshes.delete(hookFile);
      const activeWorkspace = resolveActiveXitWorkspace();
      const cursor = cursors.get(hookFile) || initializeHookCursor(hookFile);
      cursors.set(hookFile, cursor);
      const appendedEvents = readAppendedHookEvents(hookFile, cursor);
      const workspaceEvents = appendedEvents.filter(
        (event) =>
          hookEventIsFresh(event) &&
          eventBelongsToWorkspace(event, activeWorkspace),
      );
      void workspaceEvents;
      void updateStatusBar();
    }, 100));
  };

  for (const hookFile of hookFiles) {
    const watcher = vscode.workspace.createFileSystemWatcher(
      new vscode.RelativePattern(path.dirname(hookFile), path.basename(hookFile)),
    );
    watcher.onDidChange(() => scheduleRefresh(hookFile), null, context.subscriptions);
    watcher.onDidCreate(() => scheduleRefresh(hookFile), null, context.subscriptions);
    context.subscriptions.push(watcher);
  }

  context.subscriptions.push({
    dispose: () => {
      for (const pending of pendingRefreshes.values()) {
        clearTimeout(pending);
      }
      pendingRefreshes.clear();
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
    "VS Code AI bridge watcher:",
    `  watcher_alive:             ${bridgeDiagnostics.watcher_alive ? "yes" : "no"}`,
    `  event_file_path:           ${bridgeDiagnostics.event_file_path.length > 0 ? bridgeDiagnostics.event_file_path.join(" | ") : "none"}`,
    `  accepted_event_count:      ${bridgeDiagnostics.accepted_event_count}`,
    `  dropped_event_count:       ${bridgeDiagnostics.dropped_event_count}`,
    `  last_event_at:             ${bridgeDiagnostics.last_event_at || "none"}`,
    `  last_accepted_event_at:    ${bridgeDiagnostics.last_accepted_event_at || "none"}`,
    `  last_dropped_event_at:     ${bridgeDiagnostics.last_dropped_event_at || "none"}`,
    `  last_drop_reason:          ${bridgeDiagnostics.last_drop_reason || "none"}`,
    `  current_workspace_hash:    ${shortHash(bridgeDiagnostics.current_workspace_hash)}`,
    `  last_event_workspace_hash: ${shortHash(bridgeDiagnostics.last_event_workspace_hash)}`,
    `  last_event_host_hash:      ${shortHash(bridgeDiagnostics.last_event_host_instance_hash)}`,
    `  last_event_source:         ${bridgeDiagnostics.last_event_source || "none"}`,
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

  const activationWorkspace = resolveActiveXitWorkspace();
  lastObservedRunSignature = getRunSignature(readLatestRun(activationWorkspace));
  currentRunStateSignature = getCurrentRunStateSignature(activationWorkspace);
  startRefresh();
  registerWorkspaceWatchers(context);
  registerVscodeAiBridgeWatcher(context);
  registerAdapterHookWatchers(context);
  registerTerminalListeners(context);

  context.subscriptions.push(
    vscode.commands.registerCommand("xit.openDashboard", async () => {
      await updateStatusBar();
      const workspaceSnapshot = resolveActiveXitWorkspace();
      const status = await fetchStatus(workspaceSnapshot);
      showDashboard(
        context,
        status,
        buildLiveStatusOverride(workspaceSnapshot),
        workspaceSnapshot,
      );
    }),
    vscode.commands.registerCommand("xit.refresh", async () => {
      await updateStatusBar();
      vscode.window.showInformationMessage("XiT status refreshed");
    }),
    vscode.commands.registerCommand("xit.showGain", async () => {
      const workspaceSnapshot = resolveActiveXitWorkspace();
      const status = await fetchStatus(workspaceSnapshot);
      if (!status.available || !status.gain) {
        vscode.window.showWarningMessage(
          `XiT: ${status.error || "No gain data available."}`,
        );
        return;
      }
      const g = status.gain;
      const savedDisplay = formatSavedTokens(g.saved_tokens, g.saved_bytes) || "—";
      vscode.window.showInformationMessage(
        `Commands condensed: ${g.total_commands_condensed} | Saved tokens: ${savedDisplay} | Estimated reduction: ${(g.estimated_reduction * 100).toFixed(1)}%`,
      );
    }),
    vscode.commands.registerCommand("xit.openLatestRawLog", openLatestRawLog),
    vscode.commands.registerCommand("xit.showOutput", showOutput),
    vscode.commands.registerCommand("xit.runCommand", async () => {
      await promptRunCommand(beginVsCodeRun);
    }),
    vscode.commands.registerCommand("xit.runWithAutoCompression", async () => {
      await promptRunWithAutoCompression(beginVsCodeRun);
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
