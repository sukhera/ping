"use client";

import Link from "next/link";
import { useParams, useRouter } from "next/navigation";

import { MonitorForm } from "@/components/app/monitor-form";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useMonitor, useUpdateMonitor } from "@/hooks/use-monitors";
import { diffMonitorUpdate } from "@/lib/monitor-diff";

function EditLoading() {
  return (
    <div className="mx-auto max-w-[560px]">
      <Skeleton className="mb-6 h-7 w-48" />
      <div className="flex flex-col gap-6">
        {Array.from({ length: 4 }).map((_, i) => (
          <Skeleton key={i} className="h-16 w-full" />
        ))}
      </div>
    </div>
  );
}

function EditError({ onRetry }: { onRetry: () => void }) {
  return (
    <div className="flex flex-col items-center gap-4 rounded-lg border border-border bg-surface py-20 text-center">
      <p className="text-down">Couldn&apos;t load this monitor.</p>
      <Button variant="outline" onClick={onRetry}>
        Retry
      </Button>
    </div>
  );
}

function NotFound() {
  return (
    <div className="flex flex-col items-center gap-4 rounded-lg border border-border bg-surface py-20 text-center">
      <p className="text-text">Monitor not found.</p>
      <Button asChild variant="outline">
        <Link href="/dashboard">Back to dashboard</Link>
      </Button>
    </div>
  );
}

export default function EditMonitorPage() {
  const params = useParams<{ id: string }>();
  const id = params.id;
  const router = useRouter();

  const monitorQuery = useMonitor(id);
  const updateMonitor = useUpdateMonitor(id);

  if (monitorQuery.isLoading) return <EditLoading />;
  if (monitorQuery.isError) {
    const status = (monitorQuery.error as { status?: number } | undefined)?.status;
    if (status === 404 || status === 403) return <NotFound />;
    return <EditError onRetry={() => monitorQuery.refetch()} />;
  }

  const monitor = monitorQuery.data;
  if (!monitor) return <NotFound />;

  return (
    <div className="mx-auto max-w-[560px]">
      <h1 className="mb-6 text-xl font-semibold text-text">Edit {monitor.name}</h1>
      <MonitorForm
        monitor={monitor}
        submitLabel="Save changes"
        submittingLabel="Saving…"
        onSubmit={async (body) => {
          const diff = diffMonitorUpdate(monitor, body);
          if (Object.keys(diff).length > 0) {
            await updateMonitor.mutateAsync(diff);
          }
          router.push(`/monitors/${monitor.id}`);
        }}
      />
    </div>
  );
}
