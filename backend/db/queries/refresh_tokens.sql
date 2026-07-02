-- name: CreateRefreshToken :one
INSERT INTO refresh_tokens (user_id, family_id, token_hash, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetRefreshTokenByHash :one
SELECT * FROM refresh_tokens
WHERE token_hash = $1;

-- name: RotateRefreshTokenIfUnrotated :one
-- Atomically marks a token rotated only if it hasn't already been rotated or
-- revoked, closing the check-then-write race between GetRefreshTokenByHash
-- and the rotation write: two concurrent requests replaying the same token
-- can no longer both succeed, since only one UPDATE can match the WHERE
-- clause before rotated_at becomes non-null.
UPDATE refresh_tokens
SET rotated_at = now()
WHERE id = $1 AND rotated_at IS NULL AND revoked_at IS NULL
RETURNING *;

-- name: RevokeRefreshTokenFamily :exec
UPDATE refresh_tokens
SET revoked_at = now()
WHERE family_id = $1 AND revoked_at IS NULL;

-- name: DeleteExpiredRefreshTokens :exec
DELETE FROM refresh_tokens
WHERE expires_at < now();
