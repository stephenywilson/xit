import * as crypto from "node:crypto";
import * as path from "node:path";
import type { CurrentRunState, LatestRun, LiveStatusView, VscodeAiBridgeEvent } from "./types";

export interface ActiveVsCodeRun {
  startedAt: number;
  originalCommand: string;
  normalizedCommand: string;
  terminalName: string;
  mode: "auto" | "passthrough";
  state: "running" | "finishing";
  workspacePath?: string;
}

export interface RunRecordCandidate {
  command?: string;
  timestamp?: string;
  started_at?: string;
  completed_at?: string;
  finished_at?: string;
  raw_log?: string;
}

export interface TodayStats {
  todayCount: number;
  todaySavedTokens: number;
}

export function estimateTokensFromBytes(bytes: number | undefined): number {
  return Math.max(0, Math.round((bytes || 0) / 4));
}

export function getSavedTokens(
  savedTokens?: number,
  savedBytes?: number,
): number | undefined {
  if (typeof savedTokens === "number" && Number.isFinite(savedTokens) && savedTokens >= 0) {
    return Math.round(savedTokens);
  }
  if (typeof savedBytes === "number" && Number.isFinite(savedBytes) && savedBytes >= 0) {
    return estimateTokensFromBytes(savedBytes);
  }
  return undefined;
}

export function savedTokensFromRun(run: Pick<LatestRun, "saved_tokens" | "raw_bytes" | "summary_bytes">): number {
  const explicit = getSavedTokens(run.saved_tokens);
  if (explicit !== undefined) {
    return explicit;
  }
  return estimateTokensFromBytes(Math.max(0, (run.raw_bytes || 0) - (run.summary_bytes || 0)));
}

export function savedTokensFromCurrentRun(
  state: Pick<CurrentRunState, "saved_tokens" | "saved_bytes" | "raw_bytes" | "summary_bytes">,
): number | undefined {
  const explicit = getSavedTokens(state.saved_tokens, state.saved_bytes);
  if (explicit !== undefined) {
    return explicit;
  }
  if (typeof state.raw_bytes === "number" || typeof state.summary_bytes === "number") {
    return estimateTokensFromBytes(Math.max(0, (state.raw_bytes || 0) - (state.summary_bytes || 0)));
  }
  return undefined;
}

export function formatSavedTokens(
  savedTokens?: number,
  savedBytes?: number,
): string | undefined {
  const tokens = getSavedTokens(savedTokens, savedBytes);
  if (tokens === undefined) {
    return undefined;
  }
  if (tokens >= 1_000_000) {
    return `约 ${(tokens / 1_000_000).toFixed(2)}M Token`;
  }
  if (tokens >= 1000) {
    return `约 ${(tokens / 1000).toFixed(2)}k Token`;
  }
  return `${tokens} Token`;
}

function shellQuoteToken(token: string): string {
  if (/^[A-Za-z0-9_./:@%+=,-]+$/.test(token)) {
    return token;
  }
  return `'${token.replace(/'/g, `'\\''`)}'`;
}

function unquoteShellString(value: string): string {
  const trimmed = value.trim();
  if (
    (trimmed.startsWith("'") && trimmed.endsWith("'")) ||
    (trimmed.startsWith('"') && trimmed.endsWith('"'))
  ) {
    return trimmed.slice(1, -1);
  }
  return trimmed;
}

function extractShellCommand(command: string): string | undefined {
  const trimmed = command.trim();
  const match = trimmed.match(/^(?:bash|sh|zsh)\s+-lc\s+(.+)$/i);
  if (!match) {
    return undefined;
  }
  return unquoteShellString(match[1]);
}

function stripLeadingEnvAssignments(command: string): string {
  let rest = command.trim();
  if (rest.startsWith("env ")) {
    rest = rest.slice(4).trimStart();
  }
  while (/^[A-Za-z_][A-Za-z0-9_]*=/.test(rest)) {
    const match = rest.match(/^[A-Za-z_][A-Za-z0-9_]*=(?:"[^"]*"|'[^']*'|\S+)\s*/);
    if (!match) {
      break;
    }
    rest = rest.slice(match[0].length).trimStart();
  }
  return rest;
}

export function normalizeShellWhitespace(command: string): string {
  return command.trim().replace(/\s+/g, " ");
}

export function unwrapXiTAutoCommand(command: string): string {
  const inner = extractShellCommand(command);
  if (inner) {
    return unwrapXiTAutoCommand(inner);
  }
  const stripped = stripLeadingEnvAssignments(command);
  return stripped.replace(/^(?:(?:\.\/|\/[^\s"'`]+\/)?xit)\s+auto(?:\s+|$)/, "").trim();
}

export function isAlreadyWrappedByXiT(command: string): boolean {
  const inner = extractShellCommand(command);
  if (inner) {
    return isAlreadyWrappedByXiT(inner);
  }
  const stripped = stripLeadingEnvAssignments(command);
  return /^(?:(?:\.\/|\/[^\s"'`]+\/)?xit)\s+auto(?:\s|$)/.test(stripped);
}

export function buildXiTAutoCommand(
  command: string,
  binaryPath = "xit",
  _shellKind = "sh",
): string {
  const trimmed = command.trim();
  if (!trimmed || isAlreadyWrappedByXiT(trimmed)) {
    return trimmed;
  }
  return `${shellQuoteToken(binaryPath)} auto ${trimmed}`;
}

function commandForDetection(command: string): string {
  const inner = extractShellCommand(command);
  if (inner) {
    return commandForDetection(inner);
  }
  return stripLeadingEnvAssignments(command).trim().toLowerCase();
}

function largeRange(a: string, b: string): boolean {
  const start = Number.parseInt(a, 10);
  const end = Number.parseInt(b, 10);
  if (!Number.isFinite(start) || !Number.isFinite(end)) {
    return false;
  }
  return Math.abs(end - start) + 1 >= 200;
}

function hasShellHighOutputStructure(command: string): boolean {
  const patterns = [
    /\b(?:for|while)\b.*\{(-?\d+)\.\.(-?\d+)(?:\.\.\d+)?\}.*\bdo\b.*\b(?:echo|printf)\b.*\bdone\b/is,
    /\bfor\s+\w+\s+in\s+(?:\$\()?seq\s+(-?\d+)\s+(-?\d+).*?\bdo\b.*\b(?:echo|printf)\b.*\bdone\b/is,
    /\bwhile\b.*?(?:<=|<|-le|-lt)\s*(-?\d+).*?\bdo\b.*\b(?:echo|printf)\b.*\bdone\b/is,
    /(?:^|[;&|\s])seq\s+(-?\d+)\s+(-?\d+).*?\|/is,
  ];
  if (/(?:^|[;&|\s])yes(?:\s|$)/is.test(command)) {
    return true;
  }
  return patterns.some((pattern) => {
    const match = command.match(pattern);
    return !!match && largeRange(match[1], match[2]);
  });
}

export function isHighOutputCommand(command: string): boolean {
  const trimmed = commandForDetection(command);
  if (!trimmed || isAlreadyWrappedByXiT(trimmed)) {
    return false;
  }

  if (/\b(--json|--porcelain|-q|--quiet)\b/.test(trimmed)) {
    return false;
  }

  const passthroughPatterns = [
    /^pwd(?:\s|$)/,
    /^whoami(?:\s|$)/,
    /^echo\s+/,
    /^ls(?:\s|$)/,
    /^cat\s+\S+$/,
    /^go\s+version(?:\s|$)/,
    /^node\s+--version(?:\s|$)/,
    /^npm\s+--version(?:\s|$)/,
    /^git\s+status(?:\s|$)/,
    /^git\s+branch(?:\s|$)/,
    /^git\s+log\s+--oneline(?:\s|$)/,
    /^git\s+show\s+--stat(?:\s|$)/,
  ];
  if (passthroughPatterns.some((pattern) => pattern.test(trimmed))) {
    return false;
  }

  if (hasShellHighOutputStructure(trimmed)) {
    return true;
  }

  const highOutputPatterns = [
    /^go\s+test(?:\s|$)/,
    /^npm\s+test(?:\s|$)/,
    /^npm\s+run\s+(?:build|lint|test)(?:\s|$)/,
    /^pnpm\s+test(?:\s|$)/,
    /^pnpm\s+(?:run\s+)?(?:build|lint|test)(?:\s|$)/,
    /^pnpm\s+exec\s+(?:tsc|eslint)(?:\s|$)/,
    /^yarn\s+test(?:\s|$)/,
    /^yarn\s+(?:run\s+)?(?:build|lint|test)(?:\s|$)/,
    /^pytest(?:\s|$)/,
    /^cargo\s+test(?:\s|$)/,
    /^mvn\s+test(?:\s|$)/,
    /^gradle\s+test(?:\s|$)/,
    /^\.\/gradlew\s+test(?:\s|$)/,
    /^git\s+diff(?:\s|$)/,
    /^git\s+log(?:\s|$)/,
    /^(?:grep|rg|find|tree)(?:\s|$)/,
    /^docker\s+logs(?:\s|$)/,
    /^kubectl\s+logs(?:\s|$)/,
    /^(?:npx\s+)?tsc(?:\s|$)/,
    /^(?:npx\s+)?eslint(?:\s|$)/,
    /^webpack(?:\s|$)/,
    /^vite\s+build(?:\s|$)/,
    /^docker\s+build(?:\s|$)/,
    /^docker\s+compose\s+up(?:\s|$)/,
    /^make(?:\s|$)/,
    /^cmake\s+--build(?:\s|$)/,
  ];
  return highOutputPatterns.some((pattern) => pattern.test(trimmed));
}

export function bridgeHash(value: string): string {
  return crypto.createHash("sha256").update(value, "utf8").digest("hex");
}

export function normalizeWorkspaceForBridge(workspacePath: string): string {
  return path.resolve(workspacePath);
}

export function bridgeWorkspaceHash(workspacePath: string): string {
  return bridgeHash(normalizeWorkspaceForBridge(workspacePath));
}

export function bridgeHostInstanceHash(vscodePid: string | undefined, workspacePath: string): string | undefined {
  if (!vscodePid) {
    return undefined;
  }
  return bridgeHash(`${vscodePid}\x00${normalizeWorkspaceForBridge(workspacePath)}`);
}

function isHexSha256(value: unknown): value is string {
  return typeof value === "string" && /^[a-f0-9]{64}$/.test(value);
}

// Known (adapter, surface) pairs for the shared xit.vscode-ai-bridge.v1
// schema. Adding a new adapter here is the only schema-level change needed
// to accept its bridge events — anything else (e.g. adapter="gemini", or a
// mismatched pair like adapter="codex" with surface="claude_code") is
// rejected, never silently accepted.
const KNOWN_BRIDGE_ADAPTERS: Record<string, string> = {
  codex: "codex_chat",
  claude: "claude_code",
};

export function parseVscodeAiBridgeEvent(line: string): VscodeAiBridgeEvent | undefined {
  let obj: any;
  try {
    obj = JSON.parse(line);
  } catch {
    return undefined;
  }
  const KNOWN_EVENTS = ["run.started", "run.finished", "turn.started", "turn.finished"];
  if (
    obj?.schema !== "xit.vscode-ai-bridge.v1" ||
    !KNOWN_EVENTS.includes(obj.event) ||
    obj.host !== "vscode" ||
    typeof obj.adapter !== "string" ||
    KNOWN_BRIDGE_ADAPTERS[obj.adapter] !== obj.surface ||
    !isHexSha256(obj.workspace_hash) ||
    !isHexSha256(obj.host_instance_hash) ||
    (obj.thread_hash !== undefined && !isHexSha256(obj.thread_hash)) ||
    !isHexSha256(obj.command_hash) ||
    typeof obj.run_id !== "string" ||
    typeof obj.started_at !== "string"
  ) {
    return undefined;
  }
  if (obj.event === "run.finished") {
    if (typeof obj.finished_at !== "string" || typeof obj.exit_code !== "number") {
      return undefined;
    }
    if (obj.saved_tokens !== undefined && typeof obj.saved_tokens !== "number") {
      return undefined;
    }
    if (obj.saved_bytes !== undefined && typeof obj.saved_bytes !== "number") {
      return undefined;
    }
    if (obj.summary_bytes !== undefined && typeof obj.summary_bytes !== "number") {
      return undefined;
    }
    if (obj.run_count !== undefined && typeof obj.run_count !== "number") {
      return undefined;
    }
  }
  // turn.finished only needs finished_at — it carries no run-level data
  // (no exit_code/saved_tokens/etc.), unlike run.finished.
  if (obj.event === "turn.finished" && typeof obj.finished_at !== "string") {
    return undefined;
  }
  return obj as VscodeAiBridgeEvent;
}

export function bridgeEventMatchesCurrentInstance(
  event: VscodeAiBridgeEvent,
  workspacePath: string,
  vscodePid = process.env.VSCODE_PID,
): boolean {
  const hostHash = bridgeHostInstanceHash(vscodePid, workspacePath);
  return !!hostHash &&
    event.workspace_hash === bridgeWorkspaceHash(workspacePath) &&
    event.host_instance_hash === hostHash;
}

export type BridgeEventDecision =
  | { accepted: true; soft: boolean }
  | { accepted: false; reason: "workspace_hash_mismatch" };

// workspace_hash is a hard requirement — it is the only signal that reliably
// scopes an event to "this project". host_instance_hash is best-effort: it
// is derived from VSCODE_PID, which is not guaranteed to be the same value
// inside the Codex agent harness (Go side, writing the event) and inside
// this VS Code extension host (TS side, reading it) — different spawn
// points in the process tree can see different VSCODE_PID, or none at all.
// Dropping every soft-mismatched event would mean a real Codex Chat Bridge
// run for the right project is silently ignored. So: workspace match alone
// is enough to accept (soft); an additional host_instance_hash match just
// upgrades the same accept to "strong" for diagnostics.
export function classifyBridgeEvent(
  event: VscodeAiBridgeEvent,
  workspacePath: string,
  vscodePid = process.env.VSCODE_PID,
): BridgeEventDecision {
  if (event.workspace_hash !== bridgeWorkspaceHash(workspacePath)) {
    return { accepted: false, reason: "workspace_hash_mismatch" };
  }
  const hostHash = bridgeHostInstanceHash(vscodePid, workspacePath);
  const hostInstanceMatch = !!hostHash && event.host_instance_hash === hostHash;
  return { accepted: true, soft: !hostInstanceMatch };
}

export function bridgeEventIsFresh(event: VscodeAiBridgeEvent, activatedAtMs: number, nowMs = Date.now()): boolean {
  const isFinishedEvent = event.event === "run.finished" || event.event === "turn.finished";
  const eventTime = Date.parse(isFinishedEvent ? event.finished_at || event.started_at : event.started_at);
  if (!Number.isFinite(eventTime)) {
    return false;
  }
  if (eventTime < activatedAtMs - 1000) {
    return false;
  }
  return nowMs - eventTime <= 10 * 60 * 1000;
}

export function bridgeEventToLiveStatus(event: VscodeAiBridgeEvent): LiveStatusView {
  const adapterLabel = event.adapter === "claude" ? "Claude" : "Codex";
  const commandLabel = event.adapter === "claude" ? "Claude Code 对话框" : "Codex 对话框";

  if (event.event === "turn.started") {
    // AI is thinking about a new prompt — no tool call yet.
    return {
      kind: "xit_turn_active",
      label: "正在守护",
      adapter: adapterLabel,
      command: commandLabel,
      reason: "turn started",
      source: "vscode-ai-bridge",
      updatedAt: event.started_at,
    };
  }

  if (event.event === "turn.finished") {
    // Pure signal: "this turn's final answer is done". It carries no
    // run-level data of its own — the extension promotes whatever
    // run.finished result it is already holding (see promoteSettlingToFinal)
    // rather than rebuilding the view from this event alone.
    return {
      kind: "idle",
      label: "准备就绪",
      adapter: adapterLabel,
      command: commandLabel,
      reason: "turn finished",
      source: "vscode-ai-bridge",
      updatedAt: event.finished_at,
    };
  }

  if (event.event === "run.started") {
    return {
      kind: "xit_running",
      label: "正在吸T",
      adapter: adapterLabel,
      command: commandLabel,
      reason: "bridge run started",
      source: "vscode-ai-bridge",
      updatedAt: event.started_at,
    };
  }

  // run.finished: the tool call is done and its data is ready (saved
  // tokens/reduction/run count), but the AI's final answer for this turn is
  // NOT done yet — kind stays "xit_settling" ("收功中"), never jumping
  // straight to "xit_completed"/"agent_not_routed". promoteSettlingToFinal
  // performs that promotion once a real turn.finished (or the fallback
  // timer) confirms the turn is actually over.
  const success = event.exit_code === 0;
  return {
    kind: "xit_settling",
    label: "神功正在收工",
    adapter: adapterLabel,
    command: commandLabel,
    reason: success ? "bridge run finished" : "bridge run failed",
    source: "vscode-ai-bridge",
    updatedAt: event.finished_at,
    savedTokensDisplay: success ? formatSavedTokens(event.saved_tokens, event.saved_bytes) : undefined,
    exitCode: event.exit_code,
    savedTokens: event.saved_tokens,
    savedBytes: event.saved_bytes,
    summaryBytes: event.summary_bytes,
    runCount: event.run_count,
  };
}

// promoteSettlingToFinal turns a held "xit_settling" status into its true
// final form (xit_completed / agent_not_routed) once the turn is confirmed
// over (a real turn.finished event, or — for adapters/sessions where no
// turn-level signal was observed — a conservative fallback timer). Any
// other kind passes through unchanged.
export function promoteSettlingToFinal(status: LiveStatusView): LiveStatusView {
  if (status.kind !== "xit_settling") {
    return status;
  }
  const success = status.exitCode === 0;
  return {
    ...status,
    kind: success ? "xit_completed" : "agent_not_routed",
    label: success ? "本次省" : "执行失败",
  };
}

export function commandsReferToSameRun(recordCommand: string | undefined, active: ActiveVsCodeRun): boolean {
  if (!recordCommand) {
    return true;
  }
  const record = normalizeShellWhitespace(unwrapXiTAutoCommand(recordCommand));
  const original = normalizeShellWhitespace(unwrapXiTAutoCommand(active.originalCommand));
  const normalized = normalizeShellWhitespace(unwrapXiTAutoCommand(active.normalizedCommand));
  return record === original || record === normalized;
}

export function runRecordMatchesActiveTask(
  record: RunRecordCandidate,
  active: ActiveVsCodeRun | undefined,
  now = Date.now(),
  maxAgeMs = 120_000,
): boolean {
  if (!active || active.mode !== "auto") {
    return false;
  }
  const recordTime =
    Date.parse(record.completed_at || record.finished_at || record.timestamp || record.started_at || "");
  if (!Number.isFinite(recordTime)) {
    return false;
  }
  if (recordTime < active.startedAt - 15_000 || now - recordTime > maxAgeMs) {
    return false;
  }
  return commandsReferToSameRun(record.command, active);
}

export function computeTodayStats(runs: LatestRun[], now = new Date()): TodayStats {
  const start = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  let todayCount = 0;
  let todaySavedTokens = 0;
  for (const run of runs) {
    const runTime = Date.parse(run.timestamp || "");
    if (!Number.isFinite(runTime) || runTime < start) {
      continue;
    }
    todayCount++;
    todaySavedTokens += savedTokensFromRun(run);
  }
  return { todayCount, todaySavedTokens };
}

// ──────────────────────────────────────────────────────────────────
// DASHBOARD "本轮发功" — current VS Code session live run only.
// Never derives from .xit/history.jsonl's last entry: that file
// persists across restarts, so reading it here would resurrect a
// previous session's run as if it just happened.
// ──────────────────────────────────────────────────────────────────
export interface LiveRunView {
  reportPanelActive: boolean;
  subtitle: string;
  savedDisplay: string;
  reductionDisplay: string;
  runCountDisplay: string;
  statusDisplay: string;
  savedHighlight: boolean;
  reductionHighlight: boolean;
  runCountHighlight: boolean;
}

const WAITING_VIEW: LiveRunView = {
  reportPanelActive: false,
  subtitle: "等待下一轮发功",
  savedDisplay: "—",
  reductionDisplay: "—",
  runCountDisplay: "—",
  statusDisplay: "—",
  savedHighlight: false,
  reductionHighlight: false,
  runCountHighlight: false,
};

// 降噪率 = 本次省 / (本次省 + 实际保留) × 100. saved/retained must be the SAME
// unit (both bytes, or both tokens) — never mix. Returns undefined (displayed
// as "—") when either side is missing/invalid, rather than fabricating a
// percentage from incomplete data.
function computeReductionPct(saved: number | undefined, retained: number | undefined): number | undefined {
  if (typeof saved !== "number" || !Number.isFinite(saved) || saved < 0) {
    return undefined;
  }
  if (typeof retained !== "number" || !Number.isFinite(retained) || retained < 0) {
    return undefined;
  }
  const total = saved + retained;
  if (total <= 0) {
    return undefined;
  }
  return (saved / total) * 100;
}

// Whole percentages render with no decimal ("80%"); only a non-integer
// result gets a single decimal place ("87.5%"). No reliable data -> "—",
// never a fabricated number.
function formatReductionPct(pct: number | undefined): string {
  if (typeof pct !== "number" || !Number.isFinite(pct)) {
    return "—";
  }
  const clamped = Math.max(0, Math.min(100, pct));
  const rounded = Math.round(clamped * 10) / 10;
  return Number.isInteger(rounded) ? `${rounded}%` : `${rounded.toFixed(1)}%`;
}

function formatRunCount(runCount: number | undefined): string {
  return typeof runCount === "number" && Number.isFinite(runCount) && runCount >= 0 ? `${runCount}次` : "—";
}

export function computeLiveRunView(
  liveStatus: LiveStatusView,
  currentRunState: Pick<
    CurrentRunState,
    "status" | "completed_at" | "finished_at" | "exit_code" | "raw_bytes" | "summary_bytes" | "saved_tokens" | "saved_bytes" | "estimated_reduction"
  > | undefined,
  now = Date.now(),
): LiveRunView {
  const isTurnActive = liveStatus.kind === "xit_turn_active";
  const isSettling = liveStatus.kind === "xit_settling";
  const isRunning = liveStatus.kind === "xit_running";
  const isCompleted = liveStatus.kind === "xit_completed";
  const isBridgeStatus = liveStatus.source === "vscode-ai-bridge";
  const isLocalSkippedOrFailed = liveStatus.kind === "agent_not_routed" && !isBridgeStatus;

  const reportPanelActive =
    isTurnActive || isSettling || isRunning || isCompleted || isBridgeStatus || isLocalSkippedOrFailed;
  if (!reportPanelActive) {
    return WAITING_VIEW;
  }

  if (isTurnActive) {
    // AI is thinking about a new prompt — no tool call yet this turn, so
    // there is nothing real to report. Bridge-only; never fabricated for
    // adapters/sessions with no turn.started signal (they simply never
    // reach this branch).
    return {
      reportPanelActive: true,
      subtitle: "正在守护",
      savedDisplay: "—",
      reductionDisplay: "—",
      runCountDisplay: "—",
      statusDisplay: "守护中",
      savedHighlight: false,
      reductionHighlight: false,
      runCountHighlight: false,
    };
  }

  if (isRunning) {
    // 本轮共吸 uses "统计中" here (not "—") — a run is actively in flight,
    // so there genuinely IS something being counted, even though the real
    // number isn't ready. "—" is reserved for states with nothing to count
    // at all (turn-active/waiting). Never the real run_count — that would
    // mean showing a number before it's final.
    return {
      reportPanelActive: true,
      subtitle: "正在吸T",
      savedDisplay: "计算中",
      reductionDisplay: "计算中",
      runCountDisplay: "统计中",
      statusDisplay: "运行中",
      savedHighlight: false,
      reductionHighlight: false,
      runCountHighlight: false,
    };
  }

  if (isSettling) {
    // run.finished accepted — the real numbers (savedTokens/reductionPct/
    // runCount) already sit on liveStatus, ready for promoteSettlingToFinal
    // to use once the turn is confirmed over — but they must NEVER render
    // here. Showing them now would mean "the tool exited" reads as "the AI
    // is done talking", which is exactly the premature-success UX bug this
    // state exists to prevent. Render identically to "运行中" (计算中/统计
    // 中/计算中), only the status label differs ("收功中"), until
    // promoteSettlingToFinal (real turn.finished, or the fallback timer)
    // reveals everything at once.
    return {
      reportPanelActive: true,
      subtitle: "神功正在收工",
      savedDisplay: "计算中",
      reductionDisplay: "计算中",
      runCountDisplay: "统计中",
      statusDisplay: "收功中",
      savedHighlight: false,
      reductionHighlight: false,
      runCountHighlight: false,
    };
  }

  if (isBridgeStatus) {
    // Codex Bridge: saved_bytes/summary_bytes (both bytes, same unit) come
    // straight from the run.finished event — never fabricate a percentage
    // for a failed run, even if byte fields happen to be present.
    const isFailed = liveStatus.exitCode !== 0;
    const reductionPct = isFailed ? undefined : computeReductionPct(liveStatus.savedBytes, liveStatus.summaryBytes);
    const runCount = liveStatus.runCount;
    return {
      reportPanelActive: true,
      subtitle: "本次会话的最新结果",
      savedDisplay: liveStatus.savedTokensDisplay || "—",
      reductionDisplay: formatReductionPct(reductionPct),
      runCountDisplay: formatRunCount(runCount),
      statusDisplay: isFailed ? "失败" : "成功",
      savedHighlight: !!liveStatus.savedTokensDisplay && !isFailed,
      reductionHighlight: typeof reductionPct === "number" && reductionPct > 0,
      runCountHighlight: typeof runCount === "number" && runCount > 0,
    };
  }

  if (isCompleted) {
    const completedAt = currentRunState?.completed_at || currentRunState?.finished_at;
    const completedAtMs = completedAt ? Date.parse(completedAt) : Number.NaN;
    const freshFromState =
      currentRunState?.status === "completed" &&
      Number.isFinite(completedAtMs) &&
      now - completedAtMs <= 120000;

    if (freshFromState && currentRunState) {
      const savedTokens = savedTokensFromCurrentRun(currentRunState) || 0;
      const isFailed = currentRunState.exit_code !== 0;
      const savedBytesValue = typeof currentRunState.saved_bytes === "number"
        ? currentRunState.saved_bytes
        : Math.max(0, (currentRunState.raw_bytes ?? 0) - (currentRunState.summary_bytes ?? 0));
      const reductionPct = isFailed
        ? undefined
        : typeof currentRunState.estimated_reduction === "number" && currentRunState.estimated_reduction > 0
          ? currentRunState.estimated_reduction * 100
          : computeReductionPct(savedBytesValue, currentRunState.summary_bytes);
      return {
        reportPanelActive: true,
        subtitle: "本次会话的最新结果",
        savedDisplay: formatSavedTokens(savedTokens) || "—",
        reductionDisplay: formatReductionPct(reductionPct),
        // A single manual VS Code run is always exactly one run — this is a
        // local fact (not derived from any Codex turn counter), never
        // fabricated.
        runCountDisplay: "1次",
        statusDisplay: isFailed ? "失败" : "成功",
        savedHighlight: savedTokens > 0,
        reductionHighlight: typeof reductionPct === "number" && reductionPct > 0,
        runCountHighlight: true,
      };
    }

    // current-run.json went stale/missing but the in-memory live status still
    // holds this session's result — fall back to it instead of any history file.
    const isFailed = liveStatus.exitCode !== 0;
    const reductionPct = isFailed || !liveStatus.reductionPct || liveStatus.reductionPct <= 0
      ? undefined
      : liveStatus.reductionPct;
    return {
      reportPanelActive: true,
      subtitle: "本次会话的最新结果",
      savedDisplay: liveStatus.savedTokensDisplay || "—",
      reductionDisplay: formatReductionPct(reductionPct),
      runCountDisplay: "1次",
      statusDisplay: isFailed ? "失败" : "成功",
      savedHighlight: !!liveStatus.savedTokensDisplay,
      reductionHighlight: typeof reductionPct === "number" && reductionPct > 0,
      runCountHighlight: true,
    };
  }

  // isLocalSkippedOrFailed
  const isFailed = liveStatus.label === "执行失败";
  // A failed run never gets a reduction percentage — there is no meaningful
  // compression result to report, even if some retained-byte count happens
  // to be on hand. A "skipped" (no compression needed) run legitimately
  // reduced 0%, computed from real retained bytes when available.
  const reductionPct = isFailed ? undefined : computeReductionPct(0, liveStatus.summaryBytes);
  return {
    reportPanelActive: true,
    subtitle: "本次会话的最新结果",
    savedDisplay: liveStatus.savedTokensDisplay || "0 Token",
    reductionDisplay: formatReductionPct(reductionPct),
    runCountDisplay: "1次",
    statusDisplay: isFailed ? "失败" : "无需发功",
    savedHighlight: false,
    reductionHighlight: false,
    runCountHighlight: true,
  };
}
