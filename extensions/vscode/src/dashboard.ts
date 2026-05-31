import * as vscode from 'vscode';
import type { AdapterEvent, GlobalActivity, XiTStatus, LatestRun } from './types';
import { readRecentEvents, readWorkspaceHistory, readTerminalEvents, readLatestRun } from './xit';

let panel: vscode.WebviewPanel | undefined;
let panelContext: vscode.ExtensionContext | undefined;

function escapeHtml(text: string): string {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#039;');
}

function formatBytes(n: number): string {
  if (n >= 1000000) {
    return (n / 1000000).toFixed(1) + ' MB';
  }
  if (n >= 1000) {
    return (n / 1000).toFixed(1) + ' kB';
  }
  return n + ' B';
}

function formatReduction(r: number): string {
  return (r * 100).toFixed(1) + '%';
}

function formatTime(iso: string): string {
  try {
    return new Date(iso).toLocaleString();
  } catch {
    return iso;
  }
}

function gatherAllEvents(): AdapterEvent[] {
  const adapters = ['cursor', 'codex', 'claude', 'kimi'];
  let allEvents: AdapterEvent[] = [];
  for (const a of adapters) {
    allEvents = allEvents.concat(readRecentEvents(a, 10));
  }
  const workspaceEvents = readWorkspaceHistory(10);
  allEvents = allEvents.concat(workspaceEvents);
  allEvents.sort((a, b) => {
    const ta = a.time || '';
    const tb = b.time || '';
    return tb.localeCompare(ta);
  });
  return allEvents;
}

function computeActivityFromEvents(events: AdapterEvent[]): GlobalActivity {
  const adapterCounts: Record<string, number> = {};
  for (const e of events) {
    if (e.adapter) {
      adapterCounts[e.adapter] = (adapterCounts[e.adapter] || 0) + 1;
    }
  }
  const sorted = [...events].sort((a, b) => (b.time || '').localeCompare(a.time || ''));
  const latest = sorted[0];
  return {
    latestAdapter: latest?.adapter,
    latestTime: latest?.time,
    latestCommand: latest?.original_command,
    latestPolicy: latest?.policy,
    eventCount: events.length,
    adapterCounts,
  };
}

function buildDashboardHtml(
  status: XiTStatus,
  events: AdapterEvent[],
  terminalEvents: { time: string; commandLine: string; terminalName: string; cwd?: string }[],
  latestRun: LatestRun | undefined,
  cspSource: string
): string {
  const gain = status.gain;
  const hasWorkspaceGain = gain && gain.total_commands_condensed > 0;
  const activity = status.activity || computeActivityFromEvents(events);
  const hasGlobalActivity = activity.eventCount > 0;

  // Latest XiT Run section
  const hasLatestRun = latestRun !== undefined;
  const latestSavedBytes = hasLatestRun ? (latestRun.raw_bytes - latestRun.summary_bytes) : 0;
  const latestRunSection = hasLatestRun ? `
    <div class="latest-run">
      <div class="ga-row"><span class="ga-label">Command</span><span class="ga-value ga-cmd">${escapeHtml(latestRun.command)}</span></div>
      <div class="ga-row"><span class="ga-label">Executed</span><span class="ga-value">xit auto ${escapeHtml(latestRun.command)}</span></div>
      <div class="ga-row"><span class="ga-label">Exit code</span><span class="ga-value">${latestRun.exit_code}</span></div>
      <div class="ga-row"><span class="ga-label">Reduction</span><span class="ga-value">${formatReduction(latestRun.estimated_reduction)}</span></div>
      <div class="ga-row"><span class="ga-label">Saved bytes</span><span class="ga-value">${formatBytes(latestSavedBytes)}</span></div>
      <div class="ga-row"><span class="ga-label">Duration</span><span class="ga-value">${(latestRun.duration_ms / 1000).toFixed(1)}s</span></div>
      <div class="ga-row"><span class="ga-label">Raw log</span><span class="ga-value ga-cmd">${escapeHtml(latestRun.raw_log)}</span></div>
    </div>
  ` : '<p class="empty">No recent XiT run found. Use <strong>XiT: Run Command</strong> or run <code>xit auto</code> in terminal.</p>';

  // Diagnostic section (binary missing / JSON error)
  const hardErrors: string[] = [];
  if (status.state === 'binary-not-found') {
    hardErrors.push('XiT binary not found.');
  }
  if (status.state === 'gain-json-failed') {
    hardErrors.push('xit gain --json did not return valid JSON.');
  }
  if (status.error) {
    hardErrors.push(status.error);
  }
  if (status.binary) {
    hardErrors.push(`Binary: ${status.binary}`);
  }
  if (status.cwd) {
    hardErrors.push(`cwd: ${status.cwd}`);
  }
  if (status.attempts && status.attempts.length > 0) {
    hardErrors.push(`Attempted: ${status.attempts.join(', ')}`);
  }
  if (gain?.warnings && gain.warnings.length > 0) {
    hardErrors.push(`Warnings: ${gain.warnings.join('; ')}`);
  }

  // Workspace gain stats
  const topCommandsRows = gain?.top_commands
    .map(
      (c) => `
      <tr>
        <td>${escapeHtml(c.command)}</td>
        <td>${c.runs}</td>
        <td>${c.saved_tokens_display}</td>
        <td>${formatBytes(c.saved_bytes)}</td>
      </tr>
    `
    )
    .join('') || '';

  // Recent events table
  const eventRows = events
    .slice(0, 20)
    .map((e) => {
      const time = e.time ? escapeHtml(formatTime(e.time)) : '-';
      const cmd = e.original_command ? escapeHtml(e.original_command) : e.event || e.action || '-';
      const adapter = e.adapter ? escapeHtml(e.adapter) : '-';
      const policy = e.policy ? escapeHtml(e.policy) : '';
      return `
        <tr>
          <td>${time}</td>
          <td>${adapter}</td>
          <td>${cmd}</td>
          <td>${policy}</td>
        </tr>
      `;
    })
    .join('') || '';

  // VS Code Terminal events table
  const terminalEventRows = terminalEvents
    .slice(0, 20)
    .map((e) => {
      const time = e.time ? escapeHtml(formatTime(e.time)) : '-';
      const cmd = escapeHtml(e.commandLine);
      const term = escapeHtml(e.terminalName);
      const cwd = e.cwd ? escapeHtml(e.cwd) : '';
      return `
        <tr>
          <td>${time}</td>
          <td>${term}</td>
          <td><code>${cmd}</code></td>
          <td>${cwd}</td>
        </tr>
      `;
    })
    .join('') || '';

  // Adapter cards — active = has events in current session data
  const adapters = ['Cursor', 'Codex', 'Claude', 'Kimi', 'Antigravity', 'Aider'];
  const adapterCards = adapters
    .map((a) => {
      const lower = a.toLowerCase();
      const count = activity.adapterCounts[lower] || 0;
      const isActive = count > 0;
      const statusClass = isActive ? 'active' : 'inactive';
      const badge = isActive ? ` <span class="badge">${count}</span>` : '';
      return `<div class="card adapter ${statusClass}">${escapeHtml(a)}${badge}</div>`;
    })
    .join('');

  // Global activity summary block
  const globalActivityBlock = hasGlobalActivity ? `
    <div class="global-activity">
      <div class="ga-row">
        <span class="ga-label">Latest adapter</span>
        <span class="ga-value">${activity.latestAdapter ? escapeHtml(activity.latestAdapter) : '-'}</span>
      </div>
      <div class="ga-row">
        <span class="ga-label">Last event</span>
        <span class="ga-value">${activity.latestTime ? escapeHtml(formatTime(activity.latestTime)) : '-'}</span>
      </div>
      <div class="ga-row">
        <span class="ga-label">Recent events</span>
        <span class="ga-value">${activity.eventCount}</span>
      </div>
      ${activity.latestCommand ? `
      <div class="ga-row">
        <span class="ga-label">Last command</span>
        <span class="ga-value ga-cmd">${escapeHtml(activity.latestCommand)}</span>
      </div>` : ''}
    </div>
  ` : '<p class="empty">No global agent events found in ~/.xit.</p>';

  // Workspace gain section
  const workspaceGainSection = hasWorkspaceGain ? `
    <div class="grid">
      <div class="stat">
        <div class="value">${gain!.total_commands_condensed}</div>
        <div class="label">Commands condensed</div>
      </div>
      <div class="stat">
        <div class="value">${gain!.saved_tokens_display}</div>
        <div class="label">Saved tokens</div>
      </div>
      <div class="stat">
        <div class="value">${formatReduction(gain!.estimated_reduction)}</div>
        <div class="label">Reduction</div>
      </div>
      <div class="stat">
        <div class="value">${formatBytes(gain!.saved_bytes)}</div>
        <div class="label">Saved bytes</div>
      </div>
    </div>
    <h2>Top Commands</h2>
    ${gain!.top_commands.length > 0 ? `
    <table>
      <thead>
        <tr><th>Command</th><th>Runs</th><th>Saved tokens</th><th>Saved bytes</th></tr>
      </thead>
      <tbody>
        ${topCommandsRows}
      </tbody>
    </table>
    ` : '<p class="empty">No condensed commands yet.</p>'}
  ` : `
    <div class="no-workspace-gain">
      No XiT gain history for this workspace yet.<br>
      ${hasGlobalActivity ? 'Showing global agent activity from ~/.xit below.' : 'No global activity found either.'}
    </div>
  `;

  return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src ${cspSource}; script-src 'none';">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>XiT Dashboard</title>
<style>
body {
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
  color: var(--vscode-foreground, #cccccc);
  background: var(--vscode-editor-background, #1e1e1e);
  padding: 16px;
  line-height: 1.5;
}
h1 { font-size: 1.4rem; margin-bottom: 8px; }
h2 {
  font-size: 1.1rem;
  margin-top: 24px;
  margin-bottom: 8px;
  border-bottom: 1px solid var(--vscode-panel-border, #333);
  padding-bottom: 4px;
}
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
  gap: 12px;
  margin-bottom: 16px;
}
.stat {
  background: var(--vscode-editor-inactiveSelectionBackground, #2a2d2e);
  border-radius: 6px;
  padding: 12px;
}
.stat .value { font-size: 1.3rem; font-weight: 600; }
.stat .label { font-size: 0.8rem; opacity: 0.8; }
table { width: 100%; border-collapse: collapse; font-size: 0.85rem; }
th, td {
  text-align: left;
  padding: 6px 8px;
  border-bottom: 1px solid var(--vscode-panel-border, #333);
}
th { opacity: 0.7; font-weight: 500; }
.adapters { display: flex; flex-wrap: wrap; gap: 8px; margin-bottom: 16px; }
.card {
  border-radius: 6px;
  padding: 8px 12px;
  font-size: 0.85rem;
  font-weight: 500;
}
.card.adapter.active {
  background: var(--vscode-testing-iconPassed, #2ea043);
  color: var(--vscode-editor-background, #0d1117);
}
.card.adapter.inactive {
  background: var(--vscode-editor-inactiveSelectionBackground, #2a2d2e);
  opacity: 0.6;
}
.badge {
  display: inline-block;
  background: rgba(0,0,0,0.25);
  border-radius: 10px;
  padding: 0 6px;
  font-size: 0.75rem;
  margin-left: 4px;
}
.privacy {
  margin-top: 24px;
  padding: 10px 12px;
  border-radius: 6px;
  background: var(--vscode-inputValidation-infoBackground, #1e2a36);
  font-size: 0.8rem;
}
.empty { opacity: 0.6; font-size: 0.9rem; }
.diagnostic {
  border: 1px solid var(--vscode-inputValidation-warningBorder, #b89500);
  background: var(--vscode-inputValidation-warningBackground, #332b00);
  border-radius: 6px;
  padding: 10px 12px;
  margin: 12px 0 16px;
  font-size: 0.85rem;
  white-space: pre-wrap;
}
.no-workspace-gain {
  border: 1px solid var(--vscode-panel-border, #333);
  border-radius: 6px;
  padding: 12px 14px;
  font-size: 0.9rem;
  opacity: 0.8;
  margin-bottom: 8px;
}
.global-activity {
  background: var(--vscode-editor-inactiveSelectionBackground, #2a2d2e);
  border-radius: 6px;
  padding: 12px 14px;
  margin-bottom: 8px;
}
.ga-row {
  display: flex;
  gap: 12px;
  padding: 3px 0;
  font-size: 0.88rem;
}
.ga-label { opacity: 0.65; min-width: 120px; }
.ga-value { font-weight: 500; }
.ga-cmd { font-family: monospace; font-size: 0.8rem; opacity: 0.85; word-break: break-all; }
.boundary-box {
  border: 1px solid var(--vscode-panel-border, #333);
  border-radius: 6px;
  padding: 10px 12px;
  margin-bottom: 16px;
  font-size: 0.82rem;
  opacity: 0.85;
  line-height: 1.5;
}
.boundary-box strong {
  font-weight: 600;
  display: block;
  margin-bottom: 4px;
}
code {
  font-family: monospace;
  background: var(--vscode-textCodeBlock-background, #2a2d2e);
  padding: 1px 4px;
  border-radius: 3px;
  font-size: 0.8rem;
}
</style>
</head>
<body>
<h1>XiT Dashboard</h1>
${hardErrors.length > 0 ? `<div class="diagnostic">${escapeHtml(hardErrors.join('\n'))}</div>` : ''}

<div class="boundary-box">
  <strong>Visibility Boundaries</strong><br>
  XiT Status shows <em>local XiT-recorded events only</em>.<br>
  Claude Code CLI hooks are supported. Claude Code for VS Code <em>native panel</em> activity is not observable unless it enters XiT hooks or terminal listener.<br>
  For native panel support, enable terminal mode or wait for a future bridge.
</div>

<h2>Latest XiT Run</h2>
${latestRunSection}

<h2>Workspace Gain</h2>
${workspaceGainSection}

<h2>Global Activity</h2>
${globalActivityBlock}

<h2>Adapters</h2>
<div class="adapters">
  ${adapterCards}
</div>

<h2>Recent Events</h2>
${events.length > 0 ? `
<table>
  <thead>
    <tr><th>Time</th><th>Adapter</th><th>Command / Event</th><th>Policy</th></tr>
  </thead>
  <tbody>
    ${eventRows}
  </tbody>
</table>
` : '<p class="empty">No recent events.</p>'}

<h2>VS Code Terminal Events</h2>
${terminalEvents.length > 0 ? `
<table>
  <thead>
    <tr><th>Time</th><th>Terminal</th><th>Command</th><th>CWD</th></tr>
  </thead>
  <tbody>
    ${terminalEventRows}
  </tbody>
</table>
` : '<p class="empty">No terminal events recorded. Enable <code>xit.enableTerminalListener</code> to capture VS Code terminal shell executions locally.</p>'}

<div class="privacy">
  Local only. No telemetry. No network requests.
</div>
</body>
</html>
`;
}

export function showDashboard(context: vscode.ExtensionContext, status: XiTStatus): void {
  panelContext = context;
  if (panel) {
    panel.reveal(vscode.ViewColumn.One);
  } else {
    panel = vscode.window.createWebviewPanel(
      'xitDashboard',
      'XiT Dashboard',
      vscode.ViewColumn.One,
      {
        enableScripts: false,
        localResourceRoots: [],
      }
    );
    panel.onDidDispose(
      () => {
        panel = undefined;
      },
      null,
      context.subscriptions
    );
  }

  const events = gatherAllEvents();
  const terminalEvents = readTerminalEvents(20);
  const latestRun = readLatestRun();
  panel.webview.html = buildDashboardHtml(status, events, terminalEvents, latestRun, panel.webview.cspSource);
}

export function updateDashboardIfOpen(status: XiTStatus): void {
  if (!panel) {
    return;
  }
  const events = gatherAllEvents();
  const terminalEvents = readTerminalEvents(20);
  const latestRun = readLatestRun();
  panel.webview.html = buildDashboardHtml(status, events, terminalEvents, latestRun, panel.webview.cspSource);
}
