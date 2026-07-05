"use client";

import { zodResolver } from "@hookform/resolvers/zod";
import { useEffect, useMemo, useState } from "react";
import { useForm, useWatch } from "react-hook-form";
import { z } from "zod";

import { Button } from "@/components/ui/button";
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { useDescribeSchedule } from "@/hooks/use-monitors";
import { ApiError } from "@/lib/api";
import { cn } from "@/lib/utils";

/**
 * Radix's Select fires an onValueChange("") during its own mount/content-
 * registration sequence when it renders with a non-empty controlled `value`
 * before its SelectItems have registered (e.g. a field whose branch — http
 * vs heartbeat — just became visible via a kind switch). None of this
 * form's selects have a legitimate empty-string option, so treating that
 * call as a no-op avoids it silently wiping a valid default like method
 * ("GET") the instant the http fields mount.
 */
function onSelectChange<T extends string>(onChange: (value: T) => void) {
  return (value: string) => {
    if (value === "") return;
    onChange(value as T);
  };
}
import type { CreateMonitorRequest, Monitor } from "@/types/monitor";

const TIMEZONES: string[] =
  typeof Intl.supportedValuesOf === "function"
    ? Intl.supportedValuesOf("timeZone")
    : ["UTC"];

const PERIOD_PRESETS = [
  { label: "Every minute", seconds: 60 },
  { label: "Every 5 minutes", seconds: 5 * 60 },
  { label: "Every 15 minutes", seconds: 15 * 60 },
  { label: "Every hour", seconds: 60 * 60 },
  { label: "Every day", seconds: 24 * 60 * 60 },
];

const GRACE_PRESETS = [
  { label: "5 minutes", seconds: 5 * 60 },
  { label: "15 minutes", seconds: 15 * 60 },
  { label: "30 minutes", seconds: 30 * 60 },
  { label: "1 hour", seconds: 60 * 60 },
  { label: "1 day", seconds: 24 * 60 * 60 },
];

const monitorFormSchema = z
  .object({
    kind: z.enum(["heartbeat", "http"]),
    name: z.string().trim().min(1, "Name is required."),

    // heartbeat
    scheduleKind: z.enum(["period", "cron"]).optional(),
    periodS: z.number().int().optional(),
    cronExpr: z.string().optional(),
    tz: z.string().optional(),
    graceS: z.number().int().optional(),

    // http
    url: z.string().optional(),
    method: z.enum(["GET", "HEAD"]).optional(),
    intervalS: z.number().int().optional(),
    timeoutS: z.number().int().optional(),
    failThreshold: z.number().int().optional(),
    headersText: z.string().optional(),
    keyword: z.string().optional(),
    keywordNegate: z.boolean().optional(),
    followRedirects: z.boolean().optional(),
    autoResume: z.boolean().optional(),
  })
  .superRefine((values, ctx) => {
    if (values.kind === "heartbeat") {
      if (!values.tz) {
        ctx.addIssue({ code: "custom", path: ["tz"], message: "Timezone is required." });
      }
      if (values.scheduleKind === "cron") {
        if (!values.cronExpr?.trim()) {
          ctx.addIssue({ code: "custom", path: ["cronExpr"], message: "Cron expression is required." });
        }
      } else if (!values.periodS || values.periodS <= 0) {
        ctx.addIssue({ code: "custom", path: ["periodS"], message: "Interval is required." });
      }
      if (!values.graceS || values.graceS <= 0) {
        ctx.addIssue({ code: "custom", path: ["graceS"], message: "Grace period is required." });
      }
    } else {
      if (!values.url?.trim()) {
        ctx.addIssue({ code: "custom", path: ["url"], message: "URL is required." });
      } else {
        try {
          const parsed = new URL(values.url);
          if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
            throw new Error("bad protocol");
          }
        } catch {
          ctx.addIssue({ code: "custom", path: ["url"], message: "Must be a valid http or https URL." });
        }
      }
      if (values.headersText?.trim()) {
        try {
          const parsed: unknown = JSON.parse(values.headersText);
          if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
            throw new Error("not an object");
          }
        } catch {
          ctx.addIssue({
            code: "custom",
            path: ["headersText"],
            message: "Must be valid JSON (an object of header name/value pairs).",
          });
        }
      }
    }
  });

export type MonitorFormValues = z.infer<typeof monitorFormSchema>;

function defaultsFromMonitor(monitor?: Monitor): Partial<MonitorFormValues> {
  if (!monitor) {
    return {
      kind: "heartbeat",
      name: "",
      scheduleKind: "period",
      periodS: 300,
      cronExpr: "",
      tz: Intl.DateTimeFormat().resolvedOptions().timeZone,
      graceS: 300,
      url: "",
      method: "GET",
      // PRD F2.1/F2.2 defaults for a new http monitor.
      intervalS: 60,
      timeoutS: 10,
      failThreshold: 2,
      headersText: "",
      keyword: "",
      keywordNegate: false,
      followRedirects: true,
      autoResume: true,
    };
  }
  return {
    kind: monitor.kind,
    name: monitor.name,
    scheduleKind: monitor.schedule_kind ?? "period",
    periodS: monitor.period_s,
    cronExpr: monitor.cron_expr ?? "",
    tz: monitor.tz ?? Intl.DateTimeFormat().resolvedOptions().timeZone,
    graceS: monitor.grace_s,
    url: monitor.url ?? "",
    method: (monitor.method as "GET" | "HEAD") ?? "GET",
    intervalS: monitor.interval_s,
    timeoutS: monitor.timeout_s,
    failThreshold: monitor.fail_threshold,
    headersText: monitor.http_config?.headers
      ? JSON.stringify(monitor.http_config.headers, null, 2)
      : "",
    keyword: monitor.http_config?.keyword ?? "",
    keywordNegate: monitor.http_config?.keyword_negate ?? false,
    followRedirects: monitor.http_config?.follow_redirects ?? true,
    autoResume: monitor.auto_resume,
  };
}

/** Builds the API request body from validated form values — shared by
 * create and update since both accept the same field shape (update just
 * omits kind, which isn't editable). */
function toRequestBody(values: MonitorFormValues): CreateMonitorRequest {
  if (values.kind === "heartbeat") {
    return {
      kind: "heartbeat",
      name: values.name,
      schedule_kind: values.scheduleKind,
      period_s: values.scheduleKind === "period" ? values.periodS : undefined,
      cron_expr: values.scheduleKind === "cron" ? values.cronExpr : undefined,
      tz: values.tz,
      grace_s: values.graceS,
      auto_resume: values.autoResume,
    };
  }

  let headers: Record<string, string> | undefined;
  if (values.headersText?.trim()) {
    try {
      headers = JSON.parse(values.headersText) as Record<string, string>;
    } catch {
      headers = undefined;
    }
  }

  return {
    kind: "http",
    name: values.name,
    url: values.url,
    method: values.method,
    interval_s: values.intervalS,
    timeout_s: values.timeoutS,
    fail_threshold: values.failThreshold,
    auto_resume: values.autoResume,
    http_config: {
      headers,
      keyword: values.keyword?.trim() || undefined,
      keyword_negate: values.keywordNegate,
      follow_redirects: values.followRedirects,
    },
  };
}

// backend field name -> form field name, for mapping 422 fieldErrorResponse
// onto the right react-hook-form control (server/monitor.go's writeFieldError).
const FIELD_MAP: Record<string, keyof MonitorFormValues> = {
  name: "name",
  schedule_kind: "scheduleKind",
  period_s: "periodS",
  cron_expr: "cronExpr",
  tz: "tz",
  grace_s: "graceS",
  url: "url",
  method: "method",
  interval_s: "intervalS",
  timeout_s: "timeoutS",
  fail_threshold: "failThreshold",
  http_config: "headersText",
  kind: "kind",
};

type KindCardProps = {
  value: "heartbeat" | "http";
  label: string;
  glyph: string;
  description: string;
  selected: boolean;
  onSelect: () => void;
};

function KindCard({ value, label, glyph, description, selected, onSelect }: KindCardProps) {
  return (
    <label
      className={cn(
        "relative flex flex-1 cursor-pointer flex-col gap-1 rounded-lg border p-4 transition-colors",
        "has-[:focus-visible]:border-accent has-[:focus-visible]:ring-3 has-[:focus-visible]:ring-accent/50",
        selected ? "border-accent bg-surface-2" : "border-border bg-surface hover:bg-surface-2",
      )}
    >
      <input
        type="radio"
        name="kind"
        value={value}
        checked={selected}
        onChange={onSelect}
        // Not sr-only: Chromium drops zero-visible-area (clip-rect) inputs
        // from sequential Tab order even though they remain .focus()-able,
        // which silently broke the "create a monitor without a mouse" path
        // (PING-015 AC). An absolutely-positioned, opacity-0 input stays in
        // the tab order while still being visually replaced by the card
        // content below it.
        className="absolute inset-0 z-10 m-0 size-full cursor-pointer opacity-0"
      />
      <span className="text-lg">{glyph}</span>
      <span className="font-medium text-text">{label}</span>
      <span className="text-[12.5px] text-text-dim">{description}</span>
    </label>
  );
}

export type MonitorFormProps = {
  monitor?: Monitor;
  // Always the full CreateMonitorRequest shape (kind + name always present)
  // even when editing — the edit page's onSubmit is responsible for diffing
  // this down to only the fields that changed (lib/monitor-diff.ts) before
  // calling the update API, since kind/name are required here but optional
  // on UpdateMonitorRequest.
  onSubmit: (body: CreateMonitorRequest) => Promise<void>;
  submitLabel: string;
  submittingLabel: string;
};

export function MonitorForm({ monitor, onSubmit, submitLabel, submittingLabel }: MonitorFormProps) {
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);

  const form = useForm<MonitorFormValues>({
    resolver: zodResolver(monitorFormSchema),
    mode: "onBlur",
    defaultValues: defaultsFromMonitor(monitor),
  });

  const kind = useWatch({ control: form.control, name: "kind" });
  const scheduleKind = useWatch({ control: form.control, name: "scheduleKind" });
  const periodS = useWatch({ control: form.control, name: "periodS" });
  const cronExpr = useWatch({ control: form.control, name: "cronExpr" });
  const tz = useWatch({ control: form.control, name: "tz" });
  const graceS = useWatch({ control: form.control, name: "graceS" });

  const describeBody = useMemo(
    () => ({
      schedule_kind: scheduleKind,
      period_s: scheduleKind === "period" ? periodS : undefined,
      cron_expr: scheduleKind === "cron" ? cronExpr : undefined,
      tz,
      grace_s: graceS,
    }),
    [scheduleKind, periodS, cronExpr, tz, graceS],
  );

  const describeEnabled =
    kind === "heartbeat" &&
    !!tz &&
    !!graceS &&
    (scheduleKind === "period" ? !!periodS : !!cronExpr?.trim());

  const [debouncedBody, setDebouncedBody] = useState(describeBody);
  useEffect(() => {
    const timer = setTimeout(() => setDebouncedBody(describeBody), 300);
    return () => clearTimeout(timer);
  }, [describeBody]);

  const describeQuery = useDescribeSchedule(debouncedBody, describeEnabled);
  const describeError =
    describeQuery.error instanceof ApiError ? describeQuery.error.message : null;

  async function handleSubmit(values: MonitorFormValues) {
    setFormError(null);
    const body = toRequestBody(values);
    try {
      await onSubmit(body);
    } catch (err) {
      if (err instanceof ApiError && err.status === 422) {
        const field = err.field ? FIELD_MAP[err.field] : undefined;
        if (field) {
          form.setError(field, { type: "server", message: err.message });
          return;
        }
      }
      setFormError(
        err instanceof ApiError ? err.message : "Unable to reach the server.",
      );
    }
  }

  return (
    <Form {...form}>
      <form onSubmit={form.handleSubmit(handleSubmit)} className="flex flex-col gap-6">
        {!monitor && (
          <div className="flex gap-3">
            <KindCard
              value="heartbeat"
              label="Heartbeat"
              glyph="⌁"
              description="A job pings us on a schedule; we alert if it's late."
              selected={kind === "heartbeat"}
              onSelect={() => form.setValue("kind", "heartbeat", { shouldValidate: true })}
            />
            <KindCard
              value="http"
              label="HTTP check"
              glyph="⇄"
              description="We poll a URL; we alert if it stops responding."
              selected={kind === "http"}
              onSelect={() => form.setValue("kind", "http", { shouldValidate: true })}
            />
          </div>
        )}

        <FormField
          control={form.control}
          name="name"
          render={({ field }) => (
            <FormItem>
              <FormLabel>Name</FormLabel>
              <FormControl>
                <Input autoFocus placeholder="nightly-backup" {...field} />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />

        {kind === "heartbeat" ? (
          <>
            <FormField
              control={form.control}
              name="scheduleKind"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>Schedule</FormLabel>
                  <Select
                    value={field.value}
                    onValueChange={onSelectChange<"period" | "cron">(field.onChange)}
                  >
                    <FormControl>
                      <SelectTrigger className="w-full">
                        <SelectValue />
                      </SelectTrigger>
                    </FormControl>
                    <SelectContent>
                      <SelectItem value="period">Simple interval</SelectItem>
                      <SelectItem value="cron">Cron expression</SelectItem>
                    </SelectContent>
                  </Select>
                  <FormMessage />
                </FormItem>
              )}
            />

            {scheduleKind === "period" ? (
              <FormField
                control={form.control}
                name="periodS"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Expected every</FormLabel>
                    <Select
                      value={String(field.value ?? "")}
                      onValueChange={onSelectChange<string>((v) => field.onChange(Number(v)))}
                    >
                      <FormControl>
                        <SelectTrigger className="w-full">
                          <SelectValue placeholder="Choose an interval" />
                        </SelectTrigger>
                      </FormControl>
                      <SelectContent>
                        {PERIOD_PRESETS.map((p) => (
                          <SelectItem key={p.seconds} value={String(p.seconds)}>
                            {p.label}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                    <FormMessage />
                  </FormItem>
                )}
              />
            ) : (
              <FormField
                control={form.control}
                name="cronExpr"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Cron expression</FormLabel>
                    <FormControl>
                      <Input placeholder="0 4 * * *" className="mono" {...field} />
                    </FormControl>
                    <FormDescription>Standard 5-field syntax (minute hour dom month dow).</FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            )}

            <FormField
              control={form.control}
              name="tz"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>Timezone</FormLabel>
                  <Select value={field.value} onValueChange={onSelectChange<string>(field.onChange)}>
                    <FormControl>
                      <SelectTrigger className="w-full">
                        <SelectValue placeholder="Choose a timezone" />
                      </SelectTrigger>
                    </FormControl>
                    <SelectContent>
                      {TIMEZONES.map((z) => (
                        <SelectItem key={z} value={z}>
                          {z}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name="graceS"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>Alert if late by</FormLabel>
                  <Select
                    value={String(field.value ?? "")}
                    onValueChange={onSelectChange<string>((v) => field.onChange(Number(v)))}
                  >
                    <FormControl>
                      <SelectTrigger className="w-full">
                        <SelectValue placeholder="Choose a grace period" />
                      </SelectTrigger>
                    </FormControl>
                    <SelectContent>
                      {GRACE_PRESETS.map((p) => (
                        <SelectItem key={p.seconds} value={String(p.seconds)}>
                          {p.label}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  <FormMessage />
                </FormItem>
              )}
            />

            <div
              className="rounded-lg border border-border bg-surface-2 p-4 text-sm"
              aria-live="polite"
            >
              {describeError ? (
                <span className="text-down">{describeError}</span>
              ) : describeQuery.data ? (
                <div className="flex flex-col gap-2">
                  <p className="text-text">
                    Expects a ping <strong>{describeQuery.data.description}</strong>
                  </p>
                  {describeQuery.data.next_runs && describeQuery.data.next_runs.length > 0 && (
                    <div className="mono text-[12.5px] text-text-dim">
                      Next runs:{" "}
                      {describeQuery.data.next_runs
                        .map((r) => new Date(r).toLocaleString())
                        .join(" · ")}
                    </div>
                  )}
                </div>
              ) : (
                <span className="text-text-faint">Fill in the schedule to see a preview.</span>
              )}
            </div>
          </>
        ) : (
          <>
            <FormField
              control={form.control}
              name="url"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>URL</FormLabel>
                  <FormControl>
                    <Input type="url" placeholder="https://example.com/health" {...field} />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name="method"
              render={({ field }) => (
                <FormItem>
                  <FormLabel>Method</FormLabel>
                  <Select value={field.value} onValueChange={onSelectChange<"GET" | "HEAD">(field.onChange)}>
                    <FormControl>
                      <SelectTrigger className="w-full">
                        <SelectValue />
                      </SelectTrigger>
                    </FormControl>
                    <SelectContent>
                      <SelectItem value="GET">GET</SelectItem>
                      <SelectItem value="HEAD">HEAD</SelectItem>
                    </SelectContent>
                  </Select>
                  <FormMessage />
                </FormItem>
              )}
            />

            <div className="grid grid-cols-2 gap-4">
              <FormField
                control={form.control}
                name="intervalS"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Check every (seconds)</FormLabel>
                    <FormControl>
                      <Input
                        type="number"
                        min={30}
                        max={86400}
                        value={field.value ?? ""}
                        onChange={(e) => field.onChange(e.target.value === "" ? undefined : Number(e.target.value))}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name="timeoutS"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Timeout (seconds)</FormLabel>
                    <FormControl>
                      <Input
                        type="number"
                        min={1}
                        max={30}
                        value={field.value ?? ""}
                        onChange={(e) => field.onChange(e.target.value === "" ? undefined : Number(e.target.value))}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>
          </>
        )}

        <div>
          <button
            type="button"
            onClick={() => setAdvancedOpen((v) => !v)}
            className="text-sm font-medium text-accent"
            aria-expanded={advancedOpen}
          >
            {advancedOpen ? "Hide advanced" : "Advanced"}
          </button>
        </div>

        {advancedOpen && (
          <div className="flex flex-col gap-4 rounded-lg border border-border p-4">
            {kind === "http" && (
              <>
                <FormField
                  control={form.control}
                  name="failThreshold"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Confirmation threshold</FormLabel>
                      <FormControl>
                        <Input
                          type="number"
                          min={1}
                          max={10}
                          value={field.value ?? ""}
                          onChange={(e) => field.onChange(e.target.value === "" ? undefined : Number(e.target.value))}
                        />
                      </FormControl>
                      <FormDescription>
                        Consecutive failures required before alerting down.
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name="keyword"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Body must contain</FormLabel>
                      <FormControl>
                        <Input placeholder="ok" {...field} />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name="keywordNegate"
                  render={({ field }) => (
                    <FormItem className="flex flex-row items-center justify-between gap-2 space-y-0">
                      <FormLabel>Invert (fail if keyword found)</FormLabel>
                      <FormControl>
                        <Switch checked={field.value} onCheckedChange={field.onChange} />
                      </FormControl>
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name="followRedirects"
                  render={({ field }) => (
                    <FormItem className="flex flex-row items-center justify-between gap-2 space-y-0">
                      <FormLabel>Follow redirects</FormLabel>
                      <FormControl>
                        <Switch checked={field.value} onCheckedChange={field.onChange} />
                      </FormControl>
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name="headersText"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Request headers</FormLabel>
                      <FormControl>
                        <Textarea
                          rows={4}
                          className="mono"
                          placeholder={'{\n  "Authorization": "Bearer …"\n}'}
                          {...field}
                        />
                      </FormControl>
                      <FormDescription>JSON object of header name/value pairs.</FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </>
            )}
            <FormField
              control={form.control}
              name="autoResume"
              render={({ field }) => (
                <FormItem className="flex flex-row items-center justify-between gap-2 space-y-0">
                  <FormLabel>Auto-resume on next ping/probe</FormLabel>
                  <FormControl>
                    <Switch checked={field.value} onCheckedChange={field.onChange} />
                  </FormControl>
                </FormItem>
              )}
            />
          </div>
        )}

        {formError && (
          <p role="alert" className="text-sm text-down">
            {formError}
          </p>
        )}

        <Button type="submit" disabled={form.formState.isSubmitting}>
          {form.formState.isSubmitting ? submittingLabel : submitLabel}
        </Button>
      </form>
    </Form>
  );
}
