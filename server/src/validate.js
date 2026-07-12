// Validation + sanitization for the XiT metrics endpoint.
//
// This module is the privacy firewall for /v1/metrics. It:
//   - rejects oversized bodies and malformed JSON,
//   - rejects events that carry any forbidden (sensitive) key,
//   - validates the closed enums (adapter / surface / status / error_kind),
//   - returns a *sanitized* event containing ONLY the allow-listed fields, so
//     even a well-meaning future caller cannot smuggle extra data into storage.
//
// Kept as plain ESM (no build step) so it runs both in Cloudflare Workers and
// under `node --test`.

export const MAX_BODY_BYTES = 4 * 1024; // 4 KiB — a metrics event is tiny.

export const SCHEMA = "xit.metrics.v1";

export const ALLOWED_ADAPTERS = new Set([
  "codex", "claude", "kimi", "opencode", "cursor", "vscode", "unknown",
]);

// codex_cli / codex_ide / chatgpt_desktop_codex / codex_shared are the
// finer-grained Codex front-end breakdown added in XiT 0.2.51 (adapter stays
// "codex" for all of them; only surface distinguishes CLI / IDE / ChatGPT
// Desktop). Adding new surface values here is backward-compatible: existing
// clients that only ever send the original four keep working unchanged.
export const ALLOWED_SURFACES = new Set([
  "cli", "hook", "vscode", "bridge",
  "codex_cli", "codex_ide", "chatgpt_desktop_codex", "codex_shared",
]);

export const ALLOWED_STATUS = new Set(["success", "error"]);

export const ALLOWED_ERROR_KINDS = new Set([
  "none", "timeout", "command_failed", "parse_failed", "unknown",
]);

// Fields that must never be accepted or stored. Their mere presence rejects.
//
// channel_id / run_id / turn_id / event_id and their "full_*" variants are the
// multi-channel attribution ids (CLI 0.2.49). They are strictly LOCAL — used only by the VS Code
// Dashboard to isolate concurrent tasks — and must never reach the server.
// The backend only ever aggregates by adapter/surface/version, never by a
// per-channel/per-run/per-turn id, so accepting one would be a privacy
// regression. Their presence rejects the whole event.
export const FORBIDDEN_KEYS = [
  "raw_output", "raw_log", "prompt", "ai_reply", "command",
  "cwd", "path", "file_name", "repo_name", "username", "email",
  "api_key", "token", "full_session_id", "full_host_instance_hash",
  "full_workspace_hash",
  "channel_id", "run_id", "turn_id", "event_id",
  "full_channel_id", "full_run_id", "full_turn_id",
];

// The complete allow-list of stored fields.
export const ALLOWED_KEYS = [
  "schema", "event", "anonymous_install_id", "timestamp",
  "cli_version", "vscode_extension_version", "adapter", "surface",
  "os", "arch", "input_bytes", "summary_bytes", "saved_bytes",
  "estimated_saved_tokens", "compression_ratio", "run_count",
  "status", "error_kind",
];

function isInt(v) {
  return typeof v === "number" && Number.isFinite(v) && Math.floor(v) === v;
}

function nonNegInt(v) {
  return isInt(v) && v >= 0;
}

/**
 * Validate + sanitize a metrics event.
 * @param {unknown} body parsed JSON body
 * @returns {{ok:true, event:object} | {ok:false, status:number, error:string}}
 */
export function validateMetrics(body) {
  if (body === null || typeof body !== "object" || Array.isArray(body)) {
    return { ok: false, status: 400, error: "body must be a JSON object" };
  }

  // Hard reject any forbidden key, even if empty/null.
  for (const k of FORBIDDEN_KEYS) {
    if (k in body) {
      return { ok: false, status: 400, error: `forbidden field: ${k}` };
    }
  }

  if (body.schema !== SCHEMA) {
    return { ok: false, status: 400, error: "invalid or missing schema" };
  }
  if (typeof body.event !== "string" || body.event.length === 0 || body.event.length > 64) {
    return { ok: false, status: 400, error: "invalid event" };
  }
  if (typeof body.anonymous_install_id !== "string" ||
      body.anonymous_install_id.length === 0 ||
      body.anonymous_install_id.length > 128) {
    return { ok: false, status: 400, error: "invalid anonymous_install_id" };
  }
  if (typeof body.adapter !== "string" || !ALLOWED_ADAPTERS.has(body.adapter)) {
    return { ok: false, status: 400, error: "invalid adapter" };
  }
  if (typeof body.surface !== "string" || !ALLOWED_SURFACES.has(body.surface)) {
    return { ok: false, status: 400, error: "invalid surface" };
  }
  if (typeof body.status !== "string" || !ALLOWED_STATUS.has(body.status)) {
    return { ok: false, status: 400, error: "invalid status" };
  }
  const errorKind = body.error_kind === undefined ? "none" : body.error_kind;
  if (typeof errorKind !== "string" || !ALLOWED_ERROR_KINDS.has(errorKind)) {
    return { ok: false, status: 400, error: "invalid error_kind" };
  }
  for (const k of ["input_bytes", "summary_bytes", "saved_bytes", "estimated_saved_tokens", "run_count"]) {
    if (body[k] !== undefined && !nonNegInt(body[k])) {
      return { ok: false, status: 400, error: `invalid ${k}` };
    }
  }
  if (body.compression_ratio !== undefined) {
    const r = body.compression_ratio;
    if (typeof r !== "number" || !Number.isFinite(r) || r < 0 || r > 1) {
      return { ok: false, status: 400, error: "invalid compression_ratio" };
    }
  }

  // Build a sanitized event from the allow-list only.
  const event = {
    schema: SCHEMA,
    event: body.event,
    anonymous_install_id: body.anonymous_install_id,
    timestamp: typeof body.timestamp === "string" ? body.timestamp : new Date().toISOString(),
    cli_version: typeof body.cli_version === "string" ? body.cli_version.slice(0, 32) : "",
    vscode_extension_version:
      typeof body.vscode_extension_version === "string" ? body.vscode_extension_version.slice(0, 32) : "",
    adapter: body.adapter,
    surface: body.surface,
    os: ["darwin", "linux", "windows"].includes(body.os) ? body.os : "unknown",
    arch: ["arm64", "amd64"].includes(body.arch) ? body.arch : "unknown",
    input_bytes: body.input_bytes ?? 0,
    summary_bytes: body.summary_bytes ?? 0,
    saved_bytes: body.saved_bytes ?? 0,
    estimated_saved_tokens: body.estimated_saved_tokens ?? 0,
    compression_ratio: body.compression_ratio ?? 0,
    run_count: body.run_count ?? 0,
    status: body.status,
    error_kind: errorKind,
  };
  return { ok: true, event };
}
