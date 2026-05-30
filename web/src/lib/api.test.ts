import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { deleteLink, friendlyError, isApiError, listLinks } from "./api";
import type { Mock } from "vitest";

// ---------------------------------------------------------------------------
// Fetch mock helpers
// ---------------------------------------------------------------------------

let fetchMock: Mock;

beforeEach(() => {
  fetchMock = vi.fn();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function respondWith(status: number, body: unknown = null) {
  fetchMock.mockResolvedValueOnce({
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(body),
  });
}

// ---------------------------------------------------------------------------
// isApiError
// ---------------------------------------------------------------------------

describe("isApiError", () => {
  it("returns true for a well-shaped ApiError", () => {
    expect(isApiError({ status: 400, code: "bad_request", message: "Bad request" })).toBe(true);
  });

  it("returns false for null", () => {
    expect(isApiError(null)).toBe(false);
  });

  it("returns false for a plain string", () => {
    expect(isApiError("oops")).toBe(false);
  });

  it("returns false for an object missing required fields", () => {
    expect(isApiError({ status: 400 })).toBe(false);
  });

  it("returns false for a plain Error instance", () => {
    expect(isApiError(new Error("boom"))).toBe(false);
  });
});

// ---------------------------------------------------------------------------
// listLinks — URL construction
// ---------------------------------------------------------------------------

describe("listLinks", () => {
  it("calls /api/v1/links with no query string when params are empty", async () => {
    respondWith(200, { items: [], next_cursor: null });
    await listLinks({});
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/links", expect.any(Object));
  });

  it("appends the before param when provided", async () => {
    respondWith(200, { items: [], next_cursor: null });
    await listLinks({ before: 42 });
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/links?before=42", expect.any(Object));
  });

  it("appends the limit param when provided", async () => {
    respondWith(200, { items: [], next_cursor: null });
    await listLinks({ limit: 10 });
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/links?limit=10", expect.any(Object));
  });

  it("appends both params when both are provided", async () => {
    respondWith(200, { items: [], next_cursor: null });
    await listLinks({ limit: 10, before: 5 });
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/links?limit=10&before=5", expect.any(Object));
  });

  it("throws an ApiError on a non-2xx response", async () => {
    respondWith(500, { code: "internal_error", error: "server error" });
    await expect(listLinks({})).rejects.toMatchObject({ status: 500, code: "internal_error" });
  });
});

// ---------------------------------------------------------------------------
// deleteLink — idempotent 404 tolerance
// ---------------------------------------------------------------------------

describe("deleteLink", () => {
  it("resolves on 204 No Content", async () => {
    respondWith(204);
    await expect(deleteLink("abc")).resolves.toBeUndefined();
  });

  it("resolves on 404 (idempotent: already deleted)", async () => {
    respondWith(404, { code: "not_found", message: "not found" });
    await expect(deleteLink("abc")).resolves.toBeUndefined();
  });

  it("throws an ApiError on other non-2xx responses", async () => {
    respondWith(500, { code: "internal_error", message: "server error" });
    await expect(deleteLink("abc")).rejects.toMatchObject({ status: 500 });
  });
});

// ---------------------------------------------------------------------------
// friendlyError — user-facing message mapping
// ---------------------------------------------------------------------------

describe("friendlyError", () => {
  it("returns 'Request failed.' for null", () => {
    expect(friendlyError(null)).toBe("Request failed.");
  });

  it("returns 'Request failed.' for a plain Error instance", () => {
    expect(friendlyError(new Error("boom"))).toBe("Request failed.");
  });

  it("returns a friendly message for code_taken", () => {
    expect(friendlyError({ status: 409, code: "code_taken", message: "taken" })).toBe(
      "That code is already in use."
    );
  });

  it("returns a friendly message for internal_error", () => {
    expect(friendlyError({ status: 500, code: "internal_error", message: "error" })).toBe(
      "Something went wrong. Try again."
    );
  });

  it("returns the server message for unknown codes", () => {
    expect(friendlyError({ status: 400, code: "bad_request", message: "Bad request" })).toBe(
      "Bad request"
    );
  });

  it("falls back to 'Request failed.' when the server message is empty", () => {
    expect(friendlyError({ status: 400, code: "bad_request", message: "" })).toBe(
      "Request failed."
    );
  });
});
