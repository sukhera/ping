"use client";

import { Button } from "@/components/ui/button";
import { useSendTestEmail } from "@/hooks/use-alerting";
import { ApiError } from "@/lib/api";

export function AlertingTab() {
  const sendTest = useSendTestEmail();

  return (
    <div className="flex flex-col gap-4">
      <div className="rounded-[var(--radius)] border border-border bg-surface p-4">
        <h3 className="text-sm font-medium text-text">Email delivery</h3>
        <p className="mt-1 text-sm text-text-dim">
          Send a test email to confirm SMTP is configured correctly for down/recovery alerts.
        </p>
        <div className="mt-3 flex items-center gap-3">
          <Button
            type="button"
            variant="outline"
            onClick={() => sendTest.mutate()}
            disabled={sendTest.isPending}
          >
            {sendTest.isPending ? "Sending…" : "Send test email"}
          </Button>
          {sendTest.isSuccess && (
            <span className="text-sm text-up">Delivered to {sendTest.data.delivered_to}</span>
          )}
          {sendTest.isError && (
            <span className="text-sm text-destructive">
              {sendTest.error instanceof ApiError
                ? sendTest.error.message
                : "Unable to reach the server."}
            </span>
          )}
        </div>
      </div>

      <div className="rounded-[var(--radius)] border border-border bg-surface p-4">
        <h3 className="text-sm font-medium text-text">Reminder cadence</h3>
        <p className="mt-1 text-sm text-text-dim">
          While a monitor stays down, ping re-sends a reminder email every 24 hours by default.
          Per-monitor cadence is not yet editable from the UI.
        </p>
      </div>
    </div>
  );
}
