import * as path from "path";
import * as vscode from "vscode";
import type {
  AdapterEvent,
  AdapterHealthItem,
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
} from "./xit";
import {
  computeWorkflowHealth,
  formatSavedTokensForRun,
  getAiAdapterHealth,
  getTokenImpactStats,
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
  tone: "neutral" | "accent" | "success" | "warning",
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

  const latestCommand = currentRunState?.command || latestRun?.command || "No recent XiT run";
  const latestRawLog = currentRunState?.raw_log || latestRun?.raw_log;
  const latestRawTokens =
    typeof currentRunState?.raw_bytes === "number" && currentRunState.raw_bytes > 0
      ? Math.max(0, Math.round(currentRunState.raw_bytes / 4))
      : tokenImpact.latest?.rawTokens;
  const latestSummaryTokens =
    typeof currentRunState?.summary_bytes === "number" && currentRunState.summary_bytes > 0
      ? Math.max(0, Math.round(currentRunState.summary_bytes / 4))
      : tokenImpact.latest?.summaryTokens;
  const latestSavedDisplay =
    currentRunState?.saved_tokens_display ||
    (typeof currentRunState?.saved_tokens === "number"
      ? formatTokenShort(currentRunState.saved_tokens)
      : undefined) ||
    formatSavedTokensForRun(latestRun);
  const latestReductionDisplay =
    typeof currentRunState?.estimated_reduction === "number"
      ? formatReduction(currentRunState.estimated_reduction)
      : latestRun
        ? formatReduction(latestRun.estimated_reduction)
        : "--";
  const latestExitCode =
    typeof currentRunState?.exit_code === "number"
      ? String(currentRunState.exit_code)
      : latestRun
        ? String(latestRun.exit_code)
        : "--";
  const latestDuration = latestRun
    ? `${(latestRun.duration_ms / 1000).toFixed(1)}s`
    : currentRunState?.started_at && currentRunState.heartbeat_at
      ? "running"
      : "--";

  const topCommands = tokenImpact.topTokenHeavyCommands;
  const topCommandsPrimary = topCommands.slice(0, 5);
  const topCommandsExtra = topCommands.slice(5);
  const recentEventsPrimary = events.slice(0, 5);
  const recentEventsExtra = events.slice(5, 20);
  const hardErrors: string[] = [];

  if (status.state === "binary-not-found") {
    hardErrors.push("XiT binary not found");
  }
  if (status.state === "gain-json-failed") {
    hardErrors.push("xit gain --json did not return valid JSON");
  }
  if (status.error) {
    hardErrors.push(status.error);
  }
  if (gain?.warnings?.length) {
    hardErrors.push(...gain.warnings);
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
      ${renderSummaryCard(
        "Latest Saved",
        latestSavedDisplay || "0 Token",
        latestReductionDisplay === "--" ? "等待下一次 run" : `降噪 ${latestReductionDisplay}`,
        "success",
      )}
      ${renderSummaryCard(
        "Today Saved",
        tokenImpact.todaySavedDisplay,
        "今日累计",
        "neutral",
      )}
      ${renderSummaryCard(
        "Workspace Total",
        tokenImpact.workspaceTotalSavedDisplay,
        "累计节省",
        "neutral",
      )}
    </section>

    <section class="panel">
      <div class="section-heading">
        <h2>Current / Latest Run</h2>
        <span class="section-note">最新一轮命令与压缩结果</span>
      </div>
      <div class="run-grid">
        <div class="run-card">
          ${renderKeyValueRow("Command", latestCommand, {
            mono: true,
            truncate: true,
            title: latestCommand,
          })}
          ${renderKeyValueRow("Status", currentRunState?.status || (latestRun ? "completed" : "idle"))}
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
              : renderKeyValueRow("Raw log", "No raw log yet")
          }
        </div>
        <div class="metrics-grid compact">
          ${renderMetricItem("原始输出", typeof latestRawTokens === "number" ? formatTokenShort(latestRawTokens) : "--")}
          ${renderMetricItem("吸后摘要", typeof latestSummaryTokens === "number" ? formatTokenShort(latestSummaryTokens) : "--")}
          ${renderMetricItem("本次节省", latestSavedDisplay || "0 Token", true)}
          ${renderMetricItem("降噪率", latestReductionDisplay)}
        </div>
      </div>
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
