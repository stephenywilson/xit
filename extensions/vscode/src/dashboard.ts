import * as vscode from 'vscode';
import type { AdapterEvent, XiTStatus } from './types';

let panel: vscode.WebviewPanel | undefined;

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

function buildDashboardHtml(
  status: XiTStatus,
  events: AdapterEvent[],
  cspSource: string
): string {
  const gain = status.gain;
  const errorDetails = status.state !== 'ok'
    ? [
        status.state === 'binary-not-found' ? 'XiT binary not found.' : '',
        status.state === 'gain-json-failed' ? 'xit gain --json did not return valid JSON.' : '',
        status.state === 'no-data' ? 'No XiT gain history found for this workspace yet.' : '',
        status.error || '',
        status.binary ? `Binary: ${status.binary}` : '',
        status.cwd ? `cwd: ${status.cwd}` : '',
        status.attempts && status.attempts.length > 0 ? `Attempted: ${status.attempts.join(', ')}` : '',
        gain?.warnings && gain.warnings.length > 0 ? `Warnings: ${gain.warnings.join('; ')}` : '',
      ].filter(Boolean)
    : [];
  const topCommandsRows =
    gain?.top_commands
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

  const eventRows =
    events
      .slice(0, 20)
      .map((e) => {
        const time = e.time ? escapeHtml(e.time) : '-';
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

  const adapters = ['Cursor', 'Codex', 'Claude', 'Kimi', 'Antigravity', 'Aider'];
  const adapterCards = adapters
    .map((a) => {
      const lower = a.toLowerCase();
      const hasEvents = events.some((e) => e.adapter === lower);
      const statusClass = hasEvents ? 'active' : 'inactive';
      return `<div class="card adapter ${statusClass}">${escapeHtml(a)}</div>`;
    })
    .join('');

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
h1 {
  font-size: 1.4rem;
  margin-bottom: 8px;
}
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
.stat .value {
  font-size: 1.3rem;
  font-weight: 600;
}
.stat .label {
  font-size: 0.8rem;
  opacity: 0.8;
}
table {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.85rem;
}
th, td {
  text-align: left;
  padding: 6px 8px;
  border-bottom: 1px solid var(--vscode-panel-border, #333);
}
th {
  opacity: 0.7;
  font-weight: 500;
}
.adapters {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 16px;
}
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
.privacy {
  margin-top: 24px;
  padding: 10px 12px;
  border-radius: 6px;
  background: var(--vscode-inputValidation-infoBackground, #1e2a36);
  font-size: 0.8rem;
}
.empty {
  opacity: 0.6;
  font-size: 0.9rem;
}
.diagnostic {
  border: 1px solid var(--vscode-inputValidation-warningBorder, #b89500);
  background: var(--vscode-inputValidation-warningBackground, #332b00);
  border-radius: 6px;
  padding: 10px 12px;
  margin: 12px 0 16px;
  font-size: 0.85rem;
  white-space: pre-wrap;
}
</style>
</head>
<body>
<h1>XiT Dashboard</h1>
${errorDetails.length > 0 ? `<div class="diagnostic">${escapeHtml(errorDetails.join('\n'))}</div>` : ''}

<h2>Gain Summary</h2>
<div class="grid">
  <div class="stat">
    <div class="value">${gain ? gain.total_commands_condensed : '-'}</div>
    <div class="label">Commands condensed</div>
  </div>
  <div class="stat">
    <div class="value">${gain ? gain.saved_tokens_display : '-'}</div>
    <div class="label">Saved tokens</div>
  </div>
  <div class="stat">
    <div class="value">${gain ? formatReduction(gain.estimated_reduction) : '-'}</div>
    <div class="label">Reduction</div>
  </div>
  <div class="stat">
    <div class="value">${gain ? formatBytes(gain.saved_bytes) : '-'}</div>
    <div class="label">Saved bytes</div>
  </div>
</div>

<h2>Top Commands</h2>
${gain && gain.top_commands.length > 0 ? `
<table>
  <thead>
    <tr><th>Command</th><th>Runs</th><th>Saved tokens</th><th>Saved bytes</th></tr>
  </thead>
  <tbody>
    ${topCommandsRows}
  </tbody>
</table>
` : '<p class="empty">No condensed commands yet.</p>'}

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

<div class="privacy">
  Local only. No telemetry. No network requests.
</div>
</body>
</html>
`;
}

export function showDashboard(context: vscode.ExtensionContext, status: XiTStatus): void {
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

  const adapters = ['cursor', 'codex', 'claude', 'kimi'];
  let allEvents: AdapterEvent[] = [];
  for (const a of adapters) {
    // Import at runtime to avoid circular deps if any
    const xitModule = require('./xit');
    allEvents = allEvents.concat(xitModule.readRecentEvents(a, 10));
  }
  const workspaceEvents: AdapterEvent[] = require('./xit').readWorkspaceHistory(10);
  allEvents = allEvents.concat(workspaceEvents);

  // Sort by time descending if available
  allEvents.sort((a, b) => {
    const ta = a.time || '';
    const tb = b.time || '';
    return tb.localeCompare(ta);
  });

  panel.webview.html = buildDashboardHtml(status, allEvents, panel.webview.cspSource);
}
