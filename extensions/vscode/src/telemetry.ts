// XiT VS Code telemetry + version check.
//
// This module is deliberately free of any `vscode` import so it can be unit
// tested under `node --test`. All VS Code-derived values (the global telemetry
// level, the `xit.telemetry` setting, the API base, versions) are passed in by
// extension.ts at runtime.
//
// Privacy: the event built here contains ONLY the allow-listed xit.metrics.v1
// fields. There is no field for command text, output, prompts, cwd, paths or
// ids. The install id is the same anonymous random id the CLI uses (~/.xit).

import * as fs from "node:fs";
import * as path from "node:path";
import * as os from "node:os";
import * as crypto from "node:crypto";
import * as http from "node:http";
import * as https from "node:https";

export type TelemetrySetting = "default" | "on" | "off";

export interface TelemetryGateInputs {
  /** vscode.env.isTelemetryEnabled — VS Code global telemetry switch. */
  vscodeTelemetryEnabled: boolean;
  /** the `xit.telemetry` setting: "default" | "on" | "off". */
  xitSetting: TelemetrySetting;
  /** process.env.XIT_TELEMETRY, if set. */
  envOverride?: string;
}

const OFF_VALUES = new Set(["off", "0", "false", "no", "disable", "disabled"]);
const ON_VALUES = new Set(["on", "1", "true", "yes", "enable", "enabled"]);

/**
 * Resolve whether the VS Code extension may send telemetry.
 *
 * Rule order:
 *   1. If VS Code global telemetry is OFF, NEVER send (absolute — per spec).
 *   2. XIT_TELEMETRY env override (off/on).
 *   3. `xit.telemetry` setting (off/on).
 *   4. Default: enabled (anonymous-by-default, easy to disable).
 */
export function isTelemetryEnabled(i: TelemetryGateInputs): boolean {
  if (!i.vscodeTelemetryEnabled) {
    return false;
  }
  const env = (i.envOverride || "").trim().toLowerCase();
  if (OFF_VALUES.has(env)) {
    return false;
  }
  if (ON_VALUES.has(env)) {
    return true;
  }
  if (i.xitSetting === "off") {
    return false;
  }
  if (i.xitSetting === "on") {
    return true;
  }
  return true;
}

export interface MetricsEvent {
  schema: "xit.metrics.v1";
  event: string;
  anonymous_install_id: string;
  timestamp: string;
  cli_version: string;
  vscode_extension_version: string;
  adapter: string;
  surface: string;
  os: string;
  arch: string;
  input_bytes: number;
  summary_bytes: number;
  saved_bytes: number;
  estimated_saved_tokens: number;
  compression_ratio: number;
  run_count: number;
  status: string;
  error_kind: string;
}

export interface BuildEventInput {
  event?: string;
  installId: string;
  vscodeExtensionVersion: string;
  cliVersion?: string;
  adapter?: string;
  surface?: string;
  inputBytes?: number;
  summaryBytes?: number;
  savedBytes?: number;
  runCount?: number;
  status?: string;
  errorKind?: string;
  now?: () => Date;
}

const ALLOWED_ADAPTERS = new Set([
  "codex", "claude", "kimi", "opencode", "cursor", "vscode", "unknown",
]);
// codex_cli / codex_ide / chatgpt_desktop_codex / codex_shared are the
// finer-grained Codex front-end breakdown (0.2.51) — see
// internal/codexhook.DetectSurface on the CLI side. adapter stays "codex"
// for all of them; only surface distinguishes the front-end.
const ALLOWED_SURFACES = new Set([
  "cli", "hook", "vscode", "bridge",
  "codex_cli", "codex_ide", "chatgpt_desktop_codex", "codex_shared",
]);

function normOsLabel(): string {
  switch (process.platform) {
    case "darwin": return "darwin";
    case "linux": return "linux";
    case "win32": return "windows";
    default: return "unknown";
  }
}

function normArchLabel(): string {
  switch (process.arch) {
    case "arm64": return "arm64";
    case "x64": return "amd64";
    default: return "unknown";
  }
}

/** Build a fully-normalized, privacy-safe metrics event. */
export function buildMetricsEvent(i: BuildEventInput): MetricsEvent {
  const now = i.now ? i.now() : new Date();
  const saved = Math.max(0, i.savedBytes ?? 0);
  const input = Math.max(0, i.inputBytes ?? 0);
  let ratio = input > 0 && saved > 0 ? saved / input : 0;
  if (ratio > 1) ratio = 1;
  const adapter = ALLOWED_ADAPTERS.has(i.adapter || "") ? (i.adapter as string) : "vscode";
  const surface = ALLOWED_SURFACES.has(i.surface || "") ? (i.surface as string) : "vscode";
  return {
    schema: "xit.metrics.v1",
    event: i.event || "run.finished",
    anonymous_install_id: i.installId,
    timestamp: now.toISOString(),
    cli_version: i.cliVersion || "",
    vscode_extension_version: i.vscodeExtensionVersion,
    adapter,
    surface,
    os: normOsLabel(),
    arch: normArchLabel(),
    input_bytes: input,
    summary_bytes: Math.max(0, i.summaryBytes ?? 0),
    saved_bytes: saved,
    estimated_saved_tokens: saved > 0 ? Math.floor(saved / 4) : 0,
    compression_ratio: ratio,
    run_count: Math.max(0, i.runCount ?? 0),
    status: i.status === "error" ? "error" : "success",
    error_kind: normErrorKind(i.errorKind),
  };
}

function normErrorKind(k?: string): string {
  switch (k) {
    case "timeout":
    case "command_failed":
    case "parse_failed":
    case "unknown":
      return k;
    default:
      return "none";
  }
}

/**
 * Read (or create) the anonymous install id, sharing ~/.xit/telemetry.json with
 * the CLI so the same install isn't counted twice. `xitHome` defaults to ~/.xit.
 */
export function resolveInstallId(xitHome?: string): string {
  const home = xitHome && xitHome.trim() ? xitHome : path.join(os.homedir(), ".xit");
  const file = path.join(home, "telemetry.json");
  try {
    const data = JSON.parse(fs.readFileSync(file, "utf8"));
    if (data && typeof data.anonymous_install_id === "string" && data.anonymous_install_id) {
      return data.anonymous_install_id;
    }
  } catch {
    /* not present yet */
  }
  const id = crypto.randomBytes(16).toString("hex");
  try {
    fs.mkdirSync(home, { recursive: true });
    let existing: Record<string, unknown> = {};
    try {
      existing = JSON.parse(fs.readFileSync(file, "utf8"));
    } catch {
      existing = { enabled: true };
    }
    existing.anonymous_install_id = id;
    fs.writeFileSync(file, JSON.stringify(existing, null, 2) + "\n");
  } catch {
    /* fail-open: still return an id for this session */
  }
  return id;
}

/**
 * Read the CLI's telemetry opt-out from ~/.xit/telemetry.json. If the user ran
 * `xit telemetry off`, honor it in the extension too. Returns undefined when
 * the file has no explicit setting (so the caller falls back to VS Code config).
 */
export function readCliTelemetryEnabled(xitHome?: string): boolean | undefined {
  const home = xitHome && xitHome.trim() ? xitHome : path.join(os.homedir(), ".xit");
  try {
    const data = JSON.parse(fs.readFileSync(path.join(home, "telemetry.json"), "utf8"));
    if (typeof data.enabled === "boolean") {
      return data.enabled;
    }
  } catch {
    /* ignore */
  }
  return undefined;
}

/** Fire-and-forget POST of a metrics event. Never throws; ~1s timeout. */
export function sendMetrics(apiBase: string, event: MetricsEvent): void {
  if (!apiBase) {
    return;
  }
  try {
    const url = new URL(apiBase.replace(/\/+$/, "") + "/v1/metrics");
    const body = Buffer.from(JSON.stringify(event));
    const mod = url.protocol === "http:" ? http : https;
    const req = mod.request(
      url,
      {
        method: "POST",
        headers: { "content-type": "application/json", "content-length": body.length },
        timeout: 1000,
      },
      (res) => {
        res.resume(); // drain
      }
    );
    req.on("error", () => {});
    req.on("timeout", () => req.destroy());
    req.write(body);
    req.end();
  } catch {
    /* fail-open */
  }
}

export interface VersionInfo {
  latest_cli?: string;
  min_cli?: string;
  latest_vscode?: string;
  min_vscode?: string;
  severity?: string;
  message?: string;
  npm_command?: string;
  vscode_marketplace_url?: string;
}

/** Fetch /v1/version. Resolves undefined on any error (fail-open). */
export function fetchVersionInfo(apiBase: string): Promise<VersionInfo | undefined> {
  return new Promise((resolve) => {
    if (!apiBase) {
      resolve(undefined);
      return;
    }
    try {
      const url = new URL(apiBase.replace(/\/+$/, "") + "/v1/version");
      const mod = url.protocol === "http:" ? http : https;
      const req = mod.request(url, { method: "GET", timeout: 2000 }, (res) => {
        let raw = "";
        res.on("data", (c) => (raw += c));
        res.on("end", () => {
          try {
            resolve(JSON.parse(raw));
          } catch {
            resolve(undefined);
          }
        });
      });
      req.on("error", () => resolve(undefined));
      req.on("timeout", () => {
        req.destroy();
        resolve(undefined);
      });
      req.end();
    } catch {
      resolve(undefined);
    }
  });
}

/** Compare dotted versions; -1 | 0 | 1. Tolerates a leading "v". */
export function compareVersions(a: string, b: string): number {
  const pa = parseVersion(a);
  const pb = parseVersion(b);
  const n = Math.max(pa.length, pb.length);
  for (let i = 0; i < n; i++) {
    const ai = pa[i] ?? 0;
    const bi = pb[i] ?? 0;
    if (ai < bi) return -1;
    if (ai > bi) return 1;
  }
  return 0;
}

function parseVersion(v: string): number[] {
  return (v || "")
    .trim()
    .replace(/^v/, "")
    .split(".")
    .map((p) => parseInt(p.replace(/[-+].*$/, ""), 10) || 0);
}

export type VersionSeverity = "info" | "recommended" | "required" | "blocked";

export interface VersionGateResult {
  /** current < minimum (strictly). Equality is never "below". */
  belowMinimum: boolean;
  /** current < latest (strictly). false when current is at or ahead of latest. */
  upgradeNeeded: boolean;
  /** effective severity after applying the rules below. */
  severity: VersionSeverity;
}

function normalizeSeverity(s?: string): VersionSeverity {
  switch ((s || "").toLowerCase().trim()) {
    case "recommended":
      return "recommended";
    case "required":
      return "required";
    case "blocked":
      return "blocked";
    default:
      return "info";
  }
}

/**
 * Evaluate the effective version-gate severity for `current` against a
 * server-declared `minimum`, `latest`, and `serverSeverity`. Mirrors the
 * CLI's internal/updatecheck.evaluate() exactly, so both surfaces agree:
 *
 *  - current < minimum            -> "blocked" (the only tier that disables
 *    anything; escalated locally even if the server was conservative).
 *  - current >= latest (at or     -> "info", regardless of what the server
 *    ahead of the published version) said — nothing to upgrade to, so a
 *                                     stale "required"/"blocked" server flag
 *                                     can never linger for an up-to-date (or
 *                                     newer-than-known) install.
 *  - minimum <= current < latest, -> downgraded to "required" — never
 *    server said "blocked"           actually blocked when current >= min.
 *  - minimum <= current < latest, -> passed through unchanged
 *    server said info/recommended/
 *    required
 *
 * "blocked"/"required" are otherwise purely advisory here: only
 * `severity === "blocked"` should ever be treated as disabling something,
 * and current == minimum (or above) is never "blocked" — equality is never
 * "below".
 */
export function evaluateVersionGate(
  current: string,
  minimum: string | undefined,
  latest: string | undefined,
  serverSeverity: string | undefined,
): VersionGateResult {
  const belowMinimum = !!minimum && compareVersions(current, minimum) < 0;
  const upgradeNeeded = !!latest && compareVersions(current, latest) < 0;
  let severity = normalizeSeverity(serverSeverity);
  if (belowMinimum) {
    severity = "blocked";
  } else if (!upgradeNeeded) {
    severity = "info";
  } else if (severity === "blocked") {
    severity = "required";
  }
  return { belowMinimum, upgradeNeeded, severity };
}
