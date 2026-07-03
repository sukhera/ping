import type { Monitor, MonitorDisplayState } from "@/types/monitor";

// DESIGN.md §7.1: problems float to the top — down -> late -> new -> up -> paused.
const SORT_ORDER: Record<MonitorDisplayState, number> = {
  down: 0,
  late: 1,
  new: 2,
  up: 3,
  paused: 4,
};

export function sortMonitors(monitors: Monitor[]): Monitor[] {
  return [...monitors].sort(
    (a, b) =>
      SORT_ORDER[a.display_state] - SORT_ORDER[b.display_state] ||
      a.name.localeCompare(b.name),
  );
}
