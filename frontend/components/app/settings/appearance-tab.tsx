"use client";

import { Laptop, Moon, Sun } from "lucide-react";
import { useTheme } from "next-themes";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

const OPTIONS = [
  { value: "light", label: "Light", Icon: Sun },
  { value: "dark", label: "Dark", Icon: Moon },
  { value: "system", label: "System", Icon: Laptop },
] as const;

export function AppearanceTab() {
  const { theme, setTheme } = useTheme();

  return (
    <div className="rounded-[var(--radius)] border border-border bg-surface p-4">
      <h3 className="text-sm font-medium text-text">Theme</h3>
      <div className="mt-3 flex gap-2">
        {OPTIONS.map(({ value, label, Icon }) => (
          <Button
            key={value}
            type="button"
            variant={theme === value ? "default" : "outline"}
            size="sm"
            onClick={() => setTheme(value)}
            className={cn("gap-1.5")}
            aria-pressed={theme === value}
          >
            <Icon className="size-4" />
            {label}
          </Button>
        ))}
      </div>
    </div>
  );
}
