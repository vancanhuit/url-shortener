import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { humanExpiry, plural } from "./time";

describe("plural", () => {
  it("returns empty string for 1", () => {
    expect(plural(1)).toBe("");
  });

  it("returns s for 0", () => {
    expect(plural(0)).toBe("s");
  });

  it("returns s for 2", () => {
    expect(plural(2)).toBe("s");
  });
});

describe("humanExpiry", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2024-01-01T00:00:00Z"));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("returns empty string for null", () => {
    expect(humanExpiry(null)).toBe("");
  });

  it("returns empty string for undefined", () => {
    expect(humanExpiry(undefined)).toBe("");
  });

  it("returns empty string for an invalid date string", () => {
    expect(humanExpiry("not-a-date")).toBe("");
  });

  it("returns expired for a past timestamp", () => {
    expect(humanExpiry("2023-12-31T23:59:59Z")).toBe("expired");
  });

  it("returns <1m left for less than 60 seconds remaining", () => {
    const soon = new Date(Date.now() + 30_000).toISOString();
    expect(humanExpiry(soon)).toBe("<1m left");
  });

  it("returns Xm left for minutes remaining", () => {
    const future = new Date(Date.now() + 5 * 60 * 1000).toISOString();
    expect(humanExpiry(future)).toBe("5m left");
  });

  it("returns Xh left for hours remaining", () => {
    const future = new Date(Date.now() + 3 * 3600 * 1000).toISOString();
    expect(humanExpiry(future)).toBe("3h left");
  });

  it("returns Xd left for days remaining", () => {
    const future = new Date(Date.now() + 12 * 86400 * 1000).toISOString();
    expect(humanExpiry(future)).toBe("12d left");
  });
});
