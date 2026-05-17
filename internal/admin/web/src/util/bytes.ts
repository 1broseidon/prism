const UNITS = ["B", "kB", "MB", "GB", "TB"] as const;

export function fmtBytes(n: number | undefined | null): string {
  if (!n || n <= 0) return "0 B";
  let val = n;
  let i = 0;
  while (val >= 1024 && i < UNITS.length - 1) {
    val /= 1024;
    i++;
  }
  return val >= 10 || i === 0 ? `${Math.round(val)} ${UNITS[i]}` : `${val.toFixed(1)} ${UNITS[i]}`;
}

// Returns a percentage 0..100 (or null when there's no quota to compare against).
export function pctOfQuota(
  used: number | undefined | null,
  quota: number | undefined | null,
): number | null {
  if (!quota || quota <= 0) return null;
  if (!used || used <= 0) return 0;
  return Math.min(100, Math.round((used / quota) * 100));
}
