import { test } from "node:test";
import assert from "node:assert/strict";
import {
  computeDashboard,
  buildQueries,
  isValidRange,
  cutoffISO,
  RANGES,
} from "../src/dashboard.js";
import { DASHBOARD_HTML } from "../src/page.js";
import { FORBIDDEN_SELECT_COLUMNS } from "../src/stats.js";

// fakeDb records SQL and returns canned rows keyed by a substring match.
function fakeDb(rowsBySql) {
  const seen = [];
  return {
    seen,
    prepare(sql) {
      seen.push(sql);
      let binds = [];
      const stmt = {
        bind(...args) {
          binds = args;
          return stmt;
        },
        async all() {
          const key = Object.keys(rowsBySql).find((k) => sql.includes(k));
          return { results: key ? rowsBySql[key] : [] };
        },
      };
      return stmt;
    },
  };
}

// Tokens that would indicate a real data leak in the rendered markup. These are
// the structured identifier / raw-payload column names — NOT the natural-
// language privacy notice, which legitimately mentions "prompts", "tokens",
// "commands", "API keys" while describing what XiT does NOT collect.
const FORBIDDEN_TOKENS = [
  ...FORBIDDEN_SELECT_COLUMNS,
  "raw_output", "raw_log", "ai_reply", "api_key", "install_id",
];

test("every range is accepted; bad ranges are rejected", () => {
  for (const r of ["1d", "7d", "30d", "180d", "365d", "all"]) {
    assert.ok(isValidRange(r), `${r} should be valid`);
  }
  for (const r of ["", "2d", "1h", "month", "all ", "DROP", "10d"]) {
    assert.equal(isValidRange(r), false, `${r} should be invalid`);
  }
});

test("cutoffISO yields a trailing window for ranges and null for all", () => {
  const now = new Date("2026-07-01T00:00:00Z");
  assert.equal(cutoffISO("all", now), null);
  assert.equal(cutoffISO("1d", now), new Date("2026-06-30T00:00:00Z").toISOString());
  assert.equal(cutoffISO("30d", now), new Date("2026-06-01T00:00:00Z").toISOString());
});

test("computeDashboard returns summary, by_adapter, daily_trend and versions", async () => {
  const db = fakeDb({
    "active_installs": [{
      total_runs: 100, total_saved_tokens: 50000, total_saved_bytes: 200000,
      avg_compression_ratio: 0.9, success_runs: 95, error_runs: 5, active_installs: 17,
    }],
    "GROUP BY adapter\n        ORDER": [
      { adapter: "claude", runs: 60, saved_tokens: 30000, saved_bytes: 120000, success_runs: 58 },
      { adapter: "codex", runs: 40, saved_tokens: 20000, saved_bytes: 80000, success_runs: 37 },
    ],
    "GROUP BY cli_version": [{ cli_version: "0.2.50", runs: 100 }],
    "GROUP BY vscode_extension_version": [{ vscode_version: "0.0.35", runs: 80 }],
    "GROUP BY day\n        ORDER": [{ day: "2026-06-30", runs: 100, saved_tokens: 50000, saved_bytes: 200000 }],
    "GROUP BY day, adapter": [{ day: "2026-06-30", adapter: "claude", runs: 60, saved_tokens: 30000 }],
  });
  const d = await computeDashboard(db, "30d", { npm_downloads: {}, vscode_installs: { value: null } });
  assert.equal(d.range, "30d");
  assert.equal(d.summary.total_runs, 100);
  assert.equal(d.summary.total_saved_tokens, 50000);
  assert.equal(d.summary.active_installs, 17);
  assert.equal(d.summary.success_rate, 0.95);
  assert.equal(d.by_adapter.length, 2);
  assert.equal(d.by_adapter[0].adapter, "claude");
  assert.equal(d.by_cli_version[0].cli_version, "0.2.50");
  assert.equal(d.by_vscode_version[0].vscode_version, "0.0.35");
  assert.equal(d.daily_trend[0].day, "2026-06-30");
  assert.equal(d.adapter_daily_trend[0].adapter, "claude");
  assert.ok(d.generated_at);
});

test("empty DB yields a valid zeroed shape (no division by zero)", async () => {
  const d = await computeDashboard(fakeDb({}), "7d", null);
  assert.equal(d.summary.total_runs, 0);
  assert.equal(d.summary.success_rate, 0);
  assert.deepEqual(d.by_adapter, []);
  assert.deepEqual(d.daily_trend, []);
});

test("no db returns an empty-but-valid dashboard with supplied external", async () => {
  const ext = { npm_downloads: { last_day: 12 }, vscode_installs: { value: null } };
  const d = await computeDashboard(null, "all", ext);
  assert.equal(d.summary.total_runs, 0);
  assert.deepEqual(d.external, ext);
});

test("no dashboard query selects a per-user / per-channel / per-run identifier", () => {
  for (const range of Object.keys(RANGES)) {
    const q = buildQueries(cutoffISO(range, new Date()));
    for (const { sql } of Object.values(q)) {
      // Every query must be a real aggregate.
      assert.ok(/COUNT\(|SUM\(|AVG\(/.test(sql), `must aggregate: ${sql}`);
      for (const col of FORBIDDEN_SELECT_COLUMNS) {
        if (col === "anonymous_install_id") {
          // Only allowed as COUNT(DISTINCT anonymous_install_id), never bare.
          const bare = new RegExp(`(?<!COUNT\\(DISTINCT\\s)anonymous_install_id`);
          // Confirm any occurrence is inside COUNT(DISTINCT ...).
          const occurrences = (sql.match(/anonymous_install_id/g) || []).length;
          const distinctOcc = (sql.match(/COUNT\(DISTINCT anonymous_install_id\)/g) || []).length;
          assert.equal(occurrences, distinctOcc, `anonymous_install_id only via COUNT(DISTINCT): ${sql}`);
          void bare;
        } else {
          assert.ok(!sql.includes(col), `must not select ${col}: ${sql}`);
        }
      }
    }
  }
});

test("serialized dashboard JSON never contains a raw install-id value", async () => {
  // Even if a row leaked an id field, it must not survive into the payload.
  const db = fakeDb({
    "active_installs": [{ total_runs: 1, active_installs: 1, anonymous_install_id: "LEAK-ID-123" }],
  });
  const d = await computeDashboard(db, "30d", null);
  const blob = JSON.stringify(d);
  assert.ok(!blob.includes("LEAK-ID-123"), "install id must not appear in payload");
  // active_installs is a count, not an id list.
  assert.equal(typeof d.summary.active_installs, "number");
});

test("dashboard HTML does not embed any sensitive field token", () => {
  assert.match(DASHBOARD_HTML, /<!doctype html>/i);
  for (const tok of FORBIDDEN_TOKENS) {
    assert.ok(
      !DASHBOARD_HTML.includes(tok),
      `dashboard HTML must not contain sensitive token "${tok}"`,
    );
  }
  // Privacy notice (EN + ZH) must be present.
  assert.ok(DASHBOARD_HTML.includes("aggregate anonymous usage metrics only"));
  assert.ok(DASHBOARD_HTML.includes("匿名聚合数据"));
});
