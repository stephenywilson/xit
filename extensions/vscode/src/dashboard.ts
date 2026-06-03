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
  getAiAdapterHealth,
  getAdapterHookConnectivity,
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

function formatTokenShort(tokens: number): string {
  if (tokens >= 1000000) {
    return `~${Math.round(tokens / 100000) / 10}M Token`;
  }
  if (tokens >= 1000) {
    return `~${Math.round(tokens / 100) / 10}k Token`;
  }
  return `${tokens} Token`;
}

function buildCommandUri(command: string): string {
  return `command:${command}`;
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

function selectLatestSavedRun(runs: LatestRun[]): LatestRun | undefined {
  for (let i = runs.length - 1; i >= 0; i--) {
    const run = runs[i];
    const savedBytes = Math.max(0, run.raw_bytes - run.summary_bytes);
    const savedTokens = typeof run.saved_tokens === "number" ? run.saved_tokens : 0;
    if (savedBytes > 0 || savedTokens > 0) {
      return run;
    }
  }
  return undefined;
}

function buildStatusMeta(
  status: XiTStatus,
  liveStatus: LiveStatusView,
): {
  heroTitle: string;
  pillLabel: string;
  pillTone: "running" | "success" | "idle" | "warning";
} {
  if (status.state === "binary-not-found") {
    return {
      heroTitle: "请安装本地 XiT CLI 以启用降噪功能",
      pillLabel: "未接入",
      pillTone: "warning",
    };
  }

  if (liveStatus.kind === "xit_running") {
    const isTurnActive = liveStatus.reason === "turn active";
    return {
      heroTitle: isTurnActive ? "XiT 正在守护当前 AI 工作流" : "XiT 正在处理高噪音输出",
      pillLabel: "正在吸T中",
      pillTone: "running",
    };
  }

  if (liveStatus.kind === "agent_routed_pending_state" || liveStatus.kind === "agent_observing") {
    return {
      heroTitle: "XiT 正在守护你的高噪音命令",
      pillLabel: "守护你的T",
      pillTone: "idle",
    };
  }

  if (liveStatus.kind === "agent_not_routed") {
    return {
      heroTitle: "本轮 AI 活动已监测，命令无需压缩",
      pillLabel: "无需发功",
      pillTone: "warning",
    };
  }

  if (liveStatus.kind === "xit_completed") {
    return {
      heroTitle: "刚刚完成的吸T结果",
      pillLabel: "吸T完成",
      pillTone: "success",
    };
  }

  return {
    heroTitle: "XiT 正在守护你的高噪音命令",
    pillLabel: "守护你的T",
    pillTone: "idle",
  };
}

function renderSummaryCard(
  title: string,
  value: string,
  subtitle: string,
  tone: "neutral" | "accent" | "success" | "warning" | "muted-zero",
): string {
  return `
    <article class="summary-card ${tone}">
      <div class="summary-label">${escapeHtml(title)}</div>
      <div class="summary-value">${escapeHtml(value)}</div>
      <div class="summary-subtitle">${escapeHtml(subtitle)}</div>
    </article>
  `;
}

function renderMetricItem(label: string, value: string, highlight = false): string {
  return `
    <div class="metric-tile ${highlight ? "highlight" : ""}">
      <div class="metric-label">${escapeHtml(label)}</div>
      <div class="metric-value">${escapeHtml(value)}</div>
    </div>
  `;
}

type CoverageStatus = "protected" | "not_verified";

function renderCoverageItem(name: string, status: CoverageStatus): string {
  const labelCn = status === "protected" ? "已守护" : "未验证";
  const pillClass = status === "protected" ? "verified" : "not-verified";
  return `
    <div class="coverage-item">
      <span class="coverage-name">${escapeHtml(name)}</span>
      <span class="status-pill ${pillClass}">${labelCn}</span>
    </div>
  `;
}

function computeAiCoverage(): {
  claude: CoverageStatus;
  codex: CoverageStatus;
  kimi: CoverageStatus;
  cursor: CoverageStatus;
} {
  const adapterHealth = getAiAdapterHealth();
  const hookConn = getAdapterHookConnectivity();

  const getStatus = (adapter: "Claude" | "Codex" | "Cursor", hookKey: string): CoverageStatus => {
    const health = adapterHealth.find(h => h.adapter === adapter);
    const isRulesOk = health && (health.status === "verified" || health.status === "rules installed");
    const isHookConnected = hookConn[hookKey]?.connected;
    return (isRulesOk || isHookConnected) ? "protected" : "not_verified";
  };

  return {
    claude: getStatus("Claude", "claude"),
    codex: getStatus("Codex", "codex"),
    kimi: hookConn["kimi"]?.connected ? "protected" : "not_verified",
    cursor: getStatus("Cursor", "cursor"),
  };
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
  const statusMeta = buildStatusMeta(status, liveStatus);

  const validCurrentRun = isCurrentRunFreshAndRunning(currentRunState ?? undefined)
    ? currentRunState
    : undefined;

  const allRuns = readAllWorkspaceRuns();
  const latestPositiveSavedRun = selectLatestSavedRun(allRuns);
  const hasAnyXitData = allRuns.length > 0 || !!latestRun;

  // Workspace total
  const gainFallbackTokens = !tokenImpact.workspaceTotalSavedTokens && gain?.saved_bytes
    ? Math.max(0, Math.round(gain.saved_bytes / 4))
    : 0;
  const workspaceTotalSavedDisplay = tokenImpact.workspaceTotalSavedTokens > 0
    ? tokenImpact.workspaceTotalSavedDisplay
    : gainFallbackTokens > 0
      ? formatTokenShort(gainFallbackTokens)
      : "0 Token";

  // Latest saved display (for hero card 2)
  const latestSavedDisplay =
    validCurrentRun?.saved_tokens_display ||
    (typeof validCurrentRun?.saved_tokens === "number"
      ? formatTokenShort(validCurrentRun.saved_tokens)
      : undefined) ||
    (latestPositiveSavedRun ? formatSavedTokensForRun(latestPositiveSavedRun) : undefined);

  // Impact score metrics — use latest positive run
  const impactMetrics = latestPositiveSavedRun
    ? getTokenMetricsForRun(latestPositiveSavedRun)
    : tokenImpact.latest;
  const impactReductionPct = impactMetrics?.reductionPct ?? 0;
  const impactSavedTokens = impactMetrics?.savedTokens ?? 0;
  const impactSavedDisplay = impactMetrics?.savedDisplay || latestSavedDisplay;
  const latestReductionDisplay = impactMetrics && impactReductionPct > 0
    ? `${Math.round(impactReductionPct)}%`
    : "--";
  const hitRateLift = estimateHitRateLift(impactReductionPct, impactSavedTokens);
  const hitRateLiftDisplay = hitRateLift > 0 ? `预计 +${hitRateLift}%` : "--";
  const contextNoiseDisplay = impactReductionPct > 0
    ? `减少 ${Math.round(impactReductionPct)}%`
    : "--";

  // Today runs count
  const todayRunsCount = allRuns.filter(r => {
    const ms = r.timestamp ? Date.parse(r.timestamp) : 0;
    return ms >= new Date().setHours(0, 0, 0, 0);
  }).length;

  // AI Coverage
  const aiCoverage = computeAiCoverage();

  // Critical errors only
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
      <div>
        <div class="eyebrow">吸T神功 Dashboard</div>
        <h1>吸T神功 Dashboard</h1>
        <p class="hero-subtitle">本地降噪 · Token 节省 · AI 命中率提升</p>
      </div>
      <div class="hero-status">
        <span class="status-pill hero ${statusMeta.pillTone}">${escapeHtml(statusMeta.pillLabel)}</span>
        <div class="hero-status-copy">${escapeHtml(statusMeta.heroTitle)}</div>
      </div>
    </section>

    ${hardErrors.length > 0
      ? `<section class="banner warning">${escapeHtml(hardErrors.join(" · "))}</section>`
      : ""}

    <section class="summary-grid">
      ${renderSummaryCard(
        "当前状态",
        statusMeta.pillLabel,
        "当前 AI 工作流状态",
        status.state === "binary-not-found" ? "warning" : "accent",
      )}
      ${(() => {
        if (!hasAnyXitData) {
          return renderSummaryCard("本次节省", "—", "等待首次吸T", "muted-zero" as const);
        }
        const val = latestSavedDisplay || "0 Token";
        const isZero = val === "0 Token" || val === "0";
        return renderSummaryCard(
          "本次节省",
          val,
          "刚刚完成的吸T结果",
          isZero ? ("muted-zero" as const) : ("success" as const),
        );
      })()}
      ${renderSummaryCard(
        "今日节省",
        hasAnyXitData ? tokenImpact.todaySavedDisplay : "—",
        hasAnyXitData ? `今日 ${todayRunsCount} 次` : "等待首次吸T",
        "neutral",
      )}
      ${renderSummaryCard(
        "工作区累计",
        hasAnyXitData ? workspaceTotalSavedDisplay : "—",
        hasAnyXitData ? `共 ${allRuns.length} 次压缩` : "等待首次吸T",
        "neutral",
      )}
    </section>

    <section class="panel">
      <div class="section-heading">
        <h2>本次影响力</h2>
      </div>
      <div class="metrics-grid impact-grid">
        ${renderMetricItem("节省 Token", hasAnyXitData ? (impactSavedDisplay || "0 Token") : "—", impactSavedTokens > 0)}
        ${renderMetricItem("降噪率", hasAnyXitData ? latestReductionDisplay : "—")}
        ${renderMetricItem("预计命中率提升", hasAnyXitData ? hitRateLiftDisplay : "—", hitRateLift > 0)}
        ${renderMetricItem("上下文污染减少", hasAnyXitData ? contextNoiseDisplay : "—")}
      </div>
    </section>

    <section class="panel">
      <div class="section-heading">
        <h2>AI 守护状态</h2>
      </div>
      <div class="coverage-grid">
        ${renderCoverageItem("Claude", aiCoverage.claude)}
        ${renderCoverageItem("Codex", aiCoverage.codex)}
        ${renderCoverageItem("Kimi", aiCoverage.kimi)}
        ${renderCoverageItem("Cursor", aiCoverage.cursor)}
      </div>
    </section>

    <footer class="dashboard-footer">
      <span>本地处理 · 不读取聊天内容 · 无遥测</span>
      <a href="${buildCommandUri("xit.refresh")}">刷新</a>
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
