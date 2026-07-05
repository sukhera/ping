import type { Monitor } from "@/types/monitor";

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-baseline justify-between gap-3 py-1.5 text-[12.5px]">
      <span className="text-text-faint">{label}</span>
      <span className="mono truncate text-text" title={value}>
        {value}
      </span>
    </div>
  );
}

/**
 * DESIGN.md §7.2 HTTP detail: config summary (method/url/interval/timeout/
 * fail threshold + the advanced http_config fields set on create/edit).
 */
export function HTTPConfigSummary({ monitor, className }: { monitor: Monitor; className?: string }) {
  const cfg = monitor.http_config;

  return (
    <div className={className}>
      <h2 className="mb-3 text-sm font-medium text-text">HTTP config</h2>
      <div className="divide-y divide-border rounded-[var(--radius)] border border-border bg-surface px-4">
        <Row label="Method" value={monitor.method ?? "GET"} />
        <Row label="URL" value={monitor.url ?? "—"} />
        {monitor.interval_s !== undefined && <Row label="Interval" value={`${monitor.interval_s}s`} />}
        {monitor.timeout_s !== undefined && <Row label="Timeout" value={`${monitor.timeout_s}s`} />}
        {monitor.fail_threshold !== undefined && (
          <Row label="Fail threshold" value={`${monitor.fail_threshold} consecutive`} />
        )}
        {cfg?.keyword && (
          <Row
            label="Keyword"
            value={cfg.keyword_negate ? `absent: ${cfg.keyword}` : `present: ${cfg.keyword}`}
          />
        )}
        {cfg?.follow_redirects !== undefined && (
          <Row label="Follow redirects" value={cfg.follow_redirects ? "yes" : "no"} />
        )}
        {cfg?.headers && Object.keys(cfg.headers).length > 0 && (
          <Row label="Headers" value={Object.keys(cfg.headers).join(", ")} />
        )}
      </div>
    </div>
  );
}
