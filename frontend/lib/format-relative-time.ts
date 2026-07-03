/**
 * Hand-rolled to match design-mockup.html's exact style ("6s ago", "2h 12m
 * ago") — Intl.RelativeTimeFormat's output doesn't match that concatenated
 * style, and this is small enough not to warrant a date library dependency.
 */
export function formatRelativeTime(date: string | Date, now: Date = new Date()): string {
  const then = typeof date === "string" ? new Date(date) : date;
  const diffMs = now.getTime() - then.getTime();
  const diffS = Math.max(0, Math.floor(diffMs / 1000));

  if (diffS < 60) return `${diffS}s`;

  const minutes = Math.floor(diffS / 60);
  if (minutes < 60) return `${minutes}m`;

  const hours = Math.floor(minutes / 60);
  const remMinutes = minutes % 60;
  if (hours < 24) return remMinutes > 0 ? `${hours}h ${remMinutes}m` : `${hours}h`;

  const days = Math.floor(hours / 24);
  const remHours = hours % 24;
  return remHours > 0 ? `${days}d ${remHours}h` : `${days}d`;
}
