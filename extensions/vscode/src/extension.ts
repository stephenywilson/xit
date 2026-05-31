import * as vscode from 'vscode';
import { fetchStatus, openLatestRawLog, showOutput } from './xit';
import { showDashboard, updateDashboardIfOpen } from './dashboard';

let statusBarItem: vscode.StatusBarItem | undefined;
let refreshTimer: NodeJS.Timeout | undefined;

function getRefreshIntervalMs(): number {
  const cfg = vscode.workspace.getConfiguration('xit');
  const seconds = cfg.get<number>('refreshInterval', 10);
  return Math.max(5, seconds) * 1000;
}

function isEnabled(): boolean {
  const cfg = vscode.workspace.getConfiguration('xit');
  return cfg.get<boolean>('enableStatusBar', true);
}

async function updateStatusBar(): Promise<void> {
  if (!statusBarItem) {
    return;
  }
  const status = await fetchStatus();

  if (!status.available) {
    if (status.state === 'binary-not-found') {
      statusBarItem.text = 'XiT · binary not found';
    } else {
      statusBarItem.text = 'XiT · gain JSON failed';
    }
    statusBarItem.tooltip = [
      status.error || 'XiT status unavailable.',
      status.cwd ? `cwd: ${status.cwd}` : '',
      status.attempts && status.attempts.length > 0 ? `Attempted: ${status.attempts.join(', ')}` : '',
      'Click to open XiT Dashboard',
    ].filter(Boolean).join('\n');
    updateDashboardIfOpen(status);
    return;
  }

  const gain = status.gain!;
  const activity = status.activity;

  if (gain.total_commands_condensed > 0) {
    const display = gain.saved_tokens_display || `~${Math.round(gain.saved_tokens / 1000)}k`;
    statusBarItem.text = `吸T神功 · 省${display}`;
  } else if (activity && activity.eventCount > 0) {
    if (activity.latestAdapter) {
      statusBarItem.text = `XiT · latest: ${activity.latestAdapter}`;
    } else {
      statusBarItem.text = `XiT · ${activity.eventCount} events`;
    }
  } else {
    statusBarItem.text = 'XiT · no data';
  }

  const adapterSummary = activity && Object.keys(activity.adapterCounts).length > 0
    ? Object.entries(activity.adapterCounts).map(([k, v]) => `${k}:${v}`).join(', ')
    : '';

  const lines = [
    gain.total_commands_condensed > 0
      ? `Saved tokens: ${gain.saved_tokens_display}`
      : activity && activity.eventCount > 0
        ? `Global events: ${activity.eventCount}${adapterSummary ? ` (${adapterSummary})` : ''}`
        : 'No workspace history yet.',
    gain.total_commands_condensed > 0 ? `Estimated reduction: ${(gain.estimated_reduction * 100).toFixed(1)}%` : '',
    gain.total_commands_condensed > 0 ? `Commands condensed: ${gain.total_commands_condensed}` : '',
    activity?.latestAdapter ? `Latest adapter: ${activity.latestAdapter}` : '',
    activity?.latestTime ? `Last event: ${activity.latestTime}` : '',
    status.binary ? `Binary: ${status.binary}` : '',
    status.cwd ? `cwd: ${status.cwd}` : '',
    gain.warnings && gain.warnings.length > 0 ? `Warnings: ${gain.warnings.join('; ')}` : '',
    `Refreshed: ${status.refreshedAt.toLocaleTimeString()}`,
    'Click to open XiT Dashboard',
  ].filter(Boolean);
  statusBarItem.tooltip = lines.join('\n');

  updateDashboardIfOpen(status);
}

function startRefresh(): void {
  if (refreshTimer) {
    clearInterval(refreshTimer);
    refreshTimer = undefined;
  }
  if (!isEnabled()) {
    return;
  }
  updateStatusBar();
  refreshTimer = setInterval(updateStatusBar, getRefreshIntervalMs());
}

function stopRefresh(): void {
  if (refreshTimer) {
    clearInterval(refreshTimer);
    refreshTimer = undefined;
  }
}

export function activate(context: vscode.ExtensionContext): void {
  if (isEnabled()) {
    statusBarItem = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Right, 100);
    statusBarItem.command = 'xit.openDashboard';
    statusBarItem.show();
    context.subscriptions.push(statusBarItem);
  }

  startRefresh();

  context.subscriptions.push(
    vscode.commands.registerCommand('xit.openDashboard', async () => {
      const status = await fetchStatus();
      showDashboard(context, status);
    })
  );

  context.subscriptions.push(
    vscode.commands.registerCommand('xit.refresh', async () => {
      await updateStatusBar();
      vscode.window.showInformationMessage('XiT status refreshed');
    })
  );

  context.subscriptions.push(
    vscode.commands.registerCommand('xit.showGain', async () => {
      const status = await fetchStatus();
      if (!status.available || !status.gain) {
        vscode.window.showWarningMessage(`XiT: ${status.error || 'No gain data available.'}`);
        return;
      }
      const g = status.gain;
      const lines = [
        `Commands condensed: ${g.total_commands_condensed}`,
        `Saved tokens: ${g.saved_tokens_display}`,
        `Estimated reduction: ${(g.estimated_reduction * 100).toFixed(1)}%`,
        `Saved bytes: ${g.saved_bytes}`,
      ];
      vscode.window.showInformationMessage(lines.join(' | '));
    })
  );

  context.subscriptions.push(
    vscode.commands.registerCommand('xit.openLatestRawLog', openLatestRawLog)
  );

  context.subscriptions.push(
    vscode.commands.registerCommand('xit.showOutput', showOutput)
  );

  context.subscriptions.push(
    vscode.workspace.onDidChangeConfiguration((e) => {
      if (e.affectsConfiguration('xit.enableStatusBar')) {
        if (isEnabled()) {
          if (!statusBarItem) {
            statusBarItem = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Right, 100);
            statusBarItem.command = 'xit.openDashboard';
            context.subscriptions.push(statusBarItem);
          }
          statusBarItem.show();
          startRefresh();
        } else {
          stopRefresh();
          if (statusBarItem) {
            statusBarItem.hide();
          }
        }
      }
      if (e.affectsConfiguration('xit.refreshInterval')) {
        startRefresh();
      }
    })
  );
}

export function deactivate(): void {
  stopRefresh();
}
