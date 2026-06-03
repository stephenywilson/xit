import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import * as vscode from "vscode";
import type {
  CurrentRunState,
  LatestRun,
  LiveStatusView,
  XiTStatus,
} from "./types";
import {
  readCurrentRunState,
  readLatestRun,
  resolveActiveXitWorkspace,
} from "./xit";
import {
  buildLiveStatusView,
  getTokenMetricsForRun,
  readAllWorkspaceRuns,
} from "./workflow";

let panel: vscode.WebviewPanel | undefined;

// ──────────────────────────────────────────────────────────────────
// PERSISTENT CUMULATIVE CACHE
// Keyed by absolute workspace path so one project's stats never
// appear in another project's Dashboard.
// Falls back to disk cache when history read returns empty
// (e.g., VS Code workspace ≠ XiT project, or transient read failure).
// ──────────────────────────────────────────────────────────────────

interface CumulativeStats {
  totalRuns: number;
  todayCount: number;
  todaySaved: number;   // tokens
  totalSaved: number;   // tokens
}

interface CumulativeCacheEntry extends CumulativeStats {
  cachedAt: string;
}

type CumulativeCacheFile = Record<string, CumulativeCacheEntry>;

const CUMULATIVE_CACHE_FILE = path.join(os.homedir(), ".xit", "vscode-cumulative-cache.json");

// Fast in-memory layer — reset on extension host restart, backed by disk below
let memCumulative: { workspace: string; stats: CumulativeStats } | undefined;

function readCacheFile(): CumulativeCacheFile {
  try {
    if (!fs.existsSync(CUMULATIVE_CACHE_FILE)) return {};
    return JSON.parse(fs.readFileSync(CUMULATIVE_CACHE_FILE, "utf-8")) as CumulativeCacheFile;
  } catch {
    return {};
  }
}

function writeCacheEntry(workspacePath: string, stats: CumulativeStats): void {
  try {
    const cache = readCacheFile();
    cache[workspacePath] = { ...stats, cachedAt: new Date().toISOString() };
    fs.mkdirSync(path.dirname(CUMULATIVE_CACHE_FILE), { recursive: true });
    fs.writeFileSync(CUMULATIVE_CACHE_FILE, JSON.stringify(cache, null, 2), "utf-8");
  } catch {
    // ignore write failures — cache is best-effort
  }
}

function loadCacheEntry(workspacePath: string): CumulativeStats | undefined {
  // 1. fast path: memory
  if (memCumulative && path.resolve(memCumulative.workspace) === path.resolve(workspacePath)) {
    return memCumulative.stats;
  }
  // 2. disk
  try {
    const cache = readCacheFile();
    const entry = cache[workspacePath];
    if (!entry) return undefined;
    return {
      totalRuns: entry.totalRuns,
      todayCount: entry.todayCount,
      todaySaved: entry.todaySaved,
      totalSaved: entry.totalSaved,
    };
  } catch {
    return undefined;
  }
}

function updateCache(workspacePath: string, stats: CumulativeStats): void {
  memCumulative = { workspace: workspacePath, stats };
  writeCacheEntry(workspacePath, stats);
}

function escapeHtml(text: string): string {
  return text
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#039;");
}

// ──────────────────────────────────────────────────────────────────
// DISPLAY FORMAT HELPERS
// ──────────────────────────────────────────────────────────────────

function formatTokensPrecise(tokens: number): string {
  if (tokens <= 0) return "—";
  if (tokens >= 1_000_000) return `${(tokens / 1_000_000).toFixed(1)}M Token`;
  if (tokens >= 1000) return `${(tokens / 1000).toFixed(1)}k Token`;
  return `${tokens} Token`;
}

function formatTokensCumulative(tokens: number): string {
  if (tokens >= 1_000_000) return `${(tokens / 1_000_000).toFixed(1)}M Token`;
  if (tokens >= 1000) return `${(tokens / 1000).toFixed(1)}k Token`;
  return `${tokens} Token`;
}

function formatReductionPrecise(pct: number): string {
  return `${pct.toFixed(1)}%`;
}

function formatHitLiftRange(reductionPct: number): string {
  if (reductionPct >= 95) return "预计 +24–28%";
  if (reductionPct >= 90) return "预计 +18–24%";
  if (reductionPct >= 80) return "预计 +12–18%";
  if (reductionPct >= 60) return "预计 +6–12%";
  return "预计 +0–6%";
}

// summary_bytes / 4 is an estimate, marked with ~
function formatEssenceApprox(summaryBytes: number): string {
  if (summaryBytes <= 0) return "—";
  const approxTokens = Math.round(summaryBytes / 4);
  if (approxTokens >= 1000) return `~${(approxTokens / 1000).toFixed(1)}k Token`;
  return `~${approxTokens} Token`;
}

function renderMetricItem(label: string, value: string, highlight = false): string {
  return `
    <div class="metric-tile ${highlight ? "highlight" : ""}">
      <div class="metric-label">${escapeHtml(label)}</div>
      <div class="metric-value">${escapeHtml(value)}</div>
    </div>
  `;
}

// ──────────────────────────────────────────────────────────────────
// SAVED TOKENS PER RUN — best available field priority
// ──────────────────────────────────────────────────────────────────
function savedTokensFromRun(run: LatestRun): number {
  // 1. explicit saved_tokens
  if (typeof run.saved_tokens === "number" && run.saved_tokens > 0) {
    return run.saved_tokens;
  }
  // 2. raw_bytes - summary_bytes / 4
  const rawTokens = Math.round((run.raw_bytes ?? 0) / 4);
  const summaryTokens = Math.round((run.summary_bytes ?? 0) / 4);
  if (rawTokens > 0 && summaryTokens >= 0 && rawTokens > summaryTokens) {
    return rawTokens - summaryTokens;
  }
  return 0;
}

// ──────────────────────────────────────────────────────────────────
// CUMULATIVE AGGREGATE — reads all rows from history, no filtering
// ──────────────────────────────────────────────────────────────────
function computeCumulative(runs: LatestRun[]): CumulativeStats {
  const todayStart = new Date().setHours(0, 0, 0, 0);
  let todayCount = 0;
  let todaySaved = 0;
  let totalSaved = 0;

  for (const run of runs) {
    const saved = savedTokensFromRun(run);
    totalSaved += saved;
    const ts = run.timestamp ? Date.parse(run.timestamp) : 0;
    if (ts >= todayStart) {
      todayCount++;
      todaySaved += saved;
    }
  }

  return { totalRuns: runs.length, todayCount, todaySaved, totalSaved };
}

// ──────────────────────────────────────────────────────────────────
// LIVE RESULT METRICS from current-run.json
// Uses estimated_reduction (authoritative from CLI) over byte estimates.
// ──────────────────────────────────────────────────────────────────
function liveMetricsFromCurrentRun(state: CurrentRunState): {
  savedTokens: number;
  reductionPct: number;
  summaryBytes: number;
} {
  const savedTokens = typeof state.saved_tokens === "number" && state.saved_tokens > 0
    ? state.saved_tokens
    : Math.max(0, Math.round(((state.raw_bytes ?? 0) - (state.summary_bytes ?? 0)) / 4));

  const reductionPct = typeof state.estimated_reduction === "number" && state.estimated_reduction > 0
    ? state.estimated_reduction * 100
    : savedTokens > 0 && (state.raw_bytes ?? 0) > 0
      ? (savedTokens / Math.round((state.raw_bytes ?? 0) / 4)) * 100
      : 0;

  return {
    savedTokens,
    reductionPct,
    summaryBytes: state.summary_bytes ?? 0,
  };
}

function buildDashboardHtml(
  status: XiTStatus,
  _latestRun: LatestRun | undefined,   // kept for signature compat, not used for live result
  cspSource: string,
  stylesheetHref: string,
  liveOverride?: LiveStatusView,
): string {
  // ──────────────────────────────────────────────────────────────────
  // SINGLE AUTHORITY: active XiT workspace
  // All reads use this path — never mix workspace roots.
  // ──────────────────────────────────────────────────────────────────
  const activeWorkspace = resolveActiveXitWorkspace();

  const liveStatus = liveOverride ?? (status.state === "binary-not-found"
    ? { kind: "missing" as const, label: "未找到 XiT", reason: "binary not found", source: "extension status" }
    : buildLiveStatusView());

  // ──────────────────────────────────────────────────────────────────
  // LIVE STATE FLAGS
  // ──────────────────────────────────────────────────────────────────
  const isRunning  = liveStatus.kind === "xit_running";
  const isCompleted = liveStatus.kind === "xit_completed";
  const reportPanelActive = isRunning || isCompleted;

  // ──────────────────────────────────────────────────────────────────
  // LIVE RESULT METRICS — source: current-run.json only
  // Never reads from history for 发功效果.
  // ──────────────────────────────────────────────────────────────────
  const currentRunState = readCurrentRunState();
  const completedRun = isCompleted && currentRunState?.status === "completed"
    ? currentRunState
    : undefined;

  let liveSavedDisplay = "—";
  let liveReductionDisplay = "—";
  let liveHitLiftDisplay = "—";
  let liveEssenceDisplay = "—";
  let liveSavedHighlight = false;
  let liveReductionHighlight = false;
  let liveHitHighlight = false;
  let liveEssenceHighlight = false;

  if (completedRun) {
    const m = liveMetricsFromCurrentRun(completedRun);
    liveSavedDisplay      = formatTokensPrecise(m.savedTokens);
    liveReductionDisplay  = m.reductionPct > 0 ? formatReductionPrecise(m.reductionPct) : "—";
    liveHitLiftDisplay    = m.reductionPct > 0 ? formatHitLiftRange(m.reductionPct) : "—";
    liveEssenceDisplay    = formatEssenceApprox(m.summaryBytes);
    liveSavedHighlight     = m.savedTokens > 0;
    liveReductionHighlight = m.reductionPct > 0;
    liveHitHighlight       = m.reductionPct > 0;
    liveEssenceHighlight   = m.summaryBytes > 0;
  } else if (isRunning) {
    liveSavedDisplay = liveReductionDisplay = "计算中";
  }

  // ──────────────────────────────────────────────────────────────────
  // CUMULATIVE AGGREGATE (功力累计)
  // Reads history.jsonl via readAllWorkspaceRuns() which uses activeWorkspace.
  // On success: update persistent cache keyed by activeWorkspace.
  // On failure: use cache for the same workspace (never cross-project).
  // ──────────────────────────────────────────────────────────────────
  const allRuns = readAllWorkspaceRuns();
  let cum: CumulativeStats | undefined;

  if (allRuns.length > 0) {
    cum = computeCumulative(allRuns);
    updateCache(activeWorkspace, cum);
  } else {
    cum = loadCacheEntry(activeWorkspace);
  }

  const todaySavedValue  = cum && cum.todaySaved  > 0 ? formatTokensCumulative(cum.todaySaved)  : "—";
  const todayRunsValue   = cum && cum.todayCount  > 0 ? `${cum.todayCount} 次`                   : "—";
  const totalSavedValue  = cum && cum.totalSaved  > 0 ? formatTokensCumulative(cum.totalSaved)  : "—";
  const totalRunsValue   = cum && cum.totalRuns   > 0 ? `${cum.totalRuns} 次`                    : "—";

  // Critical errors
  const hardErrors: string[] = [];
  if (status.state === "binary-not-found") {
    hardErrors.push("未找到 XiT CLI，请运行 npm install -g xitsg 安装");
  }

  return `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src ${cspSource}; img-src ${cspSource} data:; font-src ${cspSource};">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>XiT Dashboard</title>
  <link rel="stylesheet" href="${stylesheetHref}">
</head>
<body>
  <main class="dashboard-shell">
    <section class="hero-card">
      <div class="hero-title-row">
        <h1>吸T神功</h1>
        <span class="hero-tagline">本地发功 · 斩断噪音 · 护住上下文</span>
      </div>
      <div class="guard-badge">已守护 Claude / Codex / Kimi / Cursor / Antigravity</div>
    </section>

    ${hardErrors.length > 0
      ? `<section class="banner warning">${escapeHtml(hardErrors.join(" · "))}</section>`
      : ""}

    <section class="panel ${reportPanelActive ? "panel-active" : ""}">
      <div class="section-heading">
        <div>
          <h2>发功效果</h2>
          <p class="section-subtitle">${isRunning ? "正在吸T中" : isCompleted ? "刚刚完成的吸T结果" : "等待下一轮发功"}</p>
        </div>
      </div>
      ${isRunning || isCompleted
        ? `<div class="metrics-grid report-grid">
          ${renderMetricItem("本次吸T",  liveSavedDisplay,     liveSavedHighlight)}
          ${renderMetricItem("降噪率",   liveReductionDisplay,  liveReductionHighlight)}
          ${renderMetricItem("命中加成", liveHitLiftDisplay,    liveHitHighlight)}
          ${renderMetricItem("保留精华", liveEssenceDisplay,    liveEssenceHighlight)}
        </div>`
        : `<div class="empty-state">
          <div class="empty-state-title">等待发功</div>
          <div class="empty-state-desc">下一次高噪音输出出现时，XiT 会自动压缩并生成本轮结果。</div>
        </div>`
      }
    </section>

    <section class="panel">
      <div class="section-heading">
        <div>
          <h2>功力累计</h2>
        </div>
      </div>
      <div class="metrics-grid report-grid">
        ${renderMetricItem("今日省T",  todaySavedValue, !!(cum && cum.todaySaved  > 0))}
        ${renderMetricItem("今日吸T",  todayRunsValue,  !!(cum && cum.todayCount  > 0))}
        ${renderMetricItem("累计省T",  totalSavedValue, !!(cum && cum.totalSaved  > 0))}
        ${renderMetricItem("累计发功", totalRunsValue,  !!(cum && cum.totalRuns   > 0))}
      </div>
    </section>

    <footer class="dashboard-footer">
      <span>本地处理 · 不读取聊天内容 · 无遥测</span>
    </footer>
  </main>
</body>
</html>`;
}

export function showDashboard(
  context: vscode.ExtensionContext,
  status: XiTStatus,
  liveOverride?: LiveStatusView,
): void {
  const mediaRoot = vscode.Uri.joinPath(context.extensionUri, "media");
  if (panel) {
    panel.reveal(vscode.ViewColumn.One);
  } else {
    panel = vscode.window.createWebviewPanel(
      "xitDashboard",
      "XiT Dashboard",
      vscode.ViewColumn.One,
      {
        enableScripts: false,
        enableCommandUris: true,
        localResourceRoots: [mediaRoot],
      },
    );
    panel.onDidDispose(() => { panel = undefined; }, null, context.subscriptions);
  }

  const latestRun = readLatestRun();
  const stylesheetHref = panel.webview
    .asWebviewUri(vscode.Uri.joinPath(mediaRoot, "dashboard.css"))
    .toString();
  panel.webview.html = buildDashboardHtml(
    status, latestRun, panel.webview.cspSource, stylesheetHref, liveOverride,
  );
}

export function updateDashboardIfOpen(status: XiTStatus, liveOverride?: LiveStatusView): void {
  if (!panel) return;
  const latestRun = readLatestRun();
  const stylesheetHref = panel.webview
    .asWebviewUri(vscode.Uri.joinPath(panel.webview.options.localResourceRoots![0], "dashboard.css"))
    .toString();
  panel.webview.html = buildDashboardHtml(
    status, latestRun, panel.webview.cspSource, stylesheetHref, liveOverride,
  );
}
