import * as vscode from "vscode";
import type {
  LatestRun,
  LiveStatusView,
  XiTStatus,
} from "./types";
import {
  readLatestRun,
} from "./xit";
import {
  buildLiveStatusView,
  getTokenMetricsForRun,
  readAllWorkspaceRuns,
} from "./workflow";

let panel: vscode.WebviewPanel | undefined;

// ──────────────────────────────────────────────────────────────────
// MODULE-LEVEL CACHE — 功力累计 never clears once loaded
// ──────────────────────────────────────────────────────────────────
interface CumulativeStats {
  totalRuns: number;       // all history rows (口径 = totalRows)
  todayCount: number;
  todaySaved: number;
  totalSaved: number;
}

let lastGoodCumulative: CumulativeStats | undefined;

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

// Precise token display: 10367 → "10.4k Token", 486 → "486 Token"
function formatTokensPrecise(tokens: number): string {
  if (tokens <= 0) return "—";
  if (tokens >= 1_000_000) {
    return `${(tokens / 1_000_000).toFixed(1)}M Token`;
  }
  if (tokens >= 1000) {
    return `${(tokens / 1000).toFixed(1)}k Token`;
  }
  return `${tokens} Token`;
}

// Cumulative display: same precision but allows 0
function formatTokensCumulative(tokens: number): string {
  if (tokens >= 1_000_000) {
    return `${(tokens / 1_000_000).toFixed(1)}M Token`;
  }
  if (tokens >= 1000) {
    return `${(tokens / 1000).toFixed(1)}k Token`;
  }
  return `${tokens} Token`;
}

// One decimal reduction: 0.986 → "98.6%", 0.99 → "99.0%"
function formatReductionPrecise(reductionPct: number): string {
  return `${reductionPct.toFixed(1)}%`;
}

// Honest hit-lift range based on reduction bracket
function formatHitLiftRange(reductionPct: number): string {
  if (reductionPct >= 95) return "预计 +24–28%";
  if (reductionPct >= 90) return "预计 +18–24%";
  if (reductionPct >= 80) return "预计 +12–18%";
  if (reductionPct >= 60) return "预计 +6–12%";
  return "预计 +0–6%";
}

// Compressed essence: summary_tokens → "78 Token"
function formatEssenceTokens(summaryTokens: number): string {
  if (summaryTokens <= 0) return "—";
  if (summaryTokens >= 1000) {
    return `${(summaryTokens / 1000).toFixed(1)}k Token`;
  }
  return `${summaryTokens} Token`;
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
// CUMULATIVE AGGREGATE — independent of live state
// Always reads history.jsonl; updates cache on success; uses cache on failure
// ──────────────────────────────────────────────────────────────────
function computeCumulativeStats(runs: LatestRun[]): CumulativeStats {
  const todayStart = new Date().setHours(0, 0, 0, 0);
  let todayCount = 0;
  let todaySaved = 0;
  let totalSaved = 0;

  for (const run of runs) {
    const metrics = getTokenMetricsForRun(run);
    if (!metrics) continue;
    totalSaved += metrics.savedTokens;
    const ts = run.timestamp ? Date.parse(run.timestamp) : 0;
    if (ts >= todayStart) {
      todayCount++;
      todaySaved += metrics.savedTokens;
    }
  }

  return {
    totalRuns: runs.length,  // totalRows — all rows, regardless of saved_tokens field
    todayCount,
    todaySaved,
    totalSaved,
  };
}

function buildDashboardHtml(
  status: XiTStatus,
  latestRun: LatestRun | undefined,
  cspSource: string,
  stylesheetHref: string,
  liveOverride?: LiveStatusView,
): string {
  const liveStatus = liveOverride ?? (status.state === "binary-not-found"
    ? {
        kind: "missing" as const,
        label: "未找到 XiT",
        reason: "binary not found",
        source: "extension status",
      }
    : buildLiveStatusView());

  // ──────────────────────────────────────────────────────────────────
  // LIVE STATE FLAGS
  // ──────────────────────────────────────────────────────────────────
  const isRunning = liveStatus.kind === "xit_running";
  const isCompleted = liveStatus.kind === "xit_completed";
  const reportPanelActive = isRunning || isCompleted;

  // ──────────────────────────────────────────────────────────────────
  // LIVE RESULT METRICS (发功效果)
  // Source: latestRun only within completed hold window. Never from history.
  // ──────────────────────────────────────────────────────────────────
  const liveResultMetrics = isCompleted && latestRun
    ? getTokenMetricsForRun(latestRun)
    : undefined;

  const liveReductionPct = liveResultMetrics?.reductionPct ?? 0;
  const liveSavedTokens = liveResultMetrics?.savedTokens ?? 0;
  const liveSummaryTokens = liveResultMetrics?.summaryTokens ?? 0;

  const liveSavedDisplay = liveResultMetrics
    ? formatTokensPrecise(liveSavedTokens)
    : isRunning ? "计算中" : "—";
  const liveReductionDisplay = liveResultMetrics && liveReductionPct > 0
    ? formatReductionPrecise(liveReductionPct)
    : isRunning ? "计算中" : "—";
  const liveHitLiftDisplay = liveResultMetrics && liveReductionPct > 0
    ? formatHitLiftRange(liveReductionPct)
    : "—";
  const liveEssenceDisplay = liveResultMetrics
    ? formatEssenceTokens(liveSummaryTokens)
    : "—";

  // ──────────────────────────────────────────────────────────────────
  // CUMULATIVE AGGREGATE (功力累计)
  // Reads history.jsonl independently. Never mixed with live state.
  // Updates module-level cache on success; uses last-good cache on failure.
  // ──────────────────────────────────────────────────────────────────
  const allRuns = readAllWorkspaceRuns();
  if (allRuns.length > 0) {
    lastGoodCumulative = computeCumulativeStats(allRuns);
  }
  const cum = lastGoodCumulative;

  const todaySavedValue  = cum && cum.todaySaved  > 0 ? formatTokensCumulative(cum.todaySaved)  : "—";
  const todayRunsValue   = cum && cum.todayCount  > 0 ? `${cum.todayCount} 次`                  : "—";
  const totalSavedValue  = cum && cum.totalSaved  > 0 ? formatTokensCumulative(cum.totalSaved)  : "—";
  const totalRunsValue   = cum && cum.totalRuns   > 0 ? `${cum.totalRuns} 次`                   : "—";

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
          ${renderMetricItem("本次吸T",  liveSavedDisplay,    isCompleted && liveSavedTokens > 0)}
          ${renderMetricItem("降噪率",   liveReductionDisplay, isCompleted && liveReductionPct > 0)}
          ${renderMetricItem("命中加成", liveHitLiftDisplay,   isCompleted && liveReductionPct > 0)}
          ${renderMetricItem("保留精华", liveEssenceDisplay,   isCompleted && liveSummaryTokens > 0)}
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
    panel.onDidDispose(
      () => {
        panel = undefined;
      },
      null,
      context.subscriptions,
    );
  }

  const latestRun = readLatestRun();
  const stylesheetHref = panel.webview
    .asWebviewUri(vscode.Uri.joinPath(mediaRoot, "dashboard.css"))
    .toString();
  panel.webview.html = buildDashboardHtml(
    status,
    latestRun,
    panel.webview.cspSource,
    stylesheetHref,
    liveOverride,
  );
}

export function updateDashboardIfOpen(status: XiTStatus, liveOverride?: LiveStatusView): void {
  if (!panel) {
    return;
  }
  const latestRun = readLatestRun();
  const stylesheetHref = panel.webview
    .asWebviewUri(vscode.Uri.joinPath(panel.webview.options.localResourceRoots![0], "dashboard.css"))
    .toString();
  panel.webview.html = buildDashboardHtml(
    status,
    latestRun,
    panel.webview.cspSource,
    stylesheetHref,
    liveOverride,
  );
}
