export type MonitorKind = "heartbeat" | "http";
export type MonitorState = "new" | "up" | "late" | "down";
export type MonitorDisplayState = MonitorState | "paused";

export type DailyStat = {
  day: string; // "2026-07-03"
  checkins: number;
  failures: number;
  downtime_s: number;
};

export type Monitor = {
  id: string;
  kind: MonitorKind;
  slug: string;
  name: string;
  state: MonitorState;
  display_state: MonitorDisplayState;
  ping_url?: string;
  schedule_kind?: "period" | "cron";
  period_s?: number;
  cron_expr?: string;
  tz?: string;
  grace_s?: number;
  url?: string;
  method?: string;
  interval_s?: number;
  timeout_s?: number;
  fail_threshold?: number;
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
