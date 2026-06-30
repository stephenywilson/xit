-- XiT metrics — D1 / SQLite schema.
--
-- This table stores ONLY anonymous, aggregate-friendly fields. There is no
-- column for IP, command text, cwd, path, prompt, AI reply, raw output, token,
-- secret, username, email, repo, or full session id — by design.

CREATE TABLE IF NOT EXISTS metrics_events (
  id                       INTEGER PRIMARY KEY AUTOINCREMENT,
  anonymous_install_id     TEXT    NOT NULL,
  ts                       TEXT    NOT NULL,           -- client RFC3339 timestamp
  received_at              TEXT    NOT NULL DEFAULT (datetime('now')),
  cli_version              TEXT,
  vscode_extension_version TEXT,
  adapter                  TEXT    NOT NULL,           -- codex|claude|kimi|opencode|cursor|vscode|unknown
  surface                  TEXT    NOT NULL,           -- cli|hook|vscode|bridge
  os                       TEXT,                       -- darwin|linux|windows|unknown
  arch                     TEXT,                       -- arm64|amd64|unknown
  input_bytes              INTEGER NOT NULL DEFAULT 0,
  summary_bytes            INTEGER NOT NULL DEFAULT 0,
  saved_bytes              INTEGER NOT NULL DEFAULT 0,
  estimated_saved_tokens   INTEGER NOT NULL DEFAULT 0,
  compression_ratio        REAL    NOT NULL DEFAULT 0,
  run_count                INTEGER NOT NULL DEFAULT 0,
  status                   TEXT    NOT NULL,           -- success|error
  error_kind               TEXT    NOT NULL DEFAULT 'none'
);

CREATE INDEX IF NOT EXISTS idx_metrics_ts      ON metrics_events (ts);
CREATE INDEX IF NOT EXISTS idx_metrics_adapter ON metrics_events (adapter);
CREATE INDEX IF NOT EXISTS idx_metrics_version ON metrics_events (cli_version);

-- Example aggregation: saved tokens per day per adapter.
--
--   SELECT substr(ts, 1, 10) AS day, adapter,
--          COUNT(*)                  AS runs,
--          SUM(estimated_saved_tokens) AS saved_tokens
--   FROM metrics_events
--   GROUP BY day, adapter
--   ORDER BY day DESC, saved_tokens DESC;
