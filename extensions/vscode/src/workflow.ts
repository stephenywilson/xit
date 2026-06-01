import * as vscode from 'vscode';
import * as fs from 'fs';
import * as path from 'path';
import { execFile } from 'child_process';
import type {
  AdapterEvent,
  DiagnoseReport,
  LatestRun,
  TerminalEventRecord,
  WorkflowHealth,
  XiTStatus,
} from './types';
import {
  isHighOutputCommand,
  readRecentEvents,
  readTerminalEvents,
  readWorkspaceHistory,
  resolveAvailableBinary,
  resolveWorkspaceCwd,
} from './xit';

const RULES_START = '<!-- XIT_AI_RULES_START -->';
const RULES_END = '<!-- XIT_AI_RULES_END -->';

const RULES_SECTION = `${RULES_START}
## XiT command-output rule

For high-noise commands, prefer running them through XiT:

\`\`\`bash
./xit auto <command>
\`\`\`

Use XiT for:

* \`go test -v ./...\`
* \`npm test\`
* \`docker logs ...\`
* \`tsc --noEmit\`
* \`eslint .\`
* large \`git diff\`
* long \`rg\` / \`find\` searches

Do not use XiT for short commands:

* \`pwd\`
* \`git status\`
* \`go version\`
* \`node --version\`
* short \`ls\`
* small \`cat\`

XiT compresses noisy terminal output locally and preserves raw logs for audit. It does not read chat content.
${RULES_END}`;

interface RuleInstallResult {
  files: string[];
  created: string[];
  updated: string[];
}

interface WorkflowEvent {
  time: string;
  command: string;
  routedThroughXiT: boolean;
}

function isWorkspaceAvailable(): boolean {
  return !!(vscode.workspace.workspaceFolders && vscode.workspace.workspaceFolders.length > 0);
}

function getWorkspaceRoot(): string | undefined {
  const folders = vscode.workspace.workspaceFolders;
  return folders && folders.length > 0 ? folders[0].uri.fsPath : undefined;
}

function normalizeLineEndings(text: string): string {
  return text.replace(/\r\n/g, '\n');
}

function upsertMarkedSection(existingContent: string, section: string): string {
  const content = normalizeLineEndings(existingContent);
  const startIndex = content.indexOf(RULES_START);
  const endIndex = content.indexOf(RULES_END);

  if (startIndex !== -1 && endIndex !== -1 && endIndex > startIndex) {
    const afterEnd = endIndex + RULES_END.length;
    const before = content.slice(0, startIndex).replace(/\s*$/, '');
    const after = content.slice(afterEnd).replace(/^\s*/, '');
    return `${before}\n\n${section}${after ? `\n\n${after}` : ''}\n`;
  }

  const trimmed = content.trimEnd();
  if (!trimmed) {
    return `${section}\n`;
  }
  return `${trimmed}\n\n${section}\n`;
}

function collectExistingRuleTargets(root: string): string[] {
  const targets: string[] = [];
  const directFiles = ['AGENTS.md', 'CLAUDE.md'];
  for (const file of directFiles) {
    const fullPath = path.join(root, file);
    if (fs.existsSync(fullPath) && fs.statSync(fullPath).isFile()) {
      targets.push(fullPath);
    }
  }

  const codexDir = path.join(root, '.codex');
  if (fs.existsSync(codexDir) && fs.statSync(codexDir).isDirectory()) {
    for (const entry of fs.readdirSync(codexDir, { withFileTypes: true })) {
      if (entry.isFile() && /\.(md|markdown|txt|mdc)$/i.test(entry.name)) {
        targets.push(path.join(codexDir, entry.name));
      }
    }
  }

  const cursorRulesDir = path.join(root, '.cursor', 'rules');
  if (fs.existsSync(cursorRulesDir) && fs.statSync(cursorRulesDir).isDirectory()) {
    for (const entry of fs.readdirSync(cursorRulesDir, { withFileTypes: true })) {
      if (entry.isFile() && /\.(md|markdown|txt|mdc)$/i.test(entry.name)) {
        targets.push(path.join(cursorRulesDir, entry.name));
      }
    }
  }

  return [...new Set(targets)];
}

export function installWorkspaceAiRules(): RuleInstallResult {
  const root = getWorkspaceRoot();
  if (!root) {
    throw new Error('No workspace folder is open.');
  }

  const existingTargets = collectExistingRuleTargets(root);
  const targets = existingTargets.length > 0 ? existingTargets : [path.join(root, 'AGENTS.md')];
  const created: string[] = [];
  const updated: string[] = [];

  for (const target of targets) {
    const alreadyExists = fs.existsSync(target);
    const current = alreadyExists ? fs.readFileSync(target, 'utf-8') : '';
    const next = upsertMarkedSection(current, RULES_SECTION);
    if (!alreadyExists) {
      fs.mkdirSync(path.dirname(target), { recursive: true });
      created.push(target);
    }
    if (current !== next) {
      fs.writeFileSync(target, next, 'utf-8');
      updated.push(target);
    }
  }

  return { files: targets, created, updated };
}

export function getWorkspaceRuleStatus(): { installed: boolean; files: string[] } {
  const root = getWorkspaceRoot();
  if (!root) {
    return { installed: false, files: [] };
  }

  const targets = collectExistingRuleTargets(root);
  const installedFiles = targets.filter((target) => {
    try {
      const content = fs.readFileSync(target, 'utf-8');
      return content.includes(RULES_START) && content.includes(RULES_END);
    } catch {
      return false;
    }
  });

  const fallbackAgents = path.join(root, 'AGENTS.md');
  if (installedFiles.length === 0 && fs.existsSync(fallbackAgents)) {
    try {
      const content = fs.readFileSync(fallbackAgents, 'utf-8');
      if (content.includes(RULES_START) && content.includes(RULES_END)) {
        installedFiles.push(fallbackAgents);
      }
    } catch {
      // ignore
    }
  }

  return { installed: installedFiles.length > 0, files: [...new Set(installedFiles)] };
}

function mapAdapterEvent(event: AdapterEvent): WorkflowEvent | undefined {
  const command = (event.original_command || '').trim();
  if (!command) {
    return undefined;
  }

  const recommended = (event.recommended_command || '').trim();
  const routedThroughXiT = /\bxit\s+auto\b/.test(command) || /\bxit\s+auto\b/.test(recommended);

  return {
    time: event.time || '',
    command,
    routedThroughXiT,
  };
}

function mapTerminalEvent(event: TerminalEventRecord): WorkflowEvent {
  return {
    time: event.time,
    command: event.commandLine,
    routedThroughXiT: /\bxit\s+auto\b/.test(event.commandLine),
  };
}

export function getRecentWorkflowRoutingStats(limit = 20): {
  recentHighNoiseCommands: number;
  recentHighNoiseRouted: number;
  routingHitRate: number;
} {
  const terminalEvents = readTerminalEvents(limit).map(mapTerminalEvent);
  const adapterSources = ['codex', 'claude', 'cursor', 'kimi']
    .flatMap((adapter) => readRecentEvents(adapter, limit))
    .map(mapAdapterEvent)
    .filter((event): event is WorkflowEvent => event !== undefined);
  const workspaceHistory = readWorkspaceHistory(limit)
    .map(mapAdapterEvent)
    .filter((event): event is WorkflowEvent => event !== undefined);

  const merged = terminalEvents
    .concat(adapterSources)
    .concat(workspaceHistory)
    .sort((a, b) => b.time.localeCompare(a.time))
    .slice(0, limit);

  let recentHighNoiseCommands = 0;
  let recentHighNoiseRouted = 0;
  const seen = new Set<string>();

  for (const event of merged) {
    const key = `${event.time}|${event.command}`;
    if (seen.has(key)) {
      continue;
    }
    seen.add(key);

    const normalized = event.command.replace(/\bxit\s+auto\s+/, '').trim();
    if (!isHighOutputCommand(normalized)) {
      continue;
    }
    recentHighNoiseCommands += 1;
    if (event.routedThroughXiT) {
      recentHighNoiseRouted += 1;
    }
  }

  return {
    recentHighNoiseCommands,
    recentHighNoiseRouted,
    routingHitRate: recentHighNoiseCommands > 0 ? recentHighNoiseRouted / recentHighNoiseCommands : 0,
  };
}

export function computeWorkflowHealth(status: XiTStatus, latestRun: LatestRun | undefined): WorkflowHealth {
  const rules = getWorkspaceRuleStatus();
  const routing = getRecentWorkflowRoutingStats(20);
  const latestSavedBytes = latestRun ? Math.max(0, latestRun.raw_bytes - latestRun.summary_bytes) : 0;

  let recommendation = 'XiT is active for this workspace';
  if (!rules.installed) {
    recommendation = 'Run XiT: Install Workspace AI Rules';
  } else if (routing.recentHighNoiseCommands > 0 && routing.recentHighNoiseRouted === 0) {
    recommendation = 'High-noise commands are not routed through XiT yet';
  }

  return {
    cliStatus: status.available || status.state === 'gain-json-failed' ? 'found' : 'missing',
    latestRunStatus: latestRun ? 'success' : 'none',
    latestSavedBytes,
    latestSavedDisplay: formatSavedBytes(latestSavedBytes),
    workspaceRulesInstalled: rules.installed,
    workspaceRuleFiles: rules.files,
    recentHighNoiseCommands: routing.recentHighNoiseCommands,
    recentHighNoiseRouted: routing.recentHighNoiseRouted,
    routingHitRate: routing.routingHitRate,
    recommendation,
  };
}

function execFilePromise(file: string, args: string[], cwd: string, timeoutMs = 5000): Promise<string> {
  return new Promise((resolve, reject) => {
    execFile(file, args, { cwd, timeout: timeoutMs, encoding: 'utf-8' }, (error, stdout, stderr) => {
      if (error) {
        reject(new Error(stderr || error.message));
        return;
      }
      resolve(stdout);
    });
  });
}

export async function buildDiagnoseReport(status: XiTStatus, latestRun: LatestRun | undefined): Promise<DiagnoseReport> {
  const workspacePath = resolveWorkspaceCwd();
  const rules = getWorkspaceRuleStatus();
  const routing = getRecentWorkflowRoutingStats(20);
  const binaryPath = resolveAvailableBinary();
  let cliVersion: string | undefined;

  if (binaryPath) {
    try {
      cliVersion = (await execFilePromise(binaryPath, ['--version'], workspacePath)).trim();
    } catch {
      cliVersion = undefined;
    }
  }

  const runsDir = path.join(workspacePath, '.xit', 'runs');
  const latestSavedBytes = latestRun ? Math.max(0, latestRun.raw_bytes - latestRun.summary_bytes) : undefined;
  const recommendation = !rules.installed
    ? 'Run XiT: Install Workspace AI Rules'
    : routing.recentHighNoiseCommands > 0 && routing.recentHighNoiseRouted === 0
      ? 'High-noise commands are not routed through XiT yet'
      : 'XiT is active for this workspace';

  return {
    binaryPath: status.binary || binaryPath,
    cliVersion,
    workspacePath,
    hasRunsDir: fs.existsSync(runsDir),
    latestRunTime: latestRun?.timestamp,
    latestSavedBytes,
    latestSavedDisplay: latestSavedBytes !== undefined ? formatSavedBytes(latestSavedBytes) : undefined,
    latestRawLogPath: latestRun?.raw_log,
    recentHighNoiseCommands: routing.recentHighNoiseCommands,
    recentHighNoiseRouted: routing.recentHighNoiseRouted,
    routingHitRate: routing.routingHitRate,
    workspaceRulesInstalled: rules.installed,
    workspaceRuleFiles: rules.files,
    recommendation,
  };
}

export function formatSavedBytes(bytes: number): string {
  if (bytes >= 1000 * 1000) {
    return `~${Math.round(bytes / (1000 * 1000))}MB`;
  }
  if (bytes >= 1000) {
    return `~${Math.round(bytes / 1000)}KB`;
  }
  return `${bytes}B`;
}
