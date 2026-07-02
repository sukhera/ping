type LogoProps = {
  className?: string;
  showWordmark?: boolean;
};

/** DESIGN.md §3 — heartbeat/pulse glyph: a flat line with a single sharp spike. */
export function Logo({ className, showWordmark = true }: LogoProps) {
  return (
    <span className={`flex items-center gap-2.5 font-semibold ${className ?? ""}`}>
      <svg
        width="22"
        height="22"
        viewBox="0 0 24 24"
        fill="none"
        aria-hidden="true"
      >
        <path
          d="M2 14h5l2.2-7 3.6 12 2.4-8.4L16.6 14H22"
          stroke="var(--up)"
          strokeWidth="2.2"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      </svg>
      {showWordmark && <span>ping</span>}
    </span>
  );
}
