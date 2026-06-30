import { test } from "node:test";
import assert from "node:assert/strict";
import {
  computeStats,
  STATS_QUERIES,
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

test("empty DB yields zeroed aggregate with no division-by-zero", async () => {
  const stats = await computeStats(fakeDb({}));
  assert.equal(stats.total_runs, 0);
  assert.equal(stats.success_rate, 0);
  assert.equal(stats.error_rate, 0);
  assert.deepEqual(stats.by_adapter, []);
});
