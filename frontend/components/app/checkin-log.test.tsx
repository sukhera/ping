import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { CheckinLog } from "./checkin-log";
import type { Checkin } from "@/types/monitor";

function checkin(overrides: Partial<Checkin> = {}): Checkin {
  return {
    id: 1,
    monitor_id: "m1",
    kind: "success",
    created_at: new Date().toISOString(),
    ...overrides,
  };
}

describe("CheckinLog", () => {
  it("renders an HTML/script check-in body as inert text, never executing it", () => {
    const xssBody = '<script>window.__xss = true;</script><img src=x onerror="window.__xss = true">';
    render(
      <CheckinLog
        checkins={[checkin({ body: xssBody })]}
        hasMore={false}
        onLoadMore={() => {}}
        loadingMore={false}
      />,
    );

    // The literal markup must appear as visible text content...
    expect(
      screen.getByText((_, node) => node?.tagName === "PRE" && node.textContent === xssBody),
    ).toBeInTheDocument();
    // ...and must never have executed as real HTML/script.
    expect((window as unknown as { __xss?: boolean }).__xss).toBeUndefined();
    // No <script> or <img> element was actually created from the body.
    expect(document.querySelector("script[src], img[onerror]")).toBeNull();
  });

  it("truncates a long body behind a Show more toggle", () => {
    const longBody = "a".repeat(200);
    render(
      <CheckinLog
        checkins={[checkin({ body: longBody })]}
        hasMore={false}
        onLoadMore={() => {}}
        loadingMore={false}
      />,
    );

    expect(screen.getByText("Show more")).toBeInTheDocument();
  });

  it("renders the empty state when there are no check-ins", () => {
    render(<CheckinLog checkins={[]} hasMore={false} onLoadMore={() => {}} loadingMore={false} />);
    expect(screen.getByText("No check-ins yet.")).toBeInTheDocument();
  });

  it("shows a Load more button only when hasMore is true", () => {
    const { rerender } = render(
      <CheckinLog checkins={[checkin()]} hasMore={true} onLoadMore={() => {}} loadingMore={false} />,
    );
    expect(screen.getByRole("button", { name: "Load more" })).toBeInTheDocument();

    rerender(<CheckinLog checkins={[checkin()]} hasMore={false} onLoadMore={() => {}} loadingMore={false} />);
    expect(screen.queryByRole("button", { name: "Load more" })).not.toBeInTheDocument();
  });
});
