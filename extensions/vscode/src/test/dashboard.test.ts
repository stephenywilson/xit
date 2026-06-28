import test from "node:test";
import assert from "node:assert/strict";
import * as fs from "node:fs";
import * as path from "node:path";
import { computeLiveRunView, promoteSettlingToFinal } from "../logic";
import type { CurrentRunState, LiveStatusView } from "../types";

// Mirrors the fallback dashboard.ts builds when no liveOverride is supplied
// (fresh activation / Reload with no in-memory live state).
const IDLE_STATUS: LiveStatusView = {
  kind: "idle",
  label: "准备就绪",
  reason: "no active VS Code run",
  source: "vscode",
};

test("Dashboard initial state does not read the last history run as '本轮发功'", () => {
  // No liveOverride (idle) and no current-run.json state at all.
  const view = computeLiveRunView(IDLE_STATUS, undefined);
  assert.equal(view.reportPanelActive, false);
  assert.equal(view.subtitle, "等待下一轮发功");
});

test("even when the last history/state run is a failed 'status --json', initial dashboard still waits", () => {
  // Simulates a stale current-run.json left over from a previous session:
  // old, failed, and for a command the user never ran this session.
  const staleFailedState: CurrentRunState = {
    status: "failed",
    command: "status --json",
    completed_at: "2026-06-23T12:24:14+08:00",
    exit_code: 1,
    raw_bytes: 1000,
    summary_bytes: 63 * 4,
  };
  const view = computeLiveRunView(IDLE_STATUS, staleFailedState);
  assert.equal(view.reportPanelActive, false, "idle liveStatus must win over any stale on-disk state");
  assert.equal(view.subtitle, "等待下一轮发功");
});

test("liveOverride present (bridge success) renders '本轮发功' from the override, not history", () => {
  const liveOverride: LiveStatusView = {
    kind: "xit_completed",
    label: "本次省",
    source: "vscode-ai-bridge",
    savedTokensDisplay: "约 9.00k Token",
    exitCode: 0,
  };
  const view = computeLiveRunView(liveOverride, undefined);
  assert.equal(view.reportPanelActive, true);
  assert.equal(view.savedDisplay, "约 9.00k Token");
  assert.equal(view.statusDisplay, "成功");
});

test("liveOverride present (local failed run) renders failed status with 0 Token, not history", () => {
  const liveOverride: LiveStatusView = {
    kind: "agent_not_routed",
    label: "执行失败",
    source: "liveState",
    savedTokensDisplay: "0 Token",
    reductionPct: 0,
    summaryBytes: 252,
  };
  const view = computeLiveRunView(liveOverride, undefined);
  assert.equal(view.reportPanelActive, true);
  assert.equal(view.savedDisplay, "0 Token");
  assert.equal(view.reductionDisplay, "—");
  assert.equal(view.statusDisplay, "失败");
});

test("reload / new activation with no active liveOverride falls back to waiting", () => {
  // Extension host restart resets in-memory live state to idle; dashboard.ts
  // passes liveOverride = undefined in this case and constructs IDLE_STATUS itself.
  const freshActivationState = undefined;
  const view = computeLiveRunView(IDLE_STATUS, freshActivationState);
  assert.equal(view.reportPanelActive, false);
  assert.equal(view.subtitle, "等待下一轮发功");
});

test("'本轮发功' view never carries a completion-time or command-summary field", () => {
  const liveOverride: LiveStatusView = {
    kind: "xit_completed",
    label: "本次省",
    source: "liveState",
    savedTokensDisplay: "约 9.00k Token",
    exitCode: 0,
  };
  const view = computeLiveRunView(liveOverride, undefined);
  const keys = Object.keys(view);
  assert.ok(!keys.includes("completedAt"));
  assert.ok(!keys.includes("command"));
  assert.deepEqual(
    keys.sort(),
    [
      "reductionDisplay",
      "reductionHighlight",
      "reportPanelActive",
      "runCountDisplay",
      "runCountHighlight",
      "savedDisplay",
      "savedHighlight",
      "statusDisplay",
      "subtitle",
    ].sort(),
  );
});

test("降噪率: saved=80, retained=20 -> 80% (bridge success path, byte-ratio formula)", () => {
  const liveOverride: LiveStatusView = {
    kind: "xit_completed",
    label: "本次省",
    source: "vscode-ai-bridge",
    savedTokensDisplay: "80 Token",
    exitCode: 0,
    savedBytes: 80,
    summaryBytes: 20,
    runCount: 1,
  };
  const view = computeLiveRunView(liveOverride, undefined);
  assert.equal(view.reductionDisplay, "80%");
});

test("降噪率: missing retained/summary data -> '—', never fabricated", () => {
  const liveOverride: LiveStatusView = {
    kind: "xit_completed",
    label: "本次省",
    source: "vscode-ai-bridge",
    savedTokensDisplay: "约 9.00k Token",
    exitCode: 0,
    savedBytes: 9000,
    // summaryBytes intentionally omitted
    runCount: 1,
  };
  const view = computeLiveRunView(liveOverride, undefined);
  assert.equal(view.reductionDisplay, "—");
});

test("降噪率: a failed bridge run always shows '—', even if byte fields are present", () => {
  const liveOverride: LiveStatusView = {
    kind: "agent_not_routed",
    label: "执行失败",
    source: "vscode-ai-bridge",
    exitCode: 1,
    savedBytes: 0,
    summaryBytes: 4000,
    runCount: 1,
  };
  const view = computeLiveRunView(liveOverride, undefined);
  assert.equal(view.reductionDisplay, "—");
  assert.equal(view.statusDisplay, "失败");
});

test("降噪率: non-integer ratio keeps exactly one decimal place", () => {
  const liveOverride: LiveStatusView = {
    kind: "xit_completed",
    label: "本次省",
    source: "vscode-ai-bridge",
    savedTokensDisplay: "70 Token",
    exitCode: 0,
    savedBytes: 70,
    summaryBytes: 10, // 70/80 = 87.5%
    runCount: 1,
  };
  const view = computeLiveRunView(liveOverride, undefined);
  assert.equal(view.reductionDisplay, "87.5%");
});

test("本轮共吸: run_count=1 from a bridge event displays '1次'", () => {
  const liveOverride: LiveStatusView = {
    kind: "xit_completed",
    label: "本次省",
    source: "vscode-ai-bridge",
    savedTokensDisplay: "约 22.72k Token",
    exitCode: 0,
    savedBytes: 90880,
    summaryBytes: 3800,
    runCount: 1,
  };
  const view = computeLiveRunView(liveOverride, undefined);
  assert.equal(view.runCountDisplay, "1次");
  assert.equal(view.runCountHighlight, true);
});

test("本轮共吸: missing run_count displays '—', never fabricated as today's run count", () => {
  const liveOverride: LiveStatusView = {
    kind: "xit_completed",
    label: "本次省",
    source: "vscode-ai-bridge",
    savedTokensDisplay: "约 9.00k Token",
    exitCode: 0,
  };
  const view = computeLiveRunView(liveOverride, undefined);
  assert.equal(view.runCountDisplay, "—");
});

test("Claude Code VS Code bridge liveOverride renders the same '本轮发功' four cards as Codex", () => {
  // isBridgeStatus is keyed on source === "vscode-ai-bridge" only — adapter
  // (Codex vs Claude) never changes which fields the Dashboard computes.
  const liveOverride: LiveStatusView = {
    kind: "xit_completed",
    label: "本次省",
    adapter: "Claude",
    command: "Claude Code 对话框",
    source: "vscode-ai-bridge",
    savedTokensDisplay: "约 22.72k Token",
    exitCode: 0,
    savedBytes: 90880,
    summaryBytes: 3800,
    runCount: 1,
  };
  const view = computeLiveRunView(liveOverride, undefined);
  assert.equal(view.reportPanelActive, true);
  assert.equal(view.savedDisplay, "约 22.72k Token");
  assert.equal(view.runCountDisplay, "1次");
  assert.equal(view.reductionDisplay, "96%");
  assert.equal(view.statusDisplay, "成功");
});

// ──────────────────────────────────────────────────────────────────
// UX timing: thinking (守护中) -> running -> settling (收功中) -> final.
// run.finished must never read as "成功" until the turn is confirmed over.
// ──────────────────────────────────────────────────────────────────

test("turn.started (AI thinking, no tool yet) renders Dashboard 状态 as '守护中', no fabricated numbers", () => {
  const liveOverride: LiveStatusView = {
    kind: "xit_turn_active",
    label: "正在守护",
    adapter: "Codex",
    command: "Codex 对话框",
    source: "vscode-ai-bridge",
  };
  const view = computeLiveRunView(liveOverride, undefined);
  assert.equal(view.reportPanelActive, true);
  assert.equal(view.statusDisplay, "守护中");
  assert.equal(view.savedDisplay, "—");
  assert.equal(view.reductionDisplay, "—");
  assert.equal(view.runCountDisplay, "—");
});

test("running: 本轮共吸 shows '统计中' (not '—'), 状态 is '运行中'", () => {
  const liveOverride: LiveStatusView = {
    kind: "xit_running",
    label: "正在吸T",
    adapter: "Codex",
    command: "Codex 对话框",
    source: "vscode-ai-bridge",
  };
  const view = computeLiveRunView(liveOverride, undefined);
  assert.equal(view.statusDisplay, "运行中");
  assert.equal(view.savedDisplay, "计算中");
  assert.equal(view.reductionDisplay, "计算中");
  // A run is genuinely in flight — "统计中" reflects that something is being
  // counted, without fabricating the real run_count before it's final.
  assert.equal(view.runCountDisplay, "统计中");
});

test("run.finished success but final answer not done: Dashboard hides the real numbers entirely (计算中/统计中/计算中/收功中)", () => {
  const liveOverride: LiveStatusView = {
    kind: "xit_settling",
    label: "神功正在收工",
    adapter: "Codex",
    command: "Codex 对话框",
    source: "vscode-ai-bridge",
    savedTokensDisplay: "约 22.72k Token",
    exitCode: 0,
    savedBytes: 90880,
    summaryBytes: 3800,
    runCount: 1,
  };
  const view = computeLiveRunView(liveOverride, undefined);
  assert.equal(view.statusDisplay, "收功中");
  assert.notEqual(view.statusDisplay, "成功");
  // The tool finishing must NEVER read as "the AI is done talking" — even
  // though the real numbers already sit on liveOverride (ready for
  // promoteSettlingToFinal), the Dashboard must render the SAME "计算中/统计
  // 中" placeholders as the running state until the turn is confirmed over.
  // 本轮共吸 shows "统计中" (not "—") — a run did happen, just not final yet.
  assert.equal(view.savedDisplay, "计算中");
  assert.equal(view.runCountDisplay, "统计中");
  assert.equal(view.reductionDisplay, "计算中");
  assert.equal(view.savedHighlight, false);
  assert.equal(view.reductionHighlight, false);
  assert.equal(view.runCountHighlight, false);
});

test("failed run settling (final answer not done): Dashboard still hides numbers — never reveals '失败' or '—' reduction early", () => {
  const liveOverride: LiveStatusView = {
    kind: "xit_settling",
    label: "神功正在收工",
    source: "vscode-ai-bridge",
    exitCode: 1,
    savedBytes: 0,
    summaryBytes: 4000,
    runCount: 1,
  };
  const view = computeLiveRunView(liveOverride, undefined);
  assert.equal(view.statusDisplay, "收功中");
  assert.notEqual(view.statusDisplay, "失败");
  // Even the failure outcome itself must not leak early — settling renders
  // identically regardless of the eventual exit code.
  assert.equal(view.savedDisplay, "计算中");
  assert.equal(view.reductionDisplay, "计算中");
  assert.equal(view.runCountDisplay, "统计中");
});

test("after promoteSettlingToFinal (turn.finished / fallback), Dashboard 状态 becomes '成功' for a successful run", () => {
  const settling: LiveStatusView = {
    kind: "xit_settling",
    label: "神功正在收工",
    source: "vscode-ai-bridge",
    savedTokensDisplay: "约 22.72k Token",
    exitCode: 0,
    savedBytes: 90880,
    summaryBytes: 3800,
    runCount: 1,
  };
  const final = promoteSettlingToFinal(settling);
  const view = computeLiveRunView(final, undefined);
  assert.equal(view.statusDisplay, "成功");
  assert.equal(view.savedDisplay, "约 22.72k Token");
  assert.equal(view.runCountDisplay, "1次");
  assert.equal(view.reductionDisplay, "96%");
});

test("after promoteSettlingToFinal, Dashboard 状态 becomes '失败' for a failed run, saved stays 0/dash", () => {
  const settling: LiveStatusView = {
    kind: "xit_settling",
    label: "神功正在收工",
    source: "vscode-ai-bridge",
    exitCode: 1,
    savedBytes: 0,
    summaryBytes: 4000,
    runCount: 1,
  };
  const final = promoteSettlingToFinal(settling);
  const view = computeLiveRunView(final, undefined);
  assert.equal(view.statusDisplay, "失败");
  assert.equal(view.reductionDisplay, "—");
});

test("compiled dashboard.js renders the four final '本轮发功' cards: 本次省 / 本轮共吸 / 降噪率 / 状态", () => {
  const compiledPath = path.join(__dirname, "..", "dashboard.js");
  const source = fs.readFileSync(compiledPath, "utf-8");
  assert.ok(!source.includes("完成时间"), "完成时间 card should be removed");
  assert.ok(!source.includes("命令摘要"), "命令摘要 card should be removed");
  for (const label of ['"本次省"', '"本轮共吸"', '"降噪率"', '"状态"']) {
    assert.ok(source.includes(label), `${label} card should be present`);
  }
});

test("'守护对象' and '保留精华' are no longer rendered as '本轮发功' main cards", () => {
  const compiledPath = path.join(__dirname, "..", "dashboard.js");
  const source = fs.readFileSync(compiledPath, "utf-8");
  assert.ok(!source.includes("守护对象"));
  assert.ok(!source.includes("保留精华"));
  assert.ok(!source.includes("本次省T"), "old '本次省T' label must be replaced by '本次省'");
});

test("Dashboard top live section is titled '本轮发功', not '本次发功'", () => {
  const compiledPath = path.join(__dirname, "..", "dashboard.js");
  const source = fs.readFileSync(compiledPath, "utf-8");
  assert.ok(source.includes("本轮发功"), "本轮发功 title should be present");
  assert.ok(!source.includes("<h2>本次发功</h2>"), "本次发功 must no longer be used as the top section title");
});

test("Dashboard waiting subtitle is '等待下一轮发功'", () => {
  // The waiting subtitle constant lives in logic.ts (WAITING_VIEW), not as a
  // literal string in dashboard.ts's HTML template.
  const compiledPath = path.join(__dirname, "..", "logic.js");
  const source = fs.readFileSync(compiledPath, "utf-8");
  assert.ok(source.includes("等待下一轮发功"));
  assert.ok(!source.includes("等待下一次发功"), "old wording must not remain");
});

test("status bar text is unchanged: '吸T神功 · 本次省 ...', never '本轮省'", () => {
  const compiledPath = path.join(__dirname, "..", "extension.js");
  const source = fs.readFileSync(compiledPath, "utf-8");
  assert.ok(source.includes("吸T神功 · 本次省"));
  assert.ok(!source.includes("本轮省"), "status bar must never say '本轮省'");
});
