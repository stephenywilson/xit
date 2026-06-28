import * as vscode from 'vscode';
import { buildXiTAutoCommand, isAlreadyWrappedByXiT, isHighOutputCommand } from './logic';
import { readLatestRun } from './xit';
import { updateDashboardIfOpen } from './dashboard';

let xitTerminal: vscode.Terminal | undefined;

export interface VsCodeCommandRunRequest {
  originalCommand: string;
  finalCommand: string;
  mode: "auto" | "passthrough";
  terminalName: string;
}

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

export async function promptRunCommand(
  onWillRun?: (request: VsCodeCommandRunRequest) => void,
): Promise<VsCodeCommandRunRequest | undefined> {
  const command = await vscode.window.showInputBox({
    prompt: 'Enter shell command',
    placeHolder: 'go test -v ./...',
  });
  if (!command || !command.trim()) {
    return undefined;
  }

  const trimmed = command.trim();
  const isHigh = isHighOutputCommand(trimmed);
  if (isHigh) {
    const finalCommand = buildXiTAutoCommand(trimmed);
    const request = {
      originalCommand: trimmed,
      finalCommand,
      mode: "auto" as const,
      terminalName: getXiTTerminal().name,
    };
    onWillRun?.(request);
    runInXiTTerminal(finalCommand);
    vscode.window.showInformationMessage(`XiT: running high-output command with auto compression`);
    return request;
  } else {
    const choice = await vscode.window.showInformationMessage(
      `XiT: passthrough command detected`,
      { modal: false },
      'Run directly',
      'Run with xit auto'
    );
    if (choice === 'Run with xit auto') {
      const finalCommand = buildXiTAutoCommand(trimmed);
      const request = {
        originalCommand: trimmed,
        finalCommand,
        mode: "auto" as const,
        terminalName: getXiTTerminal().name,
      };
      onWillRun?.(request);
      runInXiTTerminal(finalCommand);
      return request;
    } else if (choice === 'Run directly') {
      const request = {
        originalCommand: trimmed,
        finalCommand: trimmed,
        mode: "passthrough" as const,
        terminalName: getXiTTerminal().name,
      };
      onWillRun?.(request);
      runInXiTTerminal(trimmed);
      return request;
    }
  }
  return undefined;
}

export async function promptRunWithAutoCompression(
  onWillRun?: (request: VsCodeCommandRunRequest) => void,
): Promise<VsCodeCommandRunRequest | undefined> {
  const command = await vscode.window.showInputBox({
    prompt: 'Enter shell command (will run with xit auto)',
    placeHolder: 'go test -v ./...',
  });
  if (!command || !command.trim()) {
    return undefined;
  }
  const trimmed = command.trim();
  const finalCommand = buildXiTAutoCommand(trimmed);
  const request = {
    originalCommand: trimmed,
    finalCommand,
    mode: "auto" as const,
    terminalName: getXiTTerminal().name,
  };
  onWillRun?.(request);
  runInXiTTerminal(finalCommand);
  return request;
}

export async function handleTerminalHighOutput(commandLine: string): Promise<void> {
  if (isAlreadyWrappedByXiT(commandLine)) {
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
    await vscode.env.clipboard.writeText(buildXiTAutoCommand(commandLine));
    vscode.window.showInformationMessage('Copied to clipboard');
  } else if (action === 'Run in XiT Terminal') {
    runInXiTTerminal(buildXiTAutoCommand(commandLine));
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
