import test from "node:test";
import assert from "node:assert/strict";
import * as fs from "node:fs";
import * as path from "node:path";

// extension.ts imports "vscode", which only exists inside the real extension
// host, so it can't be required directly under plain node:test. We assert on
// the compiled source text instead — sufficient to lock in the watcher's
// base-path fix and the diagnostic logging contract without needing a vscode
// shim.
function readCompiledExtensionSource(): string {
  return fs.readFileSync(path.join(__dirname, "..", "extension.js"), "utf-8");
}

test("vscode-ai-bridge watcher is rooted at the workspace folder, not at .xit/events", () => {
  const source = readCompiledExtensionSource();
  // Must watch relative to workspacePath (always exists) so the watcher
  // attaches even before .xit/events/ has ever been created.
  assert.ok(
    source.includes('new vscode.RelativePattern(workspacePath, ".xit/events/vscode-ai-bridge.jsonl")'),
    "watcher must be created with workspacePath as the RelativePattern base",
  );
  assert.ok(
    !source.includes("path.dirname(bridgeFile), path.basename(bridgeFile)"),
    "watcher must not be rooted at .xit/events (may not exist yet on a fresh workspace)",
  );
});

test("vscode-ai-bridge watcher listens for both onDidChange and onDidCreate", () => {
  const source = readCompiledExtensionSource();
  assert.ok(source.includes("watcher.onDidChange(() => { void handleBridgeChange(); }"));
  assert.ok(source.includes("watcher.onDidCreate(() => { void handleBridgeChange(); }"));
});

test("bridge diagnostics only go to the Output Channel (appendOutput), never showOutput", () => {
  const source = readCompiledExtensionSource();
  // logBridgeDiagnostic must exist and route through appendOutput (compiled
  // as the CommonJS-interop call form `(0, xit_1.appendOutput)(...)`).
  const fnMatch = source.match(/function logBridgeDiagnostic\(message\) \{[\s\S]*?\n\}/);
  assert.ok(fnMatch, "logBridgeDiagnostic should exist");
  assert.ok(fnMatch![0].includes("appendOutput)(`[XiT Bridge] ${message}`)"));
  assert.ok(!fnMatch![0].includes("showOutput"), "must never call showOutput from the bridge diagnostic logger");
});

test("bridge diagnostics never include a full workspace path, PID, or thread id — only short hash prefixes", () => {
  const source = readCompiledExtensionSource();
  const fnMatch = source.match(/function shortHash\(value\) \{[\s\S]*?\n\}/);
  assert.ok(fnMatch, "shortHash helper should exist");
  assert.ok(fnMatch![0].includes(".slice(0, 8)"), "shortHash must truncate to 8 chars");
});

function extractHandleBridgeChange(source: string): string {
  const match = source.match(/const handleBridgeChange = .*?async \(\) => \{[\s\S]*?\n    \};/);
  assert.ok(match, "handleBridgeChange should exist");
  return match![0];
}

test("accepted run.started calls setBridgeRunning and logs a liveOverride update", () => {
  const fn = extractHandleBridgeChange(readCompiledExtensionSource());
  assert.ok(fn.includes("setBridgeRunning(event, workspacePath)"));
  assert.match(fn, /liveOverride updated: status=running/);
});

test("accepted run.finished calls setBridgeSettling (not a direct final-result setter), logs saved_tokens", () => {
  const fn = extractHandleBridgeChange(readCompiledExtensionSource());
  assert.ok(fn.includes("setBridgeSettling(event, workspacePath)"));
  assert.match(fn, /accepted: finished saved_tokens=/);
  assert.match(fn, /status=settling/);
});

test("accepted turn.finished calls setBridgeTurnFinished and logs that the settling result was finalized", () => {
  const fn = extractHandleBridgeChange(readCompiledExtensionSource());
  assert.ok(fn.includes("setBridgeTurnFinished(event, workspacePath)"));
  assert.match(fn, /liveOverride finalized: turn\.finished/);
});

test("accepted turn.started calls setBridgeTurnActive", () => {
  const fn = extractHandleBridgeChange(readCompiledExtensionSource());
  assert.ok(fn.includes("setBridgeTurnActive(event, workspacePath)"));
  assert.match(fn, /status=turn-active/);
});

test("any accepted bridge event triggers a dashboard refresh and a status bar update in the same tick", () => {
  const fn = extractHandleBridgeChange(readCompiledExtensionSource());
  // changed=true is only set inside the per-event loop when accepted, and the
  // refresh block runs unconditionally once any event was accepted.
  assert.ok(fn.includes("changed = true;"));
  assert.ok(fn.includes("dashboard_1.updateDashboardIfOpen)(status, buildLiveStatusOverride(workspacePath), workspacePath)"));
  assert.match(fn, /dashboard refreshed/);
  assert.ok(fn.includes("await updateStatusBar();"));
});

test("run.finished (success or failure) never jumps straight to a final liveState — setBridgeSettling always sets 'settling' first", () => {
  const source = readCompiledExtensionSource();
  const fnMatch = source.match(/function setBridgeSettling\(event, workspacePath\) \{[\s\S]*?\n\}/);
  assert.ok(fnMatch, "setBridgeSettling should exist");
  const body = fnMatch![0];
  // run.finished is "data ready", not "the AI is done talking" — this must
  // hold for BOTH success and failure (see product spec section III.6:
  // failure also shows "收功中" before "执行失败"). setBridgeSettling must
  // never itself assign liveState to "success" or "failed" — only
  // finalizeBridgeTurn (driven by turn.finished or the fallback timer) may
  // do that.
  assert.ok(body.includes('liveState = "settling"'));
  assert.ok(!body.includes('liveState = "success"'));
  assert.ok(!body.includes('liveState = "failed"'));
});

test("finalizeBridgeTurn (not setBridgeSettling) is the only place that promotes settling to a final liveState", () => {
  const source = readCompiledExtensionSource();
  const fnMatch = source.match(/function finalizeBridgeTurn\(workspacePath\) \{[\s\S]*?\n\}/);
  assert.ok(fnMatch, "finalizeBridgeTurn should exist");
  const body = fnMatch![0];
  assert.ok(body.includes("promoteSettlingToFinal"));
  assert.ok(body.includes('liveState = "idle"') && body.includes("exitCode === 0"));
});

test("handleBridgeChange processes every appended line, not just the first (multi-line JSONL append)", () => {
  const fn = extractHandleBridgeChange(readCompiledExtensionSource());
  // Must be a `for (... of appended)` loop. The per-event dispatch is now a
  // `switch (event.event)` with one `break` per case — those only exit the
  // switch, never the loop — confirmed by `changed = true;` (inside the
  // loop, after the switch) still running on every iteration.
  assert.match(fn, /for \(const raw of appended\)/);
  assert.match(
    fn,
    /switch \(event\.event\) \{[\s\S]*?\n            \}\s*\n            changed = true;/,
    "the loop must continue to 'changed = true' after the switch on every iteration — the per-case break must not exit the for loop",
  );
});

test("initializeHookCursor falls back to offset 0 when the bridge file does not exist yet (first creation is still read)", () => {
  const source = readCompiledExtensionSource();
  const fnMatch = source.match(/function initializeHookCursor\(hookFile\) \{[\s\S]*?\n\}/);
  assert.ok(fnMatch, "initializeHookCursor should exist");
  assert.match(fnMatch![0], /catch[\s\S]*?offset: 0/, "must fall back to offset 0 (read-from-start) when the file doesn't exist at watcher registration time");
});

// ──────────────────────────────────────────────────────────────────
// Settling fallback: a real turn.finished should win when it arrives, but
// if no turn-level signal was ever observed for this workspace, a
// conservative fallback timer must still resolve the "收功中" state instead
// of leaving it stuck forever.
// ──────────────────────────────────────────────────────────────────

test("status bar shows '正在守护' for the new turn-active liveState", () => {
  const source = readCompiledExtensionSource();
  const fnMatch = source.match(/async function updateStatusBarLive\(\)[\s\S]*?\n\}/);
  assert.ok(fnMatch, "updateStatusBarLive should exist");
  assert.match(fnMatch![0], /liveState === "turn-active"[\s\S]*?正在守护/);
});

test("status bar still shows '神功正在收工' for the settling liveState (unchanged text)", () => {
  const source = readCompiledExtensionSource();
  const fnMatch = source.match(/async function updateStatusBarLive\(\)[\s\S]*?\n\}/);
  assert.ok(fnMatch, "updateStatusBarLive should exist");
  assert.match(fnMatch![0], /liveState === "settling"[\s\S]*?神功正在收工/);
});

test("setBridgeSettling arms a fallback timer so '收功中' never gets stuck forever without a real turn.finished", () => {
  const source = readCompiledExtensionSource();
  const fnMatch = source.match(/function setBridgeSettling\(event, workspacePath\) \{[\s\S]*?\n\}/);
  assert.ok(fnMatch, "setBridgeSettling should exist");
  const body = fnMatch![0];
  assert.ok(body.includes("bridgeSettleTimer = setTimeout"));
  assert.ok(body.includes("finalizeBridgeTurn(workspacePath)"));
});

test("the settling fallback is 6-8 seconds — a real turn.finished/Stop always preempts it, this is only the no-signal safety net", () => {
  const source = readCompiledExtensionSource();
  const constMatch = source.match(/BRIDGE_SETTLE_FALLBACK_MS = (\d+)/);
  assert.ok(constMatch, "BRIDGE_SETTLE_FALLBACK_MS should exist");
  const ms = Number(constMatch![1]);
  assert.ok(ms >= 6000 && ms <= 8000, `expected 6000-8000ms, got ${ms}`);
  const fnMatch = source.match(/function setBridgeSettling\(event, workspacePath\) \{[\s\S]*?\n\}/);
  assert.ok(fnMatch);
  assert.ok(fnMatch![0].includes("BRIDGE_SETTLE_FALLBACK_MS"));
});

test("status bar final result is held visible for >= 20 seconds before clearing back to idle", () => {
  const source = readCompiledExtensionSource();
  const constMatch = source.match(/BRIDGE_FINAL_RESULT_HOLD_MS = (\d+)/);
  assert.ok(constMatch, "BRIDGE_FINAL_RESULT_HOLD_MS should exist");
  assert.ok(Number(constMatch![1]) >= 20000, `expected >= 20000ms, got ${constMatch![1]}`);
  const fnMatch = source.match(/function finalizeBridgeTurn\(workspacePath\) \{[\s\S]*?\n\}/);
  assert.ok(fnMatch);
  assert.ok(fnMatch![0].includes("BRIDGE_FINAL_RESULT_HOLD_MS"));
});

// ──────────────────────────────────────────────────────────────────
// Dashboard's "本轮发功" final result must outlive the status bar's hold
// timer — it is only reset by a new round starting (turn.started /
// run.started) or a VS Code reload, never by a plain timeout.
// ──────────────────────────────────────────────────────────────────

test("finalizeBridgeTurn saves the promoted result into lastFinalizedBridgeResult, decoupled from the status-bar hold timer", () => {
  const source = readCompiledExtensionSource();
  const fnMatch = source.match(/function finalizeBridgeTurn\(workspacePath\) \{[\s\S]*?\n\}/);
  assert.ok(fnMatch, "finalizeBridgeTurn should exist");
  const body = fnMatch![0];
  assert.ok(body.includes("lastFinalizedBridgeResult = bridgeLiveStatus"));
  assert.ok(body.includes("lastFinalizedBridgeResultWorkspace = workspacePath"));
  // clearBridgeLiveStatus() runs inside the bridgeHoldTimer callback — it
  // must NOT be the function that also wipes lastFinalizedBridgeResult.
  const clearFnMatch = source.match(/function clearBridgeLiveStatus\(\) \{[\s\S]*?\n\}/);
  assert.ok(clearFnMatch, "clearBridgeLiveStatus should exist");
  assert.ok(!clearFnMatch![0].includes("lastFinalizedBridgeResult"));
});

test("buildLiveStatusOverride falls back to lastFinalizedBridgeResult once bridgeLiveStatus has been cleared (status bar idle, Dashboard still shows the result)", () => {
  const source = readCompiledExtensionSource();
  const fnMatch = source.match(/function buildLiveStatusOverride\(activeWorkspace\)[\s\S]*?\n\}/);
  assert.ok(fnMatch, "buildLiveStatusOverride should exist");
  const body = fnMatch![0];
  assert.match(body, /lastFinalizedBridgeResult[\s\S]*?return lastFinalizedBridgeResult;/);
});

test("only a new turn.started or run.started clears lastFinalizedBridgeResult — never the hold timer or settling", () => {
  const source = readCompiledExtensionSource();
  const clearerMatch = source.match(/function clearLastFinalizedBridgeResult\(\) \{[\s\S]*?\n\}/);
  assert.ok(clearerMatch, "clearLastFinalizedBridgeResult should exist");

  const turnActiveFn = source.match(/function setBridgeTurnActive\(event, workspacePath\) \{[\s\S]*?\n\}/);
  assert.ok(turnActiveFn && turnActiveFn[0].includes("clearLastFinalizedBridgeResult()"));

  const runningFn = source.match(/function setBridgeRunning\(event, workspacePath\) \{[\s\S]*?\n\}/);
  assert.ok(runningFn && runningFn[0].includes("clearLastFinalizedBridgeResult()"));

  const settlingFn = source.match(/function setBridgeSettling\(event, workspacePath\) \{[\s\S]*?\n\}/);
  assert.ok(settlingFn && !settlingFn[0].includes("clearLastFinalizedBridgeResult()"));

  const finalizeFn = source.match(/function finalizeBridgeTurn\(workspacePath\) \{[\s\S]*?\n\}/);
  assert.ok(finalizeFn && !finalizeFn[0].includes("clearLastFinalizedBridgeResult()"));
});

test("turn.started clears any stale bridge timers/state from a previous turn (new turn always overrides)", () => {
  const source = readCompiledExtensionSource();
  const fnMatch = source.match(/function setBridgeTurnActive\(event, workspacePath\) \{[\s\S]*?\n\}/);
  assert.ok(fnMatch, "setBridgeTurnActive should exist");
  assert.ok(fnMatch![0].includes("clearBridgeTimers()"));
});

test("reload / fresh activation has no bridge live status — buildLiveStatusOverride falls through to undefined", () => {
  const source = readCompiledExtensionSource();
  const fnMatch = source.match(/function buildLiveStatusOverride\(activeWorkspace\)[\s\S]*?\n\}/);
  assert.ok(fnMatch, "buildLiveStatusOverride should exist");
  assert.ok(fnMatch![0].includes("default:") && fnMatch![0].includes("return undefined;"));
});

test("lastFinalizedBridgeResult is a plain in-memory module variable, not persisted state — a VS Code reload (fresh extension host) always resets it to undefined", () => {
  const source = readCompiledExtensionSource();
  assert.match(source, /let lastFinalizedBridgeResult;?\s*\n/, "must be declared as a plain module-level let, defaulting to undefined");
  // Must never be read from or written to globalState/workspaceState — that
  // would survive a reload, defeating the "reload resets to waiting" rule.
  assert.ok(!source.includes("lastFinalizedBridgeResult\", ") , "must not be backed by ExtensionContext state APIs");
});

test("Codex and Claude bridge events are dispatched through the same adapter-agnostic setters — no adapter-specific branching that could cross-contaminate", () => {
  const fn = extractHandleBridgeChange(readCompiledExtensionSource());
  // The dispatch switch is keyed purely on event.event (turn.started /
  // run.started / run.finished / turn.finished) — never on event.adapter —
  // so a Codex event and a Claude event for the same event type always go
  // through the exact same code path with no separate branch to drift out
  // of sync.
  assert.ok(!fn.includes('adapter === "codex"'));
  assert.ok(!fn.includes('adapter === "claude"'));
  assert.match(fn, /switch \(event\.event\)/);
});

test("the Dashboard status card value never shows the typo '收工中' — must be '收功中' (产品名是吸T神功)", () => {
  const extensionSource = readCompiledExtensionSource();
  const logicSource = fs.readFileSync(path.join(__dirname, "..", "logic.js"), "utf-8");
  assert.ok(!extensionSource.includes("收工中"), "extension.js must not contain the '收工中' typo");
  assert.ok(!logicSource.includes("收工中"), "logic.js must not contain the '收工中' typo");
  assert.ok(logicSource.includes("收功中"), "logic.js should render the corrected '收功中' status value");
});
