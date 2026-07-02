-- Ingest fast path (PING-008): every ping records exactly one checkin row.
-- source_ip (INET) / user_agent / body are nullable; the store passes NULL
-- when absent. body is already truncated to 10 KB by the server handler.
-- name: InsertCheckin :one
INSERT INTO checkins (monitor_id, kind, source_ip, user_agent, body)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;
