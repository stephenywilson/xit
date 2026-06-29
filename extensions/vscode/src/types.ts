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
  // AI is thinking about a new prompt — no tool call yet. Bridge-only
  // (turn.started); there is no local/manual equivalent.
  | "xit_turn_active"
  | "xit_running"
  // run.finished accepted (data ready: saved tokens/reduction/run count),
  // but the AI's final answer for this turn isn't done yet — bridge-only
  // (turn.finished, or a fallback timer, promotes this to xit_completed /
  // agent_not_routed). "Data ready" is not "experience done".
  | "xit_settling"
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
  exitCode?: number;
  reductionPct?: number;
  summaryBytes?: number;
  savedTokens?: number;
  savedBytes?: number;
  runCount?: number;
}

export interface VscodeAiBridgeEvent {
  schema: "xit.vscode-ai-bridge.v1";
  // turn.started/turn.finished are turn-LEVEL lifecycle signals ("AI started
  // thinking about a new prompt" / "AI's final answer for this turn is
  // done") — distinct from run.started/run.finished, which are per-tool-call.
  event: "run.started" | "run.finished" | "turn.started" | "turn.finished";
  host: "vscode";
  surface: "codex_chat" | "claude_code";
  adapter: "codex" | "claude";
  workspace_hash: string;
  host_instance_hash: string;
  // thread_hash is Codex-only diagnostics (hashed session id) — Claude
  // Code's PreToolUse hook bridge has no equivalent signal to hash, so it's
  // omitted there rather than faked.
  thread_hash?: string;
  run_id: string;
  command_hash: string;
  started_at: string;
  finished_at?: string;
  exit_code?: number;
  saved_tokens?: number;
  saved_bytes?: number;
  summary_bytes?: number;
  run_count?: number;
}

// Internal/dev-console diagnostics for the VS Code AI bridge watcher(s).
// Deliberately limited to timestamps, counters, and already-hashed
// workspace/host signals — never the raw command, cwd, output, prompt, AI
// reply, or full session id that produced a bridge event.
export interface BridgeDiagnostics {
  last_event_at?: string;
  last_accepted_event_at?: string;
  last_dropped_event_at?: string;
  last_drop_reason?: string;
  accepted_event_count: number;
  dropped_event_count: number;
  watcher_alive: boolean;
  event_file_path: string[];
  current_workspace_hash?: string;
  last_event_workspace_hash?: string;
  last_event_host_instance_hash?: string;
  last_event_source?: "primary" | "mirror";
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
