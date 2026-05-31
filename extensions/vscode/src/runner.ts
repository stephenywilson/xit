import * as vscode from 'vscode';
import { isHighOutputCommand, readLatestRun } from './xit';
import { updateDashboardIfOpen } from './dashboard';

let xitTerminal: vscode.Terminal | undefined;

export function getXiTTerminal(): vscode.Terminal {
  if (xitTerminal) {
    const existing = vscode.window.terminals.find((t) => t.name === 'XiT');
    if (existing) {
      xitTerminal = existing;
      return xitTerminal;
    }
  }
  xitTerminal = vscode.window.createTerminal('XiT');
  return xitTerminal;
}

export function openXiTTerminal(): vscode.Terminal {
  const terminal = getXiTTerminal();
  terminal.show();
  return terminal;
}

export function runInXiTTerminal(command: string): void {
  const terminal = getXiTTerminal();
  terminal.show();
  terminal.sendText(command, true);
}

export async function promptRunCommand(): Promise<void> {
  const command = await vscode.window.showInputBox({
    prompt: 'Enter shell command',
    placeHolder: 'go test -v ./...',
  });
  if (!command || !command.trim()) {
    return;
  }

  const isHigh = isHighOutputCommand(command);
  if (isHigh) {
    runInXiTTerminal(`xit auto ${command}`);
    vscode.window.showInformationMessage(`XiT: running high-output command with auto compression`);
  } else {
    const choice = await vscode.window.showInformationMessage(
      `XiT: passthrough command detected`,
      { modal: false },
      'Run directly',
      'Run with xit auto'
    );
    if (choice === 'Run with xit auto') {
      runInXiTTerminal(`xit auto ${command}`);
    } else if (choice === 'Run directly') {
      runInXiTTerminal(command);
    }
  }
}

export async function promptRunWithAutoCompression(): Promise<void> {
  const command = await vscode.window.showInputBox({
    prompt: 'Enter shell command (will run with xit auto)',
    placeHolder: 'go test -v ./...',
  });
  if (!command || !command.trim()) {
    return;
  }
  runInXiTTerminal(`xit auto ${command}`);
}

export async function handleTerminalHighOutput(commandLine: string): Promise<void> {
  if (commandLine.includes('xit auto')) {
    return;
  }
  if (!isHighOutputCommand(commandLine)) {
    return;
  }

  const action = await vscode.window.showInformationMessage(
    `XiT: high-output command detected`,
    { modal: false },
    'Copy xit auto command',
    'Run in XiT Terminal',
    'Ignore'
  );

  if (action === 'Copy xit auto command') {
    await vscode.env.clipboard.writeText(`xit auto ${commandLine}`);
    vscode.window.showInformationMessage('Copied to clipboard');
  } else if (action === 'Run in XiT Terminal') {
    runInXiTTerminal(`xit auto ${commandLine}`);
  }
}

export async function refreshAfterRun(): Promise<void> {
  // Give xit auto time to write history.jsonl
  await new Promise((r) => setTimeout(r, 3000));
  const latest = readLatestRun();
  if (latest) {
    updateDashboardIfOpen({
      available: true,
      state: 'ok',
      refreshedAt: new Date(),
    });
  }
}
