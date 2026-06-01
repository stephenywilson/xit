import * as fs from 'fs';
import * as path from 'path';
import * as vscode from 'vscode';
import { showDashboard, updateDashboardIfOpen } from './dashboard';
import { openXiTTerminal, promptRunCommand, promptRunWithAutoCompression } from './runner';
import type { LatestRun, XiTStatus } from './types';
import {
  appendOutput,
  clearOutput,
  fetchStatus,
  openLatestRawLog,
  readLatestRawLogMeta,
  readLatestRun,
  resolveWorkspaceCwd,
  showOutput,
  writeTerminalEvent,
} from './xit';
import {
  buildDiagnoseReport,
  computeWorkflowHealth,
  formatSavedTokensForRun,
  formatTokenCount,
  installWorkspaceAiRules,
} from './workflow';

let statusBarItem: vscode.StatusBarItem | undefined;
let refreshTimer: NodeJS.Timeout | undefined;
let liveState: 'idle' | 'guarding' | 'running' | 'success' | 'waiting' | 'missed' | 'no-binary' = 'idle';
let liveStateTimer: NodeJS.Timeout | undefined;
let waitingStateTimer: NodeJS.Timeout | undefined;
let lastObservedRunSignature: string | undefined;
let terminalListenerDisposable: vscode.Disposable | undefined;

function getRefreshIntervalMs(): number {
  const cfg = vscode.workspace.getConfiguration('xit');
  const seconds = cfg.get<number>('refreshInterval', 5);
  return Math.max(3, seconds) * 1000;
}

function isEnabled(): boolean {
  const cfg = vscode.workspace.getConfiguration('xit');
  return cfg.get<boolean>('enableStatusBar', true);
}

function isTerminalListenerEnabled(): boolean {
  const cfg = vscode.workspace.getConfiguration('xit');
  return cfg.get<boolean>('enableTerminalListener', true);
}

function getWorkspacePath(): string | undefined {
  const folders = vscode.workspace.workspaceFolders;
  return folders && folders.length > 0 ? folders[0].uri.fsPath : undefined;
}

function getStatusBarTextFromRun(run: LatestRun | undefined): string {
  if (!run) {
    return '吸T神功 · 准备就绪';
  }
  const savedBytes = Math.max(0, run.raw_bytes - run.summary_bytes);
  if (savedBytes <= 0) {
    return '吸T神功 · 无需发功';
  }
  return `吸T完成 · 本次省${formatSavedTokensForRun(run)}`;
}

function setLiveState(state: typeof liveState, durationMs = 0): void {
  liveState = state;
  if (liveStateTimer) {
    clearTimeout(liveStateTimer);
    liveStateTimer = undefined;
  }
  if (waitingStateTimer) {
    clearTimeout(waitingStateTimer);
    waitingStateTimer = undefined;
  }
  void updateStatusBarLive();
  if (durationMs > 0) {
    liveStateTimer = setTimeout(() => {
      if (state === 'success' || state === 'missed') {
        liveState = 'waiting';
        void updateStatusBarLive();
        waitingStateTimer = setTimeout(() => {
          liveState = 'idle';
          void updateStatusBar();
        }, 20000);
        return;
      }
      liveState = 'idle';
      void updateStatusBar();
    }, durationMs);
  }
}

function getRunSignature(run: LatestRun | undefined): string | undefined {
  if (!run) {
    return undefined;
  }
  return `${run.timestamp}|${run.raw_log}|${run.exit_code}`;
}

function detectActiveRawLog(): string | undefined {
  const latestRawLog = readLatestRawLogMeta();
  if (!latestRawLog) {
    return undefined;
  }

  const latestRun = readLatestRun();
  const latestRunLog = latestRun?.raw_log ? path.resolve(latestRun.raw_log) : undefined;
  const rawLogPath = path.resolve(latestRawLog.path);
  const ageMs = Date.now() - latestRawLog.mtimeMs;

  if (ageMs > 15000) {
    return undefined;
  }

  if (!latestRunLog || latestRunLog !== rawLogPath) {
    return rawLogPath;
  }

  try {
    const historyMtime = fs.statSync(path.join(resolveWorkspaceCwd(), '.xit', 'history.jsonl')).mtimeMs;
    if (latestRawLog.mtimeMs > historyMtime) {
      return rawLogPath;
    }
  } catch {
    return rawLogPath;
  }

  return undefined;
}

async function updateStatusBar(): Promise<void> {
  if (!statusBarItem) {
    return;
  }

  const status = await fetchStatus();
  const latestRun = readLatestRun();
  const latestRunSignature = getRunSignature(latestRun);
  const activeRawLog = detectActiveRawLog();
  const health = computeWorkflowHealth(status, latestRun);

  if (!status.available && status.state === 'binary-not-found') {
    liveState = 'no-binary';
    statusBarItem.text = '吸T神功 · 未找到 XiT';
    statusBarItem.tooltip = [
      '吸T神功尚未找到本地 XiT。',
      status.cwd ? `当前工作区：${status.cwd}` : '',
      status.attempts && status.attempts.length > 0 ? `已尝试：${status.attempts.join(', ')}` : '',
      '点击打开 XiT Dashboard',
    ].filter(Boolean).join('\n');
    updateDashboardIfOpen(status);
    return;
  }

  if (activeRawLog) {
    setLiveState('running');
  } else if (latestRunSignature && latestRunSignature !== lastObservedRunSignature) {
    lastObservedRunSignature = latestRunSignature;
    const savedBytes = Math.max(0, (latestRun?.raw_bytes || 0) - (latestRun?.summary_bytes || 0));
    setLiveState(savedBytes > 0 ? 'success' : 'missed', 25000);
  } else if (liveState === 'idle' && health.workspaceRulesInstalled) {
    liveState = 'guarding';
  }

  if (liveState === 'running') {
    const rawLogMeta = readLatestRawLogMeta();
    const estimatedTokens = rawLogMeta ? Math.round(rawLogMeta.size / 4) : 0;
    statusBarItem.text = estimatedTokens >= 1000
      ? `吸T神功 · 已接管${formatTokenCount(estimatedTokens)}`
      : '吸T神功 · 正在吸T中';
  } else if (liveState === 'success') {
    statusBarItem.text = getStatusBarTextFromRun(latestRun);
  } else if (liveState === 'missed') {
    statusBarItem.text = '吸T神功 · 无需发功';
  } else if (liveState === 'waiting') {
    statusBarItem.text = '吸T神功 · 等待下轮发功';
  } else if (liveState === 'guarding') {
    statusBarItem.text = '吸T神功 · 守护你的T';
  } else {
    statusBarItem.text = '吸T神功 · 准备就绪';
  }

  statusBarItem.tooltip = [
    health.workspaceRulesInstalled ? '吸T神功正在守护当前工作区' : '吸T神功已准备好，随时出手',
    latestRun ? `最近吸T：省${health.latestSavedDisplay}` : '最近吸T：尚未出手',
    latestRun?.raw_log ? `原始日志：${latestRun.raw_log}` : '',
    status.binary ? `XiT 本体：${status.binary}` : '',
    '本地处理，无遥测，无网络请求',
    '点击打开 XiT Dashboard',
  ].filter(Boolean).join('\n');

  updateDashboardIfOpen(status);
}

async function updateStatusBarLive(): Promise<void> {
  if (!statusBarItem) {
    return;
  }

  if (liveState === 'no-binary') {
    statusBarItem.text = '吸T神功 · 未找到 XiT';
    return;
  }
  if (liveState === 'running') {
    const rawLogMeta = readLatestRawLogMeta();
    const estimatedTokens = rawLogMeta ? Math.round(rawLogMeta.size / 4) : 0;
    statusBarItem.text = estimatedTokens >= 1000
      ? `吸T神功 · 已接管${formatTokenCount(estimatedTokens)}`
      : '吸T神功 · 正在吸T中';
    return;
  }
  if (liveState === 'missed') {
    statusBarItem.text = '吸T神功 · 无需发功';
    return;
  }
  if (liveState === 'success') {
    statusBarItem.text = getStatusBarTextFromRun(readLatestRun());
    return;
  }
  if (liveState === 'waiting') {
    statusBarItem.text = '吸T神功 · 等待下轮发功';
    return;
  }
  if (liveState === 'guarding') {
    statusBarItem.text = '吸T神功 · 守护你的T';
    return;
  }
  statusBarItem.text = computeWorkflowHealth(await fetchStatus(), readLatestRun()).workspaceRulesInstalled
    ? '吸T神功 · 守护你的T'
    : '吸T神功 · 准备就绪';
}

function startRefresh(): void {
  if (refreshTimer) {
    clearInterval(refreshTimer);
  }
  if (!isEnabled()) {
    return;
  }
  void updateStatusBar();
  refreshTimer = setInterval(() => {
    void updateStatusBar();
  }, getRefreshIntervalMs());
}

function stopRefresh(): void {
  if (refreshTimer) {
    clearInterval(refreshTimer);
    refreshTimer = undefined;
  }
}

function registerWorkspaceWatchers(context: vscode.ExtensionContext): void {
  const workspacePath = getWorkspacePath();
  if (!workspacePath) {
    return;
  }

  const historyPattern = new vscode.RelativePattern(workspacePath, '.xit/history.jsonl');
  const rawLogPattern = new vscode.RelativePattern(workspacePath, '.xit/runs/*.raw.log');

  const historyWatcher = vscode.workspace.createFileSystemWatcher(historyPattern);
  const rawLogWatcher = vscode.workspace.createFileSystemWatcher(rawLogPattern);

  const onHistoryChange = async (): Promise<void> => {
    const latestRun = readLatestRun();
    const signature = getRunSignature(latestRun);
    if (signature && signature !== lastObservedRunSignature) {
      lastObservedRunSignature = signature;
      const savedBytes = Math.max(0, (latestRun?.raw_bytes || 0) - (latestRun?.summary_bytes || 0));
      setLiveState(savedBytes > 0 ? 'success' : 'missed', 25000);
    }
    await updateStatusBar();
  };

  const onRawLogChange = async (): Promise<void> => {
    const active = detectActiveRawLog();
    if (active) {
      setLiveState('running');
    }
    await updateStatusBar();
  };

  historyWatcher.onDidChange(onHistoryChange, null, context.subscriptions);
  historyWatcher.onDidCreate(onHistoryChange, null, context.subscriptions);
  rawLogWatcher.onDidChange(onRawLogChange, null, context.subscriptions);
  rawLogWatcher.onDidCreate(onRawLogChange, null, context.subscriptions);

  context.subscriptions.push(historyWatcher, rawLogWatcher);
}

function registerTerminalListeners(context: vscode.ExtensionContext): void {
  terminalListenerDisposable?.dispose();
  terminalListenerDisposable = undefined;

  if (!isTerminalListenerEnabled()) {
    return;
  }

  try {
    const listener = (vscode.window as any).onDidStartTerminalShellExecution?.((event: any) => {
      const commandLine = event.execution?.commandLine?.value || '';
      const confidence = event.execution?.commandLine?.confidence ?? 0;
      const terminalName = event.terminal?.name || 'unknown';
      const cwd = event.execution?.cwd?.fsPath;
      if (!commandLine) {
        return;
      }
      writeTerminalEvent({ commandLine, confidence, terminalName, cwd });
      if (/\bxit\s+auto\b/.test(commandLine)) {
        setLiveState('running');
      }
      void updateStatusBar();
    });
    if (listener) {
      terminalListenerDisposable = listener;
      context.subscriptions.push(listener);
    }
  } catch {
    // ignore API absence
  }
}

async function runDiagnose(): Promise<void> {
  const status = await fetchStatus();
  const latestRun = readLatestRun();
  const report = await buildDiagnoseReport(status, latestRun);
  const lines = [
    'XiT AI Workflow Diagnose',
    `workspace: ${report.workspacePath}`,
    `binary_path: ${report.binaryPath || 'missing'}`,
    `cli_version: ${report.cliVersion || 'unknown'}`,
    `has_runs_dir: ${report.hasRunsDir ? 'yes' : 'no'}`,
    `latest_run_time: ${report.latestRunTime || 'none'}`,
    `latest_saved_bytes: ${report.latestSavedBytes ?? 'none'}`,
    `latest_saved_display: ${report.latestSavedDisplay || 'none'}`,
    `latest_raw_log: ${report.latestRawLogPath || 'none'}`,
    `recent_high_noise_commands: ${report.recentHighNoiseCommands}`,
    `recent_routed_through_xit: ${report.recentHighNoiseRouted}`,
    `routing_hit_rate: ${(report.routingHitRate * 100).toFixed(1)}%`,
    `workspace_rules_installed: ${report.workspaceRulesInstalled ? 'yes' : 'no'}`,
    `workspace_rule_files: ${report.workspaceRuleFiles.length > 0 ? report.workspaceRuleFiles.join(', ') : 'none'}`,
    `recommendation: ${report.recommendation || 'none'}`,
  ];
  clearOutput();
  appendOutput(lines.join('\n'));
  showOutput();
}

export function activate(context: vscode.ExtensionContext): void {
  if (isEnabled()) {
    statusBarItem = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Right, 100);
    statusBarItem.command = 'xit.openDashboard';
    statusBarItem.text = '吸T神功 · 准备就绪';
    statusBarItem.show();
    context.subscriptions.push(statusBarItem);
  }

  lastObservedRunSignature = getRunSignature(readLatestRun());
  startRefresh();
  registerWorkspaceWatchers(context);
  registerTerminalListeners(context);

  context.subscriptions.push(
    vscode.commands.registerCommand('xit.openDashboard', async () => {
      const status = await fetchStatus();
      showDashboard(context, status);
    }),
    vscode.commands.registerCommand('xit.refresh', async () => {
      await updateStatusBar();
      vscode.window.showInformationMessage('XiT status refreshed');
    }),
    vscode.commands.registerCommand('xit.showGain', async () => {
      const status = await fetchStatus();
      if (!status.available || !status.gain) {
        vscode.window.showWarningMessage(`XiT: ${status.error || 'No gain data available.'}`);
        return;
      }
      const g = status.gain;
      vscode.window.showInformationMessage(
        `Commands condensed: ${g.total_commands_condensed} | Saved tokens: ${g.saved_tokens_display} | Estimated reduction: ${(g.estimated_reduction * 100).toFixed(1)}% | Saved bytes: ${g.saved_bytes}`
      );
    }),
    vscode.commands.registerCommand('xit.openLatestRawLog', openLatestRawLog),
    vscode.commands.registerCommand('xit.showOutput', showOutput),
    vscode.commands.registerCommand('xit.runCommand', async () => {
      setLiveState('running');
      await promptRunCommand();
    }),
    vscode.commands.registerCommand('xit.runWithAutoCompression', async () => {
      setLiveState('running');
      await promptRunWithAutoCompression();
    }),
    vscode.commands.registerCommand('xit.openXiTTerminal', () => {
      openXiTTerminal();
    }),
    vscode.commands.registerCommand('xit.installWorkspaceAiRules', async () => {
      const result = installWorkspaceAiRules();
      await updateStatusBar();
      const createdSummary = result.created.length > 0 ? ` Created: ${result.created.join(', ')}` : '';
      vscode.window.showInformationMessage(
        `XiT workspace AI rules updated in ${result.files.length} file(s).${createdSummary}`
      );
    }),
    vscode.commands.registerCommand('xit.diagnoseAiWorkflow', async () => {
      await runDiagnose();
    }),
    vscode.workspace.onDidChangeConfiguration((e) => {
      if (e.affectsConfiguration('xit.enableStatusBar')) {
        if (isEnabled()) {
          if (!statusBarItem) {
            statusBarItem = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Right, 100);
            statusBarItem.command = 'xit.openDashboard';
            context.subscriptions.push(statusBarItem);
          }
          statusBarItem.show();
          statusBarItem.text = '吸T神功 · 准备就绪';
          startRefresh();
        } else {
          stopRefresh();
          statusBarItem?.hide();
        }
      }
      if (e.affectsConfiguration('xit.refreshInterval')) {
        startRefresh();
      }
      if (e.affectsConfiguration('xit.enableTerminalListener')) {
        registerTerminalListeners(context);
      }
    })
  );
}

export function deactivate(): void {
  stopRefresh();
  terminalListenerDisposable?.dispose();
}
