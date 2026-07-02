import { useQuery } from "@tanstack/react-query";

import { restoreSession } from "@/lib/api";

export const sessionKeys = {
  all: ["session"] as const,
};

/**
 * Answers "is there a valid session" on mount by attempting a silent
 * refresh against the httpOnly cookie. staleTime: Infinity + retry: false
 * because this isn't a normal poll-and-refetch query — its cache entry is
 * written directly by login/register/logout instead of being refetched.
 */
export function useSession() {
  return useQuery({
    queryKey: sessionKeys.all,
    queryFn: restoreSession,
    staleTime: Infinity,
    retry: false,
  });
}
