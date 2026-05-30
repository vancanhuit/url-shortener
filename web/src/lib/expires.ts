// Expiry presets exposed by the form's <select>. Kept on the client
// because the JSON API takes an absolute `expires_at` ISO timestamp --
// the SPA computes the timestamp from the user's preset choice and
// submits that. Anything outside this set is treated as "never".

export interface ExpiresPreset {
  value: string;
  label: string;
  /** Milliseconds from "now"; null means "never expires". */
  ms: number | null;
}

export const EXPIRES_PRESETS: readonly ExpiresPreset[] = [
  { value: "", label: "Never", ms: null },
  { value: "1h", label: "1 hour", ms: 60 * 60 * 1000 },
  { value: "1d", label: "1 day", ms: 24 * 60 * 60 * 1000 },
  { value: "7d", label: "7 days", ms: 7 * 24 * 60 * 60 * 1000 },
  { value: "30d", label: "30 days", ms: 30 * 24 * 60 * 60 * 1000 },
];

/**
 * Resolve a preset value to an ISO-8601 timestamp suitable for the
 * JSON API's `expires_at`, or null for "never".
 */
export function resolveExpiresAt(value: string): Date | null {
  const preset = EXPIRES_PRESETS.find((p) => p.value === value);
  if (!preset || preset.ms === null) return null;
  return new Date(Date.now() + preset.ms);
}
