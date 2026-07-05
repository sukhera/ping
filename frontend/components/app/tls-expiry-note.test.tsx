import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { TLSExpiryNote } from "./tls-expiry-note";

describe("TLSExpiryNote", () => {
  it("renders nothing when there is no TLS data", () => {
    const { container } = render(<TLSExpiryNote />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders days remaining for a future expiry", () => {
    const future = new Date(Date.now() + 41 * 24 * 60 * 60 * 1000).toISOString();
    render(<TLSExpiryNote expiresAt={future} />);
    expect(screen.getByText(/cert expires in 4[01] days/)).toBeInTheDocument();
  });

  it("renders an expired message for a past date", () => {
    const past = new Date(Date.now() - 5 * 24 * 60 * 60 * 1000).toISOString();
    render(<TLSExpiryNote expiresAt={past} />);
    expect(screen.getByText(/cert expired \d+ days? ago/)).toBeInTheDocument();
  });
});
