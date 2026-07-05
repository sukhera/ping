"use client";

import { useState } from "react";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useAPIKeys, useCreateAPIKey, useRevokeAPIKey } from "@/hooks/use-api-keys";
import { ApiError } from "@/lib/api";
import { cn } from "@/lib/utils";
import type { APIKey } from "@/types/apikey";

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);

  async function handleCopy() {
    await navigator.clipboard.writeText(text);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  }

  return (
    <Button type="button" variant="outline" size="sm" onClick={handleCopy} className="shrink-0">
      {copied ? "Copied" : "Copy"}
    </Button>
  );
}

function formatDate(iso: string): string {
  return new Date(iso).toLocaleString(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  });
}

function NewKeyBanner({ apiKey, onDismiss }: { apiKey: string; onDismiss: () => void }) {
  return (
    <div className="flex flex-col gap-2 rounded-[var(--radius)] border border-border bg-surface-2 p-4">
      <div className="flex items-center justify-between gap-2">
        <p className="text-sm font-medium text-text">
          Copy this key now — it won&apos;t be shown again.
        </p>
        <Button type="button" variant="ghost" size="sm" onClick={onDismiss}>
          Done
        </Button>
      </div>
      <div className="flex items-center gap-2 rounded-[var(--radius)] border border-border bg-surface px-3 py-2">
        <code className="mono flex-1 overflow-x-auto text-[12.5px] whitespace-pre text-text">
          {apiKey}
        </code>
        <CopyButton text={apiKey} />
      </div>
    </div>
  );
}

function CreateKeyForm({ onCreated }: { onCreated: (key: string) => void }) {
  const [label, setLabel] = useState("");
  const [error, setError] = useState<string | null>(null);
  const createKey = useCreateAPIKey();

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    const trimmed = label.trim();
    if (!trimmed) {
      setError("Label is required");
      return;
    }
    try {
      const created = await createKey.mutateAsync(trimmed);
      setLabel("");
      onCreated(created.key);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Unable to reach the server.");
    }
  }

  return (
    <form onSubmit={handleSubmit} className="flex items-end gap-2">
      <div className="flex flex-1 flex-col gap-1.5">
        <Label htmlFor="api-key-label">Label</Label>
        <Input
          id="api-key-label"
          placeholder="e.g. CI runner"
          value={label}
          onChange={(e) => setLabel(e.target.value)}
          aria-invalid={!!error}
        />
        {error && <p className="text-xs text-destructive">{error}</p>}
      </div>
      <Button type="submit" disabled={createKey.isPending}>
        {createKey.isPending ? "Creating…" : "New key"}
      </Button>
    </form>
  );
}

function RevokeButton({ apiKey }: { apiKey: APIKey }) {
  const revokeKey = useRevokeAPIKey();

  return (
    <AlertDialog>
      <AlertDialogTrigger asChild>
        <Button type="button" variant="outline" size="sm">
          Revoke
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Revoke &quot;{apiKey.label}&quot;?</AlertDialogTitle>
          <AlertDialogDescription>
            Any script or integration using this key will immediately stop working. This
            can&apos;t be undone.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            onClick={() => revokeKey.mutate(apiKey.id)}
            disabled={revokeKey.isPending}
          >
            Revoke key
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

function KeyRow({ apiKey }: { apiKey: APIKey }) {
  const revoked = !!apiKey.revoked_at;
  return (
    <div className="flex items-center justify-between gap-4 border-b border-border py-3 last:border-b-0">
      <div className="flex flex-col gap-0.5">
        <span className={cn("text-sm font-medium text-text", revoked && "text-text-dim line-through")}>
          {apiKey.label}
        </span>
        <span className="text-xs text-text-dim">
          {revoked ? (
            `Revoked ${formatDate(apiKey.revoked_at!)}`
          ) : (
            <>
              Created {formatDate(apiKey.created_at)} ·{" "}
              {apiKey.last_used_at ? `last used ${formatDate(apiKey.last_used_at)}` : "never used"}
            </>
          )}
        </span>
      </div>
      {revoked ? (
        <span className="text-xs text-text-faint">Revoked</span>
      ) : (
        <RevokeButton apiKey={apiKey} />
      )}
    </div>
  );
}

export function ApiKeysTab() {
  const { data: keys, isLoading, error } = useAPIKeys();
  const [newKey, setNewKey] = useState<string | null>(null);

  return (
    <div className="flex flex-col gap-4">
      {newKey && <NewKeyBanner apiKey={newKey} onDismiss={() => setNewKey(null)} />}

      <CreateKeyForm onCreated={setNewKey} />

      <div className="rounded-[var(--radius)] border border-border bg-surface px-4">
        {isLoading && <p className="py-4 text-sm text-text-dim">Loading…</p>}
        {error && (
          <p className="py-4 text-sm text-destructive">
            {error instanceof ApiError ? error.message : "Unable to reach the server."}
          </p>
        )}
        {keys && keys.length === 0 && (
          <p className="py-4 text-sm text-text-dim">
            No API keys yet. Create one to script access to the management API.
          </p>
        )}
        {keys?.map((key) => <KeyRow key={key.id} apiKey={key} />)}
      </div>
    </div>
  );
}
