import * as vscode from 'vscode';
import { fetchStatus, openLatestRawLog, showOutput, writeTerminalEvent, readRecentEvents } from './xit';
import { showDashboard, updateDashboardIfOpen } from './dashboard';
import { promptRunCommand, promptRunWithAutoCompression, openXiTTerminal, handleTerminalHighOutput, refreshAfterRun } from './runner';

let statusBarItem: vscode.StatusBarItem | undefined;
let refreshTimer: NodeJS.Timeout | undefined;
let liveState: 'idle' | 'running' | 'success' | 'missed' | 'no-binary' = 'idle';
let liveStateTimer: NodeJS.Timeout | undefined;

function getRefreshIntervalMs(): number {
  const cfg = vscode.workspace.getConfiguration('xit');
  const seconds = cfg.get<number>('refreshInterval', 10);
  return Math.max(5, seconds) * 1000;
}

function isEnabled(): boolean {
  const cfg = vscode.workspace.getConfiguration('xit');
  return cfg.get<boolean>('enableStatusBar', true);
}

function isShowActiveAiSurface(): boolean {
  const cfg = vscode.workspace.getConfiguration('xit');
  return cfg.get<boolean>('showActiveAiSurface', true);
}

type SurfaceResult =
  | { kind: 'connected'; name: string }
  | { kind: 'recent'; name: string }
  | undefined;

/**
 * Detect currently focused/connected AI surface from active VS Code UI.
 * Only uses real-time active terminal, editor, or focused tab.
 * Never reads chat content.
 */
function detectConnectedAiSurface(): string | undefined {
  if (!isShowActiveAiSurface()) {
    return undefined;
  }

  // 1. Active terminal first
  const activeTerminal = vscode.window.activeTerminal;
  if (activeTerminal) {
    const name = activeTerminal.name.toLowerCase();
    if (name.includes('claude')) { return 'Claude'; }
    if (name.includes('codex')) { return 'Codex'; }
    if (name.includes('gemini')) { return 'Gemini'; }
    if (name.includes('cursor')) { return 'Cursor'; }
    if (name.includes('kimi')) { return 'Kimi'; }
    if (name.includes('aider')) { return 'Aider'; }
  }

  // 2. All terminals (background AI terminals still count as connected)
  const terminals = vscode.window.terminals;
  for (const t of terminals) {
    const name = t.name.toLowerCase();
    if (name.includes('claude')) { return 'Claude'; }
    if (name.includes('codex')) { return 'Codex'; }
    if (name.includes('gemini')) { return 'Gemini'; }
    if (name.includes('cursor')) { return 'Cursor'; }
    if (name.includes('kimi')) { return 'Kimi'; }
    if (name.includes('aider')) { return 'Aider'; }
  }

  // 3. Active editor
  const activeEditor = vscode.window.activeTextEditor;
  if (activeEditor) {
    const doc = activeEditor.document;
    const fileName = doc.fileName.toLowerCase();
    const uriScheme = doc.uri.scheme.toLowerCase();

    if (fileName.includes('claude') || uriScheme.includes('claude')) { return 'Claude'; }
    if (fileName.includes('codex') || uriScheme.includes('codex')) { return 'Codex'; }
    if (fileName.includes('gemini') || uriScheme.includes('gemini')) { return 'Gemini'; }
    if (fileName.includes('cursor') || uriScheme.includes('cursor')) { return 'Cursor'; }
    if (uriScheme === 'vscode-chat' || uriScheme === 'chat') { return 'VS Code Chat'; }
  }

  // 4. Active/focused tab only (VS Code 1.67+)
  try {
    const tabGroups = (vscode.window as any).tabGroups;
    const activeTab = tabGroups?.activeTabGroup?.activeTab;
    if (activeTab) {
      const label = (activeTab.label || '').toLowerCase();
      if (label.includes('claude')) { return 'Claude'; }
      if (label.includes('codex')) { return 'Codex'; }
      if (label.includes('gemini')) { return 'Gemini'; }
      if (label.includes('cursor')) { return 'Cursor'; }
      if (label.includes('kimi')) { return 'Kimi'; }
      if (label.includes('aider')) { return 'Aider'; }
      if (label.includes('chat') && !label.includes('xit')) { return 'VS Code Chat'; }
    }
  } catch {
    // tabGroups API not available on this VS Code version
  }

  return undefined;
}

/**
 * Detect recently used AI surface from XiT adapter events.
 * This is historical inference, NOT a real-time connection.
 */
function detectRecentAiSurface(): string | undefined {
  if (!isShowActiveAiSurface()) {
    return undefined;
  }

  const adapters = ['claude', 'codex', 'cursor', 'kimi', 'aider'];
  let latestTime = '';
  let latestAdapter: string | undefined;

  for (const adapter of adapters) {
    const events = readRecentEvents(adapter, 1);
    if (events.length > 0) {
      const ev = events[0];
      if (ev.time && ev.time > latestTime) {
        latestTime = ev.time;
        latestAdapter = adapter;
      }
    }
  }

  if (latestAdapter) {
    return latestAdapter.charAt(0).toUpperCase() + latestAdapter.slice(1);
  }

  return undefined;
}

function detectAiSurface(): SurfaceResult {
  const connected = detectConnectedAiSurface();
  if (connected) {
    return { kind: 'connected', name: connected };
  }
  const recent = detectRecentAiSurface();
  if (recent) {
    return { kind: 'recent', name: recent };
  }
  return undefined;
}

function buildStatusBarText(state: typeof liveState, surface?: SurfaceResult): string {
  const connectedName = surface?.kind === 'connected' ? surface.name : undefined;
  const recentName = surface?.kind === 'recent' ? surface.name : undefined;

  switch (state) {
    case 'no-binary':
      return '吸T神功 · 未找到 XiT';
    case 'running':
      return connectedName
        ? `吸T神功 · ${connectedName} · 正在压缩`
        : '吸T神功 · 正在压缩';
    case 'missed':
      return connectedName
        ? `吸T神功 · ${connectedName} · 本次未触发压缩`
        : '吸T神功 · 本次未触发压缩';
    case 'success':
      return connectedName
        ? `吸T神功 · ${connectedName} · `
        : '吸T神功 · ';
    case 'idle':
    default:
      if (connectedName) {
        return `吸T神功 · 已连接 ${connectedName} · 准备就绪`;
      }
      if (recentName) {
        return `吸T神功 · 最近 ${recentName} · 准备就绪`;
      }
      return '吸T神功 · 准备就绪';
  }
}

async function updateStatusBar(): Promise<void> {
  if (!statusBarItem) {
    return;
  }

  // Live transient states take priority over periodic refresh
  if (liveState === 'running' || liveState === 'missed') {
    return;
  }

  const status = await fetchStatus();

  if (!status.available) {
    if (status.state === 'binary-not-found') {
      statusBarItem.text = '吸T神功 · 未找到 XiT';
    } else if (status.state === 'gain-json-failed') {
      statusBarItem.text = 'XiT · no data';
    } else {
      statusBarItem.text = 'XiT · no data';
    }
    statusBarItem.tooltip = [
      status.error || 'XiT status unavailable.',
      'Shows local XiT data only.',
      status.cwd ? `cwd: ${status.cwd}` : '',
      status.attempts && status.attempts.length > 0 ? `Attempted: ${status.attempts.join(', ')}` : '',
      'Click to open XiT Dashboard',
    ].filter(Boolean).join('\n');
    updateDashboardIfOpen(status);
    return;
  }

  // Idle: never show historical gain in status bar text
  const surface = detectAiSurface();
  statusBarItem.text = buildStatusBarText('idle', surface);

  const gain = status.gain!;
  const surfaceLine = surface
    ? surface.kind === 'connected'
      ? `已连接 AI: ${surface.name} (从 VS Code UI 元数据检测)`
      : `最近 AI: ${surface.name} (从 XiT adapter 事件推断)`
    : '';

  const lines = [
    gain.total_commands_condensed > 0
      ? `历史累计省: ${gain.saved_tokens_display}`
      : 'No XiT gain data for this workspace yet.',
    gain.total_commands_condensed > 0 ? `Estimated reduction: ${(gain.estimated_reduction * 100).toFixed(1)}%` : '',
    gain.total_commands_condensed > 0 ? `Commands condensed: ${gain.total_commands_condensed}` : '',
    surfaceLine,
    'XiT does not read chat content.',
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

function setLiveState(state: typeof liveState, durationMs = 5000): void {
  liveState = state;
  updateStatusBarLive();
  if (liveStateTimer) {
    clearTimeout(liveStateTimer);
  }
  if (state !== 'idle' && state !== 'no-binary') {
    liveStateTimer = setTimeout(() => {
      liveState = 'idle';
      updateStatusBarLive();
    }, durationMs);
  }
}

async function updateStatusBarLive(): Promise<void> {
  if (!statusBarItem) {
    return;
  }
  if (liveState === 'no-binary') {
    statusBarItem.text = '吸T神功 · 未找到 XiT';
    return;
  }

  const surface = detectAiSurface();

  if (liveState === 'running') {
    statusBarItem.text = buildStatusBarText('running', surface);
    return;
  }
  if (liveState === 'missed') {
    statusBarItem.text = buildStatusBarText('missed', surface);
    return;
  }
  if (liveState === 'success') {
    const latest = (await import('./xit')).readLatestRun();
    if (latest) {
      const saved = latest.raw_bytes - latest.summary_bytes;
      const display = saved >= 1000 ? `~${Math.round(saved / 1000)}KB` : `${saved}B`;
      const connectedName = surface?.kind === 'connected' ? surface.name : undefined;
      const base = connectedName ? `吸T神功 · ${connectedName}` : '吸T神功';
      statusBarItem.text = `${base} · 本次省${display}`;
    } else {
      statusBarItem.text = buildStatusBarText('idle', surface);
    }
    return;
  }
  // idle: never show historical gain in status bar text
  statusBarItem.text = buildStatusBarText('idle', surface);
}

function isTerminalListenerEnabled(): boolean {
  const cfg = vscode.workspace.getConfiguration('xit');
  return cfg.get<boolean>('enableTerminalListener', false);
}

function registerTerminalListeners(context: vscode.ExtensionContext): void {
  if (!isTerminalListenerEnabled()) {
    return;
  }

  // VS Code 1.120+ terminal shell execution API
  try {
    const startDisposable = (vscode.window as any).onDidStartTerminalShellExecution?.((event: any) => {
      const commandLine = event.execution?.commandLine?.value || '';
      const confidence = event.execution?.commandLine?.confidence ?? 0;
      const terminalName = event.terminal?.name || 'unknown';
      const cwd = event.execution?.cwd?.fsPath;
      if (!commandLine) {
        return;
      }
      writeTerminalEvent({ commandLine, confidence, terminalName, cwd });

      // High-output detection notification
      (async () => {
        await handleTerminalHighOutput(commandLine);
        setLiveState('missed', 4000);
      })();
    });
    if (startDisposable) {
      context.subscriptions.push(startDisposable);
    }
  } catch {
    // API not available on this VS Code version
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
  registerTerminalListeners(context);

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
    vscode.commands.registerCommand('xit.runCommand', async () => {
      setLiveState('running', 5000);
      await promptRunCommand();
      await refreshAfterRun();
      setLiveState('success', 5000);
    })
  );

  context.subscriptions.push(
    vscode.commands.registerCommand('xit.runWithAutoCompression', async () => {
      setLiveState('running', 5000);
      await promptRunWithAutoCompression();
      await refreshAfterRun();
      setLiveState('success', 5000);
    })
  );

  context.subscriptions.push(
    vscode.commands.registerCommand('xit.openXiTTerminal', () => {
      openXiTTerminal();
    })
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
      if (e.affectsConfiguration('xit.enableTerminalListener')) {
        // Terminal listener change requires reload; inform user
        vscode.window.showInformationMessage('XiT: Terminal listener setting changed. Reload window to apply.');
      }
    })
  );
}

export function deactivate(): void {
  stopRefresh();
}
