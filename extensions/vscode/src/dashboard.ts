import * as vscode from "vscode";
import type {
  CurrentRunState,
  LatestRun,
  LiveStatusView,
  XiTStatus,
} from "./types";
import {
  readLatestRun,
  readCurrentRunState,
} from "./xit";
import {
  buildLiveStatusView,
  estimateHitRateLift,
  formatSavedTokensForRun,
  getTokenImpactStats,
  getTokenMetricsForRun,
  readAllWorkspaceRuns,
} from "./workflow";

let panel: vscode.WebviewPanel | undefined;

function escapeHtml(text: string): string {
  return text
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#039;");
}

function isCurrentRunFreshAndRunning(state: CurrentRunState | undefined): boolean {
  if (!state || state.status !== "running") {
    return false;
  }
  const heartbeat = state.heartbeat_at || state.started_at;
  if (!heartbeat) {
    return false;
  }
  const ms = Date.parse(heartbeat);
  return !Number.isNaN(ms) && Date.now() - ms <= 15000;
}

function renderMetricItem(label: string, value: string, highlight = false): string {
  return `
    <div class="metric-tile ${highlight ? "highlight" : ""}">
      <div class="metric-label">${escapeHtml(label)}</div>
      <div class="metric-value">${escapeHtml(value)}</div>
    </div>
  `;
}

function buildDashboardHtml(
  status: XiTStatus,
  latestRun: LatestRun | undefined,
  cspSource: string,
  stylesheetHref: string,
  liveOverride?: LiveStatusView,
): string {
  const gain = status.gain;
  const tokenImpact = getTokenImpactStats(latestRun);
  const currentRunState = readCurrentRunState();
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

  // 发功效果 panel shows gold border only when there is a live result
  const reportPanelActive = isRunning || isCompleted;

  // ──────────────────────────────────────────────────────────────────
  // LIVE RESULT METRICS
  // Source: currentRunState (running) or latestRun (completed within hold window).
  // Never read from history for 发功效果.
  // ──────────────────────────────────────────────────────────────────
  const liveResultMetrics = isCompleted && latestRun
    ? getTokenMetricsForRun(latestRun)
    : undefined;
  const liveReductionPct = liveResultMetrics?.reductionPct ?? 0;
  const liveSavedTokens = liveResultMetrics?.savedTokens ?? 0;

  const liveSavedDisplay = liveResultMetrics
    ? (liveResultMetrics.savedDisplay || formatSavedTokensForRun(latestRun))
    : isRunning ? "计算中" : "—";
  const liveReductionDisplay = liveResultMetrics && liveReductionPct > 0
    ? `${Math.round(liveReductionPct)}%`
    : isRunning ? "计算中" : "—";
  const liveHitRateLift = liveResultMetrics
    ? estimateHitRateLift(liveReductionPct, liveSavedTokens)
    : 0;
  const liveHitRateLiftDisplay = liveHitRateLift > 0 ? `+${liveHitRateLift}%` : "—";
  const liveContextNoiseDisplay = liveReductionPct > 0
    ? `${Math.round(liveReductionPct)}%`
    : "—";

  // ──────────────────────────────────────────────────────────────────
  // CUMULATIVE / HISTORY — 功力累计 reads from history aggregates
  // ──────────────────────────────────────────────────────────────────
  const allRuns = readAllWorkspaceRuns();
  const hasAnyXitData = allRuns.length > 0 || !!latestRun;

  const gainFallbackTokens = !tokenImpact.workspaceTotalSavedTokens && gain?.saved_bytes
    ? Math.max(0, Math.round(gain.saved_bytes / 4))
    : 0;
  const workspaceTotalSavedDisplay = tokenImpact.workspaceTotalSavedTokens > 0
    ? tokenImpact.workspaceTotalSavedDisplay
    : gainFallbackTokens > 0
      ? formatTokenShort(gainFallbackTokens)
      : "—";

  const todayRunsCount = allRuns.filter(r => {
    const ms = r.timestamp ? Date.parse(r.timestamp) : 0;
    return ms >= new Date().setHours(0, 0, 0, 0);
  }).length;

  const todaySavedValue = tokenImpact.todaySavedTokens > 0
    ? tokenImpact.todaySavedDisplay
    : "—";
  const todayRunsValue = todayRunsCount > 0
    ? `${todayRunsCount} 次`
    : "—";
  const workspaceTotalValue = tokenImpact.workspaceTotalSavedTokens > 0 || gainFallbackTokens > 0
    ? workspaceTotalSavedDisplay
    : "—";
  const totalRunsValue = allRuns.length > 0
    ? `${allRuns.length} 次`
    : "—";

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
          ${renderMetricItem("本次吸T", liveSavedDisplay, isCompleted && liveSavedTokens > 0)}
          ${renderMetricItem("降噪率", liveReductionDisplay, isCompleted && liveReductionPct > 0)}
          ${renderMetricItem("命中加成", liveHitRateLiftDisplay, isCompleted && liveHitRateLift > 0)}
          ${renderMetricItem("浊气减少", liveContextNoiseDisplay, isCompleted && liveReductionPct > 0)}
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
        ${renderMetricItem("今日省T", todaySavedValue, tokenImpact.todaySavedTokens > 0)}
        ${renderMetricItem("今日吸T", todayRunsValue, todayRunsCount > 0)}
        ${renderMetricItem("累计省T", workspaceTotalValue, tokenImpact.workspaceTotalSavedTokens > 0)}
        ${renderMetricItem("累计发功", totalRunsValue, allRuns.length > 0)}
      </div>
    </section>

    <footer class="dashboard-footer">
      <span>本地处理 · 不读取聊天内容 · 无遥测</span>
    </footer>
  </main>
</body>
</html>`;
}

function formatTokenShort(tokens: number): string {
  if (tokens >= 1000000) {
    return `~${Math.round(tokens / 100000) / 10}M Token`;
  }
  if (tokens >= 1000) {
    return `~${Math.round(tokens / 100) / 10}k Token`;
  }
  return `${tokens} Token`;
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
