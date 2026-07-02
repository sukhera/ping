-- auto_resume controls whether a successful check-in clears paused_at (PING-010).
-- Default true preserves the pre-PING-010 always-auto-resume behaviour.
ALTER TABLE monitors ADD COLUMN auto_resume BOOLEAN NOT NULL DEFAULT true;

-- Supports the global event feed's "recent activity across all my monitors"
-- path: ORDER BY id DESC LIMIT n becomes a reverse index scan. The per-monitor
-- feed keeps using idx_events_monitor (monitor_id, created_at DESC).
CREATE INDEX idx_events_id ON events (id DESC);
