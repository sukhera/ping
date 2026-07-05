import { daysUntil } from "@/lib/tls-expiry";
import { cn } from "@/lib/utils";

/**
 * DESIGN.md §7.2 HTTP detail: "TLS expiry note ('cert expires in 41 days')"
 * in the header area. Renders nothing when no TLS data is available yet
 * (non-HTTPS target, or no successful probe has completed) — degrade
 * gracefully rather than showing a misleading placeholder.
 */
export function TLSExpiryNote({ expiresAt }: { expiresAt?: string }) {
  if (!expiresAt) return null;

  const days = daysUntil(expiresAt);
  const warn = days <= 14;

  return (
    <div
      className={cn(
        "mono text-[12.5px]",
        warn ? "text-down" : "text-text-faint",
      )}
    >
      {days < 0
        ? `cert expired ${Math.abs(days)} day${Math.abs(days) === 1 ? "" : "s"} ago`
        : `cert expires in ${days} day${days === 1 ? "" : "s"}`}
    </div>
  );
}
