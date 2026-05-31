import * as vscode from 'vscode';
import * as child_process from 'child_process';
import * as path from 'path';
import * as fs from 'fs';
import * as os from 'os';
import type { GainData, AdapterEvent, XiTStatus } from './types';

const OUTPUT_CHANNEL = vscode.window.createOutputChannel('XiT Status');

function getConfig(): vscode.WorkspaceConfiguration {
  return vscode.workspace.getConfiguration('xit');
}

function log(message: string): void {
  OUTPUT_CHANNEL.appendLine(message);
}

function showOutput(): void {
  OUTPUT_CHANNEL.show(true);
}

function resolveXiTHome(): string {
  const cfg = getConfig();
  const configured = cfg.get<string>('home', '');
  if (configured) {
    return configured;
  }
  return path.join(os.homedir(), '.xit');
}

function resolveBinary(): string {
  const cfg = getConfig();
  const configured = cfg.get<string>('binaryPath', '');
  if (configured) {
    return configured;
  }

  // Try PATH
  const fromPath = tryWhich('xit');
  if (fromPath) {
    return fromPath;
  }

  // Try ~/.local/bin/xit
  const localBin = path.join(os.homedir(), '.local', 'bin', 'xit');
  if (fs.existsSync(localBin)) {
    return localBin;
  }

  // Try workspace ./xit
  const workspaceFolders = vscode.workspace.workspaceFolders;
  if (workspaceFolders && workspaceFolders.length > 0) {
    const workspaceBin = path.join(workspaceFolders[0].uri.fsPath, 'xit');
    if (fs.existsSync(workspaceBin)) {
      return workspaceBin;
    }
  }

  return 'xit';
}

function tryWhich(command: string): string | undefined {
  try {
    const envPath = process.env.PATH || '';
    const paths = envPath.split(path.delimiter);
    for (const p of paths) {
      const candidate = path.join(p, command);
      if (fs.existsSync(candidate)) {
        return candidate;
      }
      if (process.platform === 'win32') {
        const candidateExe = candidate + '.exe';
        if (fs.existsSync(candidateExe)) {
          return candidateExe;
        }
      }
    }
  } catch {
    // ignore
  }
  return undefined;
}

function execFilePromise(
  file: string,
  args: string[],
  timeoutMs = 5000
): Promise<{ stdout: string; stderr: string }> {
  return new Promise((resolve, reject) => {
    const child = child_process.execFile(
      file,
      args,
      { timeout: timeoutMs, encoding: 'utf-8' },
      (error, stdout, stderr) => {
        if (error) {
          reject(error);
        } else {
          resolve({ stdout: stdout as string, stderr: stderr as string });
        }
      }
    );
  });
}

export async function fetchGain(): Promise<GainData | undefined> {
  const binary = resolveBinary();
  try {
    const { stdout } = await execFilePromise(binary, ['gain', '--json']);
    const data = JSON.parse(stdout) as GainData;
    return data;
  } catch (err) {
    log(`fetchGain failed: ${err}`);
    return undefined;
  }
}

export async function fetchStatus(): Promise<XiTStatus> {
  const gain = await fetchGain();
  if (!gain) {
    return { available: false, refreshedAt: new Date() };
  }
  return { available: true, gain, refreshedAt: new Date() };
}

export function readRecentEvents(adapter: string, maxLines = 20): AdapterEvent[] {
  const home = resolveXiTHome();
  const eventPaths: Record<string, string[]> = {
    cursor: [path.join(home, 'cursor-hooks', 'events.jsonl')],
    codex: [path.join(home, 'codex-hooks', 'events.jsonl')],
    claude: [path.join(home, 'claude-hooks', 'events.jsonl')],
    kimi: [
      path.join(home, 'kimi-hooks', 'turn-events.jsonl'),
      path.join(home, 'kimi-hooks', 'events.jsonl'),
    ],
  };

  const paths = eventPaths[adapter] || [];
  const events: AdapterEvent[] = [];

  for (const p of paths) {
    try {
      if (!fs.existsSync(p)) {
        continue;
      }
      const content = fs.readFileSync(p, 'utf-8');
      const lines = content.split('\n').filter((l) => l.trim().length > 0);
      const tail = lines.slice(-maxLines);
      for (const line of tail) {
        try {
          const obj = JSON.parse(line) as AdapterEvent;
          obj.adapter = adapter;
          events.push(obj);
        } catch {
          // skip malformed lines
        }
      }
    } catch {
      // skip unreadable files
    }
  }

  return events.reverse();
}

export function readWorkspaceHistory(maxLines = 20): AdapterEvent[] {
  const folders = vscode.workspace.workspaceFolders;
  if (!folders || folders.length === 0) {
    return [];
  }
  const historyPath = path.join(folders[0].uri.fsPath, '.xit', 'history.jsonl');
  try {
    if (!fs.existsSync(historyPath)) {
      return [];
    }
    const content = fs.readFileSync(historyPath, 'utf-8');
    const lines = content.split('\n').filter((l) => l.trim().length > 0);
    const tail = lines.slice(-maxLines);
    return tail
      .map((line) => {
        try {
          return JSON.parse(line) as AdapterEvent;
        } catch {
          return undefined;
        }
      })
      .filter((e): e is AdapterEvent => e !== undefined)
      .reverse();
  } catch {
    return [];
  }
}

export function findLatestRawLog(): string | undefined {
  const folders = vscode.workspace.workspaceFolders;
  if (!folders || folders.length === 0) {
    return undefined;
  }
  const runsDir = path.join(folders[0].uri.fsPath, '.xit', 'runs');
  try {
    if (!fs.existsSync(runsDir)) {
      return undefined;
    }
    const files = fs.readdirSync(runsDir).filter((f) => f.endsWith('.raw.log'));
    if (files.length === 0) {
      return undefined;
    }
    let latest = files[0];
    let latestMtime = fs.statSync(path.join(runsDir, latest)).mtimeMs;
    for (const f of files.slice(1)) {
      const mtime = fs.statSync(path.join(runsDir, f)).mtimeMs;
      if (mtime > latestMtime) {
        latest = f;
        latestMtime = mtime;
      }
    }
    return path.join(runsDir, latest);
  } catch {
    return undefined;
  }
}

export async function openLatestRawLog(): Promise<void> {
  const logPath = findLatestRawLog();
  if (!logPath) {
    vscode.window.showInformationMessage('XiT: No raw log found in workspace .xit/runs/');
    return;
  }
  const doc = await vscode.workspace.openTextDocument(logPath);
  await vscode.window.showTextDocument(doc);
}

export { showOutput, log };
