export type MonitorKind = "heartbeat" | "http";
export type MonitorState = "new" | "up" | "late" | "down";
export type MonitorDisplayState = MonitorState | "paused";

export type DailyStat = {
  day: string; // "2026-07-03"
  checkins: number;
  failures: number;
  downtime_s: number;
};

export type ScheduleKind = "period" | "cron";

export type HTTPConfig = {
  headers?: Record<string, string>;
  keyword?: string;
  keyword_negate?: boolean;
  follow_redirects?: boolean;
};

export type Monitor = {
  id: string;
  kind: MonitorKind;
  slug: string;
  name: string;
  state: MonitorState;
  display_state: MonitorDisplayState;
  ping_url?: string;
  schedule_kind?: ScheduleKind;
  period_s?: number;
  cron_expr?: string;
  tz?: string;
  grace_s?: number;
  url?: string;
  method?: string;
  interval_s?: number;
  timeout_s?: number;
  fail_threshold?: number;
  http_config?: HTTPConfig;
  fail_streak: number;
  alerts_muted: boolean;
  auto_resume: boolean;
  last_checkin_at?: string;
  next_deadline?: string;
  paused_at?: string;
  created_at: string;
  updated_at: string;
  schedule_summary?: string;
  daily_stats?: DailyStat[];
};

// Shared by create/update monitor requests and the schedule-describe preview
// request — mirrors backend/server/monitor.go's scheduleFields.
export type ScheduleFields = {
  schedule_kind?: ScheduleKind;
  period_s?: number;
  cron_expr?: string;
  tz?: string;
  grace_s?: number;
};

export type HTTPFields = {
  url?: string;
  method?: string;
  interval_s?: number;
  timeout_s?: number;
  fail_threshold?: number;
  http_config?: HTTPConfig;
};

export type CreateMonitorRequest = {
  kind: MonitorKind;
  name: string;
  auto_resume?: boolean;
} & ScheduleFields &
  HTTPFields;

export type UpdateMonitorRequest = {
  name?: string;
  auto_resume?: boolean;
} & ScheduleFields &
  HTTPFields;

export type DescribeScheduleRequest = ScheduleFields;

export type DescribeScheduleResponse = {
  description: string;
  next_runs?: string[];
};

export type MonitorListResponse = {
  monitors: Monitor[];
  next_cursor?: string;
};

export type MonitorListParams = {
  q?: string;
  kind?: MonitorKind;
  state?: MonitorDisplayState;
  cursor?: string;
  limit?: number;
};

export type CheckinKind = "success" | "start" | "fail";

export type Checkin = {
  id: number;
  monitor_id: string;
  kind: CheckinKind;
  source_ip?: string;
  user_agent?: string;
  body?: string;
  created_at: string;
};

export type CheckinListResponse = {
  checkins: Checkin[];
  next_cursor?: string;
};

export type CheckinListParams = {
  cursor?: string;
  limit?: number;
};

export type MonitorEvent = {
  id: number;
  monitor_id: string;
  type: string;
  message: string;
  meta?: Record<string, unknown>;
  created_at: string;
};

export type EventListResponse = {
  events: MonitorEvent[];
  next_cursor?: string;
};

export type EventListParams = {
  type?: string;
  cursor?: string;
  limit?: number;
};

export type ProbeResult = {
  id: number;
  monitor_id: string;
  ok: boolean;
  http_status?: number;
  latency_ms?: number;
  error?: string;
  tls_expires_at?: string;
  created_at: string;
};

export type ProbeResultListResponse = {
  results: ProbeResult[];
  next_cursor?: string;
};

export type ProbeResultListParams = {
  outcome?: "success" | "fail";
  cursor?: string;
  limit?: number;
};

export type LatencyWindow = "24h" | "7d" | "30d";

export type LatencyPoint = {
  bucket_start: string;
  p50: number;
  p95: number;
  avg: number;
  sample_count: number;
};

export type LatencySeriesResponse = {
  window: LatencyWindow;
  points: LatencyPoint[];
};
