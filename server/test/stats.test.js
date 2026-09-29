import { test } from "node:test";
import assert from "node:assert/strict";
import {
  computeStats,
  STATS_QUERIES,
  buildStatsQueries,
  FORBIDDEN_SELECT_COLUMNS,
} from "../src/stats.js";

// fakeDb records every SQL string it is asked to prepare, and returns canned
// rows so computeStats can be exercised without a real D1 binding.
function fakeDb(rowsBySql) {
  const seen = [];
  return {
    seen,
    prepare(sql) {
      seen.push(sql);
      return {
        async all() {
          const key = Object.keys(rowsBySql).find((k) => sql.includes(k));
          return { results: key ? rowsBySql[key] : [] };
        },
      };
    },
  };
}

test("computeStats returns every required dashboard aggregate (spec §四)", async () => {
  const db = fakeDb({
    "total_runs": [{
      total_runs: 10, total_saved_tokens: 1234, total_saved_bytes: 4936,
      success_runs: 8, error_runs: 2,
    }],
    "GROUP BY adapter": [{ adapter: "claude", runs: 6, saved_tokens: 1000 }],
    "GROUP BY cli_version": [{ cli_version: "0.2.49", runs: 10 }],
    "GROUP BY day": [{ day: "2026-06-30", runs: 10, saved_tokens: 1234 }],
  });
  const stats = await computeStats(db);
  assert.equal(stats.total_runs, 10);
  assert.equal(stats.total_saved_tokens, 1234);
  assert.equal(stats.success_runs, 8);
  assert.equal(stats.error_runs, 2);
  assert.equal(stats.success_rate, 0.8);
  assert.equal(stats.error_rate, 0.2);
  assert.deepEqual(stats.by_adapter, [{ adapter: "claude", runs: 6, saved_tokens: 1000 }]);
  assert.deepEqual(stats.by_version, [{ cli_version: "0.2.49", runs: 10 }]);
  assert.equal(stats.daily_trend[0].day, "2026-06-30");
});

test("no stats query exposes per-user / per-channel / per-run detail", () => {
  for (const sql of Object.values(STATS_QUERIES)) {
    for (const col of FORBIDDEN_SELECT_COLUMNS) {
      assert.ok(
        !sql.includes(col),
        `stats SQL must never select ${col} (aggregate-only): ${sql}`,
      );
    }
    // Every query must be an aggregate (roll-up), not a row dump.
    assert.ok(
      /COUNT\(|SUM\(/.test(sql),
      `stats query must aggregate, got: ${sql}`,
    );
  }
});

test("stats queries strictly filter for event = 'run.finished'", () => {
  for (const sql of Object.values(STATS_QUERIES)) {
    assert.match(sql, /event\s*=\s*'run\.finished'/);
  }
  const cutoffQueries = buildStatsQueries("2026-06-30T00:00:00Z");
  for (const sql of Object.values(cutoffQueries)) {
    assert.match(sql, /ts\s*>=\s*\?/);
    assert.match(sql, /event\s*=\s*'run\.finished'/);
  }
});

test("empty DB yields zeroed aggregate with no division-by-zero", async () => {
  const stats = await computeStats(fakeDb({}));
  assert.equal(stats.total_runs, 0);
  assert.equal(stats.success_rate, 0);
  assert.equal(stats.error_rate, 0);
  assert.deepEqual(stats.by_adapter, []);
});

test("D1 insert throws -> endpoint returns 503, never 202", async () => {
  const failingDb = {
    prepare() {
      return {
        bind() {
          return {
            async run() {
              throw new Error("D1 simulated storage failure");
            },
          };
        },
      };
    },
  };

  const payload = {
    schema: "xit.metrics.v1",
    event: "run.finished",
    anonymous_install_id: "test-install-id-1234567890",
    adapter: "codex",
    surface: "cli",
    status: "success",
  };

  const req = new Request("https://xit-api.stephenwilson.dev/v1/metrics", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(payload),
  });

  const worker = (await import("../src/index.js")).default;
  const res = await worker.fetch(req, { METRICS_DB: failingDb });
  assert.equal(res.status, 503);
  const data = await res.json();
  assert.equal(data.error, "metrics storage unavailable");
  assert.notEqual(res.status, 202);
});

test("/v1/version returns 0.2.53 fallback metadata when no env override is present", async () => {
  const req = new Request("https://xit-api.stephenwilson.dev/v1/version", {
    method: "GET",
  });
  const worker = (await import("../src/index.js")).default;
  const res = await worker.fetch(req, {});
  assert.equal(res.status, 200);
  const data = await res.json();
  assert.equal(data.latest_cli, "0.2.53");
  assert.equal(data.min_cli, "0.2.52");
  assert.equal(data.latest_vscode, "0.0.36");
  assert.equal(data.min_vscode, "0.0.36");
});
