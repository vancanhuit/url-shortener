// Coarse-grained "time-until-X" formatter for the recent-list expiry
// badge.

/**
 * @param expiresAt ISO-8601 timestamp, null, or undefined for "never".
 * @returns A short label like "expired", "<1m left", "5m left",
 *          "3h left", "12d left", or "" for "never".
 */
export function humanExpiry(expiresAt: string | null | undefined): string {
  if (!expiresAt) return "";
  const target = Date.parse(expiresAt);
  if (Number.isNaN(target)) return "";
  const ms = target - Date.now();
  if (ms <= 0) return "expired";
  const sec = Math.floor(ms / 1000);
  if (sec < 60) return "<1m left";
  if (sec < 3600) return `${Math.floor(sec / 60)}m left`;
  if (sec < 86400) return `${Math.floor(sec / 3600)}h left`;
  return `${Math.floor(sec / 86400)}d left`;
}

/** English plural suffix; "" when n === 1, "s" otherwise. */
export function plural(n: number): "" | "s" {
  return n === 1 ? "" : "s";
}
