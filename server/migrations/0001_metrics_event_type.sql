-- Migration 0001: Add event column to metrics_events
--
-- Adds the event column to distinguish run.finished from other event kinds
-- (such as extension.activated) without adding DEFAULT 'run.finished' so that
-- older events in flight are not falsely marked as runs.

ALTER TABLE metrics_events ADD COLUMN event TEXT;
