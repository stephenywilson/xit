import { test } from "node:test";
import assert from "node:assert/strict";
import {
  computeDashboard,
  computeCoverage,
  resolvePublicStart,
  laterCutoff,
  cutoffISO,
} from "../src/dashboard.js";
import { computeStats } from "../src/stats.js";
import worker from "../src/index.js";

// memDb is an in-memory D1 fake that ACTUALLY honors the `ts >= ?` bind, so a
// cutover test proves real exclusion (not just that a WHERE was emitted). It
// implements only the aggregate shapes the dashboard/stats/coverage queries use.
function memDb(events) {
  const seen = [];
  const filtered = (binds) =>
    binds && binds.length ? events.filter((e) => e.ts >= binds[0]) : events.slice();
  return {
    seen,
    prepare(sql) {
      let binds = [];
      const stmt = {
        bind(...a) { binds = a; return stmt; },
        async all() {
          seen.push({ sql, binds });
          const ev = filtered(binds);
          const sum = (f) => ev.reduce((a, e) => a + (f(e) || 0), 0);
          const succ = ev.filter((e) => e.status === "success").length;
          const err = ev.filter((e) => e.status === "error").length;

          if (sql.includes("active_installs")) {
            return { results: [{
              total_runs: ev.length,
              total_saved_tokens: sum((e) => e.tok),
              total_saved_bytes: sum((e) => e.bytes),
              avg_compression_ratio: 0,
              success_runs: succ, error_runs: err,
              active_installs: new Set(ev.map((e) => e.install)).size,
            }] };
          }
          if (sql.includes("MIN(ts)")) {
            const all = events.slice().sort((a, b) => (a.ts < b.ts ? -1 : 1));
            const days = new Set(events.map((e) => e.ts.slice(0, 10)));
            return { results: [{
              first_event_at: all.length ? all[0].ts : null,
              last_event_at: all.length ? all[all.length - 1].ts : null,
              event_days: days.size,
              total_events: events.length,
            }] };
          }
          if (sql.includes("public_events")) {
            return { results: [{ public_events: ev.length }] };
          }
          if (sql.includes("total_runs")) { // stats totals (no active_installs)
            return { results: [{
              total_runs: ev.length,
              total_saved_tokens: sum((e) => e.tok),
              total_saved_bytes: sum((e) => e.bytes),
              success_runs: succ, error_runs: err,
            }] };
          }
          if (sql.includes("GROUP BY day, adapter")) {
            const m = {};
            for (const e of ev) {
              const k = e.ts.slice(0, 10) + "|" + e.adapter;
              (m[k] = m[k] || { day: e.ts.slice(0, 10), adapter: e.adapter, runs: 0, saved_tokens: 0 });
              m[k].runs++; m[k].saved_tokens += e.tok || 0;
            }
            return { results: Object.values(m) };
          }
          if (sql.includes("GROUP BY adapter")) {
            const m = {};
            for (const e of ev) {
              (m[e.adapter] = m[e.adapter] || { adapter: e.adapter, runs: 0, saved_tokens: 0, saved_bytes: 0, success_runs: 0 });
              const r = m[e.adapter];
              r.runs++; r.saved_tokens += e.tok || 0; r.saved_bytes += e.bytes || 0;
              if (e.status === "success") r.success_runs++;
            }
            return { results: Object.values(m).sort((a, b) => b.runs - a.runs) };
          }
          if (sql.includes("GROUP BY cli_version")) {
            const m = {};
            for (const e of ev) { (m[e.cli] = m[e.cli] || { cli_version: e.cli, runs: 0 }); m[e.cli].runs++; }
            return { results: Object.values(m) };
          }
          if (sql.includes("vscode_extension_version")) {
            return { results: [] };
          }
          if (sql.includes("GROUP BY day")) {
            const m = {};
            for (const e of ev) {
              const d = e.ts.slice(0, 10);
              (m[d] = m[d] || { day: d, runs: 0, saved_tokens: 0, saved_bytes: 0 });
              m[d].runs++; m[d].saved_tokens += e.tok || 0; m[d].saved_bytes += e.bytes || 0;
            }
            return { results: Object.values(m).sort((a, b) => (a.day < b.day ? -1 : 1)) };
          }
          return { results: [] };
        },
      };
      return stmt;
    },
  };
}

// A dataset spanning a pre-cutover "test" day and post-cutover "real" days.
function dataset() {
  return [
    // --- pre-cutover internal test data (2026-06-30, before 17:10) ---
    { ts: "2026-06-30T09:00:00.000Z", adapter: "claude", cli: "0.2.49", status: "success", tok: 1000, bytes: 4000, install: "test-A" },
    { ts: "2026-06-30T09:05:00.000Z", adapter: "codex",  cli: "0.2.49", status: "error",   tok: 500,  bytes: 2000, install: "test-B" },
    { ts: "2026-06-30T10:00:00.000Z", adapter: "unknown",cli: "0.2.49", status: "success", tok: 700,  bytes: 2800, install: "test-C" },
    // --- post-cutover real data ---
    { ts: "2026-06-30T18:00:00.000Z", adapter: "claude", cli: "0.2.50", status: "success", tok: 2000, bytes: 8000, install: "real-1" },
    { ts: "2026-07-01T08:00:00.000Z", adapter: "codex",  cli: "0.2.50", status: "success", tok: 3000, bytes: 12000, install: "real-2" },
    { ts: "2026-07-01T09:00:00.000Z", adapter: "claude", cli: "0.2.50", status: "success", tok: 1500, bytes: 6000, install: "real-1" },
  ];
}

const CUTOVER = "2026-06-30T17:10:00.000Z";
const NOW = new Date("2026-07-01T12:00:00.000Z");

test("resolvePublicStart: off / on / error modes", () => {
  assert.deepEqual(resolvePublicStart(undefined), { mode: "off", value: null });
  assert.deepEqual(resolvePublicStart(""), { mode: "off", value: null });
  assert.deepEqual(resolvePublicStart("   "), { mode: "off", value: null });
  const on = resolvePublicStart(CUTOVER);
  assert.equal(on.mode, "on");
  assert.equal(on.value, CUTOVER);
  assert.equal(resolvePublicStart("not-a-date").mode, "error");
  assert.equal(resolvePublicStart("2026-13-99").mode, "error");
});

test("laterCutoff picks the more-recent lower bound", () => {
  assert.equal(laterCutoff(null, null), null);
  assert.equal(laterCutoff("2026-01-01T00:00:00Z", null), "2026-01-01T00:00:00Z");
  assert.equal(laterCutoff(null, CUTOVER), CUTOVER);
  assert.equal(laterCutoff("2026-01-01T00:00:00Z", CUTOVER), CUTOVER);
  assert.equal(laterCutoff("2026-12-01T00:00:00Z", CUTOVER), "2026-12-01T00:00:00Z");
});

test("dashboard summary excludes pre-cutover events", async () => {
  const db = memDb(dataset());
  const withCut = await computeDashboard(db, "all", null, CUTOVER, NOW);
  assert.equal(withCut.public_start_at, CUTOVER);
  assert.equal(withCut.summary.total_runs, 3);            // only post-cutover
  assert.equal(withCut.summary.total_saved_tokens, 6500); // 2000+3000+1500

  const noCut = await computeDashboard(db, "all", null, null, NOW);
  assert.equal(noCut.summary.total_runs, 6);              // all events (legacy)
});

test("by_adapter excludes pre-cutover events", async () => {
  const d = await computeDashboard(memDb(dataset()), "all", null, CUTOVER, NOW);
  const byName = Object.fromEntries(d.by_adapter.map((r) => [r.adapter, r.runs]));
  assert.equal(byName.claude, 2); // real-1 x2
  assert.equal(byName.codex, 1);  // real-2
  assert.equal("unknown" in byName, false); // pre-cutover only → gone
});

test("daily_trend excludes the pre-cutover portion of the cutover day", async () => {
  const d = await computeDashboard(memDb(dataset()), "all", null, CUTOVER, NOW);
  const byDay = Object.fromEntries(d.daily_trend.map((r) => [r.day, r.runs]));
  assert.equal(byDay["2026-06-30"], 1); // only the 18:00 real event, not the 3 test ones
  assert.equal(byDay["2026-07-01"], 2);
});

test("active_installs counts only post-cutover distinct installs", async () => {
  const d = await computeDashboard(memDb(dataset()), "all", null, CUTOVER, NOW);
  // real-1 (x2) + real-2 → 2 distinct; the 3 test-* installs are excluded.
  assert.equal(d.summary.active_installs, 2);
  const noCut = await computeDashboard(memDb(dataset()), "all", null, null, NOW);
  assert.equal(noCut.summary.active_installs, 5); // test-A/B/C + real-1 + real-2
});

test("effective cutoff is the LATER of range window and cutover", async () => {
  // The 1d window from NOW starts 2026-06-30T12:00Z (earlier than the cutover),
  // so the cutover must win and still exclude the morning test data.
  assert.equal(laterCutoff(cutoffISO("1d", NOW), CUTOVER), CUTOVER);
  const d = await computeDashboard(memDb(dataset()), "1d", null, CUTOVER, NOW);
  assert.equal(d.summary.total_runs, 3); // cutover dominates the range window
});

test("computeStats (/v1/stats) also excludes pre-cutover events", async () => {
  const withCut = await computeStats(memDb(dataset()), CUTOVER);
  assert.equal(withCut.total_runs, 3);
  assert.equal(withCut.public_start_at, CUTOVER);
  const legacy = await computeStats(memDb(dataset()), null);
  assert.equal(legacy.total_runs, 6); // backward compatible when unset
});

test("external npm downloads are NOT affected by the cutover", async () => {
  const external = { npm_downloads: { last_day: 178, last_month: 1364 }, vscode_installs: { value: null } };
  const withCut = await computeDashboard(memDb(dataset()), "all", external, CUTOVER, NOW);
  assert.deepEqual(withCut.external, external); // passed through unchanged
});

test("computeCoverage reports total vs public event counts (aggregate only)", async () => {
  const cov = await computeCoverage(memDb(dataset()), CUTOVER, NOW);
  assert.equal(cov.total_events, 6);
  assert.equal(cov.public_events, 3);
  assert.equal(cov.first_event_at, "2026-06-30T09:00:00.000Z");
  assert.equal(cov.last_event_at, "2026-07-01T09:00:00.000Z");
  assert.equal(cov.event_days, 2);
  assert.equal(cov.public_start_at, CUTOVER);
  // never leaks an install id
  assert.ok(!JSON.stringify(cov).includes("install"));
});

test("computeCoverage with no cutover: public_events == total_events", async () => {
  const cov = await computeCoverage(memDb(dataset()), null, NOW);
  assert.equal(cov.total_events, 6);
  assert.equal(cov.public_events, 6);
  assert.equal(cov.public_start_at, null);
});

// --- handler-level integration (fail-closed + coverage route) ---

function reqFor(path) {
  return new Request("https://x" + path, { method: "GET" });
}

test("invalid METRICS_PUBLIC_START_AT fails CLOSED with 500 (no data served)", async () => {
  const env = { METRICS_DB: memDb(dataset()), METRICS_PUBLIC_START_AT: "garbage" };
  for (const path of ["/api/dashboard?range=30d", "/api/dashboard/coverage", "/v1/stats"]) {
    const res = await worker.fetch(reqFor(path), env);
    assert.equal(res.status, 500, `${path} must fail closed`);
    const body = await res.json();
    assert.match(body.error, /METRICS_PUBLIC_START_AT/);
  }
});

test("valid cutover: /api/dashboard applies it end-to-end", async () => {
  const env = { METRICS_DB: memDb(dataset()), METRICS_PUBLIC_START_AT: CUTOVER };
  const res = await worker.fetch(reqFor("/api/dashboard?range=all"), env);
  assert.equal(res.status, 200);
  const body = await res.json();
  assert.equal(body.public_start_at, CUTOVER);
  assert.equal(body.summary.total_runs, 3);
});

test("/api/dashboard/coverage route returns aggregate diagnostics", async () => {
  const env = { METRICS_DB: memDb(dataset()), METRICS_PUBLIC_START_AT: CUTOVER };
  const res = await worker.fetch(reqFor("/api/dashboard/coverage"), env);
  assert.equal(res.status, 200);
  const body = await res.json();
  assert.equal(body.total_events, 6);
  assert.equal(body.public_events, 3);
  assert.ok(!JSON.stringify(body).includes("anonymous_install_id"));
});

test("no cutover configured: legacy behavior (all events public)", async () => {
  const env = { METRICS_DB: memDb(dataset()) }; // METRICS_PUBLIC_START_AT unset
  const res = await worker.fetch(reqFor("/api/dashboard?range=all"), env);
  const body = await res.json();
  assert.equal(body.public_start_at, null);
  assert.equal(body.summary.total_runs, 6);
});
