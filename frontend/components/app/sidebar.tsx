"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";

import { Logo } from "@/components/app/logo";
import { ThemeToggle } from "@/components/app/theme-toggle";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { sessionKeys } from "@/hooks/use-session";
import { cn } from "@/lib/utils";
import { logout, type User } from "@/lib/api";

const NAV_ITEMS = [
  { href: "/dashboard", label: "Monitors" },
  { href: "/events", label: "Events" },
  { href: "/settings", label: "Settings" },
] as const;

export function Sidebar({ user }: { user: User }) {
  const pathname = usePathname();
  const router = useRouter();
  const queryClient = useQueryClient();

  const logoutMutation = useMutation({
    mutationFn: logout,
    onSuccess: () => {
      queryClient.setQueryData(sessionKeys.all, null);
      router.push("/login");
    },
  });

  return (
    <aside className="sticky top-0 flex h-screen w-[220px] shrink-0 flex-col border-r border-border bg-surface p-3 py-5">
      <div className="px-2 pb-[18px]">
        <Logo />
      </div>

      {/* Global status summary — placeholder until PING-013 wires real
          monitor aggregation; zero-state is intentional, not a bug. */}
      <div
        title="Global status"
        className="mb-[18px] flex gap-3 rounded-[var(--radius)] border border-border bg-surface-2 px-3 py-2.5 text-[12.5px] text-text-dim"
      >
        <span>
          <span className="mr-1.5 inline-block size-[7px] rounded-full bg-up align-middle" />
          <b className="font-medium text-text">0</b> up
        </span>
        <span>
          <span className="mr-1.5 inline-block size-[7px] rounded-full bg-down align-middle" />
          <b className="font-medium text-text">0</b> down
        </span>
        <span>
          <span className="mr-1.5 inline-block size-[7px] rounded-full bg-late align-middle" />
          <b className="font-medium text-text">0</b> late
        </span>
      </div>

      <nav className="flex flex-col gap-0.5">
        {NAV_ITEMS.map((item) => {
          const active = pathname === item.href;
          return (
            <Link
              key={item.href}
              href={item.href}
              className={cn(
                "rounded-[var(--radius)] px-2.5 py-2 text-[13.5px] text-text-dim hover:text-text",
                active && "bg-surface-2 text-text",
              )}
            >
              {item.label}
            </Link>
          );
        })}
      </nav>

      <div className="mt-auto flex items-center justify-between border-t border-border pt-2.5 text-[12.5px] text-text-faint">
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button
              className="min-w-0 flex-1 truncate text-left hover:text-text"
              aria-label={`Account menu for ${user.email}`}
            >
              {user.email}
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start">
            <DropdownMenuItem
              onSelect={() => logoutMutation.mutate()}
              disabled={logoutMutation.isPending}
            >
              Log out
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
        <ThemeToggle />
      </div>
    </aside>
  );
}
