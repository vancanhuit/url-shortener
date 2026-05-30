import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { EXPIRES_PRESETS, resolveExpiresAt } from "./expires";

describe("EXPIRES_PRESETS", () => {
  it("has 5 presets", () => {
    expect(EXPIRES_PRESETS).toHaveLength(5);
  });

  it("first preset is Never with null ms", () => {
    expect(EXPIRES_PRESETS[0]).toMatchObject({ value: "", label: "Never", ms: null });
  });

  it("each non-never preset has a positive ms value", () => {
    for (const preset of EXPIRES_PRESETS.slice(1)) {
      expect(preset.ms).toBeGreaterThan(0);
    }
  });

  it("presets are in ascending duration order", () => {
    const durations = EXPIRES_PRESETS.slice(1).map((p) => p.ms as number);
    for (let i = 1; i < durations.length; i++) {
      expect(durations[i]).toBeGreaterThan(durations[i - 1]!);
    }
  });
});

describe("resolveExpiresAt", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2024-01-01T00:00:00Z"));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("returns null for the never preset (empty string)", () => {
    expect(resolveExpiresAt("")).toBeNull();
  });

  it("returns null for an unknown preset value", () => {
    expect(resolveExpiresAt("5h")).toBeNull();
  });

  it("returns a Date 1h in the future for '1h'", () => {
    expect(resolveExpiresAt("1h")?.toISOString()).toBe("2024-01-01T01:00:00.000Z");
  });

  it("returns a Date 1d in the future for '1d'", () => {
    expect(resolveExpiresAt("1d")?.toISOString()).toBe("2024-01-02T00:00:00.000Z");
  });

  it("returns a Date 7d in the future for '7d'", () => {
    expect(resolveExpiresAt("7d")?.toISOString()).toBe("2024-01-08T00:00:00.000Z");
  });

  it("returns a Date 30d in the future for '30d'", () => {
    expect(resolveExpiresAt("30d")?.toISOString()).toBe("2024-01-31T00:00:00.000Z");
  });
});
