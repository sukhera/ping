#!/usr/bin/env bash
# Sourced by run.sh. Provides the chaos loop: every random interval, pick one
# of restart-postgres / restart-redis / restart-app / no-op, log it, and apply
# it. Never restarts Postgres and Redis in the same action — at least one
# dependency stays up so the app can make progress between chaos events.
#
# Expects these to already be set by run.sh: RUN_DIR, COMPOSE_PROJECT,
# CHAOS_MIN_INTERVAL_S, CHAOS_MAX_INTERVAL_S, APP_PID (via app_pid_file).

CHAOS_LOG="$RUN_DIR/chaos.log"

chaos_log() {
  echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) $*" >>"$CHAOS_LOG"
}

# restart_app sends SIGTERM to the currently running app process (graceful
# shutdown, matching main.go's signal.NotifyContext handling) and waits for
# run.sh's supervisor loop to respawn it. run.sh's app supervisor (a small
# respawn loop) is what actually restarts the binary; this function only
# triggers the kill.
chaos_restart_app() {
  local pid
  pid=$(cat "$RUN_DIR/app.pid" 2>/dev/null) || return 0
  if [ -z "$pid" ] || ! kill -0 "$pid" 2>/dev/null; then
    chaos_log "restart_app: no live process at pid=$pid, skipping"
    return 0
  fi
  chaos_log "restart_app: SIGTERM pid=$pid"
  kill -TERM "$pid" 2>/dev/null || true
}

chaos_restart_postgres() {
  chaos_log "restart_postgres: docker compose restart postgres"
  docker compose -p "$COMPOSE_PROJECT" restart postgres >>"$RUN_DIR/docker.log" 2>&1 || true
}

chaos_restart_redis() {
  chaos_log "restart_redis: docker compose restart redis"
  docker compose -p "$COMPOSE_PROJECT" restart redis >>"$RUN_DIR/docker.log" 2>&1 || true
}

# chaos_loop runs until the file $RUN_DIR/chaos.stop appears (run.sh creates it
# to signal the loop to exit before waiting on it).
chaos_loop() {
  chaos_log "chaos loop started (interval ${CHAOS_MIN_INTERVAL_S}-${CHAOS_MAX_INTERVAL_S}s)"
  while [ ! -f "$RUN_DIR/chaos.stop" ]; do
    local interval=$((CHAOS_MIN_INTERVAL_S + RANDOM % (CHAOS_MAX_INTERVAL_S - CHAOS_MIN_INTERVAL_S)))
    sleep "$interval" &
    local sleep_pid=$!
    wait "$sleep_pid" 2>/dev/null

    [ -f "$RUN_DIR/chaos.stop" ] && break

    case $((RANDOM % 4)) in
      0) chaos_restart_postgres ;;
      1) chaos_restart_redis ;;
      2) chaos_restart_app ;;
      3) chaos_log "no-op tick" ;;
    esac
  done
  chaos_log "chaos loop stopped"
}
