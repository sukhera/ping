import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { ProbeLog } from "./probe-log";
import type { ProbeResult } from "@/types/monitor";

function probe(overrides: Partial<ProbeResult> = {}): ProbeResult {
  return {
    id: 1,
    monitor_id: "m1",
    ok: true,
    created_at: new Date().toISOString(),
    ...overrides,
  };
}

describe("ProbeLog", () => {
  it("renders an HTML/script error message as inert text, never executing it", () => {
    const xssError = '<script>window.__xss = true;</script><img src=x onerror="window.__xss = true">';
    render(
      <ProbeLog
        probes={[probe({ ok: false, error: xssError })]}
        hasMore={false}
        onLoadMore={() => {}}
        loadingMore={false}
      />,
    );

    expect(
      screen.getByText((_, node) => node?.tagName === "PRE" && node.textContent === xssError),
    ).toBeInTheDocument();
    expect((window as unknown as { __xss?: boolean }).__xss).toBeUndefined();
    expect(document.querySelector("script[src], img[onerror]")).toBeNull();
  });

  it("renders the empty state when there are no probes", () => {
    render(<ProbeLog probes={[]} hasMore={false} onLoadMore={() => {}} loadingMore={false} />);
    expect(screen.getByText("No probes yet.")).toBeInTheDocument();
  });

  it("shows a Load more button only when hasMore is true", () => {
    const { rerender } = render(
      <ProbeLog probes={[probe()]} hasMore={true} onLoadMore={() => {}} loadingMore={false} />,
    );
    expect(screen.getByRole("button", { name: "Load more" })).toBeInTheDocument();

    rerender(<ProbeLog probes={[probe()]} hasMore={false} onLoadMore={() => {}} loadingMore={false} />);
    expect(screen.queryByRole("button", { name: "Load more" })).not.toBeInTheDocument();
  });

  it("shows http status and latency for a successful probe", () => {
    render(
      <ProbeLog
        probes={[probe({ http_status: 200, latency_ms: 123 })]}
        hasMore={false}
        onLoadMore={() => {}}
        loadingMore={false}
      />,
    );
    expect(screen.getByText("200")).toBeInTheDocument();
    expect(screen.getByText("123ms")).toBeInTheDocument();
  });
});
