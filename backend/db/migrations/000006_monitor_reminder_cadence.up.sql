-- reminder_every_s is the per-monitor cadence, in seconds, for "still down"
-- reminder emails (PING-012). Default 86400 = one reminder per day while a
-- monitor stays down; 0 disables reminders for that monitor. The alerter worker
-- enqueues a reminder alert once a down monitor's most recent alert is older
-- than this cadence.
ALTER TABLE monitors ADD COLUMN reminder_every_s INTEGER NOT NULL DEFAULT 86400;

-- is_reminder distinguishes a "still down" reminder outbox row from the original
-- down alert. Both reference the same 'down' event (a reminder has no event of
-- its own), so the alerter can't tell them apart from the event type alone — it
-- reads this flag to render the reminder template ("still down after X") rather
-- than the down template. Default false: transition alerts are not reminders.
ALTER TABLE alerts ADD COLUMN is_reminder BOOLEAN NOT NULL DEFAULT false;
