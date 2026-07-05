"use client";

import { useState } from "react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import type { Monitor } from "@/types/monitor";

function CopyButton({ text, label }: { text: string; label: string }) {
  const [copied, setCopied] = useState(false);

  async function handleCopy() {
    await navigator.clipboard.writeText(text);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  }

  return (
    <Button
      type="button"
      variant="outline"
      size="sm"
      onClick={handleCopy}
      aria-label={`Copy ${label}`}
      className="shrink-0"
    >
      {copied ? "Copied" : "Copy"}
    </Button>
  );
}

function SnippetRow({
  label,
  code,
  copyLabel,
}: {
  label: string;
  code: string;
  copyLabel: string;
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <div className="text-[11px] tracking-[0.08em] text-text-faint uppercase">{label}</div>
      <div className="flex items-center gap-2 rounded-[var(--radius)] border border-border bg-surface-2 px-3 py-2">
        <code className="mono flex-1 overflow-x-auto text-[12.5px] whitespace-pre text-text">
          {code}
        </code>
        <CopyButton text={code} label={copyLabel} />
      </div>
    </div>
  );
}

/**
 * DESIGN.md §7.2 "How to ping" panel: curl + crontab snippets built from the
 * monitor's real ping_url (never a placeholder), so the copy buttons always
 * produce a working command.
 */
export function HowToPing({ monitor, className }: { monitor: Monitor; className?: string }) {
  if (!monitor.ping_url) return null;

  const curl = `curl -fsS ${monitor.ping_url}`;
  const cron = `* * * * * ${curl} >/dev/null 2>&1`;

  return (
    <div className={cn("rounded-[var(--radius)] border border-border bg-surface p-4", className)}>
      <h2 className="mb-3 text-sm font-medium text-text">How to ping</h2>
      <div className="flex flex-col gap-3">
        <SnippetRow label="curl" code={curl} copyLabel="curl command" />
        <SnippetRow label="crontab example" code={cron} copyLabel="crontab line" />
      </div>
    </div>
  );
}
