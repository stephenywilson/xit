import * as vscode from 'vscode';
import { fetchStatus, openLatestRawLog, showOutput } from './xit';
import { showDashboard } from './dashboard';

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
    statusBarItem.text = 'XiT · not found';
    statusBarItem.tooltip = 'XiT binary not found. Install xitsg or set xit.binaryPath.';
    return;
  }
  const gain = status.gain!;
  const display = gain.saved_tokens_display || `~${Math.round(gain.saved_tokens / 1000)}k`;
  statusBarItem.text = `吸T神功 · 省${display}`;
  const lines = [
    `Saved tokens: ${gain.saved_tokens_display}`,
    `Estimated reduction: ${(gain.estimated_reduction * 100).toFixed(1)}%`,
    `Total commands condensed: ${gain.total_commands_condensed}`,
    `Refreshed: ${status.refreshedAt.toLocaleTimeString()}`,
    'Click to open XiT Dashboard',
  ];
  statusBarItem.tooltip = lines.join('\n');
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
      showDashboard(context, status.gain);
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
        vscode.window.showWarningMessage('XiT: No gain data available.');
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
