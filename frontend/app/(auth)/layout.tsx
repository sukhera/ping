import { Logo } from "@/components/app/logo";

/** DESIGN.md §7.6 — minimal centered card, no marketing chrome. */
export default function AuthLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <main className="flex min-h-screen flex-col items-center justify-center gap-6 bg-bg px-4">
      <Logo className="text-lg" />
      <div className="w-full max-w-sm">{children}</div>
    </main>
  );
}
