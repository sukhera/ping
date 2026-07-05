import type { HTTPConfig, Monitor, UpdateMonitorRequest } from "@/types/monitor";

// Maps each UpdateMonitorRequest key to how it reads off a Monitor, so the
// comparison is spelled out once instead of relying on same-named keys.
const MONITOR_FIELD: {
  [K in keyof UpdateMonitorRequest]: (m: Monitor) => UpdateMonitorRequest[K];
} = {
  name: (m) => m.name,
  auto_resume: (m) => m.auto_resume,
  schedule_kind: (m) => m.schedule_kind,
  period_s: (m) => m.period_s,
  cron_expr: (m) => m.cron_expr,
  tz: (m) => m.tz,
  grace_s: (m) => m.grace_s,
  url: (m) => m.url,
  method: (m) => m.method,
  interval_s: (m) => m.interval_s,
  timeout_s: (m) => m.timeout_s,
  fail_threshold: (m) => m.fail_threshold,
  http_config: (m) => m.http_config,
};

// The server stores http_config as whatever JSON was last sent — an
// as-yet-untouched HTTP monitor has {} (see backend/store: an absent
// http_config on create still round-trips as {}), not the form's own
// defaults (keyword_negate: false, follow_redirects: true, etc). Comparing
// the two objects structurally (rather than isEqual/stringify on the raw
// values) means "the form's reconstructed object happens to spell out
// defaults the stored blob left implicit" is correctly treated as no change.
function normalizeHTTPConfig(cfg: HTTPConfig | undefined) {
  return {
    headers: cfg?.headers && Object.keys(cfg.headers).length > 0 ? cfg.headers : undefined,
    keyword: cfg?.keyword || undefined,
    keyword_negate: cfg?.keyword_negate ?? false,
    follow_redirects: cfg?.follow_redirects ?? true,
  };
}

function httpConfigEqual(a: HTTPConfig | undefined, b: HTTPConfig | undefined): boolean {
  return JSON.stringify(normalizeHTTPConfig(a)) === JSON.stringify(normalizeHTTPConfig(b));
}

function valuesEqual(key: keyof UpdateMonitorRequest, a: unknown, b: unknown): boolean {
  if (key === "http_config") {
    return httpConfigEqual(a as HTTPConfig | undefined, b as HTTPConfig | undefined);
  }
  return a === b;
}

/**
 * Reduces a full form-built request body down to only the fields that
 * actually differ from `monitor`'s current values. The backend records a
 * config_change event for any field present in a PATCH body regardless of
 * whether its value matches what's already stored (store/monitor.go's
 * changedFieldsMeta checks "was this field sent", not "did it change") — so
 * an edit form that resubmits every field on every save would spam the
 * event timeline. This is what makes an unchanged submit a true no-op
 * (PING-015 AC).
 */
export function diffMonitorUpdate(
  monitor: Monitor,
  body: UpdateMonitorRequest,
): UpdateMonitorRequest {
  const diff: UpdateMonitorRequest = {};
  // Object.keys(MONITOR_FIELD), not Object.keys(body): callers may pass a
  // CreateMonitorRequest-shaped body (MonitorForm always builds one, even
  // when editing — see MonitorFormProps.onSubmit), which carries a "kind"
  // key that isn't part of UpdateMonitorRequest and must never reach the
  // PATCH body.
  for (const key of Object.keys(MONITOR_FIELD) as (keyof UpdateMonitorRequest)[]) {
    if (!(key in body)) continue;
    const next = body[key];
    const current = MONITOR_FIELD[key]?.(monitor);
    if (!valuesEqual(key, next, current)) {
      (diff[key] as unknown) = next;
    }
  }
  return diff;
}
