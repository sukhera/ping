export type APIKey = {
  id: string;
  label: string;
  last_used_at?: string;
  revoked_at?: string;
  created_at: string;
};

// CreatedAPIKey includes `key` (the plaintext "pk_..." token) — returned only
// once, from createAPIKey. APIKey (list) never carries it.
export type CreatedAPIKey = APIKey & {
  key: string;
};
