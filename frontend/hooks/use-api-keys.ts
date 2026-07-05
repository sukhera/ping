import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { createAPIKey, listAPIKeys, revokeAPIKey } from "@/lib/api";

export const apiKeyKeys = {
  all: ["api-keys"] as const,
};

export function useAPIKeys() {
  return useQuery({
    queryKey: apiKeyKeys.all,
    queryFn: ({ signal }) => listAPIKeys(signal),
  });
}

export function useCreateAPIKey() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (label: string) => createAPIKey(label),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: apiKeyKeys.all });
    },
  });
}

export function useRevokeAPIKey() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => revokeAPIKey(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: apiKeyKeys.all });
    },
  });
}
