-- tls_warned_expires_at tracks the tls_expires_at value the prober already
-- warned about (PING-018). The prober compares the probe's freshly-observed
-- tls_expires_at against this column: if they match, a warning was already
-- sent for this exact certificate and it must not fire again every probe
-- tick; if they differ (certificate renewed, new NotAfter), the warning
-- re-arms. NULL means "never warned".
ALTER TABLE monitors ADD COLUMN tls_warned_expires_at TIMESTAMPTZ;
