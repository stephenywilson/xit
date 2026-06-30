// External (public) download / install stats for the XiT dashboard.
//
// These are PUBLIC counters, not user data:
//   - npm downloads for `xitsg` (download counts, NOT user counts)
//   - VS Code Marketplace installs for `XiT.xit-vscode` (cumulative installs,
//     NOT active users)
//
// They live in their own table (external_stats_snapshots) and are refreshed by
// a daily scheduled (cron) handler. Every fetch is fail-open: a network error
// must never break the main API or the scheduled run.

export const NPM_PACKAGE = "xitsg";
export const VSCODE_EXTENSION = "XiT.xit-vscode";

// npm download point endpoints. last-day/week/month are stable public JSON.
const NPM_POINTS = [
  ["last_day", "last-day"],
  ["last_week", "last-week"],
  ["last_month", "last-month"],
];

// fetchNpmDownloads returns { last_day, last_week, last_month } download counts.
// Any individual point that fails is simply omitted (fail-open).
export async function fetchNpmDownloads(pkg = NPM_PACKAGE, fetchImpl = fetch) {
  const out = {};
  for (const [key, point] of NPM_POINTS) {
    try {
      const res = await fetchImpl(`https://api.npmjs.org/downloads/point/${point}/${pkg}`);
      if (!res || !res.ok) {
        continue;
      }
      const j = await res.json();
      if (j && typeof j.downloads === "number") {
        out[key] = j.downloads;
      }
    } catch {
      /* fail-open: skip this point */
    }
  }
  return out;
}

// upsertSnapshot writes one external metric for a given UTC day. Idempotent:
// re-running the cron the same day overwrites the value instead of duplicating.
export async function upsertSnapshot(db, { day, source, metric, value, raw_json, created_at }) {
  await db
    .prepare(
      `INSERT INTO external_stats_snapshots (day, source, metric, value, raw_json, created_at)
       VALUES (?,?,?,?,?,?)
       ON CONFLICT(day, source, metric) DO UPDATE SET
         value = excluded.value,
         raw_json = excluded.raw_json,
         created_at = excluded.created_at`
    )
    .bind(day, source, metric, Math.trunc(Number(value) || 0), raw_json ?? null, created_at)
    .run();
}

// runScheduled is the daily cron body. It refreshes npm downloads and, if an
// operator has manually configured VSCODE_INSTALLS, records that too. It NEVER
// throws — the worker's scheduled() wrapper relies on that.
export async function runScheduled(env, now = new Date(), fetchImpl = fetch) {
  if (!env || !env.METRICS_DB) {
    return { stored: 0, note: "METRICS_DB not bound" };
  }
  const day = now.toISOString().slice(0, 10);
  const created_at = now.toISOString();
  let stored = 0;

  // --- npm downloads (auto) ---
  try {
    const dl = await fetchNpmDownloads(NPM_PACKAGE, fetchImpl);
    for (const [metricKey, value] of Object.entries(dl)) {
      await upsertSnapshot(env.METRICS_DB, {
        day,
        source: "npm",
        metric: `downloads_${metricKey}`,
        value,
        raw_json: JSON.stringify({ package: NPM_PACKAGE, point: metricKey, value }),
        created_at,
      });
      stored++;
    }
  } catch {
    /* fail-open: npm refresh failure must not break the cron */
  }

  // --- VS Code Marketplace installs (manual / configured) ---
  // The Marketplace has no stable public JSON download API, so we do NOT scrape
  // it. If an operator sets VSCODE_INSTALLS (a number) we record it as a
  // snapshot; otherwise the dashboard shows N/A.
  try {
    const manual = env.VSCODE_INSTALLS != null ? Number(env.VSCODE_INSTALLS) : NaN;
    if (Number.isFinite(manual)) {
      await upsertSnapshot(env.METRICS_DB, {
        day,
        source: "vscode_marketplace",
        metric: "installs",
        value: manual,
        raw_json: JSON.stringify({ extension: VSCODE_EXTENSION, source: "configured", value: manual }),
        created_at,
      });
      stored++;
    }
  } catch {
    /* fail-open */
  }

  return { stored };
}

// readExternal shapes the latest external snapshots for the dashboard payload.
// Returns aggregate counters only; fail-open to an empty shape on any error.
export async function readExternal(db, env) {
  const shape = {
    npm_downloads: {},
    vscode_installs: { value: null, note: "not collected" },
  };
  if (!db) {
    return shape;
  }
  try {
    // Latest value per (source, metric).
    const sql = `
      SELECT e.source AS source, e.metric AS metric, e.value AS value, e.day AS day
      FROM external_stats_snapshots e
      WHERE e.day = (
        SELECT MAX(e2.day) FROM external_stats_snapshots e2
        WHERE e2.source = e.source AND e2.metric = e.metric
      )
      ORDER BY e.source, e.metric`;
    const res = await db.prepare(sql).all();
    const list = (res && res.results) || [];
    for (const r of list) {
      if (r.source === "npm") {
        // metric like downloads_last_day → key last_day
        const key = String(r.metric).replace(/^downloads_/, "");
        shape.npm_downloads[key] = Number(r.value);
        shape.npm_downloads.as_of = r.day;
      } else if (r.source === "vscode_marketplace" && r.metric === "installs") {
        shape.vscode_installs = { value: Number(r.value), as_of: r.day, note: "cumulative installs (not active users)" };
      }
    }
  } catch {
    /* fail-open: external is best-effort */
  }
  // Allow a live env override for vscode installs even without a snapshot row.
  try {
    if (shape.vscode_installs.value == null && env && env.VSCODE_INSTALLS != null) {
      const v = Number(env.VSCODE_INSTALLS);
      if (Number.isFinite(v)) {
        shape.vscode_installs = { value: v, note: "configured (not active users)" };
      }
    }
  } catch {
    /* ignore */
  }
  return shape;
}
