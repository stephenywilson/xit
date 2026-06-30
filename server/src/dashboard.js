// Aggregate-only dashboard data for the XiT backend.
//
// This module powers GET /api/dashboard?range=... and the /dashboard HTML page.
// Every query is a GROUP BY / SUM / COUNT / AVG roll-up. The ONLY reference to
// anonymous_install_id is inside COUNT(DISTINCT anonymous_install_id) — an
// aggregate count of distinct installs, never the id values themselves. No
// query selects a per-user / per-channel / per-run / per-turn / per-event id,
// so the response can only ever contain aggregates.
//
// Privacy is enforced two ways and asserted in dashboard.test.js:
//   1. No SELECT exposes a raw identifier column (anonymous_install_id appears
//      only as COUNT(DISTINCT ...)).
//   2. The serialized JSON response never contains a stored install-id value.

// Supported time ranges → trailing-window length in days. null = no time filter.
export const RANGES = {
  "1d": 1,
  "7d": 7,
  "30d": 30,
  "180d": 180,
  "365d": 365,
  "all": null,
};

export function isValidRange(range) {
  return Object.prototype.hasOwnProperty.call(RANGES, range);
}

// cutoffISO returns the RFC3339 lower bound for a range, or null for "all".
export function cutoffISO(range, now = new Date()) {
  const days = RANGES[range];
  if (days == null) {
    return null;
  }
  return new Date(now.getTime() - days * 86400000).toISOString();
}

// buildQueries returns the {sql, binds} for every dashboard roll-up, scoped to
// an optional ts cutoff. Exported so tests can scan the SQL for safety.
export function buildQueries(cutoff) {
  const w = cutoff ? "WHERE ts >= ?" : "";
  const b = cutoff ? [cutoff] : [];
  // vscode version query needs an extra predicate; compose the WHERE safely.
  const vscodeWhere = cutoff
    ? "WHERE ts >= ? AND vscode_extension_version <> ''"
    : "WHERE vscode_extension_version <> ''";
  return {
    totals: {
      sql: `
        SELECT
          COUNT(*)                                  AS total_runs,
          COALESCE(SUM(estimated_saved_tokens), 0)  AS total_saved_tokens,
          COALESCE(SUM(saved_bytes), 0)             AS total_saved_bytes,
          COALESCE(AVG(compression_ratio), 0)       AS avg_compression_ratio,
          SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END) AS success_runs,
          SUM(CASE WHEN status = 'error'   THEN 1 ELSE 0 END) AS error_runs,
          COUNT(DISTINCT anonymous_install_id)      AS active_installs
        FROM metrics_events ${w}`,
      binds: b,
    },
    byAdapter: {
      sql: `
        SELECT adapter,
               COUNT(*)                                 AS runs,
               COALESCE(SUM(estimated_saved_tokens), 0) AS saved_tokens,
               COALESCE(SUM(saved_bytes), 0)            AS saved_bytes,
               SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END) AS success_runs
        FROM metrics_events ${w}
        GROUP BY adapter
        ORDER BY runs DESC`,
      binds: b,
    },
    byCliVersion: {
      sql: `
        SELECT cli_version, COUNT(*) AS runs
        FROM metrics_events ${w}
        GROUP BY cli_version
        ORDER BY runs DESC`,
      binds: b,
    },
    byVscodeVersion: {
      sql: `
        SELECT vscode_extension_version AS vscode_version, COUNT(*) AS runs
        FROM metrics_events ${vscodeWhere}
        GROUP BY vscode_extension_version
        ORDER BY runs DESC`,
      binds: b,
    },
    dailyTrend: {
      sql: `
        SELECT substr(ts, 1, 10) AS day,
               COUNT(*)                                 AS runs,
               COALESCE(SUM(estimated_saved_tokens), 0) AS saved_tokens,
               COALESCE(SUM(saved_bytes), 0)            AS saved_bytes
        FROM metrics_events ${w}
        GROUP BY day
        ORDER BY day ASC
        LIMIT 400`,
      binds: b,
    },
    adapterDailyTrend: {
      sql: `
        SELECT substr(ts, 1, 10) AS day, adapter,
               COUNT(*)                                 AS runs,
               COALESCE(SUM(estimated_saved_tokens), 0) AS saved_tokens
        FROM metrics_events ${w}
        GROUP BY day, adapter
        ORDER BY day ASC
        LIMIT 4000`,
      binds: b,
    },
  };
}

async function rows(db, q) {
  let stmt = db.prepare(q.sql);
  if (q.binds && q.binds.length) {
    stmt = stmt.bind(...q.binds);
  }
  const res = await stmt.all();
  return (res && res.results) || [];
}

function emptyDashboard(range, generated_at) {
  return {
    range,
    summary: {
      total_runs: 0,
      total_saved_tokens: 0,
      total_saved_bytes: 0,
      avg_compression_ratio: 0,
      success_runs: 0,
      error_runs: 0,
      success_rate: 0,
      active_installs: 0,
    },
    by_adapter: [],
    by_cli_version: [],
    by_vscode_version: [],
    daily_trend: [],
    adapter_daily_trend: [],
    external: { npm_downloads: {}, vscode_installs: { value: null, note: "not collected" } },
    generated_at,
  };
}

// computeDashboard runs every aggregate roll-up for a range and shapes the
// dashboard payload. `external` is supplied by the caller (external.js) so this
// stays a pure metrics roll-up. With no db, returns a valid empty shape.
export async function computeDashboard(db, range, external, now = new Date()) {
  const generated_at = now.toISOString();
  if (!db) {
    const empty = emptyDashboard(range, generated_at);
    if (external) empty.external = external;
    return empty;
  }
  const cutoff = cutoffISO(range, now);
  const q = buildQueries(cutoff);

  const totalsRows = await rows(db, q.totals);
  const t = totalsRows[0] || {};
  const totalRuns = Number(t.total_runs || 0);
  const successRuns = Number(t.success_runs || 0);
  const errorRuns = Number(t.error_runs || 0);

  return {
    range,
    summary: {
      total_runs: totalRuns,
      total_saved_tokens: Number(t.total_saved_tokens || 0),
      total_saved_bytes: Number(t.total_saved_bytes || 0),
      avg_compression_ratio: Number(t.avg_compression_ratio || 0),
      success_runs: successRuns,
      error_runs: errorRuns,
      success_rate: totalRuns > 0 ? successRuns / totalRuns : 0,
      active_installs: Number(t.active_installs || 0),
    },
    by_adapter: await rows(db, q.byAdapter),
    by_cli_version: await rows(db, q.byCliVersion),
    by_vscode_version: await rows(db, q.byVscodeVersion),
    daily_trend: await rows(db, q.dailyTrend),
    adapter_daily_trend: await rows(db, q.adapterDailyTrend),
    external: external || { npm_downloads: {}, vscode_installs: { value: null, note: "not collected" } },
    generated_at,
  };
}
