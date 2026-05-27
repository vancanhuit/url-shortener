// Thin fetch wrappers around the public JSON API. All calls are
// same-origin: the SPA is served by the Go binary in production, and
// in dev `vite.config.ts` proxies `/api/*` to localhost:8080.
//
// Types here mirror `internal/handlers` (LinkResponse, ErrorResponse).
// Keep them in sync with `api/openapi.yaml`, which is the contract
// of record.

export interface Link {
  code: string;
  short_url: string;
  target_url: string;
  created_at: string;
  expires_at?: string | null;
  click_count: number;
}

export interface CreateLinkInput {
  target_url: string;
  code?: string;
  expires_at?: string | null;
}

export interface ListLinksParams {
  limit?: number;
  before?: number;
}

export interface ListLinksResponse {
  items: Link[];
  next_cursor: number | null;
}

export interface ApiError {
  status: number;
  code: string;
  message: string;
}

export interface CreateLinkResult {
  link: Link;
  /** True when a fresh row was inserted (server returned 201); false
   *  when an existing permanent row was reused for dedup (200). */
  created: boolean;
}

/**
 * Build an `ApiError` from a non-2xx response. Falls back to a generic
 * message if the body isn't a parseable error envelope.
 */
async function asApiError(resp: Response): Promise<ApiError> {
  let code = "unknown_error";
  let message = `request failed with ${resp.status}`;
  try {
    const body = (await resp.json()) as unknown;
    if (body && typeof body === "object") {
      const env = body as Partial<{ code: unknown; message: unknown }>;
      if (typeof env.code === "string") code = env.code;
      if (typeof env.message === "string") message = env.message;
    }
  } catch {
    // Non-JSON body or empty -- keep defaults.
  }
  return { status: resp.status, code, message };
}

export async function createLink(input: CreateLinkInput): Promise<CreateLinkResult> {
  const resp = await fetch("/api/v1/links", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
  if (!resp.ok) throw await asApiError(resp);
  const link = (await resp.json()) as Link;
  return { link, created: resp.status === 201 };
}

export async function getLink(code: string): Promise<Link> {
  const resp = await fetch(`/api/v1/links/${encodeURIComponent(code)}`);
  if (!resp.ok) throw await asApiError(resp);
  return (await resp.json()) as Link;
}

/**
 * Soft-deletes a link. The server returns 204 on first delete and 404
 * on a subsequent delete; we surface both as success here so the UI
 * doesn't show an error if a poll races a delete from another tab.
 */
export async function deleteLink(code: string): Promise<void> {
  const resp = await fetch(`/api/v1/links/${encodeURIComponent(code)}`, {
    method: "DELETE",
  });
  if (resp.ok || resp.status === 404) return;
  throw await asApiError(resp);
}

export async function listLinks(params: ListLinksParams = {}): Promise<ListLinksResponse> {
  const qs = new URLSearchParams();
  if (params.limit) qs.set("limit", String(params.limit));
  if (params.before) qs.set("before", String(params.before));
  const url = qs.size > 0 ? `/api/v1/links?${qs.toString()}` : "/api/v1/links";
  const resp = await fetch(url);
  if (!resp.ok) throw await asApiError(resp);
  return (await resp.json()) as ListLinksResponse;
}

export interface VersionInfo {
  version: string;
  commit: string;
  date: string;
}

export async function getVersion(): Promise<VersionInfo> {
  const resp = await fetch("/version");
  if (!resp.ok) throw await asApiError(resp);
  return (await resp.json()) as VersionInfo;
}

/**
 * Type-guard for `ApiError` values. Useful in catch blocks where
 * `unknown` is the inferred error type under TypeScript strict mode.
 */
export function isApiError(err: unknown): err is ApiError {
  return (
    typeof err === "object" && err !== null && "status" in err && "code" in err && "message" in err
  );
}

/**
 * Translate an API error (or any thrown value) into a user-facing message.
 * Well-known error codes are mapped to friendly strings; everything else
 * falls back to the server's `message` field or a generic fallback.
 */
export function friendlyError(err: unknown): string {
  if (!isApiError(err)) return "Request failed.";
  if (err.code === "code_taken") return "That code is already in use.";
  if (err.code === "internal_error") return "Something went wrong. Try again.";
  return err.message || "Request failed.";
}
