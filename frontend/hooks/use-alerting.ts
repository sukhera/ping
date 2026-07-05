import { useMutation } from "@tanstack/react-query";

import { sendTestEmail } from "@/lib/api";

export function useSendTestEmail() {
  return useMutation({
    mutationFn: sendTestEmail,
  });
}
