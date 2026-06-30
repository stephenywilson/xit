import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import {
  fetchNpmDownloads,
  upsertSnapshot,
  runScheduled,
  readExternal,
  NPM_PACKAGE,
  VSCODE_EXTENSION,
} from "../src/external.js";

// fakeDb captures upsert binds and serves canned SELECT rows.
function fakeDb(selectRows = []) {
  const writes = [];
  return {
    writes,
    prepare(sql) {
      let binds = [];
      const stmt = {
        bind(...args) {
          binds = args;
          return stmt;
        },
        async run() {
          writes.push({ sql, binds });
          return { success: true };
        },
        async all() {
          return { results: selectRows };
        },
      };
      return stmt;
    },
  };
}

// fetch stub: maps URL substrings to {ok, json}.
function fakeFetch(map) {
  return async (urlStr) => {
    for (const key of Object.keys(map)) {
      if (urlStr.includes(key)) {
        const v = map[key];
        if (v === "throw") throw new Error("network down");
        return { ok: v.ok !== false, async json() { return v.body; } };
      }
    }
    return { ok: false, async json() { return {}; } };
  };
}

test("schema.sql defines external_stats_snapshots correctly", () => {
  const schema = readFileSync(
    fileURLToPath(new URL("../schema.sql", import.meta.url)),
    "utf8",
  );
  assert.match(schema, /CREATE TABLE IF NOT EXISTS external_stats_snapshots/);
  for (const col of ["day", "source", "metric", "value", "raw_json", "created_at"]) {
    assert.ok(schema.includes(col), `schema must declare column ${col}`);
  }
  assert.match(schema, /PRIMARY KEY \(day, source, metric\)/);
});

test("fetchNpmDownloads returns last_day/week/month and skips failed points", async () => {
  const f = fakeFetch({
    "last-day": { body: { downloads: 5 } },
    "last-week": { ok: false },
    "last-month": { body: { downloads: 120 } },
  });
  const out = await fetchNpmDownloads(NPM_PACKAGE, f);
  assert.equal(out.last_day, 5);
  assert.equal("last_week" in out, false); // failed point omitted (fail-open)
  assert.equal(out.last_month, 120);
});

test("fetchNpmDownloads is fail-open when fetch throws", async () => {
  const out = await fetchNpmDownloads(NPM_PACKAGE, fakeFetch({ "last-day": "throw" }));
  assert.deepEqual(out, {}); // no points, no crash
});

test("upsertSnapshot binds a coerced integer value", async () => {
  const db = fakeDb();
  await upsertSnapshot(db, {
    day: "2026-07-01", source: "npm", metric: "downloads_last_day",
    value: "42.9", raw_json: "{}", created_at: "2026-07-01T00:10:00Z",
  });
  assert.equal(db.writes.length, 1);
  assert.match(db.writes[0].sql, /INSERT INTO external_stats_snapshots/);
  assert.match(db.writes[0].sql, /ON CONFLICT\(day, source, metric\) DO UPDATE/);
  assert.equal(db.writes[0].binds[3], 42); // truncated integer
});

test("runScheduled inserts npm download snapshots", async () => {
  const db = fakeDb();
  const f = fakeFetch({
    "last-day": { body: { downloads: 3 } },
    "last-week": { body: { downloads: 21 } },
    "last-month": { body: { downloads: 90 } },
  });
  const res = await runScheduled({ METRICS_DB: db }, new Date("2026-07-01T00:10:00Z"), f);
  assert.equal(res.stored, 3);
  const metrics = db.writes.map((w) => w.binds[2]);
  assert.ok(metrics.includes("downloads_last_day"));
  assert.ok(metrics.includes("downloads_last_month"));
  // every write is for day 2026-07-01, source npm
  for (const w of db.writes) {
    assert.equal(w.binds[0], "2026-07-01");
    assert.equal(w.binds[1], "npm");
  }
});

test("runScheduled also records configured VS Code installs when set", async () => {
  const db = fakeDb();
  const f = fakeFetch({}); // npm returns nothing
  const res = await runScheduled(
    { METRICS_DB: db, VSCODE_INSTALLS: "1234" },
    new Date("2026-07-01T00:10:00Z"),
    f,
  );
  const vscodeWrite = db.writes.find((w) => w.binds[1] === "vscode_marketplace");
  assert.ok(vscodeWrite, "should record a vscode_marketplace snapshot");
  assert.equal(vscodeWrite.binds[2], "installs");
  assert.equal(vscodeWrite.binds[3], 1234);
  assert.ok(res.stored >= 1);
});

test("runScheduled is fail-open: npm failure does not throw and VS Code stays N/A", async () => {
  const db = fakeDb();
  const f = fakeFetch({ "last-day": "throw", "last-week": "throw", "last-month": "throw" });
  const res = await runScheduled({ METRICS_DB: db }, new Date(), f);
  assert.equal(res.stored, 0); // nothing stored, but no exception
});

test("runScheduled with no DB binding returns a note instead of throwing", async () => {
  const res = await runScheduled({}, new Date());
  assert.match(res.note, /METRICS_DB/);
});

test("readExternal shapes latest npm + vscode snapshots (aggregate only)", async () => {
  const db = fakeDb([
    { source: "npm", metric: "downloads_last_day", value: 5, day: "2026-07-01" },
    { source: "npm", metric: "downloads_last_month", value: 90, day: "2026-07-01" },
    { source: "vscode_marketplace", metric: "installs", value: 1234, day: "2026-07-01" },
  ]);
  const ext = await readExternal(db, {});
  assert.equal(ext.npm_downloads.last_day, 5);
  assert.equal(ext.npm_downloads.last_month, 90);
  assert.equal(ext.npm_downloads.as_of, "2026-07-01");
  assert.equal(ext.vscode_installs.value, 1234);
  // no raw identifier / id field anywhere
  assert.ok(!JSON.stringify(ext).includes("install_id"));
});

test("readExternal falls back to N/A vscode and is fail-open with no db", async () => {
  const ext = await readExternal(null, {});
  assert.equal(ext.vscode_installs.value, null);
  assert.deepEqual(ext.npm_downloads, {});
});

test("VS Code extension id is the published identifier", () => {
  assert.equal(VSCODE_EXTENSION, "XiT.xit-vscode");
});
