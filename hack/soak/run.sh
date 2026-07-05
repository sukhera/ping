#!/usr/bin/env bash
# PING-023 soak/chaos harness: spins up an isolated stack, creates N synthetic
# heartbeat + HTTP monitors, randomly restarts Postgres/Redis/the app binary
# over the run duration, then audits DB invariants (see backend/cmd/soakaudit).
#
# Usage (from repo root):
#   ./hack/soak/run.sh
#   SOAK_DURATION=10m MONITOR_COUNT=4 ./hack/soak/run.sh   # quick smoke run
#
# Env vars (all optional, defaults are the real 48h soak configuration):
#   SOAK_DURATION         total run time, Go duration syntax (default 48h)
#   MONITOR_COUNT         total synthetic monitors, split evenly (default 10)
#   CHAOS_MIN_INTERVAL_S  minimum seconds between chaos actions (default 120)
#   CHAOS_MAX_INTERVAL_S  maximum seconds between chaos actions (default 600)
#
# Leaves the stack running after the audit so results can be inspected
# manually; does not docker compose down automatically.
set -euo pipefail

# parse_duration_seconds converts a Go-style duration string (e.g. "48h",
# "90m", "5m30s") to whole seconds — a small pure-bash parser rather than
# shelling out to `date`, whose relative-offset flags differ between GNU and
# BSD date (macOS ships BSD date, no `-d`).
parse_duration_seconds() {
  local input="$1" total=0 num unit
  while [[ "$input" =~ ^([0-9]+)(h|m|s)(.*)$ ]]; do
    num="${BASH_REMATCH[1]}"
    unit="${BASH_REMATCH[2]}"
    input="${BASH_REMATCH[3]}"
    case "$unit" in
      h) total=$((total + num * 3600)) ;;
      m) total=$((total + num * 60)) ;;
      s) total=$((total + num)) ;;
    esac
  done
  if [ "$total" -le 0 ]; then
    echo "error: invalid SOAK_DURATION '$1', expected Go duration syntax like 48h, 90m, 5m30s" >&2
    exit 1
  fi
  echo "$total"
}

SOAK_DURATION="${SOAK_DURATION:-48h}"
MONITOR_COUNT="${MONITOR_COUNT:-10}"
CHAOS_MIN_INTERVAL_S="${CHAOS_MIN_INTERVAL_S:-120}"
CHAOS_MAX_INTERVAL_S="${CHAOS_MAX_INTERVAL_S:-600}"

if [ "$MONITOR_COUNT" -lt 2 ]; then
  echo "error: MONITOR_COUNT must be >= 2 (needs at least one heartbeat and one http monitor)"
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$REPO_ROOT"

[ -f TECH-PLAN.md ] || { echo "error: run from the repo root (TECH-PLAN.md not found)"; exit 1; }
command -v docker >/dev/null 2>&1 || { echo "error: docker not installed"; exit 1; }
command -v go >/dev/null 2>&1 || { echo "error: go not installed"; exit 1; }
command -v openssl >/dev/null 2>&1 || { echo "error: openssl not installed"; exit 1; }

COMPOSE_PROJECT="ping-soak"
DB_PORT=15432
REDIS_PORT=16379
APP_PORT=18080
FLAKY_BASE_PORT=19101

RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)"
RUN_DIR="$SCRIPT_DIR/run-$RUN_ID"
mkdir -p "$RUN_DIR"
echo "soak run directory: $RUN_DIR"

DATABASE_URL="postgres://ping:ping@localhost:${DB_PORT}/ping?sslmode=disable"
REDIS_URL="redis://localhost:${REDIS_PORT}/0"
APP_BASE_URL="http://localhost:${APP_PORT}"

cleanup() {
  echo "run.sh: cleaning up background processes (stack is left running for inspection)"
  touch "$RUN_DIR/chaos.stop" 2>/dev/null || true
  [ -n "${CHAOS_PID:-}" ] && kill "$CHAOS_PID" 2>/dev/null || true
  [ -n "${SUPERVISOR_PID:-}" ] && kill "$SUPERVISOR_PID" 2>/dev/null || true
  [ -n "${LOADGEN_PID:-}" ] && kill "$LOADGEN_PID" 2>/dev/null || true
  [ -n "${FLAKY_PID:-}" ] && kill "$FLAKY_PID" 2>/dev/null || true
  # supervise_app's respawn loop backgrounds the app binary as its own child,
  # so killing SUPERVISOR_PID alone leaves the current app process running —
  # read the pid file it maintains and kill that directly too.
  local app_pid
  app_pid=$(cat "$RUN_DIR/app.pid" 2>/dev/null) || true
  [ -n "${app_pid:-}" ] && kill "$app_pid" 2>/dev/null || true
}
trap cleanup EXIT

# Refuse to start if a previous run's app process is still bound to
# APP_PORT — this run's supervise_app would otherwise fail to bind and the
# chaos loop's restart_app checks would silently no-op against a stale pid,
# masking real chaos-restart coverage for the whole run.
if lsof -i ":${APP_PORT}" -sTCP:LISTEN >/dev/null 2>&1; then
  echo "error: port ${APP_PORT} is already in use — a previous soak run's app process may still be running."
  echo "Find it with: lsof -i :${APP_PORT}"
  exit 1
fi

echo "==> starting isolated stack ($COMPOSE_PROJECT: postgres:$DB_PORT redis:$REDIS_PORT)"
POSTGRES_PORT_OVERRIDE="$DB_PORT" REDIS_PORT_OVERRIDE="$REDIS_PORT" \
  docker compose -p "$COMPOSE_PROJECT" -f "$REPO_ROOT/docker-compose.yml" \
  -f "$SCRIPT_DIR/docker-compose.soak.yml" up -d postgres redis >"$RUN_DIR/docker.log" 2>&1

echo "==> waiting for postgres to be healthy"
for _ in $(seq 1 30); do
  if docker compose -p "$COMPOSE_PROJECT" -f "$REPO_ROOT/docker-compose.yml" -f "$SCRIPT_DIR/docker-compose.soak.yml" \
      ps postgres --format json 2>/dev/null | grep -q '"Health":"healthy"'; then
    break
  fi
  sleep 2
done

echo "==> migrations"
migrate -path "$REPO_ROOT/backend/db/migrations" -database "$DATABASE_URL" up

echo "==> generating JWT keypair"
mkdir -p "$RUN_DIR/keys"
openssl genrsa -out "$RUN_DIR/keys/jwt_private.pem" 2048 >/dev/null 2>&1
openssl rsa -in "$RUN_DIR/keys/jwt_private.pem" -pubout -out "$RUN_DIR/keys/jwt_public.pem" >/dev/null 2>&1

echo "==> building binaries"
(cd "$REPO_ROOT/backend" && go build -o "$RUN_DIR/ping" ./cmd/ping)
(cd "$REPO_ROOT/backend" && go build -o "$RUN_DIR/soakloadgen" ./cmd/soakloadgen)
(cd "$REPO_ROOT/backend" && go build -o "$RUN_DIR/soakflakytarget" ./cmd/soakflakytarget)
(cd "$REPO_ROOT/backend" && go build -o "$RUN_DIR/soakaudit" ./cmd/soakaudit)

echo "==> starting flaky HTTP targets"
FLAKY_PORTS=""
for i in $(seq 0 $((MONITOR_COUNT / 2 - 1))); do
  port=$((FLAKY_BASE_PORT + i))
  FLAKY_PORTS="${FLAKY_PORTS:+$FLAKY_PORTS,}$port"
done
"$RUN_DIR/soakflakytarget" --ports="$FLAKY_PORTS" >"$RUN_DIR/flakytarget.log" 2>&1 &
FLAKY_PID=$!
echo "$FLAKY_PID" >"$RUN_DIR/flakytarget.pid"

# app_env execs the app binary with its full env block, replacing the calling
# shell's process image — so when supervise_app backgrounds it, $! is the
# actual app pid (not a wrapper subshell's), and chaos.sh's SIGTERM reaches
# the real process directly. PING_ENV=test disables the auth rate limiter for
# loadgen's setup burst (same escape hatch frontend/e2e relies on) and is
# never used in dev/production.
app_env() {
  exec env \
    PING_PORT="$APP_PORT" \
    PING_ENV=test \
    PING_BASE_URL="$APP_BASE_URL" \
    DATABASE_URL="$DATABASE_URL" \
    REDIS_URL="$REDIS_URL" \
    CORS_ALLOWED_ORIGIN="http://localhost:3000" \
    JWT_PRIVATE_KEY_PATH="$RUN_DIR/keys/jwt_private.pem" \
    JWT_PUBLIC_KEY_PATH="$RUN_DIR/keys/jwt_public.pem" \
    JWT_ACCESS_TTL=15m \
    JWT_REFRESH_TTL=720h \
    REGISTRATION_OPEN=true \
    SSRF_ALLOWLIST="127.0.0.1/32,::1/128" \
    "$@"
}

echo "==> starting app (supervised respawn loop, so chaos SIGTERMs get replaced)"
supervise_app() {
  while [ ! -f "$RUN_DIR/chaos.stop" ]; do
    (app_env "$RUN_DIR/ping" --role=all) >>"$RUN_DIR/app.log" 2>&1 &
    local pid=$!
    echo "$pid" >"$RUN_DIR/app.pid"
    echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) app started pid=$pid" >>"$RUN_DIR/chaos.log"
    wait "$pid" 2>/dev/null
    echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) app exited, respawning" >>"$RUN_DIR/chaos.log"
    sleep 1
  done
}
supervise_app &
SUPERVISOR_PID=$!

echo "==> waiting for app to be reachable"
for _ in $(seq 1 30); do
  curl -sf "$APP_BASE_URL/health" >/dev/null 2>&1 && break
  sleep 1
done

echo "==> starting load generator"
"$RUN_DIR/soakloadgen" \
  --base-url="$APP_BASE_URL" \
  --monitor-count="$MONITOR_COUNT" \
  --flaky-target-base-url="http://127.0.0.1" \
  --flaky-target-base-port="$FLAKY_BASE_PORT" \
  >"$RUN_DIR/loadgen.log" 2>&1 &
LOADGEN_PID=$!

echo "==> starting chaos loop (interval ${CHAOS_MIN_INTERVAL_S}-${CHAOS_MAX_INTERVAL_S}s)"
# shellcheck source=./chaos.sh
source "$SCRIPT_DIR/chaos.sh"
chaos_loop &
CHAOS_PID=$!

echo "==> running for $SOAK_DURATION"
sleep_duration_s=$(parse_duration_seconds "$SOAK_DURATION")
sleep "$sleep_duration_s"

echo "==> stopping chaos loop and load generator"
touch "$RUN_DIR/chaos.stop"
kill "$CHAOS_PID" 2>/dev/null || true
kill "$LOADGEN_PID" 2>/dev/null || true
kill "$SUPERVISOR_PID" 2>/dev/null || true
wait "$CHAOS_PID" 2>/dev/null || true

echo "==> running invariant audit"
"$RUN_DIR/soakaudit" --database-url="$DATABASE_URL" | tee "$RUN_DIR/report.md"

echo ""
echo "soak run complete. Artifacts in: $RUN_DIR"
echo "  app.log, loadgen.log, flakytarget.log, chaos.log, report.md"
echo "Stack ($COMPOSE_PROJECT) left running — tear down with:"
echo "  docker compose -p $COMPOSE_PROJECT -f docker-compose.yml -f hack/soak/docker-compose.soak.yml down -v"
