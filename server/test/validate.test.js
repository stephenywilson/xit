import { test } from "node:test";
import assert from "node:assert/strict";
import {
  validateMetrics,
  FORBIDDEN_KEYS,
  ALLOWED_KEYS,
  MAX_BODY_BYTES,
  SCHEMA,
} from "../src/validate.js";

function goodEvent(overrides = {}) {
  return {
    schema: SCHEMA,
    event: "run.finished",
    anonymous_install_id: "abc123",
    timestamp: "2026-06-29T00:00:00Z",
    cli_version: "0.2.49",
    adapter: "claude",
    surface: "cli",
    os: "darwin",
    arch: "arm64",
    input_bytes: 100000,
    summary_bytes: 1000,
    saved_bytes: 99000,
    estimated_saved_tokens: 24750,
    compression_ratio: 0.99,
    run_count: 1,
    status: "success",
    error_kind: "none",
    ...overrides,
  };
}

test("accepts a valid event and sanitizes to allow-list only", () => {
  const r = validateMetrics(goodEvent());
  assert.equal(r.ok, true);
  for (const k of Object.keys(r.event)) {
    assert.ok(ALLOWED_KEYS.includes(k), `unexpected stored key: ${k}`);
  }
});

test("rejects each forbidden sensitive field", () => {
  for (const k of FORBIDDEN_KEYS) {
    const r = validateMetrics(goodEvent({ [k]: "secret-value" }));
    assert.equal(r.ok, false, `should reject forbidden key ${k}`);
    assert.equal(r.status, 400);
  }
});

test("rejects multi-channel attribution ids (channel_id/run_id/turn_id/event_id)", () => {
  // Spec §七/§八: these are LOCAL-only Dashboard ids and must never reach the
  // backend; a single one present rejects the whole event.
  for (const k of [
    "channel_id", "run_id", "turn_id", "event_id",
    "full_channel_id", "full_run_id", "full_turn_id",
  ]) {
    assert.ok(FORBIDDEN_KEYS.includes(k), `${k} must be forbidden`);
    const r = validateMetrics(goodEvent({ [k]: "deadbeef" }));
    assert.equal(r.ok, false, `should reject ${k}`);
    assert.equal(r.status, 400);
  }
});

test("the stored allow-list contains no per-channel/per-run/per-turn id", () => {
  // The backend only ever aggregates by adapter/surface/version — never by a
  // per-channel/run/turn id, so none may be storable.
  for (const k of [
    "channel_id", "run_id", "turn_id", "event_id",
    "full_channel_id", "full_run_id", "full_turn_id",
  ]) {
    assert.equal(ALLOWED_KEYS.includes(k), false, `${k} must not be storable`);
  }
});

test("strips unknown extra fields rather than storing them", () => {
  const r = validateMetrics(goodEvent({ some_future_field: "x", another: 1 }));
  assert.equal(r.ok, true);
  assert.equal("some_future_field" in r.event, false);
  assert.equal("another" in r.event, false);
});

test("rejects invalid adapter", () => {
  const r = validateMetrics(goodEvent({ adapter: "antigravity" }));
  assert.equal(r.ok, false);
  assert.match(r.error, /adapter/);
});

test("rejects invalid surface", () => {
  const r = validateMetrics(goodEvent({ surface: "telepathy" }));
  assert.equal(r.ok, false);
});

// 0.2.51: finer-grained Codex front-end surfaces must be accepted
// backward-compatibly (adapter stays "codex"; only surface differs).
test("accepts the 0.2.51 Codex front-end surface values", () => {
  for (const surface of ["codex_cli", "codex_ide", "chatgpt_desktop_codex", "codex_shared"]) {
    const r = validateMetrics(goodEvent({ adapter: "codex", surface }));
    assert.equal(r.ok, true, `surface=${surface} should be accepted`);
    assert.equal(r.event.surface, surface);
    assert.equal(r.event.adapter, "codex");
  }
});

test("rejects invalid status / error_kind", () => {
  assert.equal(validateMetrics(goodEvent({ status: "maybe" })).ok, false);
  assert.equal(validateMetrics(goodEvent({ error_kind: "explosion" })).ok, false);
});

test("rejects wrong schema", () => {
  const r = validateMetrics(goodEvent({ schema: "xit.metrics.v999" }));
  assert.equal(r.ok, false);
});

test("rejects negative or non-integer byte counts", () => {
  assert.equal(validateMetrics(goodEvent({ input_bytes: -1 })).ok, false);
  assert.equal(validateMetrics(goodEvent({ saved_bytes: 1.5 })).ok, false);
});

test("rejects compression_ratio out of [0,1]", () => {
  assert.equal(validateMetrics(goodEvent({ compression_ratio: 1.5 })).ok, false);
  assert.equal(validateMetrics(goodEvent({ compression_ratio: -0.1 })).ok, false);
});

test("rejects non-object body", () => {
  assert.equal(validateMetrics(null).ok, false);
  assert.equal(validateMetrics([1, 2, 3]).ok, false);
  assert.equal(validateMetrics("string").ok, false);
});

test("oversized-payload constant is small (defense for the Worker body check)", () => {
  // The Worker rejects bodies > MAX_BODY_BYTES; assert the bound is tight.
  assert.ok(MAX_BODY_BYTES <= 8 * 1024);
  const huge = JSON.stringify(goodEvent({ anonymous_install_id: "a".repeat(MAX_BODY_BYTES) }));
  assert.ok(huge.length > MAX_BODY_BYTES, "huge payload should exceed the limit");
});

test("install id length is bounded", () => {
  const r = validateMetrics(goodEvent({ anonymous_install_id: "x".repeat(200) }));
  assert.equal(r.ok, false);
});
