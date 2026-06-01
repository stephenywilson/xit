import * as fs from "fs";
import * as path from "path";
import * as vscode from "vscode";
import type {
  AdapterEvent,
  AdapterHealthItem,
  AgentTurnView,
  CurrentRunState,
  GlobalActivity,
  LatestRun,
  XiTStatus,
} from "./types";
import {
  readRecentEvents,
  readWorkspaceHistory,
  readTerminalEvents,
  readLatestRun,
  readCurrentRunState,
  resolveWorkspaceCwd,
} from "./xit";
import {
  buildAgentTurnView,
  computeWorkflowHealth,
  formatSavedTokensForRun,
  getAiAdapterHealth,
  getAdapterHookConnectivity,
  getTokenImpactStats,
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

function formatBytes(n: number): string {
  if (n >= 1000000) {
    return (n / 1000000).toFixed(1) + " MB";
  }
  if (n >= 1000) {
    return (n / 1000).toFixed(1) + " kB";
  }
  return n + " B";
}

function formatReduction(r: number): string {
  return `${Math.round(r * 100)}%`;
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

function formatTime(iso: string): string {
  try {
    return new Date(iso).toLocaleString();
  } catch {
    return iso;
  }
}

function gatherAllEvents(): AdapterEvent[] {
  const adapters = ["cursor", "codex", "claude", "kimi"];
  let allEvents: AdapterEvent[] = [];
  for (const adapter of adapters) {
    allEvents = allEvents.concat(readRecentEvents(adapter, 12));
  }
  allEvents = allEvents.concat(readWorkspaceHistory(12));
  allEvents.sort((a, b) => (b.time || "").localeCompare(a.time || ""));
  return allEvents;
}

function computeActivityFromEvents(events: AdapterEvent[]): GlobalActivity {
  const adapterCounts: Record<string, number> = {};
  for (const event of events) {
    if (event.adapter) {
      adapterCounts[event.adapter] = (adapterCounts[event.adapter] || 0) + 1;
    }
  }
  const latest = [...events].sort((a, b) =>
    (b.time || "").localeCompare(a.time || ""),
  )[0];
  return {
    latestAdapter: latest?.adapter,
    latestTime: latest?.time,
    latestCommand: latest?.original_command,
    latestPolicy: latest?.policy,
    eventCount: events.length,
    adapterCounts,
  };
}

function buildCommandUri(command: string): string {
  return `command:${command}`;
}

// True only when current-run.json is actively running with a fresh heartbeat (≤15s).
// A stale completed state must never override history.jsonl as the latest run source.
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

// Walk history newest-first and return the first entry with positive saved bytes/tokens.
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

interface WorkspaceWatchInfo {
  workspaceRoot: string;
  statePath: string;
  historyPath: string;
  stateExists: boolean;
  historyExists: boolean;
  hasAnyXitData: boolean;
}

function getWorkspaceWatchInfo(): WorkspaceWatchInfo {
  const workspaceRoot = resolveWorkspaceCwd();
  const statePath = path.join(workspaceRoot, ".xit", "state", "current-run.json");
  const historyPath = path.join(workspaceRoot, ".xit", "history.jsonl");
  const runsDir = path.join(workspaceRoot, ".xit", "runs");
  const stateExists = fs.existsSync(statePath);
  const historyExists = fs.existsSync(historyPath);
  const hasAnyXitData = stateExists || historyExists || fs.existsSync(runsDir);
  return { workspaceRoot, statePath, historyPath, stateExists, historyExists, hasAnyXitData };
}

function renderWorkspaceWatchBanner(info: WorkspaceWatchInfo): string {
  const stateLabel = info.stateExists ? "found" : "not found";
  const historyLabel = info.historyExists ? "found" : "not found";
  const stateClass = info.stateExists ? "watch-path-ok" : "watch-path-missing";
  const historyClass = info.historyExists ? "watch-path-ok" : "watch-path-missing";

  const noDataWarning = !info.hasAnyXitData ? `
    <div class="workspace-no-data">
      当前工作区没有记录 — No XiT run state found in this workspace.<br>
      Open the xit project folder or run <code>xit auto &lt;command&gt;</code> inside this workspace.
    </div>` : "";

  return `
    <section class="workspace-watch-banner">
      <div class="workspace-watch-header">Watching workspace</div>
      <div class="workspace-watch-root mono">${escapeHtml(info.workspaceRoot)}</div>
      <div class="workspace-watch-paths">
        <span class="watch-path-item ${stateClass}">
          State: <span class="mono">.xit/state/current-run.json</span> — ${stateLabel}
        </span>
        <span class="watch-path-item ${historyClass}">
          History: <span class="mono">.xit/history.jsonl</span> — ${historyLabel}
        </span>
      </div>
      ${noDataWarning}
    </section>
  `;
}

function buildStatusMeta(
  status: XiTStatus,
  latestRun: LatestRun | undefined,
): {
  heroTitle: string;
  heroSubtitle: string;
  pillLabel: string;
  pillTone: "running" | "success" | "idle" | "warning";
} {
  const currentRunState = readCurrentRunState();
  if (status.state === "binary-not-found") {
    return {
      heroTitle: "XiT 未连接",
      heroSubtitle: "未找到本地 XiT CLI，Dashboard 只能显示有限状态。",
      pillLabel: "未找到 XiT",
      pillTone: "warning",
    };
  }

  if (currentRunState?.status === "running") {
    return {
      heroTitle: "XiT 正在处理高噪音输出",
      heroSubtitle: "当前命令正在吸T中，摘要会在 run 完成后更新。",
      pillLabel: "正在吸T中",
      pillTone: "running",
    };
  }

  if (latestRun) {
    return {
      heroTitle: "XiT 最近一次压缩已完成",
      heroSubtitle: "当前工作区已有可用节省结果与路由记录。",
      pillLabel: "吸T完成",
      pillTone: "success",
    };
  }

  return {
    heroTitle: "XiT 正在守护你的高噪音命令",
    heroSubtitle: "本地降噪 · Token 节省 · AI Agent Routing",
    pillLabel: "守护中",
    pillTone: "idle",
  };
}

function getCurrentStatusLabel(status: XiTStatus, latestRun: LatestRun | undefined): string {
  const meta = buildStatusMeta(status, latestRun);
  if (meta.pillLabel === "守护中") {
    return "守护你的T";
  }
  return meta.pillLabel;
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

function renderKeyValueRow(
  label: string,
  value: string,
  options?: {
    mono?: boolean;
    truncate?: boolean;
    title?: string;
    href?: string;
  },
): string {
  const classNames = [
    "kv-value",
    options?.mono ? "mono" : "",
    options?.truncate ? "truncate" : "",
  ]
    .filter(Boolean)
    .join(" ");
  const titleAttr = options?.title
    ? ` title="${escapeHtml(options.title)}"`
    : "";
  const content = options?.href
    ? `<a class="${classNames}" href="${options.href}"${titleAttr}>${escapeHtml(value)}</a>`
    : `<span class="${classNames}"${titleAttr}>${escapeHtml(value)}</span>`;
  return `
    <div class="kv-row">
      <span class="kv-label">${escapeHtml(label)}</span>
      ${content}
    </div>
  `;
}

function renderAdapterCard(item: AdapterHealthItem): string {
  const routed = item.routedCount ?? 0;
  const observed = item.observedCount ?? 0;
  const ratio = observed > 0 ? `${routed}/${observed} routed` : "No routed sample";
  const evidence = item.ruleFiles.length > 0
    ? `${path.basename(item.ruleFiles[0])}${observed > 0 ? ` · ${ratio}` : ""}`
    : item.evidence;

  return `
    <article class="adapter-card">
      <div class="adapter-topline">
        <div class="adapter-name">${escapeHtml(item.adapter)}</div>
        <span class="status-pill ${escapeHtml(item.status).replace(/\s+/g, "-")}">${escapeHtml(item.status)}</span>
      </div>
      <div class="adapter-evidence" title="${escapeHtml(item.evidence)}">${escapeHtml(evidence)}</div>
      <div class="adapter-ratio">${escapeHtml(ratio)}</div>
    </article>
  `;
}

function renderEventRows(events: AdapterEvent[]): string {
  return events
    .map((event) => {
      const time = event.time ? formatTime(event.time) : "-";
      const command = event.original_command || event.event || event.action || "-";
      return `
        <tr>
          <td>${escapeHtml(time)}</td>
          <td>${escapeHtml(event.adapter || "-")}</td>
          <td class="mono truncate-cell" title="${escapeHtml(command)}">${escapeHtml(command)}</td>
          <td>${escapeHtml(event.policy || "-")}</td>
        </tr>
      `;
    })
    .join("");
}

function renderTerminalRows(
  terminalEvents: {
    time: string;
    commandLine: string;
    terminalName: string;
    cwd?: string;
  }[],
): string {
  return terminalEvents
    .map((event) => {
      const time = event.time ? formatTime(event.time) : "-";
      return `
        <tr>
          <td>${escapeHtml(time)}</td>
          <td>${escapeHtml(event.terminalName)}</td>
          <td class="mono truncate-cell" title="${escapeHtml(event.commandLine)}">${escapeHtml(event.commandLine)}</td>
          <td class="truncate-cell" title="${escapeHtml(event.cwd || "-")}">${escapeHtml(event.cwd || "-")}</td>
        </tr>
      `;
    })
    .join("");
}

function agentTurnStatusLabel(status: AgentTurnView["status"]): string {
  switch (status) {
    case "working": return "AI 正在工作";
    case "xit_running": return "正在吸T中";
    case "completed": return "本轮已完成";
    case "stopped": return "已停止";
    case "idle": return "空闲";
    default: return "未知";
  }
}

function agentTurnStatusTone(status: AgentTurnView["status"]): string {
  switch (status) {
    case "working": return "running";
    case "xit_running": return "running";
    case "completed": return "success";
    case "stopped": return "warning";
    default: return "idle";
  }
}

function renderAgentTurnSection(turn: AgentTurnView, hasAnyXitData: boolean): string {
  if (!hasAnyXitData || (turn.status === "idle" && turn.commandsObserved === 0)) {
    return `<p class="empty-state">当前 workspace 暂无 agent turn 记录 — 已安装 workspace rules 后，让 Claude/Codex 正常执行一次任务即可生成记录</p>`;
  }

  const tone = agentTurnStatusTone(turn.status);
  const statusLabel = agentTurnStatusLabel(turn.status);
  const adapterLabel = turn.adapter === "unknown" ? "—" : turn.adapter;
  const updatedLabel = turn.updatedAt ? new Date(turn.updatedAt).toLocaleTimeString() : "—";
  const startedLabel = turn.startedAt ? new Date(turn.startedAt).toLocaleTimeString() : "—";
  const hasTurnLifecycle = turn.adapter === "kimi";

  return `
    <div class="agent-turn-card">
      <div class="agent-turn-header">
        <div class="agent-turn-adapter">${escapeHtml(adapterLabel.toUpperCase())}</div>
        <span class="status-pill ${escapeHtml(tone)}">${escapeHtml(statusLabel)}</span>
        ${!hasTurnLifecycle ? `<span class="turn-no-lifecycle" title="仅有命令路由事件，无 UserPromptSubmit/Stop lifecycle">命令路由模式</span>` : ""}
      </div>
      <div class="agent-turn-metrics">
        <div class="metric-tile">
          <div class="metric-label">本轮命令</div>
          <div class="metric-value">${turn.commandsObserved}</div>
        </div>
        <div class="metric-tile">
          <div class="metric-label">路由 XiT</div>
          <div class="metric-value">${turn.routedThroughXit} / ${turn.commandsObserved}</div>
        </div>
        <div class="metric-tile ${turn.savedTokensThisTurn > 0 ? "highlight" : ""}">
          <div class="metric-label">本轮节省</div>
          <div class="metric-value">${escapeHtml(turn.savedTokensDisplay)}</div>
        </div>
        <div class="metric-tile">
          <div class="metric-label">最近更新</div>
          <div class="metric-value">${escapeHtml(updatedLabel)}</div>
        </div>
      </div>
      ${turn.latestEvent ? `
      <div class="kv-row">
        <span class="kv-label">Latest event</span>
        <span class="kv-value">${escapeHtml(turn.latestEvent)}</span>
      </div>` : ""}
      ${turn.startedAt ? `
      <div class="kv-row">
        <span class="kv-label">Turn started</span>
        <span class="kv-value">${escapeHtml(startedLabel)}</span>
      </div>` : ""}
    </div>
    ${!hasTurnLifecycle ? `
    <p class="turn-note">
      ${escapeHtml(adapterLabel)} conversation hooks: 命令路由模式（PreToolUse 级别）。
      无 UserPromptSubmit / Stop lifecycle 记录。
      Kimi hooks 有完整 turn lifecycle；Claude/Codex/Cursor 仅记录命令路由事件。
    </p>` : ""}
  `;
}

function buildDashboardHtml(
  status: XiTStatus,
  events: AdapterEvent[],
  terminalEvents: {
    time: string;
    commandLine: string;
    terminalName: string;
    cwd?: string;
  }[],
  latestRun: LatestRun | undefined,
  cspSource: string,
  stylesheetHref: string,
): string {
  const gain = status.gain;
  const activity = status.activity || computeActivityFromEvents(events);
  const health = computeWorkflowHealth(status, latestRun);
  const tokenImpact = getTokenImpactStats(latestRun);
  const adapterHealth = getAiAdapterHealth();
  const currentRunState = readCurrentRunState();
  const statusMeta = buildStatusMeta(status, latestRun);
  const agentTurn = buildAgentTurnView();
  const hookConnectivity = getAdapterHookConnectivity();

  // Only treat current-run.json as active when it is freshly running (status=running + heartbeat ≤15s).
  // Any stale completed state — even one with raw_bytes>0 — must not override history.jsonl.
  const validCurrentRun = isCurrentRunFreshAndRunning(currentRunState ?? undefined)
    ? currentRunState
    : undefined;

  // Latest entry in history with positive savings (used for the "Latest Saved" card).
  const allRuns = readAllWorkspaceRuns();
  const latestPositiveSavedRun = selectLatestSavedRun(allRuns);

  const hasAnyRun = !!(validCurrentRun || latestRun);

  const latestCommand = validCurrentRun?.command || latestRun?.command;
  const latestRawLog = validCurrentRun?.raw_log || latestRun?.raw_log;
  const latestRawTokens =
    typeof validCurrentRun?.raw_bytes === "number" && validCurrentRun.raw_bytes > 0
      ? Math.max(0, Math.round(validCurrentRun.raw_bytes / 4))
      : tokenImpact.latest?.rawTokens;
  const latestSummaryTokens =
    typeof validCurrentRun?.summary_bytes === "number" && validCurrentRun.summary_bytes > 0
      ? Math.max(0, Math.round(validCurrentRun.summary_bytes / 4))
      : tokenImpact.latest?.summaryTokens;
  const latestSavedDisplay =
    validCurrentRun?.saved_tokens_display ||
    (typeof validCurrentRun?.saved_tokens === "number"
      ? formatTokenShort(validCurrentRun.saved_tokens)
      : undefined) ||
    formatSavedTokensForRun(latestRun);
  const latestReductionDisplay =
    typeof validCurrentRun?.estimated_reduction === "number"
      ? formatReduction(validCurrentRun.estimated_reduction)
      : latestRun
        ? formatReduction(latestRun.estimated_reduction)
        : "--";
  const latestExitCode =
    typeof validCurrentRun?.exit_code === "number"
      ? String(validCurrentRun.exit_code)
      : latestRun
        ? String(latestRun.exit_code)
        : "--";
  const latestDuration = latestRun
    ? `${(latestRun.duration_ms / 1000).toFixed(1)}s`
    : validCurrentRun?.started_at && validCurrentRun.heartbeat_at
      ? "running"
      : "--";

  // Workspace total: prefer history aggregation; fall back to gain.saved_bytes if history is empty.
  const gainFallbackTokens = !tokenImpact.workspaceTotalSavedTokens && gain?.saved_bytes
    ? Math.max(0, Math.round(gain.saved_bytes / 4))
    : 0;
  const workspaceTotalSavedDisplay = tokenImpact.workspaceTotalSavedTokens > 0
    ? tokenImpact.workspaceTotalSavedDisplay
    : gainFallbackTokens > 0
      ? formatTokenShort(gainFallbackTokens)
      : "0 Token";

  const workspaceWatchInfo = getWorkspaceWatchInfo();
  const topCommands = tokenImpact.topTokenHeavyCommands;
  const topCommandsPrimary = topCommands.slice(0, 5);
  const topCommandsExtra = topCommands.slice(5);
  const recentEventsPrimary = events.slice(0, 5);
  const recentEventsExtra = events.slice(5, 20);

  // Critical errors only — shown in main banner.
  const hardErrors: string[] = [];
  if (status.state === "binary-not-found") {
    hardErrors.push("未找到 XiT CLI，请运行 npm install -g xitsg 安装");
  }

  // Low-severity notes relegated to Debug panel only.
  const debugNotes: string[] = [];
  if (status.state === "gain-json-failed") {
    debugNotes.push("xit gain --json 未返回有效 JSON");
  }
  if (status.error && status.state !== "binary-not-found") {
    debugNotes.push(status.error);
  }
  if (gain?.warnings?.length) {
    debugNotes.push(...gain.warnings);
  }

  const rawLogHref = latestRawLog ? buildCommandUri("xit.openLatestRawLog") : undefined;

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
        <p class="hero-subtitle">${escapeHtml(
          statusMeta.heroSubtitle || "本地降噪 · Token 节省 · AI Agent Routing",
        )}</p>
      </div>
      <div class="hero-status">
        <span class="status-pill hero ${statusMeta.pillTone}">${escapeHtml(statusMeta.pillLabel)}</span>
        <div class="hero-status-copy">${escapeHtml(statusMeta.heroTitle)}</div>
      </div>
    </section>

    ${renderWorkspaceWatchBanner(workspaceWatchInfo)}

    ${
      hardErrors.length > 0
        ? `<section class="banner warning">${escapeHtml(hardErrors.join(" · "))}</section>`
        : ""
    }

    <section class="summary-grid">
      ${renderSummaryCard(
        "Current Status",
        getCurrentStatusLabel(status, latestRun),
        status.available ? "XiT 工作区守护状态" : "需要本地 XiT CLI",
        status.state === "binary-not-found" ? "warning" : "accent",
      )}
      ${(() => {
        if (!workspaceWatchInfo.hasAnyXitData) {
          return renderSummaryCard("Latest Saved", "—", "当前工作区没有记录", "muted-zero" as const);
        }
        // Use the latest history entry with positive savings — never let a stale zero-gain run override it.
        const positiveSavedVal = latestPositiveSavedRun
          ? formatSavedTokensForRun(latestPositiveSavedRun)
          : undefined;
        const savedVal = positiveSavedVal || "0 Token";
        const isZero = savedVal === "0 Token" || savedVal === "0";
        const tone = isZero ? ("muted-zero" as const) : ("success" as const);
        const positiveReduction = latestPositiveSavedRun
          ? `降噪 ${formatReduction(latestPositiveSavedRun.estimated_reduction)}`
          : "等待下一次 run";
        return renderSummaryCard("Latest Saved", savedVal, positiveReduction, tone);
      })()}
      ${renderSummaryCard(
        "Today Saved",
        workspaceWatchInfo.hasAnyXitData ? tokenImpact.todaySavedDisplay : "—",
        workspaceWatchInfo.hasAnyXitData ? "今日累计" : "当前工作区没有记录",
        "neutral",
      )}
      ${renderSummaryCard(
        "Workspace Total",
        workspaceWatchInfo.hasAnyXitData ? workspaceTotalSavedDisplay : "—",
        workspaceWatchInfo.hasAnyXitData ? "累计节省" : "当前工作区没有记录",
        "neutral",
      )}
    </section>

    <section class="panel">
      <div class="section-heading">
        <h2>Current Agent Turn</h2>
        <span class="section-note">AI 对话轮次感知（基于本地 hook 事件，不读取聊天内容）</span>
      </div>
      ${renderAgentTurnSection(agentTurn, workspaceWatchInfo.hasAnyXitData)}
    </section>

    <section class="panel">
      <div class="section-heading">
        <h2>Current / Latest Run</h2>
        <span class="section-note">最新一轮命令与压缩结果</span>
      </div>
      ${hasAnyRun ? `
      <div class="run-grid">
        <div class="run-card">
          ${renderKeyValueRow("Command", latestCommand ?? "—", {
            mono: true,
            truncate: true,
            title: latestCommand ?? "—",
          })}
          ${renderKeyValueRow("Status", validCurrentRun?.status || (latestRun ? "completed" : "idle"))}
          ${renderKeyValueRow("Exit code", latestExitCode)}
          ${renderKeyValueRow("Duration", latestDuration)}
          ${
            latestRawLog
              ? renderKeyValueRow("Raw log", latestRawLog, {
                  mono: true,
                  truncate: true,
                  title: latestRawLog,
                  href: rawLogHref,
                })
              : renderKeyValueRow("Raw log", "暂无 raw log")
          }
        </div>
        <div class="metrics-grid compact">
          ${renderMetricItem("原始输出", typeof latestRawTokens === "number" ? formatTokenShort(latestRawTokens) : "--")}
          ${renderMetricItem("吸后摘要", typeof latestSummaryTokens === "number" ? formatTokenShort(latestSummaryTokens) : "--")}
          ${renderMetricItem("本次节省", latestSavedDisplay || "0 Token", true)}
          ${renderMetricItem("降噪率", latestReductionDisplay)}
        </div>
      </div>
      ` : `<p class="empty-state">${workspaceWatchInfo.hasAnyXitData ? "暂无最近运行 — 运行一次高噪音命令后，这里会显示本次吸T结果。" : "当前工作区没有记录 — 请在此工作区运行 xit auto 命令，或切换到含有 .xit 数据的工作区。"}</p>`}
    </section>

    <section class="panel">
      <div class="section-heading">
        <h2>AI Adapter Health</h2>
        <span class="section-note">Codex / Claude / Gemini / Cursor</span>
      </div>
      <div class="adapter-grid">
        ${adapterHealth.map(renderAdapterCard).join("")}
      </div>
    </section>

    <section class="panel">
      <div class="section-heading">
        <h2>Token Impact</h2>
        <span class="section-note">最近一次、今日和工作区累计</span>
      </div>
      <div class="metrics-grid six-up">
        ${renderMetricItem("Latest raw tokens", tokenImpact.latest ? formatTokenShort(tokenImpact.latest.rawTokens) : "--")}
        ${renderMetricItem("Latest summary tokens", tokenImpact.latest ? formatTokenShort(tokenImpact.latest.summaryTokens) : "--")}
        ${renderMetricItem("Latest saved tokens", tokenImpact.latest?.savedDisplay || "0 Token", true)}
        ${renderMetricItem("Latest reduction %", tokenImpact.latest ? `${Math.round(tokenImpact.latest.reductionPct)}%` : "--")}
        ${renderMetricItem("Today saved tokens", tokenImpact.todaySavedDisplay)}
        ${renderMetricItem("Workspace total saved tokens", tokenImpact.workspaceTotalSavedDisplay)}
      </div>
    </section>

    <section class="panel">
      <div class="section-heading">
        <h2>Top Token-Heavy Commands</h2>
        <span class="section-note">最值得优先使用 XiT 的命令</span>
      </div>
      ${
        topCommandsPrimary.length > 0
          ? `
            <div class="table-wrap">
              <table>
                <thead>
                  <tr><th>Command</th><th>Runs</th><th>Saved</th><th>Reduction</th></tr>
                </thead>
                <tbody>
                  ${topCommandsPrimary
                    .map((entry) => {
                      const reduction =
                        entry.rawTokens > 0
                          ? `${Math.round((entry.savedTokens / entry.rawTokens) * 100)}%`
                          : "--";
                      return `
                        <tr>
                          <td class="mono truncate-cell" title="${escapeHtml(entry.command)}">${escapeHtml(entry.command)}</td>
                          <td>${entry.runs}</td>
                          <td class="saved-emphasis">${escapeHtml(entry.savedDisplay)}</td>
                          <td>${reduction}</td>
                        </tr>
                      `;
                    })
                    .join("")}
                </tbody>
              </table>
            </div>
            ${
              topCommandsExtra.length > 0
                ? `
                  <details class="details-block">
                    <summary>Show more</summary>
                    <div class="table-wrap">
                      <table>
                        <thead>
                          <tr><th>Command</th><th>Runs</th><th>Saved</th><th>Reduction</th></tr>
                        </thead>
                        <tbody>
                          ${topCommandsExtra
                            .map((entry) => {
                              const reduction =
                                entry.rawTokens > 0
                                  ? `${Math.round((entry.savedTokens / entry.rawTokens) * 100)}%`
                                  : "--";
                              return `
                                <tr>
                                  <td class="mono truncate-cell" title="${escapeHtml(entry.command)}">${escapeHtml(entry.command)}</td>
                                  <td>${entry.runs}</td>
                                  <td class="saved-emphasis">${escapeHtml(entry.savedDisplay)}</td>
                                  <td>${reduction}</td>
                                </tr>
                              `;
                            })
                            .join("")}
                        </tbody>
                      </table>
                    </div>
                  </details>
                `
                : ""
            }
          `
          : `<p class="empty-state">No token-heavy commands recorded yet.</p>`
      }
    </section>

    <section class="panel">
      <div class="section-heading">
        <h2>Recent Events</h2>
        <span class="section-note">默认显示最近 5 条</span>
      </div>
      ${
        recentEventsPrimary.length > 0
          ? `
            <div class="table-wrap">
              <table>
                <thead>
                  <tr><th>Time</th><th>Adapter</th><th>Event / Command</th><th>Policy</th></tr>
                </thead>
                <tbody>${renderEventRows(recentEventsPrimary)}</tbody>
              </table>
            </div>
            ${
              recentEventsExtra.length > 0
                ? `
                  <details class="details-block">
                    <summary>Show older events</summary>
                    <div class="table-wrap">
                      <table>
                        <thead>
                          <tr><th>Time</th><th>Adapter</th><th>Event / Command</th><th>Policy</th></tr>
                        </thead>
                        <tbody>${renderEventRows(recentEventsExtra)}</tbody>
                      </table>
                    </div>
                  </details>
                `
                : ""
            }
          `
          : `<p class="empty-state">No recent events.</p>`
      }
    </section>

    <details class="debug-panel">
      <summary>Advanced / Debug</summary>
      <div class="debug-grid">
        ${debugNotes.length > 0 ? `
        <div class="debug-card full-span">
          <h3>Notes</h3>
          ${debugNotes.map((n) => `<div class="kv-row"><span class="kv-value">${escapeHtml(n)}</span></div>`).join("")}
        </div>
        ` : ""}
        <div class="debug-card">
          <h3>Paths</h3>
          ${renderKeyValueRow("Binary path", status.binary || "Not resolved", {
            mono: true,
            truncate: true,
            title: status.binary || "Not resolved",
          })}
          ${renderKeyValueRow("Workspace cwd", status.cwd || "Unknown", {
            mono: true,
            truncate: true,
            title: status.cwd || "Unknown",
          })}
          ${
            latestRawLog
              ? renderKeyValueRow("Raw log full path", latestRawLog, {
                  mono: true,
                  truncate: true,
                  title: latestRawLog,
                })
              : ""
          }
          ${renderKeyValueRow(
            "Attempted paths",
            status.attempts?.join(" , ") || "None",
            {
              mono: true,
              truncate: true,
              title: status.attempts?.join("\n") || "None",
            },
          )}
        </div>

        <div class="debug-card">
          <h3>Workspace / Global</h3>
          ${renderKeyValueRow(
            "Workspace commands condensed",
            gain ? String(gain.total_commands_condensed) : "0",
          )}
          ${renderKeyValueRow(
            "Workspace saved bytes",
            gain ? formatBytes(gain.saved_bytes) : "0 B",
          )}
          ${renderKeyValueRow(
            "Recent routed",
            `${health.recentHighNoiseRouted}/${health.recentHighNoiseCommands}`,
          )}
          ${renderKeyValueRow(
            "Latest adapter",
            activity.latestAdapter || "None",
          )}
          ${renderKeyValueRow(
            "Latest policy",
            activity.latestPolicy || "None",
          )}
        </div>

        <div class="debug-card">
          <h3>Adapters Raw Counts</h3>
          ${Object.entries(activity.adapterCounts).length > 0
            ? Object.entries(activity.adapterCounts)
                .map(([adapter, count]) => renderKeyValueRow(adapter, String(count)))
                .join("")
            : '<p class="empty-state">No adapter counts recorded.</p>'}
        </div>

        <div class="debug-card">
          <h3>Agent Conversation Hooks</h3>
          ${Object.entries(hookConnectivity).map(([adapter, info]) => {
            const hookStatus = info.connected
              ? (info.hasTurnLifecycle ? "connected (turn lifecycle)" : "connected (command routing only)")
              : "not connected";
            const detail = info.connected && info.latestEventTime
              ? `${info.eventCount} events, last ${info.latestEventTime}`
              : "no events";
            return renderKeyValueRow(adapter, `${hookStatus} — ${detail}`);
          }).join("")}
          <div class="kv-row" style="margin-top:6px">
            <span class="kv-value" style="font-size:0.75rem;opacity:0.6">
              VS Code extension 不读取聊天内容，只使用本地 hook metadata。
              Claude/Codex/Cursor = 命令路由; Kimi = 完整 turn lifecycle。
            </span>
          </div>
        </div>

        <div class="debug-card full-span">
          <h3>VS Code Terminal Events</h3>
          ${
            terminalEvents.length > 0
              ? `
                <div class="table-wrap">
                  <table>
                    <thead>
                      <tr><th>Time</th><th>Terminal</th><th>Command</th><th>CWD</th></tr>
                    </thead>
                    <tbody>${renderTerminalRows(terminalEvents.slice(0, 10))}</tbody>
                  </table>
                </div>
              `
              : '<p class="empty-state">No terminal events recorded.</p>'
          }
        </div>
      </div>
    </details>

    <footer class="dashboard-footer">
      <span>Local only. No telemetry. No network requests.</span>
      <a href="${buildCommandUri("xit.refresh")}">Refresh Dashboard</a>
    </footer>
  </main>
</body>
</html>`;
}

export function showDashboard(
  context: vscode.ExtensionContext,
  status: XiTStatus,
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

  const events = gatherAllEvents();
  const terminalEvents = readTerminalEvents(20);
  const latestRun = readLatestRun();
  const stylesheetHref = panel.webview
    .asWebviewUri(vscode.Uri.joinPath(mediaRoot, "dashboard.css"))
    .toString();
  panel.webview.html = buildDashboardHtml(
    status,
    events,
    terminalEvents,
    latestRun,
    panel.webview.cspSource,
    stylesheetHref,
  );
}

export function updateDashboardIfOpen(status: XiTStatus): void {
  if (!panel) {
    return;
  }
  const events = gatherAllEvents();
  const terminalEvents = readTerminalEvents(20);
  const latestRun = readLatestRun();
  const stylesheetHref = panel.webview
    .asWebviewUri(vscode.Uri.joinPath(panel.webview.options.localResourceRoots![0], "dashboard.css"))
    .toString();
  panel.webview.html = buildDashboardHtml(
    status,
    events,
    terminalEvents,
    latestRun,
    panel.webview.cspSource,
    stylesheetHref,
  );
}
