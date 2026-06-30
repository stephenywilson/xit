// Aggregate-only dashboard stats for the XiT backend.
//
// Every query here is a GROUP BY / SUM / COUNT roll-up. NONE selects
// anonymous_install_id (or any per-user / per-channel / per-run identifier),
// so the dashboard can only ever show aggregates — never a single user's or a
// single install's detail. This is asserted in stats.test.js by scanning the
// emitted SQL for forbidden columns.

// Columns a dashboard query may never select — selecting one would expose
// per-user/per-event detail instead of an aggregate.
export const FORBIDDEN_SELECT_COLUMNS = [
  "anonymous_install_id",
  "channel_id",
  "run_id",
  "turn_id",
  "event_id",
  "full_session_id",
  "full_channel_id",
  "full_run_id",
  "full_turn_id",
];

export const STATS_QUERIES = {
  totals: `
    SELECT
      COUNT(*)                         AS total_runs,
      COALESCE(SUM(estimated_saved_tokens), 0) AS total_saved_tokens,
      COALESCE(SUM(saved_bytes), 0)    AS total_saved_bytes,
      SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END) AS success_runs,
      SUM(CASE WHEN status = 'error'   THEN 1 ELSE 0 END) AS error_runs
    FROM metrics_events`,
  byAdapter: `
    SELECT adapter,
           COUNT(*)                          AS runs,
           COALESCE(SUM(estimated_saved_tokens), 0) AS saved_tokens
    FROM metrics_events
    GROUP BY adapter
    ORDER BY runs DESC`,
  byVersion: `
    SELECT cli_version,
           COUNT(*) AS runs
    FROM metrics_events
    GROUP BY cli_version
    ORDER BY runs DESC`,
  dailyTrend: `
    SELECT substr(ts, 1, 10) AS day,
           COUNT(*)                          AS runs,
           COALESCE(SUM(estimated_saved_tokens), 0) AS saved_tokens
    FROM metrics_events
    GROUP BY day
    ORDER BY day DESC
    LIMIT 30`,
};

async function allRows(db, sql) {
  const res = await db.prepare(sql).all();
  return (res && res.results) || [];
}

// computeStats runs the aggregate roll-ups against a D1-like binding and
// returns the dashboard payload. Spec §四: Total runs, total estimated saved
// tokens, runs by adapter, runs by version, daily trend, success/error rate.
export async function computeStats(db) {
  const totalsRows = await allRows(db, STATS_QUERIES.totals);
  const totals = totalsRows[0] || {};
  const totalRuns = Number(totals.total_runs || 0);
  const successRuns = Number(totals.success_runs || 0);
  const errorRuns = Number(totals.error_runs || 0);

  return {
    total_runs: totalRuns,
    total_saved_tokens: Number(totals.total_saved_tokens || 0),
    total_saved_bytes: Number(totals.total_saved_bytes || 0),
    success_runs: successRuns,
    error_runs: errorRuns,
    success_rate: totalRuns > 0 ? successRuns / totalRuns : 0,
    error_rate: totalRuns > 0 ? errorRuns / totalRuns : 0,
    by_adapter: await allRows(db, STATS_QUERIES.byAdapter),
    by_version: await allRows(db, STATS_QUERIES.byVersion),
    daily_trend: await allRows(db, STATS_QUERIES.dailyTrend),
    generated_at: new Date().toISOString(),
  };
}
