import test from "node:test";
import assert from "node:assert/strict";
import * as fs from "node:fs";
import * as path from "node:path";
import { ChannelStore, deriveChannelId } from "../channels";
import type { VscodeAiBridgeEvent } from "../types";

const HEX = (seed: string): string =>
  // 64-hex placeholder derived from a seed (tests only need distinct,
  // well-formed hashes — not real sha256 of anything).
  (seed + "0".repeat(64)).replace(/[^0-9a-f]/g, "a").slice(0, 64);

const WS = HEX("workspace");
const HOST_A = HEX("hosta");

interface MakeOpts {
  adapter?: "claude" | "codex";
  surface?: "claude_code" | "codex_chat";
  host?: string;
  ws?: string;
  runId?: string;
  startedAt?: string;
  finishedAt?: string;
  exitCode?: number;
  savedTokens?: number;
  savedBytes?: number;
  summaryBytes?: number;
  runCount?: number;
  eventId?: string;
}

function make(event: VscodeAiBridgeEvent["event"], o: MakeOpts = {}): VscodeAiBridgeEvent {
  const adapter = o.adapter ?? "claude";
  const surface = o.surface ?? (adapter === "claude" ? "claude_code" : "codex_chat");
  const ev: VscodeAiBridgeEvent = {
    schema: "xit.vscode-ai-bridge.v1",
    event,
    host: "vscode",
    surface,
    adapter,
    workspace_hash: o.ws ?? WS,
    host_instance_hash: o.host ?? HOST_A,
    run_id: o.runId ?? "run-1",
    command_hash: HEX("cmd"),
    started_at: o.startedAt ?? "2026-06-30T10:00:00Z",
    event_id: o.eventId,
  };
  if (event === "run.finished" || event === "turn.finished") {
    ev.finished_at = o.finishedAt ?? "2026-06-30T10:00:05Z";
  }
  if (event === "run.finished") {
    ev.exit_code = o.exitCode ?? 0;
    ev.saved_tokens = o.savedTokens;
    ev.saved_bytes = o.savedBytes;
    ev.summary_bytes = o.summaryBytes;
    ev.run_count = o.runCount;
  }
  return ev;
}

// Channel A = claude/claude_code; Channel B = codex/codex_chat — same window
// (host) and workspace, different AI task => different channel.
const A = { adapter: "claude" as const };
const B = { adapter: "codex" as const };

test("deriveChannelId is anonymous, stable, and excludes raw signals", () => {
  const id = deriveChannelId({
    adapter: "claude",
    surface: "claude_code",
    host_instance_hash: HOST_A,
    workspace_hash: WS,
  });
  assert.match(id, /^[a-f0-9]{64}$/);
  // Different adapter/surface => different channel.
  const other = deriveChannelId({
    adapter: "codex",
    surface: "codex_chat",
    host_instance_hash: HOST_A,
    workspace_hash: WS,
  });
  assert.notEqual(id, other);
  // The id is a hash; none of the input plaintext leaks through.
  assert.ok(!id.includes("claude") && !id.includes(WS));
});

test("prefers a Go-supplied channel_id but derives it identically when absent", () => {
  const derived = deriveChannelId({ adapter: "claude", surface: "claude_code", host_instance_hash: HOST_A, workspace_hash: WS });
  const withId = deriveChannelId({ adapter: "claude", surface: "claude_code", host_instance_hash: HOST_A, workspace_hash: WS, channel_id: derived });
  assert.equal(withId, derived);
});

test("two different channels both running => Dashboard shows 2 tasks", () => {
  const store = new ChannelStore();
  store.apply(make("run.started", { ...A, runId: "a1", startedAt: "2026-06-30T10:00:00Z" }));
  store.apply(make("run.started", { ...B, runId: "b1", startedAt: "2026-06-30T10:00:01Z" }));
  const view = store.dashboardView();
  assert.equal(view.activeCount, 2);
  assert.equal(view.tasks.length, 2);
  assert.equal(view.topStatus, "2 个任务正在吸T");
});

test("channel A finished, B still running => status bar NOT ready", () => {
  const store = new ChannelStore();
  const now = Date.parse("2026-06-30T10:00:10Z");
  store.apply(make("run.started", { ...A, runId: "a1", startedAt: "2026-06-30T10:00:00Z" }));
  store.apply(make("run.started", { ...B, runId: "b1", startedAt: "2026-06-30T10:00:01Z" }));
  // A finishes its whole turn.
  store.apply(make("run.finished", { ...A, runId: "a1", finishedAt: "2026-06-30T10:00:05Z", savedBytes: 4000, summaryBytes: 1000 }));
  store.apply(make("turn.finished", { ...A, runId: "a1", finishedAt: "2026-06-30T10:00:06Z" }));
  // B is still running. Status bar must reflect 1 active task, never 准备就绪.
  const text = store.statusBarText(now);
  assert.equal(text, "吸T神功 · Codex Chat 正在吸T");
  assert.notEqual(text, "吸T神功 · 准备就绪");
});

test("channel A final result does not overwrite channel B running state", () => {
  const store = new ChannelStore();
  const observedAt = Date.parse("2026-06-30T10:00:06Z");
  store.apply(make("run.started", { ...A, runId: "a1", startedAt: "2026-06-30T10:00:00Z" }), observedAt);
  store.apply(make("run.started", { ...B, runId: "b1", startedAt: "2026-06-30T10:00:01Z" }), observedAt);
  store.apply(make("run.finished", { ...A, runId: "a1", finishedAt: "2026-06-30T10:00:05Z", savedBytes: 8000, summaryBytes: 2000 }), observedAt);
  store.apply(make("turn.finished", { ...A, runId: "a1", finishedAt: "2026-06-30T10:00:06Z" }), observedAt);
  const aId = deriveChannelId(make("run.started", A));
  const bId = deriveChannelId(make("run.started", B));
  assert.equal(store.get(aId)?.status, "finished");
  // B must still be running — A's finalize did not touch it.
  assert.equal(store.get(bId)?.status, "running");
  assert.equal(store.activeChannels().length, 1);
});

test("interleaved A/B event ordering stays correct per channel", () => {
  const store = new ChannelStore();
  store.apply(make("turn.started", { ...A, startedAt: "2026-06-30T10:00:00Z" }));
  store.apply(make("turn.started", { ...B, startedAt: "2026-06-30T10:00:00Z" }));
  store.apply(make("run.started", { ...A, runId: "a1", startedAt: "2026-06-30T10:00:01Z" }));
  store.apply(make("run.started", { ...B, runId: "b1", startedAt: "2026-06-30T10:00:02Z" }));
  store.apply(make("run.finished", { ...B, runId: "b1", finishedAt: "2026-06-30T10:00:03Z", savedBytes: 4000, summaryBytes: 1000 }));
  store.apply(make("run.finished", { ...A, runId: "a1", finishedAt: "2026-06-30T10:00:04Z", savedBytes: 2000, summaryBytes: 500 }));
  const aId = deriveChannelId(make("run.started", A));
  const bId = deriveChannelId(make("run.started", B));
  // Both are settling (run.finished held), independent results.
  assert.equal(store.get(aId)?.status, "settling");
  assert.equal(store.get(bId)?.status, "settling");
  assert.equal(store.get(aId)?.savedBytes, 2000);
  assert.equal(store.get(bId)?.savedBytes, 4000);
});

test("stale (older) event never overwrites a newer per-channel state", () => {
  const store = new ChannelStore();
  store.apply(make("run.finished", { ...A, runId: "a2", startedAt: "2026-06-30T10:00:10Z", finishedAt: "2026-06-30T10:00:11Z", savedBytes: 4000, summaryBytes: 1000 }));
  // A late straggler from an OLDER run arrives after — must be ignored.
  const ignored = store.apply(make("run.started", { ...A, runId: "a1", startedAt: "2026-06-30T10:00:00Z" }));
  assert.equal(ignored, undefined);
  const aId = deriveChannelId(make("run.started", A));
  assert.equal(store.get(aId)?.status, "settling");
});

test("mirror + primary duplicate event is deduped (not double counted)", () => {
  const store = new ChannelStore();
  const primary = make("run.finished", { ...A, runId: "a1", eventId: "ev-xyz", savedBytes: 4000, summaryBytes: 1000, runCount: 3 });
  const mirror = make("run.finished", { ...A, runId: "a1", eventId: "ev-xyz", savedBytes: 4000, summaryBytes: 1000, runCount: 3 });
  const first = store.apply(primary);
  const second = store.apply(mirror);
  assert.ok(first);
  assert.equal(second, undefined, "duplicate event_id must be deduped");
  const aId = deriveChannelId(primary);
  assert.equal(store.get(aId)?.runCount, 3);
});

test("dashboard reopen replays ALL active channels, not just the last", () => {
  const store = new ChannelStore();
  store.apply(make("run.started", { ...A, runId: "a1", startedAt: "2026-06-30T10:00:00Z" }));
  store.apply(make("run.started", { ...B, runId: "b1", startedAt: "2026-06-30T10:00:01Z" }));
  // Simulate a fresh dashboardView() call (what reopen does).
  const view = store.dashboardView();
  const adapters = view.tasks.map((t) => t.adapter).sort();
  assert.deepEqual(adapters, ["claude", "codex"]);
});

test("status bar active-count rules are deterministic", () => {
  const store = new ChannelStore();
  // 0 active and nothing finished => ready.
  assert.equal(store.statusBarText(), undefined); // no channels at all => caller falls back
  store.apply(make("run.started", { ...A, runId: "a1", startedAt: "2026-06-30T10:00:00Z" }));
  assert.equal(store.statusBarText(Date.parse("2026-06-30T10:00:02Z")), "吸T神功 · Claude Code 正在吸T");
  store.apply(make("run.started", { ...B, runId: "b1", startedAt: "2026-06-30T10:00:01Z" }));
  assert.equal(store.statusBarText(Date.parse("2026-06-30T10:00:02Z")), "吸T神功 · 2 个任务正在吸T");
});

test("all tasks finished + hold elapsed => bar reverts to 准备就绪", () => {
  const store = new ChannelStore();
  const observedAt = Date.parse("2026-06-30T10:00:06Z");
  store.apply(make("run.started", { ...A, runId: "a1", startedAt: "2026-06-30T10:00:00Z" }), observedAt);
  store.apply(make("run.finished", { ...A, runId: "a1", finishedAt: "2026-06-30T10:00:05Z", savedBytes: 4000, summaryBytes: 1000 }), observedAt);
  store.apply(make("turn.finished", { ...A, runId: "a1", finishedAt: "2026-06-30T10:00:06Z" }), observedAt);
  // Just after finish => still showing the final result (within hold).
  assert.match(store.statusBarText(Date.parse("2026-06-30T10:00:07Z"))!, /本次省/);
  // Long after the hold => store hands control back (undefined) so the
  // caller's local state machine renders 准备就绪 (and won't mask a later
  // manual run).
  assert.equal(store.statusBarText(Date.parse("2026-06-30T10:01:00Z")), undefined);
});

test("finalize fallback promotes a settling channel without a turn.finished", () => {
  const store = new ChannelStore();
  store.apply(make("run.finished", { ...A, runId: "a1", finishedAt: "2026-06-30T10:00:05Z", exitCode: 0, savedBytes: 4000, summaryBytes: 1000 }));
  const aId = deriveChannelId(make("run.finished", A));
  assert.equal(store.get(aId)?.status, "settling");
  assert.equal(store.finalize(aId), true);
  assert.equal(store.get(aId)?.status, "finished");
});

test("a finished run that saved nothing reads 无需发功, never a fake 本次省 0 Token", () => {
  const store = new ChannelStore();
  // exit 0 but zero savings / zero runs — the screenshot's empty-success case.
  store.apply(make("run.finished", { ...A, runId: "a1", finishedAt: "2026-06-30T10:00:05Z", exitCode: 0, savedBytes: 0, summaryBytes: 0, runCount: 0 }));
  const aId = deriveChannelId(make("run.finished", A));
  store.finalize(aId, Date.parse("2026-06-30T10:00:06Z"));
  // Status bar must NOT claim a saving.
  const text = store.statusBarText(Date.parse("2026-06-30T10:00:07Z"));
  assert.equal(text, "吸T神功 · 准备就绪");
  assert.ok(!/本次省 0/.test(text || ""));
  // The task card reads 无需发功.
  const card = store.dashboardView(Date.parse("2026-06-30T10:00:07Z")).tasks[0];
  assert.equal(card.savedDisplay, "无需发功");
});

// Privacy: the channel store carries no telemetry/network code, so multi-
// channel activity can never itself send anything, and the channel ids never
// appear in any uploaded payload (asserted against the source text).
test("channels module contains no network/telemetry sink", () => {
  const src = fs.readFileSync(path.join(__dirname, "..", "channels.js"), "utf-8");
  assert.ok(!/fetch\(|https?:|sendMetrics|XIT_API_BASE/.test(src), "channels.ts must not reach the network");
});
