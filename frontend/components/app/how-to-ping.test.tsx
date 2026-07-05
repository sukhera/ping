import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { HowToPing } from "./how-to-ping";
import type { Monitor } from "@/types/monitor";

function monitor(overrides: Partial<Monitor> = {}): Monitor {
  return {
    id: "m1",
    kind: "heartbeat",
    slug: "abc123",
    name: "nightly backup",
    state: "up",
    display_state: "up",
    fail_streak: 0,
    alerts_muted: false,
    auto_resume: true,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ping_url: "https://ping.example.com/p/abc123",
    ...overrides,
  };
}

describe("HowToPing", () => {
  it("renders nothing when the monitor has no ping_url", () => {
    const { container } = render(<HowToPing monitor={monitor({ ping_url: undefined })} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("builds curl and crontab snippets from the monitor's real slug URL", () => {
    render(<HowToPing monitor={monitor()} />);

    expect(screen.getByText("curl -fsS https://ping.example.com/p/abc123")).toBeInTheDocument();
    expect(
      screen.getByText("* * * * * curl -fsS https://ping.example.com/p/abc123 >/dev/null 2>&1"),
    ).toBeInTheDocument();
  });

  it("copy button writes the real command containing the slug URL to the clipboard", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, { clipboard: { writeText } });

    render(<HowToPing monitor={monitor()} />);
    await userEvent.click(screen.getByRole("button", { name: "Copy curl command" }));

    expect(writeText).toHaveBeenCalledWith("curl -fsS https://ping.example.com/p/abc123");
  });
});
