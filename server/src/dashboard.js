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

// resolvePublicStart interprets the METRICS_PUBLIC_START_AT config.
//
// The public dashboard / stats must exclude pre-cutover events (internal
// E2E / smoke / local-verify data). We normalize the operator-supplied value:
//   - unset/empty            -> { mode: "off" }   (legacy behavior, no filter)
//   - a valid timestamp      -> { mode: "on",  value: <ISO> }
//   - present but unparseable -> { mode: "error" }
//
// A misconfigured cutover is treated as an ERROR (fail-CLOSED), not fail-open.
// Rationale: the whole point of the cutover is to keep internal test data OUT
// of public metrics; silently ignoring a bad value would leak exactly the data
// we are trying to hide. Failing closed makes the misconfiguration loud and
// keeps private data private until it is fixed (verifiable via /api/dashboard/
// coverage).
export function resolvePublicStart(raw) {
  if (raw == null || (typeof raw === "string" && raw.trim() === "")) {
    return { mode: "off", value: null };
  }
  const s = String(raw).trim();
  const t = Date.parse(s);
  if (Number.isNaN(t)) {
    return { mode: "error", value: null };
  }
  return { mode: "on", value: new Date(t).toISOString() };
}

// laterCutoff returns the more-recent of two ISO lower bounds (or null if both
// are null). Both a range window and the public-start cutover are lower bounds
// on ts, so the effective cutoff is simply the later of the two.
export function laterCutoff(a, b) {
  if (a == null) return b;
  if (b == null) return a;
  return Date.parse(a) >= Date.parse(b) ? a : b;
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
    // bySurface / surfaceDailyTrend break down by the `surface` column —
    // including the finer-grained Codex front-end values added in 0.2.51
    // (codex_cli / codex_ide / chatgpt_desktop_codex / codex_shared) alongside
    // the original generic surfaces (cli / hook / vscode / bridge). Same
    // aggregate-only shape as byAdapter/adapterDailyTrend above; no per-event
    // identifier is ever selected.
    bySurface: {
      sql: `
        SELECT surface,
               COUNT(*)                                 AS runs,
               COALESCE(SUM(estimated_saved_tokens), 0) AS saved_tokens,
               COALESCE(SUM(saved_bytes), 0)            AS saved_bytes,
               SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END) AS success_runs
        FROM metrics_events ${w}
        GROUP BY surface
        ORDER BY runs DESC`,
      binds: b,
    },
    surfaceDailyTrend: {
      sql: `
        SELECT substr(ts, 1, 10) AS day, surface,
               COUNT(*)                                 AS runs,
               COALESCE(SUM(estimated_saved_tokens), 0) AS saved_tokens
        FROM metrics_events ${w}
        GROUP BY day, surface
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

function emptyDashboard(range, generated_at, public_start_at = null) {
  return {
    range,
    public_start_at,
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
    by_surface: [],
    daily_trend: [],
    adapter_daily_trend: [],
    surface_daily_trend: [],
    external: { npm_downloads: {}, vscode_installs: { value: null, note: "not collected" } },
    generated_at,
  };
}

// computeDashboard runs every aggregate roll-up for a range and shapes the
// dashboard payload. `external` is supplied by the caller (external.js) so this
// stays a pure metrics roll-up. `publicStart` (ISO or null) is the cutover
// lower bound — the effective ts filter is the later of the range window and
// the cutover, so pre-cutover internal test data never enters public metrics.
// With no db, returns a valid empty shape.
export async function computeDashboard(db, range, external, publicStart = null, now = new Date()) {
  const generated_at = now.toISOString();
  if (!db) {
    const empty = emptyDashboard(range, generated_at, publicStart);
    if (external) empty.external = external;
    return empty;
  }
  const cutoff = laterCutoff(cutoffISO(range, now), publicStart);
  const q = buildQueries(cutoff);

  const totalsRows = await rows(db, q.totals);
  const t = totalsRows[0] || {};
  const totalRuns = Number(t.total_runs || 0);
  const successRuns = Number(t.success_runs || 0);
  const errorRuns = Number(t.error_runs || 0);

  return {
    range,
    public_start_at: publicStart,
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
    by_surface: await rows(db, q.bySurface),
    daily_trend: await rows(db, q.dailyTrend),
    adapter_daily_trend: await rows(db, q.adapterDailyTrend),
    surface_daily_trend: await rows(db, q.surfaceDailyTrend),
    external: external || { npm_downloads: {}, vscode_installs: { value: null, note: "not collected" } },
    generated_at,
  };
}

// computeCoverage returns aggregate-only diagnostics about the event window, so
// an operator can see WHY e.g. 1d/7d/30d look identical (only one day of data
// exists) and how many events fall after the public cutover. Every field is a
// MIN/MAX/COUNT roll-up — never an anonymous_install_id or any per-event id.
export async function computeCoverage(db, publicStart = null, now = new Date()) {
  const generated_at = now.toISOString();
  if (!db) {
    return {
      first_event_at: null,
      last_event_at: null,
      event_days: 0,
      total_events: 0,
      public_start_at: publicStart,
      public_events: 0,
      generated_at,
    };
  }
  const totalSql = `
    SELECT
      MIN(ts)                          AS first_event_at,
      MAX(ts)                          AS last_event_at,
      COUNT(DISTINCT substr(ts, 1, 10)) AS event_days,
      COUNT(*)                         AS total_events
    FROM metrics_events`;
  const totalRows = await rows(db, { sql: totalSql, binds: [] });
  const c = totalRows[0] || {};

  let publicEvents = Number(c.total_events || 0);
  if (publicStart) {
    const pubRows = await rows(db, {
      sql: `SELECT COUNT(*) AS public_events FROM metrics_events WHERE ts >= ?`,
      binds: [publicStart],
    });
    publicEvents = Number((pubRows[0] || {}).public_events || 0);
  }

  return {
    first_event_at: c.first_event_at || null,
    last_event_at: c.last_event_at || null,
    event_days: Number(c.event_days || 0),
    total_events: Number(c.total_events || 0),
    public_start_at: publicStart,
    public_events: publicEvents,
    generated_at,
  };
}
