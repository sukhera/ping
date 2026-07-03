import { cn } from "@/lib/utils";
import type { MonitorDisplayState } from "@/types/monitor";

const LABELS: Record<MonitorDisplayState, string> = {
  up: "up",
  down: "down",
  late: "late",
  new: "new",
  paused: "paused",
};

/**
 * Shape + color + text, never color alone (DESIGN.md §4/§10, WCAG 1.4.1).
 * aria-label carries the text redundancy programmatically (the mockup only
 * uses `title`, which isn't reliably exposed to assistive tech).
 */
export function StatusChip({
  state,
  size = "sm",
  pulse = false,
  pulseKey,
}: {
  state: MonitorDisplayState;
  size?: "sm" | "lg";
  /** Renders one pulse ring when true and state is "up". */
  pulse?: boolean;
  /**
   * Remount key for the pulse ring (typically the monitor's last_checkin_at)
   * so each fresh check-in restarts the one-shot CSS animation without any
   * JS timer/state — the animation itself (no `infinite`) stops after 600ms.
   */
  pulseKey?: string;
}) {
  const dim = size === "lg" ? "size-3.5" : "size-2.5";

  return (
    <span
      role="img"
      aria-label={`status: ${LABELS[state]}`}
      className="relative inline-flex items-center justify-center"
    >
      {pulse && state === "up" && (
        <span
          key={pulseKey}
          aria-hidden="true"
          className={cn(
            "absolute inset-0 rounded-full bg-up motion-safe:animate-[status-pulse_600ms_ease-out]",
            "motion-reduce:hidden",
          )}
        />
      )}
      {state === "up" && <span className={cn(dim, "rounded-full bg-up")} />}
      {state === "down" && <span className={cn(dim, "rounded-[2px] bg-down")} />}
      {state === "late" && (
        <span
          aria-hidden="true"
          className="border-x-transparent border-b-late"
          style={{
            width: 0,
            height: 0,
            borderLeftWidth: size === "lg" ? 7 : 5.5,
            borderRightWidth: size === "lg" ? 7 : 5.5,
            borderBottomWidth: size === "lg" ? 12 : 10,
            borderStyle: "solid",
          }}
        />
      )}
      {state === "paused" && (
        <span className={cn(dim, "rounded-full border-2 border-paused")} />
      )}
      {state === "new" && (
        <span className={cn(dim, "rounded-[1px] bg-new")} style={{ transform: "rotate(45deg)" }} />
      )}
    </span>
  );
}
