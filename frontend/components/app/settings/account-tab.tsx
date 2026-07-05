"use client";

import { useSession } from "@/hooks/use-session";

export function AccountTab() {
  const { data: user, isLoading } = useSession();

  return (
    <div className="rounded-[var(--radius)] border border-border bg-surface p-4">
      <h3 className="text-sm font-medium text-text">Account</h3>
      <dl className="mt-3 flex flex-col gap-2 text-sm">
        <div className="flex items-center justify-between gap-4">
          <dt className="text-text-dim">Email</dt>
          <dd className="mono text-text">{isLoading ? "Loading…" : (user?.email ?? "—")}</dd>
        </div>
      </dl>
    </div>
  );
}
