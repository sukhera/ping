import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { StatusChip } from "./status-chip";
import type { MonitorDisplayState } from "@/types/monitor";

const STATES: MonitorDisplayState[] = ["up", "down", "late", "new", "paused"];

describe("StatusChip", () => {
  it.each(STATES)("exposes an accessible name for state=%s", (state) => {
    render(<StatusChip state={state} />);

    expect(screen.getByRole("img", { name: `status: ${state}` })).toBeInTheDocument();
  });

  it("renders a pulse ring only when state is up and pulse is true", () => {
    const { container, rerender } = render(<StatusChip state="up" pulse />);
    expect(container.querySelector(".motion-safe\\:animate-\\[status-pulse_600ms_ease-out\\]")).not.toBeNull();

    rerender(<StatusChip state="up" pulse={false} />);
    expect(container.querySelector(".motion-safe\\:animate-\\[status-pulse_600ms_ease-out\\]")).toBeNull();

    rerender(<StatusChip state="down" pulse />);
    expect(container.querySelector(".motion-safe\\:animate-\\[status-pulse_600ms_ease-out\\]")).toBeNull();
  });
});
