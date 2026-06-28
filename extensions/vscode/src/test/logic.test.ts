import test from "node:test";
import assert from "node:assert/strict";
import {
  buildXiTAutoCommand,
  bridgeEventIsFresh,
  bridgeEventMatchesCurrentInstance,
  bridgeEventToLiveStatus,
  bridgeHash,
  bridgeHostInstanceHash,
  bridgeWorkspaceHash,
  classifyBridgeEvent,
  computeTodayStats,
  formatSavedTokens,
  isAlreadyWrappedByXiT,
  isHighOutputCommand,
  parseVscodeAiBridgeEvent,
  promoteSettlingToFinal,
  runRecordMatchesActiveTask,
  type ActiveVsCodeRun,
} from "../logic";
import type { LatestRun, LiveStatusView } from "../types";

test("formatSavedTokens uses numeric tokens before bytes", () => {
  assert.equal(formatSavedTokens(841), "841 Token");
  assert.equal(formatSavedTokens(3733), "约 3.73k Token");
  assert.equal(formatSavedTokens(undefined, 4000), "约 1.00k Token");
  assert.equal(formatSavedTokens(undefined, undefined), undefined);
});

test("isAlreadyWrappedByXiT detects supported wrapper forms", () => {
  const wrapped = [
    "xit auto go test ./...",
    "./xit auto go test ./...",
    "/Users/me/bin/xit auto go test ./...",
    "X=1 xit auto go test ./...",
    "env X=1 xit auto go test ./...",
    "bash -lc 'xit auto go test ./...'",
    'bash -lc "./xit auto go test ./..."',
  ];
  for (const command of wrapped) {
    assert.equal(isAlreadyWrappedByXiT(command), true, command);
  }
  assert.equal(isAlreadyWrappedByXiT("go test ./..."), false);
});

test("buildXiTAutoCommand wraps once and preserves user shell command", () => {
  assert.equal(
    buildXiTAutoCommand("go test ./... | tee out.log"),
    "xit auto go test ./... | tee out.log",
  );
  assert.equal(
    buildXiTAutoCommand("xit auto go test ./..."),
    "xit auto go test ./...",
  );
  assert.equal(
    buildXiTAutoCommand("go test ./...", "/tmp/XiT Bin/xit"),
    "'/tmp/XiT Bin/xit' auto go test ./...",
  );
});

test("isHighOutputCommand covers noisy commands without broad false positives", () => {
  const high = [
    "go test",
    "go test -v ./...",
    "git diff",
    "git log",
    "docker logs app",
    "kubectl logs pod/app",
    "npm test",
    "npm run build",
    "pnpm test",
    "yarn test",
    "tsc --noEmit",
    "eslint .",
    "npx eslint .",
    "pnpm exec tsc --noEmit",
    "pytest",
    "cargo test",
    "mvn test",
    "gradle test",
    "rg pattern .",
    "find . -type f",
    "tree .",
    "bash -lc 'for i in {1..1200}; do echo \"vscode-codex-chat-test line=$i aaaa bbbb\"; done'",
    "bash -lc 'seq 1 1200 | sed \"s/^/line=/\"'",
    "yes hello",
  ];
  for (const command of high) {
    assert.equal(isHighOutputCommand(command), true, command);
  }

  const low = [
    "pwd",
    "whoami",
    "git status --short",
    "echo hello",
    "ls",
    "git log --oneline -5",
    'for f in a b c; do test -f "$f"; done',
    "bash -lc 'for i in {1..3}; do echo line=$i; done'",
  ];
  for (const command of low) {
    assert.equal(isHighOutputCommand(command), false, command);
  }
});

function bridgeEvent(overrides: Record<string, unknown> = {}): string {
  const workspace = "/tmp/xit-workspace";
  const base = {
    schema: "xit.vscode-ai-bridge.v1",
    event: "run.started",
    host: "vscode",
    surface: "codex_chat",
    adapter: "codex",
    workspace_hash: bridgeWorkspaceHash(workspace),
    host_instance_hash: bridgeHostInstanceHash("1234", workspace),
    thread_hash: bridgeHash("thread"),
    run_id: "run-1",
    command_hash: bridgeHash("command"),
    started_at: "2026-06-23T01:00:00Z",
  };
  return JSON.stringify({ ...base, ...overrides });
}

test("parseVscodeAiBridgeEvent accepts only bridge schema", () => {
  const parsed = parseVscodeAiBridgeEvent(bridgeEvent());
  assert.equal(parsed?.schema, "xit.vscode-ai-bridge.v1");
  assert.equal(parsed?.event, "run.started");
  assert.equal(parseVscodeAiBridgeEvent("{bad"), undefined);
  // Mismatched adapter/surface pair (adapter changed, surface still
  // "codex_chat" from the base fixture) must still be rejected.
  assert.equal(parseVscodeAiBridgeEvent(bridgeEvent({ adapter: "claude" })), undefined);
});

test("parseVscodeAiBridgeEvent accepts adapter=claude surface=claude_code", () => {
  const parsed = parseVscodeAiBridgeEvent(
    bridgeEvent({ adapter: "claude", surface: "claude_code", thread_hash: undefined }),
  );
  assert.ok(parsed, "expected a valid Claude Code bridge event to parse");
  assert.equal(parsed?.adapter, "claude");
  assert.equal(parsed?.surface, "claude_code");
});

test("parseVscodeAiBridgeEvent rejects unknown adapters and surfaces", () => {
  assert.equal(parseVscodeAiBridgeEvent(bridgeEvent({ adapter: "gemini", surface: "gemini_chat" })), undefined);
  // Known adapter, but paired with the WRONG surface for that adapter.
  assert.equal(parseVscodeAiBridgeEvent(bridgeEvent({ adapter: "claude", surface: "codex_chat" })), undefined);
  assert.equal(parseVscodeAiBridgeEvent(bridgeEvent({ adapter: "codex", surface: "claude_code" })), undefined);
});

test("bridge event matching isolates workspace and VS Code host instance", () => {
  const workspace = "/tmp/xit-workspace";
  const parsed = parseVscodeAiBridgeEvent(bridgeEvent());
  assert.ok(parsed);
  assert.equal(bridgeEventMatchesCurrentInstance(parsed, workspace, "1234"), true);
  assert.equal(bridgeEventMatchesCurrentInstance(parsed, "/tmp/other-workspace", "1234"), false);
  assert.equal(bridgeEventMatchesCurrentInstance(parsed, workspace, "9999"), false);
  assert.equal(bridgeEventMatchesCurrentInstance(parsed, workspace, undefined), false);
});

test("classifyBridgeEvent accepts (strong) when workspace_hash and host_instance_hash both match", () => {
  const workspace = "/tmp/xit-workspace";
  const parsed = parseVscodeAiBridgeEvent(bridgeEvent());
  assert.ok(parsed);
  const decision = classifyBridgeEvent(parsed, workspace, "1234");
  assert.deepEqual(decision, { accepted: true, soft: false });
});

test("classifyBridgeEvent soft-accepts when host_instance_hash mismatches but workspace_hash matches", () => {
  // Reproduces the real bug: the Codex PreToolUse hook process (Go side,
  // writes the event) and this extension host can see different VSCODE_PID
  // values, so host_instance_hash legitimately differs even for a real event
  // in the right workspace. It must not be dropped.
  const workspace = "/tmp/xit-workspace";
  const parsed = parseVscodeAiBridgeEvent(bridgeEvent());
  assert.ok(parsed);
  assert.deepEqual(classifyBridgeEvent(parsed, workspace, "9999"), { accepted: true, soft: true });
  assert.deepEqual(classifyBridgeEvent(parsed, workspace, undefined), { accepted: true, soft: true });
});

test("classifyBridgeEvent rejects when workspace_hash mismatches, regardless of host_instance_hash", () => {
  const parsed = parseVscodeAiBridgeEvent(bridgeEvent());
  assert.ok(parsed);
  assert.deepEqual(
    classifyBridgeEvent(parsed, "/tmp/other-workspace", "1234"),
    { accepted: false, reason: "workspace_hash_mismatch" },
  );
});

// classifyBridgeEvent/bridgeEventMatchesCurrentInstance never look at
// adapter/surface — these confirm the SAME workspace/host matching policy
// holds for Claude Code bridge events, not just Codex.
test("classifyBridgeEvent applies the same workspace/host policy to Claude bridge events", () => {
  const workspace = "/tmp/xit-workspace";
  const claudeStarted = parseVscodeAiBridgeEvent(
    bridgeEvent({ adapter: "claude", surface: "claude_code", thread_hash: undefined }),
  );
  assert.ok(claudeStarted);
  assert.deepEqual(classifyBridgeEvent(claudeStarted, workspace, "1234"), { accepted: true, soft: false });
  // host_instance_hash mismatch -> soft accept, same as Codex.
  assert.deepEqual(classifyBridgeEvent(claudeStarted, workspace, "9999"), { accepted: true, soft: true });
  // workspace_hash mismatch -> rejected, same as Codex.
  assert.deepEqual(
    classifyBridgeEvent(claudeStarted, "/tmp/other-workspace", "1234"),
    { accepted: false, reason: "workspace_hash_mismatch" },
  );
});

test("bridge started and finished events map to live status", () => {
  const started = parseVscodeAiBridgeEvent(bridgeEvent());
  assert.ok(started);
  const running = bridgeEventToLiveStatus(started);
  assert.equal(running.kind, "xit_running");
  assert.equal(running.source, "vscode-ai-bridge");

  const success = parseVscodeAiBridgeEvent(bridgeEvent({
    event: "run.finished",
    finished_at: "2026-06-23T01:00:05Z",
    exit_code: 0,
    saved_tokens: 3730,
    saved_bytes: 14920,
    summary_bytes: 1280,
    run_count: 1,
  }));
  assert.ok(success);
  const settled = bridgeEventToLiveStatus(success);
  // run.finished only means "data ready" — kind must be the intermediate
  // "xit_settling" (收功中), never the final "xit_completed", until the
  // turn is confirmed over (promoteSettlingToFinal).
  assert.equal(settled.kind, "xit_settling");
  assert.equal(settled.savedTokensDisplay, "约 3.73k Token");
  assert.equal(settled.savedBytes, 14920);
  assert.equal(settled.summaryBytes, 1280);
  assert.equal(settled.runCount, 1);

  const completed = promoteSettlingToFinal(settled);
  assert.equal(completed.kind, "xit_completed");
  assert.equal(completed.label, "本次省");
  // Promotion must preserve all the held data, only changing kind/label.
  assert.equal(completed.savedTokensDisplay, "约 3.73k Token");
  assert.equal(completed.savedBytes, 14920);
  assert.equal(completed.summaryBytes, 1280);
  assert.equal(completed.runCount, 1);

  const failed = parseVscodeAiBridgeEvent(bridgeEvent({
    event: "run.finished",
    finished_at: "2026-06-23T01:00:05Z",
    exit_code: 1,
    saved_tokens: 0,
    saved_bytes: 0,
  }));
  assert.ok(failed);
  const failedSettling = bridgeEventToLiveStatus(failed);
  assert.equal(failedSettling.kind, "xit_settling");
  assert.equal(failedSettling.savedTokensDisplay, undefined);
  const failedStatus = promoteSettlingToFinal(failedSettling);
  assert.equal(failedStatus.kind, "agent_not_routed");
  assert.equal(failedStatus.label, "执行失败");
  assert.equal(failedStatus.savedTokensDisplay, undefined);
});

test("promoteSettlingToFinal leaves non-settling statuses unchanged", () => {
  const turnActive: LiveStatusView = { kind: "xit_turn_active", label: "正在守护", source: "vscode-ai-bridge" };
  assert.deepEqual(promoteSettlingToFinal(turnActive), turnActive);
  const running: LiveStatusView = { kind: "xit_running", label: "正在吸T", source: "vscode-ai-bridge" };
  assert.deepEqual(promoteSettlingToFinal(running), running);
});

test("Claude Code bridge events map to live status with a Claude-labeled adapter/command, same fields as Codex", () => {
  const started = parseVscodeAiBridgeEvent(
    bridgeEvent({ adapter: "claude", surface: "claude_code", thread_hash: undefined }),
  );
  assert.ok(started);
  const running = bridgeEventToLiveStatus(started);
  assert.equal(running.kind, "xit_running");
  assert.equal(running.source, "vscode-ai-bridge");
  assert.equal(running.adapter, "Claude");
  assert.equal(running.command, "Claude Code 对话框");

  const success = parseVscodeAiBridgeEvent(bridgeEvent({
    adapter: "claude",
    surface: "claude_code",
    thread_hash: undefined,
    event: "run.finished",
    finished_at: "2026-06-23T01:00:05Z",
    exit_code: 0,
    saved_tokens: 22720,
    saved_bytes: 90880,
    summary_bytes: 3800,
    run_count: 1,
  }));
  assert.ok(success);
  const settled = bridgeEventToLiveStatus(success);
  assert.equal(settled.kind, "xit_settling");
  assert.equal(settled.adapter, "Claude");
  assert.equal(settled.savedBytes, 90880);
  assert.equal(settled.summaryBytes, 3800);
  assert.equal(settled.runCount, 1);

  const completed = promoteSettlingToFinal(settled);
  assert.equal(completed.kind, "xit_completed");
  assert.equal(completed.adapter, "Claude");
  assert.equal(completed.savedBytes, 90880);
  assert.equal(completed.summaryBytes, 3800);
  assert.equal(completed.runCount, 1);
});

test("turn.started/turn.finished bridge events map to live status", () => {
  const turnStarted = parseVscodeAiBridgeEvent(
    bridgeEvent({ event: "turn.started", thread_hash: undefined }),
  );
  assert.ok(turnStarted);
  const turnActive = bridgeEventToLiveStatus(turnStarted);
  assert.equal(turnActive.kind, "xit_turn_active");
  assert.equal(turnActive.label, "正在守护");
  assert.equal(turnActive.source, "vscode-ai-bridge");

  const turnFinished = parseVscodeAiBridgeEvent(
    bridgeEvent({ event: "turn.finished", finished_at: "2026-06-23T01:00:10Z", thread_hash: undefined }),
  );
  assert.ok(turnFinished);
  // bridgeEventToLiveStatus alone has no run data to promote — that's
  // handled statefully in extension.ts via promoteSettlingToFinal against
  // whatever xit_settling status is already held. This just confirms the
  // event itself parses and translates without error.
  const turnFinishedStatus = bridgeEventToLiveStatus(turnFinished);
  assert.equal(turnFinishedStatus.source, "vscode-ai-bridge");
});

test("Claude bridge events do not affect Codex bridge event handling (adapter-isolated)", () => {
  const codexEvent = parseVscodeAiBridgeEvent(bridgeEvent());
  assert.ok(codexEvent);
  const codexStatus = bridgeEventToLiveStatus(codexEvent);
  assert.equal(codexStatus.adapter, "Codex");
  assert.equal(codexStatus.command, "Codex 对话框");
});

test("bridge freshness ignores stale events and accepts activation-time appends", () => {
  const event = parseVscodeAiBridgeEvent(bridgeEvent({ started_at: "2026-06-23T01:00:00Z" }));
  assert.ok(event);
  assert.equal(
    bridgeEventIsFresh(event, Date.parse("2026-06-23T00:59:59Z"), Date.parse("2026-06-23T01:00:05Z")),
    true,
  );
  assert.equal(
    bridgeEventIsFresh(event, Date.parse("2026-06-23T01:00:05Z"), Date.parse("2026-06-23T01:00:06Z")),
    false,
  );
  assert.equal(
    bridgeEventIsFresh(event, Date.parse("2026-06-23T00:00:00Z"), Date.parse("2026-06-23T01:20:00Z")),
    false,
  );
});

test("runRecordMatchesActiveTask requires active auto task and fresh matching command", () => {
  const startedAt = Date.parse("2026-06-23T01:00:00.000Z");
  const active: ActiveVsCodeRun = {
    startedAt,
    originalCommand: "go test ./...",
    normalizedCommand: "xit auto go test ./...",
    terminalName: "XiT",
    mode: "auto",
    state: "running",
    workspacePath: "/repo",
  };
  assert.equal(
    runRecordMatchesActiveTask(
      { timestamp: "2026-06-23T01:00:05.000Z", command: "go test ./..." },
      active,
      Date.parse("2026-06-23T01:00:06.000Z"),
    ),
    true,
  );
  assert.equal(
    runRecordMatchesActiveTask(
      { timestamp: "2026-06-23T01:00:05.000Z", command: "npm test" },
      active,
      Date.parse("2026-06-23T01:00:06.000Z"),
    ),
    false,
  );
  assert.equal(
    runRecordMatchesActiveTask(
      { timestamp: "2026-06-23T00:55:00.000Z", command: "go test ./..." },
      active,
      Date.parse("2026-06-23T01:00:06.000Z"),
    ),
    false,
  );
  assert.equal(
    runRecordMatchesActiveTask(
      { timestamp: "2026-06-23T01:00:05.000Z", command: "go test ./..." },
      { ...active, mode: "passthrough" },
      Date.parse("2026-06-23T01:00:06.000Z"),
    ),
    false,
  );
});

test("computeTodayStats filters by local day and numeric savings", () => {
  const runs: LatestRun[] = [
    {
      timestamp: "2026-06-23T02:00:00.000Z",
      command: "go test",
      exit_code: 0,
      raw_bytes: 8000,
      summary_bytes: 1000,
      saved_tokens: 1750,
      estimated_reduction: 0.875,
      duration_ms: 1,
      filter: "auto",
      confidence: "high",
      policy: "should_compress",
      raw_log: "/tmp/1.raw.log",
    },
    {
      timestamp: "2026-06-22T00:00:00.000Z",
      command: "git diff",
      exit_code: 0,
      raw_bytes: 4000,
      summary_bytes: 0,
      estimated_reduction: 1,
      duration_ms: 1,
      filter: "auto",
      confidence: "high",
      policy: "should_compress",
      raw_log: "/tmp/2.raw.log",
    },
  ];
  const stats = computeTodayStats(runs, new Date("2026-06-23T12:00:00.000Z"));
  assert.equal(stats.todayCount, 1);
  assert.equal(stats.todaySavedTokens, 1750);
});
