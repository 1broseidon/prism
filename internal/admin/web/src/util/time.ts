export function fmtUptime(raw: string): string {
  if (!raw) return "";
  const hMatch = raw.match(/(\d+)h/);
  const mMatch = raw.match(/(\d+)m(?!s)/);
  const sMatch = raw.match(/([\d.]+)s$/);
  const h = hMatch ? parseInt(hMatch[1]) : 0;
  const min = mMatch ? parseInt(mMatch[1]) : 0;
  const s = sMatch ? Math.floor(parseFloat(sMatch[1])) : 0;
  if (h > 24) {
    const d = Math.floor(h / 24);
    return `${d}d ${h % 24}h`;
  }
  if (h > 0) return `${h}h ${min}m`;
  if (min > 0) return `${min}m ${s}s`;
  return `${s}s`;
}

export function fmtAge(ts: string | undefined): string {
  if (!ts) return "—";
  const age = Date.now() - new Date(ts).getTime();
  if (age < 0) return "now";
  const s = Math.floor(age / 1000);
  if (s < 60) return "now";
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  return `${d}d ago`;
}

export function fmtTimeOfDay(iso: string | undefined): string {
  if (!iso) return "";
  const parts = iso.split("T");
  if (parts.length < 2) return iso;
  return parts[1].replace("Z", "").split(".")[0];
}

export function splitLabel(label: string): [string, string] {
  const m = label.match(/^(.+?)\s*\((.+)\)$/);
  return m ? [m[1], m[2]] : [label, ""];
}
