"use client";

import { useRouter } from "next/navigation";
import { useEffect } from "react";

import { Sidebar } from "@/components/app/sidebar";
import { useSession } from "@/hooks/use-session";

export default function AppLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const router = useRouter();
  const { data: user, isPending } = useSession();

  useEffect(() => {
    if (!isPending && !user) {
      router.replace("/login");
    }
  }, [isPending, user, router]);

  if (isPending) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-bg text-text-dim">
        Loading…
      </div>
    );
  }

  if (!user) {
    // Redirect is in flight (see effect above); render nothing to avoid a
    // flash of authenticated content.
    return null;
  }

  return (
    <div className="flex min-h-screen">
      <Sidebar user={user} />
      <main className="max-w-[1200px] flex-1 px-9 py-7">{children}</main>
    </div>
  );
}
