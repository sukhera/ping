import Link from "next/link";

import { Button } from "@/components/ui/button";
import { Logo } from "@/components/app/logo";

/** DESIGN.md §7.1 empty state: pulse glyph, copy, curl example, CTA — never a blank page. */
export default function DashboardPage() {
  return (
    <div>
      <div className="mb-[22px] flex items-center justify-between">
        <h1 className="text-xl font-semibold">Monitors</h1>
        <Button asChild>
          <Link href="/monitors/new">+ New monitor</Link>
        </Button>
      </div>

      <div className="flex flex-col items-center gap-4 rounded-lg border border-border bg-surface py-20 text-center">
        <Logo showWordmark={false} className="opacity-60" />
        <p className="text-text">No monitors yet.</p>
        <code className="mono rounded-[var(--radius)] border border-border bg-surface-2 px-3 py-1.5 text-xs text-text-dim">
          curl https://ping.example.com/p/demo
        </code>
      </div>
    </div>
  );
}
