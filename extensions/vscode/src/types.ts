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
  recommended_command?: string;
  policy?: string;
  time?: string;
  mode?: string;
  reason?: string;
  source_file?: string;
  // turn-event fields (kimi turn-events.jsonl)
  status?: string;
  session_id?: string;
  cwd?: string;
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
  source: "vscode-terminal";
  time: string;
  commandLine: string;
  confidence: number;
  terminalName: string;
  cwd?: string;
}

export interface LatestRun {
  timestamp: string;
  command: string;
  exit_code: number;
  raw_bytes: number;
  summary_bytes: number;
  saved_tokens?: number;
  saved_tokens_display?: string;
  estimated_reduction: number;
  duration_ms: number;
  filter: string;
  confidence: string;
  policy: string;
  raw_log: string;
}

export interface CurrentRunState {
  schema_version?: number;
  status: "running" | "completed" | "failed";
  command?: string;
  started_at?: string;
  heartbeat_at?: string;
  completed_at?: string;
  finished_at?: string;
  exit_code?: number;
  raw_bytes?: number;
  summary_bytes?: number;
  saved_bytes?: number;
  saved_tokens?: number;
  saved_tokens_display?: string;
  estimated_reduction?: number;
  raw_log?: string;
  pid?: number;
}

export interface XiTStatus {
  available: boolean;
  state: "ok" | "binary-not-found" | "gain-json-failed" | "no-data";
  gain?: GainData;
  activity?: GlobalActivity;
  error?: string;
  binary?: string;
  cwd?: string;
  attempts?: string[];
  refreshedAt: Date;
}

export interface TerminalEventRecord {
  time: string;
  commandLine: string;
  terminalName: string;
  cwd?: string;
}

export interface LatestRawLogMeta {
  path: string;
  mtimeMs: number;
  size: number;
}

export interface WorkflowHealth {
  cliStatus: "found" | "missing";
  latestRunStatus: "success" | "none";
  latestSavedBytes: number;
  latestSavedDisplay: string;
  workspaceRulesInstalled: boolean;
  workspaceRuleFiles: string[];
  recentHighNoiseCommands: number;
  recentHighNoiseRouted: number;
  routingHitRate: number;
  recommendation: string;
}

export interface TokenMetrics {
  rawTokens: number;
  summaryTokens: number;
  savedTokens: number;
  savedDisplay: string;
  reductionPct: number;
}

export interface TokenImpactStats {
  latest?: TokenMetrics;
  todaySavedTokens: number;
  todaySavedDisplay: string;
  workspaceTotalSavedTokens: number;
  workspaceTotalSavedDisplay: string;
  topTokenHeavyCommands: Array<{
    command: string;
    runs: number;
    savedTokens: number;
    savedDisplay: string;
    rawTokens: number;
    summaryTokens: number;
  }>;
}

export interface AdapterHealthItem {
  adapter: "Codex" | "Claude" | "Gemini" | "Cursor";
  status: "verified" | "rules installed" | "unknown" | "not verified";
  evidence: string;
  ruleFiles: string[];
  routedCount?: number;
  observedCount?: number;
}

export interface VerifyRoutingReport {
  workspacePath: string;
  rulesFilesInstalled: string[];
  currentRunState: "running" | "completed" | "failed" | "none";
  latestRunTime?: string;
  latestRunRawLog?: string;
  latestHighNoiseCommands: string[];
  latestXiTAutoCommands: string[];
  recentHighNoiseCommands: number;
  recentHighNoiseRouted: number;
  codex: AdapterHealthItem;
  claude: AdapterHealthItem;
  gemini: AdapterHealthItem;
  cursor: AdapterHealthItem;
  recommendation: string;
}

export interface TurnState {
  status: string;
  event?: string;
  started_at?: string;
  finished_at?: string;
  session_id?: string;
  cwd?: string;
}

export type AgentTurnStatus =
  | "idle"
  | "working"
  | "xit_running"
  | "completed"
  | "stopped"
  | "unknown";

export interface LatestActivityInfo {
  adapter: string;
  timestamp: string;
  eventType: string;
  command?: string;
  cwd?: string;
  routedThroughXit: boolean;
  reason: string;
  sourceFile: string;
}

export interface StaleTurnRecord {
  adapter: string;
  stoppedAt?: string;
  ageHours: number;
  reason: string;
}

export type LiveStatusKind =
  | "xit_running"
  | "xit_completed"
  | "agent_observing"
  | "agent_not_routed"
  | "agent_routed_pending_state"
  | "idle"
  | "missing";

export interface LiveStatusView {
  kind: LiveStatusKind;
  label: string;
  adapter?: string;
  command?: string;
  reason?: string;
  source?: string;
  updatedAt?: string;
  savedTokensDisplay?: string;
}

export interface AgentTurnView {
  adapter: "claude" | "codex" | "kimi" | "gemini" | "cursor" | "unknown";
  status: AgentTurnStatus;
  latestEvent?: string;
  startedAt?: string;
  updatedAt?: string;
  commandsObserved: number;
  routedThroughXit: number;
  savedTokensThisTurn: number;
  savedTokensDisplay: string;
  evidence: string[];
  isFreshActive: boolean;
  staleTurnReason?: string;
  latestActivity?: LatestActivityInfo;
  selectedTurnSource?: string;
  selectedActivitySource?: string;
  ignoredStaleTurns: StaleTurnRecord[];
}

export interface AdapterHookInfo {
  connected: boolean;
  hasTurnLifecycle: boolean;
  latestEventTime?: string;
  eventCount: number;
}

export interface DiagnoseReport {
  binaryPath?: string;
  cliVersion?: string;
  workspacePath: string;
  watchedStatePath: string;
  watchedHistoryPath: string;
  watchedRunsDir: string;
  stateFileExists: boolean;
  historyFileExists: boolean;
  agentsMdDetected: boolean;
  claudeMdDetected: boolean;
  hasRunsDir: boolean;
  currentRunState?: "running" | "completed" | "failed" | "none";
  latestRunTime?: string;
  latestHistoryTimestamp?: string;
  latestSavedBytes?: number;
  latestSavedDisplay?: string;
  latestRawLogPath?: string;
  recentHighNoiseCommands: number;
  recentHighNoiseRouted: number;
  routingHitRate: number;
  workspaceRulesInstalled: boolean;
  workspaceRuleFiles: string[];
  recommendation?: string;
}
