"use client";

import { useRouter } from "next/navigation";

import { MonitorForm } from "@/components/app/monitor-form";
import { useCreateMonitor } from "@/hooks/use-monitors";

export default function NewMonitorPage() {
  const router = useRouter();
  const createMonitor = useCreateMonitor();

  return (
    <div className="mx-auto max-w-[560px]">
      <h1 className="mb-6 text-xl font-semibold text-text">New monitor</h1>
      <MonitorForm
        submitLabel="Create monitor"
        submittingLabel="Creating…"
        onSubmit={async (body) => {
          const monitor = await createMonitor.mutateAsync(body);
          router.push(`/monitors/${monitor.id}`);
        }}
      />
    </div>
  );
}
