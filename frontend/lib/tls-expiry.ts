import type { ProbeResult } from "@/types/monitor";

/**
 * There is no dedicated "latest TLS expiry" field on Monitor (PING-018 only
 * persisted it per-probe-result) — derive it from whichever probe log page is
 * already loaded, newest first. Walks forward past probes without TLS data
 * (e.g. a failed probe that never reached the TLS handshake) rather than
 * assuming the single newest row has it.
 */
export function latestTLSExpiry(probes: ProbeResult[]): string | undefined {
  for (const p of probes) {
    if (p.tls_expires_at) return p.tls_expires_at;
  }
  return undefined;
}

export function daysUntil(iso: string, now: Date = new Date()): number {
  const diffMs = new Date(iso).getTime() - now.getTime();
  return Math.ceil(diffMs / (24 * 60 * 60 * 1000));
}
