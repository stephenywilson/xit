// ──────────────────────────────────────────────────────────────────
// XiT MULTI-CHANNEL TASK STATE
//
// The blocker this module fixes: the extension used to hold ONE global
// "current run" / "live status" / status-bar state. When two AI tasks ran
// concurrently (e.g. Claude Code in this panel + Codex/another agent in a
// different project, both dual-writing into the shared bridge mirror file
// this window watches), every event funnelled through those globals. They
// overwrote each other — the status bar flickered, the Dashboard was stuck
// on "计算中", and one task finishing reset the other (still-running) task
// to "准备就绪".
//
// The fix is a Map<channel_id, ChannelState>. One channel == one AI task /
// conversation / active run group. Different tasks land in different
// channels and never overwrite each other. The status bar and Dashboard are
// derived as a DETERMINISTIC aggregate over the channels, so no single task
// can stomp the global view.
//
// channel_id is an anonymous hash derived only from already-anonymous /
// hashed signals (adapter, surface, host_instance_hash, workspace_hash). It
// never contains a prompt, AI reply, command, cwd, path, repo name,
// username, or full session id, and its full value is never uploaded (see
// docs/telemetry.md): only adapter/surface aggregate fields ever leave the
// machine.
// ──────────────────────────────────────────────────────────────────

import * as crypto from "node:crypto";
import type { VscodeAiBridgeEvent } from "./types";
import { formatSavedTokens, getSavedTokens } from "./logic";

export type ChannelStatus = "idle" | "running" | "settling" | "finished" | "error";

export interface ChannelState {
  channelId: string;
  adapter: string; // "claude" | "codex" | ...
  surface: string; // "claude_code" | "codex_chat" | ...
  status: ChannelStatus;
  lastEventAtMs: number;
  lastEventIso: string;
  startedAtMs?: number;
  activeRunId?: string;
  // Result fields, populated once run.finished is observed. Held but NOT
  // promoted to "finished" until the turn is confirmed over (turn.finished
  // or the caller's fallback timer) — same "data ready != AI done talking"
  // rule the single-channel machine used, now per channel.
  savedTokens?: number;
  savedBytes?: number;
  summaryBytes?: number;
  runCount?: number;
  exitCode?: number;
  reductionPct?: number;
}

// How long a finished channel keeps contributing its final result to the
// aggregate status bar ("本次省 …") before the bar reverts to 准备就绪. The
// Dashboard keeps showing finished cards longer (see DASHBOARD_FINISHED_TTL).
const STATUSBAR_FINAL_HOLD_MS = 20_000;

// Finished channels stay visible as Dashboard cards for this long, then are
// pruned. Active (running/settling) channels are never pruned by age.
const DASHBOARD_FINISHED_TTL_MS = 30 * 60 * 1000;

// Hard cap on retained finished channels so a long session can't grow the
// map without bound. Oldest finished channels are dropped first.
const MAX_FINISHED_CHANNELS = 12;

export function deriveChannelId(event: {
  adapter: string;
  surface: string;
  host_instance_hash: string;
  workspace_hash: string;
  channel_id?: string;
}): string {
  // Prefer a Go-supplied stable channel_id when present (new schema), but
  // fall back to deriving it ourselves so OLD v1 events (no channel_id) are
  // still grouped correctly. Both sides hash the same inputs, so the value
  // is identical either way.
  if (typeof event.channel_id === "string" && /^[a-f0-9]{64}$/.test(event.channel_id)) {
    return event.channel_id;
  }
  const material = [event.adapter, event.surface, event.host_instance_hash, event.workspace_hash].join("|");
  return crypto.createHash("sha256").update(material, "utf8").digest("hex");
}

function eventTimeMs(event: VscodeAiBridgeEvent): number {
  const iso =
    event.event === "run.finished" || event.event === "turn.finished"
      ? event.finished_at || event.started_at
      : event.started_at;
  const ms = Date.parse(iso);
  return Number.isFinite(ms) ? ms : Date.now();
}

function adapterLabel(adapter: string): string {
  switch (adapter) {
    case "claude":
      return "Claude Code";
    case "codex":
      return "Codex Chat";
    case "kimi":
      return "Kimi";
    case "cursor":
      return "Cursor";
    case "opencode":
      return "OpenCode";
    default:
      return adapter || "AI";
  }
}

function computeReductionPct(savedBytes?: number, summaryBytes?: number): number | undefined {
  if (typeof savedBytes !== "number" || !Number.isFinite(savedBytes) || savedBytes < 0) {
    return undefined;
  }
  if (typeof summaryBytes !== "number" || !Number.isFinite(summaryBytes) || summaryBytes < 0) {
    return undefined;
  }
  const total = savedBytes + summaryBytes;
  if (total <= 0) {
    return undefined;
  }
  return (savedBytes / total) * 100;
}

export interface DashboardTaskCard {
  channelId: string;
  adapterLabel: string;
  adapter: string;
  surface: string;
  status: ChannelStatus;
  statusLabel: string;
  savedDisplay: string;
  runCountDisplay: string;
  reductionDisplay: string;
  lastEventAtIso: string;
  relativeTime: string;
}

export interface DashboardView {
  topStatus: string;
  activeCount: number;
  tasks: DashboardTaskCard[];
}

// ChannelStore is the single authority for bridge-driven (AI task) live
// state. It is intentionally free of any vscode/timer dependency so it can
// be unit-tested in isolation; the extension feeds it events and drives the
// fallback finalize timers.
export class ChannelStore {
  private channels = new Map<string, ChannelState>();
  // Dedupe across the primary + mirror bridge files (Go dual-writes the same
  // event to both). Keyed by event_id when present, else event+run_id.
  private seenEventKeys: string[] = [];
  private seenEventKeySet = new Set<string>();
  private static readonly DEDUPE_CAP = 400;

  private dedupeKey(event: VscodeAiBridgeEvent): string {
    const anyEv = event as VscodeAiBridgeEvent & { event_id?: string };
    if (typeof anyEv.event_id === "string" && anyEv.event_id.length > 0) {
      return `id:${anyEv.event_id}`;
    }
    return `${event.event}|${event.run_id}`;
  }

  // markSeen returns false if this exact event was already processed (a
  // primary/mirror duplicate) — the caller should then skip it so it is
  // never counted twice.
  private markSeen(event: VscodeAiBridgeEvent): boolean {
    const key = this.dedupeKey(event);
    if (this.seenEventKeySet.has(key)) {
      return false;
    }
    this.seenEventKeySet.add(key);
    this.seenEventKeys.push(key);
    if (this.seenEventKeys.length > ChannelStore.DEDUPE_CAP) {
      const oldest = this.seenEventKeys.shift();
      if (oldest !== undefined) {
        this.seenEventKeySet.delete(oldest);
      }
    }
    return true;
  }

  // apply ingests one accepted bridge event into its channel. Returns the
  // affected channelId, or undefined if the event was a duplicate or stale
  // (older than the channel's last event — newer always wins, so interleaved
  // A/B event ordering and a late straggler can never resurrect an old
  // state).
  apply(event: VscodeAiBridgeEvent, now = Date.now()): string | undefined {
    if (!this.markSeen(event)) {
      return undefined; // primary/mirror duplicate
    }
    const channelId = deriveChannelId(event);
    const tMs = eventTimeMs(event);
    const existing = this.channels.get(channelId);

    // Stale-event protection: a strictly-older event for an EXISTING channel
    // must not overwrite a newer state. (A brand-new channel always passes —
    // its first event defines the baseline.)
    if (existing && tMs < existing.lastEventAtMs) {
      return undefined;
    }

    const base: ChannelState =
      existing ?? {
        channelId,
        adapter: event.adapter,
        surface: event.surface,
        status: "idle",
        lastEventAtMs: tMs,
        lastEventIso: event.started_at,
      };
    base.adapter = event.adapter;
    base.surface = event.surface;
    base.lastEventAtMs = tMs;
    base.lastEventIso =
      event.event === "run.finished" || event.event === "turn.finished"
        ? event.finished_at || event.started_at
        : event.started_at;

    switch (event.event) {
      case "turn.started":
        base.status = "running";
        base.startedAtMs = tMs;
        base.activeRunId = undefined;
        // A new turn invalidates the previous turn's result.
        base.savedTokens = undefined;
        base.savedBytes = undefined;
        base.summaryBytes = undefined;
        base.runCount = undefined;
        base.exitCode = undefined;
        base.reductionPct = undefined;
        break;
      case "run.started":
        base.status = "running";
        base.startedAtMs = base.startedAtMs ?? tMs;
        base.activeRunId = event.run_id;
        break;
      case "run.finished":
        // A finished event for a DIFFERENT run than the one we're tracking is
        // ignored only when it is older; here it is >= lastEventAt, so adopt
        // it. Hold the data in "settling" until the turn is confirmed over.
        base.status = "settling";
        base.activeRunId = event.run_id;
        base.exitCode = event.exit_code;
        base.savedTokens = getSavedTokens(event.saved_tokens, event.saved_bytes);
        base.savedBytes = event.saved_bytes;
        base.summaryBytes = event.summary_bytes;
        base.runCount = event.run_count;
        base.reductionPct =
          event.exit_code === 0 ? computeReductionPct(event.saved_bytes, event.summary_bytes) : undefined;
        break;
      case "turn.finished":
        // The AI's final answer for this turn is done — promote whatever we
        // are holding to its final form.
        this.finalizeState(base);
        break;
    }

    this.channels.set(channelId, base);
    this.prune(now);
    return channelId;
  }

  private finalizeState(state: ChannelState): void {
    if (state.status === "settling") {
      state.status = state.exitCode === 0 ? "finished" : "error";
    } else if (state.status === "running") {
      // Turn ended without a tool run producing a result — nothing to show.
      state.status = "finished";
    }
  }

  // finalize promotes a held "settling" channel to its final state. Called by
  // the extension on a real turn.finished OR a per-channel fallback timer.
  // No-op if the channel is gone or not settling.
  finalize(channelId: string, now = Date.now()): boolean {
    const state = this.channels.get(channelId);
    if (!state || state.status !== "settling") {
      return false;
    }
    state.status = state.exitCode === 0 ? "finished" : "error";
    state.lastEventAtMs = Math.max(state.lastEventAtMs, now);
    this.channels.set(channelId, state);
    return true;
  }

  get(channelId: string): ChannelState | undefined {
    return this.channels.get(channelId);
  }

  all(): ChannelState[] {
    return [...this.channels.values()];
  }

  // activeChannels are the tasks currently "吸T" — running or settling.
  activeChannels(): ChannelState[] {
    return this.all().filter((c) => c.status === "running" || c.status === "settling");
  }

  // mostRecent returns the channel touched last — the default "selected"
  // channel for the Dashboard's 本轮发功 detail panel. Never lets that
  // selection overwrite the others (they remain independent in the map).
  mostRecent(): ChannelState | undefined {
    let best: ChannelState | undefined;
    for (const c of this.all()) {
      if (!best || c.lastEventAtMs > best.lastEventAtMs) {
        best = c;
      }
    }
    return best;
  }

  hasAny(): boolean {
    return this.channels.size > 0;
  }

  private recentlyFinished(now: number): ChannelState[] {
    return this.all().filter(
      (c) =>
        (c.status === "finished" || c.status === "error") &&
        now - c.lastEventAtMs <= STATUSBAR_FINAL_HOLD_MS,
    );
  }

  // ── DETERMINISTIC AGGREGATE VIEWS ───────────────────────────────────

  // statusBarText returns the status-bar string, or undefined when there are
  // no bridge channels at all (so the caller falls back to its local
  // single-run state machine for a manual VS Code run). Rules per spec §四:
  //   active == 0 → recently-finished aggregate, else 准备就绪
  //   active == 1 → that task's own state (吸T / 收功)
  //   active  > 1 → "N 个任务正在吸T"
  statusBarText(now = Date.now()): string | undefined {
    if (!this.hasAny()) {
      return undefined;
    }
    const active = this.activeChannels();
    if (active.length > 1) {
      return `吸T神功 · ${active.length} 个任务正在吸T`;
    }
    if (active.length === 1) {
      const c = active[0];
      if (c.status === "settling") {
        return "吸T神功 · 神功正在收工";
      }
      return `吸T神功 · ${adapterLabel(c.adapter)} 正在吸T`;
    }
    // No active channel. Only revert to ready once every channel is
    // finished/idle AND the most-recent final result's hold has elapsed.
    const finished = this.recentlyFinished(now);
    if (finished.length > 0) {
      const totalSaved = finished.reduce((sum, c) => sum + (c.savedTokens || 0), 0);
      const anyError = finished.some((c) => c.status === "error");
      // Never advertise "本次省 0 Token": a finished run that saved nothing
      // did no real work, so the bar reverts to 准备就绪 (or 执行失败) rather
      // than presenting a fabricated zero saving.
      if (totalSaved > 0) {
        const display = formatSavedTokens(totalSaved);
        if (finished.length > 1 && display) {
          return `吸T神功 · ${finished.length} 个任务本轮共省 ${display}`;
        }
        return display ? `吸T神功 · 本次省 ${display}` : "吸T神功 · 准备就绪";
      }
      if (anyError) {
        return "吸T神功 · 执行失败";
      }
      return "吸T神功 · 准备就绪";
    }
    // Nothing active and nothing recently finished (only stale finished cards
    // remain for the Dashboard). Hand control back to the caller's local state
    // machine — returning a fixed string here would mask a later manual run.
    return undefined;
  }

  private statusLabel(status: ChannelStatus): string {
    switch (status) {
      case "running":
        return "running";
      case "settling":
        return "settling";
      case "finished":
        return "finished";
      case "error":
        return "error";
      default:
        return "idle";
    }
  }

  private cardSavedDisplay(c: ChannelState): string {
    if (c.status === "running") {
      return "计算中";
    }
    if (c.status === "settling") {
      return "统计中";
    }
    if (c.status === "error") {
      return "执行失败";
    }
    // finished: a run that saved nothing reads "无需发功", never "0 Token".
    if (!c.savedTokens || c.savedTokens <= 0) {
      return "无需发功";
    }
    return formatSavedTokens(c.savedTokens) || "—";
  }

  private cardRunCountDisplay(c: ChannelState): string {
    if (c.status === "running" || c.status === "settling") {
      return "统计中";
    }
    return typeof c.runCount === "number" && c.runCount >= 0 ? `${c.runCount} 次` : "—";
  }

  private cardReductionDisplay(c: ChannelState): string {
    if (c.status === "running" || c.status === "settling") {
      return "计算中";
    }
    if (typeof c.reductionPct === "number" && Number.isFinite(c.reductionPct)) {
      const clamped = Math.max(0, Math.min(100, c.reductionPct));
      const rounded = Math.round(clamped * 10) / 10;
      return Number.isInteger(rounded) ? `${rounded}%` : `${rounded.toFixed(1)}%`;
    }
    return "—";
  }

  private relativeTime(c: ChannelState, now: number): string {
    const deltaMs = Math.max(0, now - c.lastEventAtMs);
    const mins = Math.floor(deltaMs / 60000);
    if (mins <= 0) {
      return "刚刚";
    }
    if (mins < 60) {
      return `${mins} 分钟前`;
    }
    const hours = Math.floor(mins / 60);
    return `${hours} 小时前`;
  }

  // dashboardView builds the multi-task Dashboard payload (spec §三). Tasks
  // are ordered most-recent-first. The top status reflects the number of
  // ACTIVE tasks, never letting a finished card flip it back to idle while
  // another task is still running.
  dashboardView(now = Date.now()): DashboardView {
    const active = this.activeChannels();
    let topStatus: string;
    if (active.length === 0) {
      topStatus = "等待下一轮发功";
    } else if (active.length === 1) {
      topStatus = "正在吸T";
    } else {
      topStatus = `${active.length} 个任务正在吸T`;
    }
    const tasks = this.all()
      .sort((a, b) => b.lastEventAtMs - a.lastEventAtMs)
      .map<DashboardTaskCard>((c) => ({
        channelId: c.channelId,
        adapter: c.adapter,
        surface: c.surface,
        adapterLabel: adapterLabel(c.adapter),
        status: c.status,
        statusLabel: this.statusLabel(c.status),
        savedDisplay: this.cardSavedDisplay(c),
        runCountDisplay: this.cardRunCountDisplay(c),
        reductionDisplay: this.cardReductionDisplay(c),
        lastEventAtIso: c.lastEventIso,
        relativeTime: this.relativeTime(c, now),
      }));
    return { topStatus, activeCount: active.length, tasks };
  }

  private prune(now: number): void {
    // Drop finished/idle channels past their Dashboard TTL.
    for (const [id, c] of [...this.channels.entries()]) {
      const inactive = c.status === "finished" || c.status === "error" || c.status === "idle";
      if (inactive && now - c.lastEventAtMs > DASHBOARD_FINISHED_TTL_MS) {
        this.channels.delete(id);
      }
    }
    // Enforce the cap on finished channels (oldest finished dropped first);
    // active channels are never pruned.
    const finished = this.all()
      .filter((c) => c.status === "finished" || c.status === "error" || c.status === "idle")
      .sort((a, b) => a.lastEventAtMs - b.lastEventAtMs);
    let overflow = finished.length - MAX_FINISHED_CHANNELS;
    for (let i = 0; i < finished.length && overflow > 0; i++, overflow--) {
      this.channels.delete(finished[i].channelId);
    }
  }

  // resetForTest clears all state (used by unit tests).
  resetForTest(): void {
    this.channels.clear();
    this.seenEventKeys = [];
    this.seenEventKeySet.clear();
  }
}
