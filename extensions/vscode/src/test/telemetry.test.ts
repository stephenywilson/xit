import test from "node:test";
import assert from "node:assert/strict";
import {
  isTelemetryEnabled,
  buildMetricsEvent,
  compareVersions,
} from "../telemetry";

const FORBIDDEN_KEYS = [
  "raw_output", "raw_log", "prompt", "ai_reply", "command",
  "cwd", "path", "file_name", "repo_name", "username", "email",
  "api_key", "token", "full_session_id", "full_host_instance_hash",
  "full_workspace_hash",
  // Multi-channel attribution ids are LOCAL-only and must never be uploaded.
  "channel_id", "run_id", "turn_id", "event_id",
  "full_channel_id", "full_run_id", "full_turn_id",
];

test("VS Code global telemetry OFF disables sending (absolute)", () => {
  // Even if every other signal says "on", vscode-level off wins.
  assert.equal(
    isTelemetryEnabled({ vscodeTelemetryEnabled: false, xitSetting: "on", envOverride: "on" }),
    false
  );
});

test("default is enabled when vscode telemetry is on", () => {
  assert.equal(isTelemetryEnabled({ vscodeTelemetryEnabled: true, xitSetting: "default" }), true);
});

test("xit.telemetry=off disables sending", () => {
  assert.equal(isTelemetryEnabled({ vscodeTelemetryEnabled: true, xitSetting: "off" }), false);
});

test("XIT_TELEMETRY=off env overrides xit.telemetry=on", () => {
  assert.equal(
    isTelemetryEnabled({ vscodeTelemetryEnabled: true, xitSetting: "on", envOverride: "off" }),
    false
  );
});

test("XIT_TELEMETRY=on enables when vscode telemetry on", () => {
  assert.equal(
    isTelemetryEnabled({ vscodeTelemetryEnabled: true, xitSetting: "default", envOverride: "on" }),
    true
  );
});

test("built event contains no forbidden/sensitive fields", () => {
  const ev = buildMetricsEvent({
    installId: "anon123",
    vscodeExtensionVersion: "0.0.35",
    adapter: "claude",
    surface: "bridge",
    inputBytes: 100000,
    summaryBytes: 1000,
    savedBytes: 99000,
    runCount: 2,
    status: "success",
  });
  const keys = Object.keys(ev);
  for (const k of FORBIDDEN_KEYS) {
    assert.equal(keys.includes(k), false, `forbidden key present: ${k}`);
  }
});

test("saved-token estimate uses saved_bytes/4", () => {
  const ev = buildMetricsEvent({
    installId: "x",
    vscodeExtensionVersion: "0.0.35",
    savedBytes: 122222,
    inputBytes: 130000,
  });
  assert.equal(ev.estimated_saved_tokens, 30555);
});

test("unlisted adapter/surface normalize to vscode", () => {
  const ev = buildMetricsEvent({
    installId: "x",
    vscodeExtensionVersion: "0.0.35",
    adapter: "antigravity",
    surface: "telepathy",
  });
  assert.equal(ev.adapter, "vscode");
  assert.equal(ev.surface, "vscode");
});

test("compression_ratio clamps to [0,1]", () => {
  const ev = buildMetricsEvent({
    installId: "x",
    vscodeExtensionVersion: "0.0.35",
    inputBytes: 10,
    savedBytes: 999,
  });
  assert.ok(ev.compression_ratio <= 1 && ev.compression_ratio >= 0);
});

test("compareVersions handles upgrade detection", () => {
  assert.equal(compareVersions("0.0.34", "0.0.35"), -1);
  assert.equal(compareVersions("0.0.35", "0.0.35"), 0);
  assert.equal(compareVersions("v0.0.36", "0.0.35"), 1);
});
