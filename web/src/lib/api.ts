// Generated TypeScript client + application-level wrappers.
//
// Types are auto-generated from `api/openapi.yaml` by openapi-generator-cli
// (see `web/openapitools.json` and `just web-generate`). Do not edit files
// under `./generated/` by hand.
//
// This module wraps the generated `LinksApi` and `OperationalApi` to:
//  - use same-origin relative paths (basePath: "")
//  - translate `ResponseError` into the app-level `ApiError` shape
//  - add the `created` flag to `createLink` (201 vs 200 dedup)
//  - treat 404 as success for `deleteLink` (idempotent delete)

import {
  Configuration,
  LinksApi,
  OperationalApi,
  ResponseError,
  type LinkRequest,
  type LinkResponse,
  type ListResponse,
  type VersionResponse,
} from "./generated";

// Re-export generated types under the names callers expect.
export type { LinkResponse as Link } from "./generated";
export type { LinkRequest as CreateLinkInput } from "./generated";
export type { ListResponse as ListLinksResponse } from "./generated";
export type { VersionResponse as VersionInfo } from "./generated";

// Singleton API instances configured for same-origin relative paths.
// basePath: "" makes all requests root-relative (/api/v1/links etc.),
// which works both in production (Go binary serves SPA + API) and in
// dev (Vite proxies /api/* to localhost:8080).
const _cfg = new Configuration({ basePath: "" });
const _linksApi = new LinksApi(_cfg);
const _opApi = new OperationalApi(_cfg);

export interface ApiError {
  status: number;
  code: string;
  message: string;
}

export interface CreateLinkResult {
  link: LinkResponse;
  /** True when a fresh row was inserted (server returned 201); false
   *  when an existing permanent row was reused for dedup (200). */
  created: boolean;
}

/** Translate a `ResponseError` thrown by the generated client into an `ApiError`. */
async function toApiError(err: unknown): Promise<ApiError> {
  if (err instanceof ResponseError) {
    let code = "unknown_error";
    let message = `request failed with ${err.response.status}`;
    try {
      const body = (await err.response.json()) as unknown;
      if (body && typeof body === "object") {
        const env = body as Partial<{ code: unknown; error: unknown }>;
        if (typeof env.code === "string") code = env.code;
        if (typeof env.error === "string") message = env.error;
      }
    } catch {
      // Non-JSON body or empty — keep defaults.
    }
    return { status: err.response.status, code, message };
  }
  return { status: 0, code: "unknown_error", message: String(err) };
}

export async function createLink(input: LinkRequest): Promise<CreateLinkResult> {
  try {
    const raw = await _linksApi.createLinkRaw({ linkRequest: input });
    const link = await raw.value();
    return { link, created: raw.raw.status === 201 };
  } catch (err) {
    throw await toApiError(err);
  }
}

export async function getLink(code: string): Promise<LinkResponse> {
  try {
    return await _linksApi.getLink({ code });
  } catch (err) {
    throw await toApiError(err);
  }
}

/**
 * Soft-deletes a link. The server returns 204 on first delete and 404
 * on a subsequent delete; we surface both as success here so the UI
 * doesn't show an error if a poll races a delete from another tab.
 */
export async function deleteLink(code: string): Promise<void> {
  try {
    await _linksApi.deleteLink({ code });
  } catch (err) {
    if (err instanceof ResponseError && err.response.status === 404) return;
    throw await toApiError(err);
  }
}

export interface ListLinksParams {
  limit?: number;
  before?: number;
}

export async function listLinks(params: ListLinksParams = {}): Promise<ListResponse> {
  try {
    return await _linksApi.listLinks(params);
  } catch (err) {
    throw await toApiError(err);
  }
}

export async function getVersion(): Promise<VersionResponse> {
  try {
    return await _opApi.version();
  } catch (err) {
    throw await toApiError(err);
  }
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
