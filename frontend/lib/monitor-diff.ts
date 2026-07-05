import type { Monitor, UpdateMonitorRequest } from "@/types/monitor";

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

function valuesEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  // http_config (and its nested headers) is the only object-valued field;
  // everything else is a primitive already caught by ===.
  if (typeof a === "object" || typeof b === "object") {
    return JSON.stringify(a ?? null) === JSON.stringify(b ?? null);
  }
  return false;
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
    if (!valuesEqual(next, current)) {
      (diff[key] as unknown) = next;
    }
  }
  return diff;
}
