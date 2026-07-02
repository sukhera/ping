import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
}));

import RegisterPage from "./page";

function renderPage() {
  const queryClient = new QueryClient();
  return render(
    <QueryClientProvider client={queryClient}>
      <RegisterPage />
    </QueryClientProvider>,
  );
}

describe("RegisterPage", () => {
  it("shows required-field errors on empty submit", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.click(screen.getByRole("button", { name: /create account/i }));

    expect(
      await screen.findByText(/enter a valid email address/i),
    ).toBeInTheDocument();
    expect(
      await screen.findByText(/at least 12 characters/i),
    ).toBeInTheDocument();
  });

  it("rejects a password shorter than 12 characters", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.type(screen.getByLabelText(/email/i), "a@example.com");
    await user.type(screen.getByLabelText(/password/i), "short");
    await user.tab();

    expect(
      await screen.findByText(/at least 12 characters/i),
    ).toBeInTheDocument();
  });
});
