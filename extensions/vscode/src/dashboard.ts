import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import * as vscode from "vscode";
import type {
  LatestRun,
  LiveStatusView,
  XiTStatus,
} from "./types";
import {
  readCurrentRunState,
  resolveActiveXitWorkspace,
} from "./xit";
import {
  readAllWorkspaceRuns,
} from "./workflow";
import {
  computeLiveRunView,
  computeTodayStats,
  formatSavedTokens,
  savedTokensFromRun,
} from "./logic";

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
  updatedAt: string;
  cachedAt?: string;
}

type CumulativeCacheFile = Record<string, CumulativeCacheEntry>;

const CUMULATIVE_CACHE_FILE = path.join(os.homedir(), ".xit", "vscode-cumulative-cache.json");

// Fast in-memory layer — reset on extension host restart, backed by disk below
let memCumulative: { workspace: string; stats: CumulativeStats } | undefined;

function canonicalWorkspacePath(workspacePath: string): string {
  const absolute = path.resolve(workspacePath);
  try {
    return fs.realpathSync.native(absolute);
  } catch {
    return absolute;
  }
}

function isValidCumulativeStats(value: unknown): value is CumulativeStats {
  if (!value || typeof value !== "object") return false;
  const entry = value as Partial<CumulativeCacheEntry>;
  return (
    typeof entry.totalRuns === "number" &&
    Number.isFinite(entry.totalRuns) &&
    typeof entry.todayCount === "number" &&
    Number.isFinite(entry.todayCount) &&
    typeof entry.todaySaved === "number" &&
    Number.isFinite(entry.todaySaved) &&
    typeof entry.totalSaved === "number" &&
    Number.isFinite(entry.totalSaved)
  );
}

function readCacheFile(): CumulativeCacheFile {
  try {
    if (!fs.existsSync(CUMULATIVE_CACHE_FILE)) return {};
    const parsed = JSON.parse(
      fs.readFileSync(CUMULATIVE_CACHE_FILE, "utf-8"),
    ) as Record<string, unknown>;
    const valid: CumulativeCacheFile = {};
    for (const [workspace, value] of Object.entries(parsed)) {
      if (!isValidCumulativeStats(value)) continue;
      const entry = value as Partial<CumulativeCacheEntry>;
      const updatedAt = entry.updatedAt || entry.cachedAt;
      if (typeof updatedAt !== "string") continue;
      valid[canonicalWorkspacePath(workspace)] = {
        totalRuns: entry.totalRuns!,
        todayCount: entry.todayCount!,
        todaySaved: entry.todaySaved!,
        totalSaved: entry.totalSaved!,
        updatedAt,
      };
    }
    return valid;
  } catch {
    return {};
  }
}

function writeCacheEntry(workspacePath: string, stats: CumulativeStats): void {
  try {
    const canonical = canonicalWorkspacePath(workspacePath);
    const home = canonicalWorkspacePath(os.homedir());
    const hasXitData =
      fs.existsSync(path.join(canonical, ".xit", "history.jsonl")) ||
      fs.existsSync(path.join(canonical, ".xit", "state"));
    if (canonical === home || !hasXitData) return;
    const cache = readCacheFile();
    cache[canonical] = { ...stats, updatedAt: new Date().toISOString() };
    fs.mkdirSync(path.dirname(CUMULATIVE_CACHE_FILE), { recursive: true });
    const tempPath = `${CUMULATIVE_CACHE_FILE}.${process.pid}.${Date.now()}.tmp`;
    fs.writeFileSync(tempPath, JSON.stringify(cache, null, 2), "utf-8");
    fs.renameSync(tempPath, CUMULATIVE_CACHE_FILE);
  } catch {
    // ignore write failures — cache is best-effort
  }
}

function loadCacheEntry(workspacePath: string): CumulativeStats | undefined {
  const canonical = canonicalWorkspacePath(workspacePath);
  // 1. fast path: memory
  if (memCumulative && memCumulative.workspace === canonical) {
    return memCumulative.stats;
  }
  // 2. disk
  try {
    const cache = readCacheFile();
    const entry = cache[canonical];
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
  const canonical = canonicalWorkspacePath(workspacePath);
  memCumulative = { workspace: canonical, stats };
  writeCacheEntry(canonical, stats);
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

// Standard user-visible token format: 约 X.XXk Token (two decimals) / N Token.
function formatTokensCumulative(tokens: number): string {
  return formatSavedTokens(tokens) || "—";
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
// CUMULATIVE AGGREGATE — reads all rows from history, no filtering
// ──────────────────────────────────────────────────────────────────
export function computeCumulative(runs: LatestRun[]): CumulativeStats {
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

export function selectCumulativeStats(
  runs: LatestRun[],
  cached: CumulativeStats | undefined,
): CumulativeStats | undefined {
  return runs.length > 0 ? computeCumulative(runs) : cached;
}

function buildDashboardHtml(
  status: XiTStatus,
  cspSource: string,
  stylesheetHref: string,
  workspaceSnapshot: string,
  liveOverride?: LiveStatusView,
): string {
  // ──────────────────────────────────────────────────────────────────
  // SINGLE AUTHORITY: active XiT workspace
  // All reads use this path — never mix workspace roots.
  // ──────────────────────────────────────────────────────────────────
  const liveStatus = liveOverride ?? (status.state === "binary-not-found"
    ? { kind: "missing" as const, label: "未找到 XiT", reason: "binary not found", source: "extension status" }
    : { kind: "idle" as const, label: "准备就绪", reason: "no active VS Code run", source: "vscode" });

  // ──────────────────────────────────────────────────────────────────
  // 本次发功：current VS Code session only (liveOverride / current-run.json
  // matched to this session). Never falls back to .xit/history.jsonl's last
  // entry — that file persists across restarts and would resurrect a run
  // from a previous session as if it just happened.
  // ──────────────────────────────────────────────────────────────────
  const currentRunState = readCurrentRunState(workspaceSnapshot);
  const liveRunView = computeLiveRunView(liveStatus, currentRunState);

  // ──────────────────────────────────────────────────────────────────
  // CUMULATIVE AGGREGATE (功力累计)
  // Reads history.jsonl using the render's workspace snapshot.
  // On success: update persistent cache keyed by that snapshot.
  // On failure: use cache for the same workspace (never cross-project).
  // ──────────────────────────────────────────────────────────────────
  const allRuns = readAllWorkspaceRuns(workspaceSnapshot);
  const today = computeTodayStats(allRuns);
  const cached = loadCacheEntry(workspaceSnapshot);
  const gainTotalRuns = status.gain?.total_commands_condensed;
  const gainTotalSaved = typeof status.gain?.saved_tokens === "number"
    ? status.gain.saved_tokens
    : typeof status.gain?.saved_bytes === "number"
      ? Math.round(status.gain.saved_bytes / 4)
      : undefined;
  const cum = selectCumulativeStats(allRuns, cached);

  if (allRuns.length > 0 && cum) {
    updateCache(workspaceSnapshot, cum);
  }

  const todaySavedValue  = today.todaySavedTokens > 0 ? formatTokensCumulative(today.todaySavedTokens) : "—";
  const todayRunsValue   = today.todayCount > 0 ? `${today.todayCount} 次` : "—";
  const totalSavedValue  = gainTotalSaved !== undefined ? formatTokensCumulative(gainTotalSaved) : "—";
  const totalRunsValue   = gainTotalRuns !== undefined ? `${gainTotalRuns} 次` : "—";

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
        <span class="hero-tagline">本地发功 · 斩断噪音 · 守护你的T</span>
      </div>
      <div class="guard-badge">XiT CLI 已接入 · VS Code 主动工作流已就绪</div>
    </section>

    ${hardErrors.length > 0
      ? `<section class="banner warning">${escapeHtml(hardErrors.join(" · "))}</section>`
      : ""}

    <section class="panel ${liveRunView.reportPanelActive ? "panel-active" : ""}">
      <div class="section-heading">
        <div>
          <h2>本轮发功</h2>
          <p class="section-subtitle">${escapeHtml(liveRunView.subtitle)}</p>
        </div>
      </div>
      ${liveRunView.reportPanelActive
        ? `<div class="metrics-grid report-grid">
          ${renderMetricItem("本次省",   liveRunView.savedDisplay,     liveRunView.savedHighlight)}
          ${renderMetricItem("本轮共吸", liveRunView.runCountDisplay,  liveRunView.runCountHighlight)}
          ${renderMetricItem("降噪率",   liveRunView.reductionDisplay, liveRunView.reductionHighlight)}
          ${renderMetricItem("状态",     liveRunView.statusDisplay,    liveRunView.statusDisplay === "成功")}
        </div>`
        : `<div class="empty-state">
          <div class="empty-state-title">等待发功</div>
          <div class="empty-state-desc">下一次高噪音输出出现时，XiT 会自动压缩并生成本轮结果。</div>
        </div>`
      }
    </section>

    <div class="panel-row">
      <section class="panel">
        <div class="section-heading">
          <div>
            <h2>今日功力</h2>
          </div>
        </div>
        <div class="metrics-grid report-grid two-col">
          ${renderMetricItem("今日省T",  todaySavedValue, today.todaySavedTokens > 0)}
          ${renderMetricItem("今日发功", todayRunsValue,  today.todayCount > 0)}
        </div>
      </section>

      <section class="panel">
        <div class="section-heading">
          <div>
            <h2>功力累计</h2>
          </div>
        </div>
        <div class="metrics-grid report-grid two-col">
          ${renderMetricItem("累计省T",  totalSavedValue, gainTotalSaved !== undefined && gainTotalSaved > 0)}
          ${renderMetricItem("累计运行", totalRunsValue,  gainTotalRuns !== undefined && gainTotalRuns > 0)}
        </div>
      </section>
    </div>

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
  workspaceSnapshot = resolveActiveXitWorkspace(),
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

  const stylesheetHref = panel.webview
    .asWebviewUri(vscode.Uri.joinPath(mediaRoot, "dashboard.css"))
    .toString();
  panel.webview.html = buildDashboardHtml(
    status, panel.webview.cspSource, stylesheetHref, workspaceSnapshot, liveOverride,
  );
}

export function updateDashboardIfOpen(
  status: XiTStatus,
  liveOverride?: LiveStatusView,
  workspaceSnapshot = resolveActiveXitWorkspace(),
): void {
  if (!panel) return;
  const stylesheetHref = panel.webview
    .asWebviewUri(vscode.Uri.joinPath(panel.webview.options.localResourceRoots![0], "dashboard.css"))
    .toString();
  panel.webview.html = buildDashboardHtml(
    status, panel.webview.cspSource, stylesheetHref, workspaceSnapshot, liveOverride,
  );
}
