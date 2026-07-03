import { cn } from "@/lib/utils";
import type { MonitorDisplayState, MonitorKind } from "@/types/monitor";

type Props = {
  search: string;
  kind: MonitorKind | null;
  state: MonitorDisplayState | null;
  onSearchChange: (value: string) => void;
  onKindChange: (value: MonitorKind | null) => void;
  onStateChange: (value: MonitorDisplayState | null) => void;
};

/**
 * Controlled — no internal state. The mockup's chip row (All / Heartbeat /
 * HTTP / Down) behaves single-select, but backs independent ?kind=/?state=
 * URL params/API filters so a future UI can combine them (e.g. "HTTP and
 * late") without a URL-shape migration.
 */
export function MonitorFilters({
  search,
  kind,
  state,
  onSearchChange,
  onKindChange,
  onStateChange,
}: Props) {
  const isAll = kind === null && state === null;

  return (
    <div className="mb-3.5 flex items-center gap-2">
      <input
        className="w-[260px] rounded-[var(--radius)] border border-border bg-surface-2 px-2.5 py-1.5 text-[13px] text-text placeholder:text-text-faint"
        placeholder="Search monitors…"
        aria-label="Search monitors"
        value={search}
        onChange={(e) => onSearchChange(e.target.value)}
      />
      <FilterChip
        active={isAll}
        onClick={() => {
          onKindChange(null);
          onStateChange(null);
        }}
      >
        All
      </FilterChip>
      <FilterChip
        active={kind === "heartbeat"}
        onClick={() => {
          onKindChange(kind === "heartbeat" ? null : "heartbeat");
          onStateChange(null);
        }}
      >
        ⌁ Heartbeat
      </FilterChip>
      <FilterChip
        active={kind === "http"}
        onClick={() => {
          onKindChange(kind === "http" ? null : "http");
          onStateChange(null);
        }}
      >
        ⇄ HTTP
      </FilterChip>
      <FilterChip
        active={state === "down"}
        onClick={() => {
          onStateChange(state === "down" ? null : "down");
          onKindChange(null);
        }}
      >
        Down
      </FilterChip>
    </div>
  );
}

function FilterChip({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={cn(
        "rounded-full border border-border px-3 py-1.5 text-[12.5px] text-text-dim",
        active && "border-text-faint bg-surface-2 text-text",
      )}
    >
      {children}
    </button>
  );
}
