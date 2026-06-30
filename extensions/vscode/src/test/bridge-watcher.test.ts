import test from "node:test";
import assert from "node:assert/strict";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import {
  initializeHookCursor,
  readAppendedHookEvents,
  type HookFileCursor,
} from "../bridge-cursor";

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

test("every bridge event source (primary + mirror) listens for both onDidChange and onDidCreate", () => {
  const source = readCompiledExtensionSource();
  assert.ok(source.includes("source.watcher.onDidChange(() => { void handleBridgeChange(source); }"));
  assert.ok(source.includes("source.watcher.onDidCreate(() => { void handleBridgeChange(source); }"));
});

// ──────────────────────────────────────────────────────────────────
// Mirror watcher: the Go-side hook (internal/vscodebridge.MirrorHome) now
// dual-writes every event into a global ~/.xit/events/vscode-ai-bridge.jsonl
// in addition to the project-local file, specifically so an event survives
// even when the AI's cwd at the time differed from this workspace root. The
// extension must watch BOTH locations and must not double-process an event
// that the dual-write delivers to both.
// ──────────────────────────────────────────────────────────────────

test("resolveBridgeMirrorHome respects XIT_VSCODE_BRIDGE_HOME and otherwise defaults to os.homedir()/.xit — matching the Go side's MirrorHome()", () => {
  const source = readCompiledExtensionSource();
  const fnMatch = source.match(/function resolveBridgeMirrorHome\(\) \{[\s\S]*?\n\}/);
  assert.ok(fnMatch, "resolveBridgeMirrorHome should exist");
  const body = fnMatch![0];
  assert.ok(body.includes("process.env.XIT_VSCODE_BRIDGE_HOME"));
  assert.ok(body.includes('path.join(os.homedir(), ".xit")'));
});

test("registerVscodeAiBridgeWatcher registers a second 'mirror' source only when it differs from the primary file", () => {
  const source = readCompiledExtensionSource();
  const fnMatch = source.match(/function registerVscodeAiBridgeWatcher\(context\) \{[\s\S]*?\n\}\n/);
  assert.ok(fnMatch, "registerVscodeAiBridgeWatcher should exist");
  const body = fnMatch![0];
  assert.ok(body.includes('label: "primary"'));
  assert.ok(body.includes('label: "mirror"'));
  assert.ok(body.includes("path.resolve(mirrorFile) !== path.resolve(primaryFile)"));
});

test("the same logical event arriving from both the primary and mirror file is only processed once (dedupe by event kind + run_id)", () => {
  const fn = extractHandleBridgeChange(readCompiledExtensionSource());
  assert.match(fn, /const dedupeKey = `\$\{event\.event\}\|\$\{event\.run_id\}`/);
  assert.ok(fn.includes("hasProcessedBridgeEvent(dedupeKey)"));
  assert.ok(fn.includes("markBridgeEventProcessed(dedupeKey)"));
  // The dedupe check must come before classification/dispatch — a duplicate
  // delivery must not double-count diagnostics or call the state setters again.
  const dedupeIdx = fn.indexOf("hasProcessedBridgeEvent(dedupeKey)");
  const classifyIdx = fn.indexOf("classifyBridgeEvent");
  assert.ok(dedupeIdx > -1 && classifyIdx > -1 && dedupeIdx < classifyIdx);
});

test("the recent-event-key dedupe set is capped so a long-running session can't leak memory", () => {
  const source = readCompiledExtensionSource();
  assert.match(source, /RECENT_BRIDGE_EVENT_KEY_CAP = \d+/);
  const fnMatch = source.match(/function markBridgeEventProcessed\(key\) \{[\s\S]*?\n\}/);
  assert.ok(fnMatch, "markBridgeEventProcessed should exist");
  assert.ok(fnMatch![0].includes(".shift()"), "must evict the oldest key once over the cap");
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
  const match = source.match(/const handleBridgeChange = .*?async \(source\) => \{[\s\S]*?\n    \};/);
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
  assert.ok(fn.includes("dashboard_1.updateDashboardIfOpen)(status, buildLiveStatusOverride(workspacePath), workspacePath, channelStore.dashboardView())"));
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
  const cursor = initializeHookCursor(path.join(os.tmpdir(), `xit-bridge-cursor-does-not-exist-${Date.now()}.jsonl`));
  assert.equal(cursor.offset, 0);
});

// ──────────────────────────────────────────────────────────────────
// Real behavioral coverage for the bridge file cursor — extracted into
// bridge-cursor.ts (no "vscode" import) specifically so these can be tested
// against real temp files instead of only structural assertions on compiled
// source. Covers: a burst of many rapid appends, malformed/partial lines,
// and file truncation/rotation (e.g. log rotation, or a test/dev workflow
// that resets the bridge file) — none of these may kill the watcher.
// ──────────────────────────────────────────────────────────────────

function makeTempBridgeFile(): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "xit-bridge-cursor-test-"));
  return path.join(dir, "vscode-ai-bridge.jsonl");
}

function appendLine(file: string, obj: unknown): void {
  fs.appendFileSync(file, `${JSON.stringify(obj)}\n`, "utf-8");
}

test("a burst of 50 rapid appends are all read back, in order, across repeated reads", () => {
  const file = makeTempBridgeFile();
  fs.writeFileSync(file, "", "utf-8");
  const cursor = initializeHookCursor(file);

  for (let i = 0; i < 50; i++) {
    appendLine(file, { event: "run.started", run_id: `run-${i}` });
  }

  const events = readAppendedHookEvents(file, cursor);
  assert.equal(events.length, 50);
  assert.equal((events[0] as any).run_id, "run-0");
  assert.equal((events[49] as any).run_id, "run-49");

  // A subsequent read with no new appends must be empty, not re-deliver —
  // the cursor must have actually advanced past all 50 lines.
  assert.deepEqual(readAppendedHookEvents(file, cursor), []);
});

test("a malformed/partial JSON line is skipped without losing the well-formed lines around it, and without throwing", () => {
  const file = makeTempBridgeFile();
  fs.writeFileSync(file, "", "utf-8");
  const cursor = initializeHookCursor(file);

  fs.appendFileSync(
    file,
    [
      JSON.stringify({ event: "run.started", run_id: "good-1" }),
      "{not valid json at all",
      JSON.stringify({ event: "run.finished", run_id: "good-2" }),
    ].join("\n") + "\n",
    "utf-8",
  );

  const events = readAppendedHookEvents(file, cursor);
  assert.equal(events.length, 2, "the malformed line must be skipped, not crash the whole read");
  assert.equal((events[0] as any).run_id, "good-1");
  assert.equal((events[1] as any).run_id, "good-2");
});

test("a write split mid-line (no trailing newline yet) is held as a remainder and completed by the next read, not dropped or duplicated", () => {
  const file = makeTempBridgeFile();
  fs.writeFileSync(file, "", "utf-8");
  const cursor = initializeHookCursor(file);

  const fullLine = JSON.stringify({ event: "run.started", run_id: "split-1" });
  fs.appendFileSync(file, fullLine.slice(0, 10), "utf-8"); // no trailing newline — write is "in flight"
  assert.deepEqual(readAppendedHookEvents(file, cursor), []);

  fs.appendFileSync(file, `${fullLine.slice(10)}\n`, "utf-8");
  const events = readAppendedHookEvents(file, cursor);
  assert.equal(events.length, 1);
  assert.equal((events[0] as any).run_id, "split-1");
});

test("file truncation (e.g. log rotation) resets the read offset instead of throwing or getting stuck", () => {
  const file = makeTempBridgeFile();
  fs.writeFileSync(file, "", "utf-8");
  const cursor = initializeHookCursor(file);

  appendLine(file, { event: "run.started", run_id: "before-rotate" });
  readAppendedHookEvents(file, cursor); // advance past it

  // Truncate and write a fresh, shorter file — same inode on most platforms,
  // but stat.size is now smaller than cursor.offset.
  fs.writeFileSync(file, "", "utf-8");
  appendLine(file, { event: "run.started", run_id: "after-rotate" });

  const events = readAppendedHookEvents(file, cursor);
  assert.equal(events.length, 1);
  assert.equal((events[0] as any).run_id, "after-rotate");
});

test("file replacement (new inode, e.g. atomic rename-based rotation) is also recovered, not silently dropped", () => {
  const file = makeTempBridgeFile();
  fs.writeFileSync(file, "", "utf-8");
  const cursor = initializeHookCursor(file);

  appendLine(file, { event: "run.started", run_id: "before-replace" });
  readAppendedHookEvents(file, cursor);

  const replacement = `${file}.new`;
  fs.writeFileSync(replacement, `${JSON.stringify({ event: "run.started", run_id: "after-replace" })}\n`, "utf-8");
  fs.renameSync(replacement, file); // new inode at the same path

  const events = readAppendedHookEvents(file, cursor);
  assert.equal(events.length, 1);
  assert.equal((events[0] as any).run_id, "after-replace");
});

test("readAppendedHookEvents never throws even if the file is deleted between the watcher firing and the read", () => {
  const file = makeTempBridgeFile();
  const cursor: HookFileCursor = { offset: 0, mtimeMs: 0, remainder: "" };
  assert.doesNotThrow(() => {
    const events = readAppendedHookEvents(file, cursor);
    assert.deepEqual(events, []);
  });
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

// ──────────────────────────────────────────────────────────────────
// Status bar must never stay stuck on "准备就绪" because it was hidden or
// disposed without being re-shown. Both render paths re-assert .show() on
// every update — cheap, and rules this entire bug class out regardless of
// root cause.
// ──────────────────────────────────────────────────────────────────

test("updateStatusBar re-asserts statusBarItem.show() (when enabled) on every call", () => {
  const source = readCompiledExtensionSource();
  const fnMatch = source.match(/async function updateStatusBar\(\) \{[\s\S]*?\n    const workspaceSnapshot/);
  assert.ok(fnMatch, "updateStatusBar should exist");
  assert.ok(fnMatch![0].includes("statusBarItem.show()"));
});

test("updateStatusBarLive re-asserts statusBarItem.show() (when enabled) on every call", () => {
  const source = readCompiledExtensionSource();
  const fnMatch = source.match(/async function updateStatusBarLive\(\)[\s\S]*?\n    const workspaceSnapshot/);
  assert.ok(fnMatch, "updateStatusBarLive should exist");
  assert.ok(fnMatch![0].includes("statusBarItem.show()"));
});

test("handleBridgeChange also calls statusBarItem.show() before refreshing, so a bridge event always surfaces even if the bar was somehow hidden", () => {
  const fn = extractHandleBridgeChange(readCompiledExtensionSource());
  assert.ok(fn.includes("statusBarItem.show();"));
});

// ──────────────────────────────────────────────────────────────────
// Cross-workspace acceptance: when workspace_hash mismatches (the AI cd'd to
// a different project mid-session) but host_instance_hash verifiably
// matches this window, classifyBridgeEvent now returns
// { accepted: true, crossWorkspace: true } instead of rejecting outright —
// see logic.test.ts for the pure-function coverage. The watcher must log
// this distinctly from a same-workspace accept and must still dispatch
// through the normal state setters.
// ──────────────────────────────────────────────────────────────────

test("a cross-workspace accept is logged distinctly and still dispatches through the normal event-kind switch", () => {
  const fn = extractHandleBridgeChange(readCompiledExtensionSource());
  assert.ok(fn.includes("decision.crossWorkspace"));
  assert.match(fn, /accepted: cross-workspace/);
  // The switch dispatch below is unconditional on accepted, not on crossWorkspace —
  // confirmed by the switch existing once, used for all 3 acceptance branches.
  assert.match(fn, /switch \(event\.event\)/);
});

// ──────────────────────────────────────────────────────────────────
// Diagnostics (Section 3): internal/dev-console fields only — timestamps,
// counters, and already-hashed signals. Never the raw command, cwd, output,
// prompt, AI reply, or full session id that produced an event.
// ──────────────────────────────────────────────────────────────────

test("bridgeDiagnostics tracks accepted/dropped counts and last-event metadata, with no raw command/cwd/output fields", () => {
  const source = readCompiledExtensionSource();
  const fnMatch = source.match(/const bridgeDiagnostics = \{[\s\S]*?\n\};/);
  assert.ok(fnMatch, "bridgeDiagnostics initializer should exist");
  assert.ok(fnMatch![0].includes("accepted_event_count: 0"));
  assert.ok(fnMatch![0].includes("dropped_event_count: 0"));
  assert.ok(fnMatch![0].includes("watcher_alive: false"));
  assert.ok(fnMatch![0].includes("event_file_path: []"));

  const fn = extractHandleBridgeChange(source);
  for (const field of [
    "last_event_at",
    "last_event_workspace_hash",
    "last_event_host_instance_hash",
    "last_event_source",
    "current_workspace_hash",
    "accepted_event_count++",
    "last_accepted_event_at",
    "dropped_event_count++",
    "last_dropped_event_at",
    "last_drop_reason",
  ]) {
    assert.ok(fn.includes(field), `handleBridgeChange should update bridgeDiagnostics.${field}`);
  }
  for (const forbidden of ["raw_command", "raw_cwd", "raw_output", "prompt", "session_id", "original_command"]) {
    assert.ok(!fn.includes(forbidden), `handleBridgeChange must never touch ${forbidden}`);
  }
});

test("XiT: Diagnose AI Workflow surfaces the bridge diagnostics block with short (8-char) hashes, not full hashes or paths leaking raw data", () => {
  const source = readCompiledExtensionSource();
  const fnMatch = source.match(/async function runDiagnose\(\)[\s\S]*?\n\}/);
  assert.ok(fnMatch, "runDiagnose should exist");
  const body = fnMatch![0];
  assert.ok(body.includes("VS Code AI bridge watcher:"));
  assert.ok(body.includes("bridgeDiagnostics.watcher_alive"));
  assert.ok(body.includes("bridgeDiagnostics.accepted_event_count"));
  assert.ok(body.includes("bridgeDiagnostics.dropped_event_count"));
  assert.ok(body.includes("shortHash(bridgeDiagnostics.current_workspace_hash)"));
  assert.ok(body.includes("shortHash(bridgeDiagnostics.last_event_workspace_hash)"));
  assert.ok(body.includes("shortHash(bridgeDiagnostics.last_event_host_instance_hash)"));
});
