import { cn } from "@/lib/utils";

export function StatBlock({
  label,
  value,
  valueClassName,
  sub,
  danger = false,
}: {
  label: string;
  value: string;
  valueClassName?: string;
  sub?: string;
  danger?: boolean;
}) {
  return (
    <div
      className={cn(
        "rounded-[var(--radius)] border border-border bg-surface px-4 py-3.5",
        danger && "border-down/40 shadow-[0_0_24px_rgba(244,86,78,.12)]",
      )}
    >
      <div className="mb-1.5 text-[11px] tracking-[0.08em] text-text-faint uppercase">
        {label}
      </div>
      <div className={cn("mono text-[26px] leading-none font-medium", valueClassName)}>
        {value}
      </div>
      {sub && <div className="mt-[5px] text-[11.5px] text-text-faint">{sub}</div>}
    </div>
  );
}
