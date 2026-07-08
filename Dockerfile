# ── Stage 1: Build Go backend ──────────────────
FROM golang:1.26-alpine AS backend-build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-w -s" -o /ping ./cmd/ping

# Install golang-migrate for running DB migrations
RUN go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest

# ── Stage 2: Build Next.js frontend ───────────
FROM node:20-alpine AS frontend-build
WORKDIR /src
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ .

ARG NEXT_PUBLIC_API_BASE_URL=
ENV NEXT_PUBLIC_API_BASE_URL=${NEXT_PUBLIC_API_BASE_URL}
ENV NEXT_TELEMETRY_DISABLED=1

RUN npm run build

# ── Stage 3: Runtime ───────────────────────────
FROM caddy:2-alpine
RUN apk add --no-cache ca-certificates nodejs

# Go backend binary
COPY --from=backend-build /ping /usr/local/bin/ping

# golang-migrate binary
COPY --from=backend-build /go/bin/migrate /usr/local/bin/migrate

# DB migration files
COPY backend/db/migrations /migrations

# Next.js standalone output
COPY --from=frontend-build /src/.next/standalone /app
COPY --from=frontend-build /src/.next/static /app/.next/static

# Caddyfile: route between Go backend and Next.js
RUN cat > /etc/caddy/Caddyfile <<'EOF'
:80 {
    # Go backend API
    handle /api/v1/* {
        reverse_proxy localhost:8080
    }
    handle /health {
        reverse_proxy localhost:8080
    }

    # Heartbeat/probe ping endpoints
    handle /p/* {
        reverse_proxy localhost:8080
    }

    # Next.js static assets
    handle /_next/* {
        reverse_proxy localhost:3000
    }
    handle /favicon.ico {
        reverse_proxy localhost:3000
    }

    # Known frontend routes → Next.js
    @frontend path / /login /register /dashboard /settings /events
    handle @frontend {
        reverse_proxy localhost:3000
    }

    # Frontend routes with sub-paths
    handle /monitors/* {
        reverse_proxy localhost:3000
    }
    @monitors_root path /monitors
    handle @monitors_root {
        reverse_proxy localhost:3000
    }

    # Everything else → Go backend
    handle {
        reverse_proxy localhost:8080
    }
}
EOF

# Start script: write JWT keys, run migrations, start services
RUN cat > /start.sh <<'SCRIPT'
#!/bin/sh
set -e

# Write JWT keys from env vars to files (if provided as env vars)
if [ -n "$JWT_PRIVATE_KEY" ]; then
    mkdir -p /keys
    printf '%s\n' "$JWT_PRIVATE_KEY" > /keys/private.pem
    printf '%s\n' "$JWT_PUBLIC_KEY" > /keys/public.pem
    export JWT_PRIVATE_KEY_PATH=/keys/private.pem
    export JWT_PUBLIC_KEY_PATH=/keys/public.pem
fi

# Run database migrations
echo "Running database migrations..."
migrate -path /migrations -database "$DATABASE_URL" up || true

# Start Next.js
cd /app
NODE_ENV=production HOSTNAME=0.0.0.0 PORT=3000 node server.js &

# Start Go backend
/usr/local/bin/ping &

# Start Caddy (foreground)
exec caddy run --config /etc/caddy/Caddyfile --adapter caddyfile
SCRIPT
RUN chmod +x /start.sh

EXPOSE 80

CMD ["/start.sh"]
