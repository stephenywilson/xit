export interface GainData {
  total_commands_condensed: number;
  raw_bytes: number;
  summary_bytes: number;
  saved_bytes: number;
  estimated_reduction: number;
  saved_tokens: number;
  saved_tokens_display: string;
  top_commands: TopCommand[];
  warnings?: string[];
  sources: {
    history_path: string;
    runs_dir: string;
  };
}

export interface TopCommand {
  command: string;
  runs: number;
  raw_bytes: number;
  summary_bytes: number;
  saved_bytes: number;
  saved_tokens: number;
  saved_tokens_display: string;
}

export interface AdapterEvent {
  adapter?: string;
  action?: string;
  event?: string;
  original_command?: string;
  policy?: string;
  time?: string;
  mode?: string;
  reason?: string;
}

export interface GlobalActivity {
  latestAdapter?: string;
  latestTime?: string;
  latestCommand?: string;
  latestPolicy?: string;
  eventCount: number;
  adapterCounts: Record<string, number>;
}

export interface TerminalEvent {
  source: 'vscode-terminal';
  time: string;
  commandLine: string;
  confidence: number;
  terminalName: string;
  cwd?: string;
}

export interface XiTStatus {
  available: boolean;
  state: 'ok' | 'binary-not-found' | 'gain-json-failed' | 'no-data';
  gain?: GainData;
  activity?: GlobalActivity;
  error?: string;
  binary?: string;
  cwd?: string;
  attempts?: string[];
  refreshedAt: Date;
}
