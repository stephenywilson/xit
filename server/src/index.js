// XiT backend — Cloudflare Worker.
//
// Endpoints:
//   GET  /v1/health   liveness probe
//   GET  /v1/version  version-check payload for the CLI / VS Code extension
//   POST /v1/metrics  anonymous usage metrics ingest (validated + sanitized)
//
// Storage is intentionally pluggable: a D1 binding named METRICS_DB. If it is
// not bound (local dev), ingest still validates and returns 202 without storing.
//
// Privacy posture:
//   - request IPs are NOT written to the business table (only optionally to a
//     short-lived rate-limit KV, never joined to metrics rows),
//   - the raw request body is never stored — only the sanitized allow-listed
//     fields from validateMetrics(),
//   - CORS is minimal: the metrics POST does not need credentials and is only
//     ever called by the CLI (no browser origin) — we allow no extra origins.

import { validateMetrics, MAX_BODY_BYTES } from "./validate.js";
import { computeStats } from "./stats.js";
import { computeDashboard, isValidRange } from "./dashboard.js";
import { readExternal, runScheduled } from "./external.js";
import { DASHBOARD_HTML } from "./page.js";

// Version-check payload. In production this is best served from config/KV so it
// can change without a redeploy; inline default keeps the Worker self-contained.
const VERSION_INFO = {
  latest_cli: "0.2.49",
  min_cli: "0.2.48",
  latest_vscode: "0.0.35",
  min_vscode: "0.0.34",
  severity: "info",
  message: "",
  npm_command: "npm install -g xitsg@latest",
  vscode_marketplace_url: "https://marketplace.visualstudio.com/items?itemName=XiT.xit-vscode",
};

function json(data, status = 200, extraHeaders = {}) {
  return new Response(JSON.stringify(data), {
    status,
    headers: {
      "content-type": "application/json; charset=utf-8",
      "cache-control": "no-store",
      ...extraHeaders,
    },
  });
}

export default {
  async fetch(request, env) {
    const url = new URL(request.url);
    const { pathname } = url;

    if (request.method === "GET" && pathname === "/v1/health") {
      return json({ status: "ok", time: new Date().toISOString() });
    }

    if (request.method === "GET" && pathname === "/v1/version") {
      const info = env && env.VERSION_INFO_JSON ? safeParse(env.VERSION_INFO_JSON) : null;
      return json(info || VERSION_INFO);
    }

    if (request.method === "POST" && pathname === "/v1/metrics") {
      return handleMetrics(request, env);
    }

    // Aggregate-only dashboard. Returns roll-ups (totals, by adapter, by
    // version, daily trend, success/error rate) — never per-user/per-install
    // detail. Requires a METRICS_DB binding; without one (local dev) it
    // returns an empty-but-valid shape instead of erroring.
    if (request.method === "GET" && pathname === "/v1/stats") {
      return handleStats(env);
    }

    // Visual dashboard (static HTML). The page fetches /api/dashboard itself.
    if (request.method === "GET" && pathname === "/dashboard") {
      return new Response(DASHBOARD_HTML, {
        status: 200,
        headers: {
          "content-type": "text/html; charset=utf-8",
          "cache-control": "public, max-age=300",
        },
      });
    }

    // Aggregate-only dashboard JSON. ?range=1d|7d|30d|180d|365d|all (default
    // 30d). Returns summary, by_adapter, by_cli_version, by_vscode_version,
    // daily_trend, adapter_daily_trend, and external (public) counters. Never
    // returns any per-user / per-install / per-run identifier.
    if (request.method === "GET" && pathname === "/api/dashboard") {
      return handleDashboard(url, env);
    }

    return json({ error: "not found" }, 404);
  },

  // Daily cron (see wrangler.toml [triggers]). Refreshes external public
  // counters (npm downloads, optional VS Code installs). Fail-open: a fetch or
  // storage error is swallowed so a bad run never wedges the schedule.
  async scheduled(event, env, ctx) {
    try {
      await runScheduled(env);
    } catch {
      /* fail-open: never throw out of scheduled() */
    }
  },
};

function safeParse(s) {
  try {
    return JSON.parse(s);
  } catch {
    return null;
  }
}

async function handleStats(env) {
  if (!env || !env.METRICS_DB) {
    // Local dev / no DB bound: valid empty aggregate so the dashboard renders.
    return json({
      total_runs: 0,
      total_saved_tokens: 0,
      total_saved_bytes: 0,
      success_runs: 0,
      error_runs: 0,
      success_rate: 0,
      error_rate: 0,
      by_adapter: [],
      by_version: [],
      daily_trend: [],
      generated_at: new Date().toISOString(),
      note: "METRICS_DB not bound (local dev)",
    });
  }
  try {
    return json(await computeStats(env.METRICS_DB));
  } catch {
    return json({ error: "stats unavailable" }, 503);
  }
}

async function handleDashboard(url, env) {
  const range = url.searchParams.get("range") || "30d";
  if (!isValidRange(range)) {
    return json(
      { error: "invalid range (use 1d|7d|30d|180d|365d|all)" },
      400,
      { "access-control-allow-origin": "*" },
    );
  }
  const db = env && env.METRICS_DB ? env.METRICS_DB : null;
  try {
    const external = await readExternal(db, env);
    const payload = await computeDashboard(db, range, external);
    // Read-only aggregate; safe to expose to any origin for the dashboard page.
    return json(payload, 200, { "access-control-allow-origin": "*" });
  } catch {
    return json({ error: "dashboard unavailable" }, 503, {
      "access-control-allow-origin": "*",
    });
  }
}

async function handleMetrics(request, env) {
  // Body-size limit: reject anything larger than a single tiny event. Check the
  // declared length first, then enforce again after reading.
  const declared = Number(request.headers.get("content-length") || "0");
  if (declared > MAX_BODY_BYTES) {
    return json({ error: "payload too large" }, 413);
  }

  const ct = request.headers.get("content-type") || "";
  if (!ct.includes("application/json")) {
    return json({ error: "content-type must be application/json" }, 415);
  }

  const text = await request.text();
  if (text.length > MAX_BODY_BYTES) {
    return json({ error: "payload too large" }, 413);
  }

  const parsed = safeParse(text);
  if (parsed === null) {
    return json({ error: "invalid json" }, 400);
  }

  const result = validateMetrics(parsed);
  if (!result.ok) {
    return json({ error: result.error }, result.status);
  }

  // Optional: best-effort rate limit (per anonymous_install_id) using KV, if a
  // RATE_LIMIT_KV binding exists. Never blocks ingest on KV errors.
  if (env && env.RATE_LIMIT_KV) {
    try {
      const ok = await allowRate(env.RATE_LIMIT_KV, result.event.anonymous_install_id);
      if (!ok) {
        return json({ error: "rate limited" }, 429);
      }
    } catch {
      /* fail-open */
    }
  }

  // Store sanitized fields only. No IP, no raw body.
  if (env && env.METRICS_DB) {
    try {
      await storeEvent(env.METRICS_DB, result.event);
    } catch (e) {
      // Don't leak storage errors to clients; accept the event and move on.
      return json({ status: "accepted" }, 202);
    }
  }

  return json({ status: "accepted" }, 202);
}

// allowRate caps an install id to N writes per window using KV counters.
async function allowRate(kv, installId, limit = 120, windowSec = 60) {
  const bucket = Math.floor(Date.now() / 1000 / windowSec);
  const key = `rl:${installId}:${bucket}`;
  const current = Number((await kv.get(key)) || "0");
  if (current >= limit) {
    return false;
  }
  await kv.put(key, String(current + 1), { expirationTtl: windowSec * 2 });
  return true;
}

// storeEvent inserts the sanitized event into D1. The schema (server/schema.sql)
// stores only anonymous aggregate-friendly columns.
async function storeEvent(db, e) {
  const stmt = db.prepare(
    `INSERT INTO metrics_events (
        anonymous_install_id, ts, cli_version, vscode_extension_version,
        adapter, surface, os, arch, input_bytes, summary_bytes, saved_bytes,
        estimated_saved_tokens, compression_ratio, run_count, status, error_kind
     ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
  );
  await stmt
    .bind(
      e.anonymous_install_id,
      e.timestamp,
      e.cli_version,
      e.vscode_extension_version,
      e.adapter,
      e.surface,
      e.os,
      e.arch,
      e.input_bytes,
      e.summary_bytes,
      e.saved_bytes,
      e.estimated_saved_tokens,
      e.compression_ratio,
      e.run_count,
      e.status,
      e.error_kind
    )
    .run();
}
