// How values read on the page.

import type { Operation, Release } from "./api/schema.ts";

/** clock is a timestamp's local time of day, to the second: events of one operation share a day. */
export function clock(ts: string): string {
  const d = new Date(ts);
  if (Number.isNaN(d.getTime())) {
    return ts;
  }
  return [d.getHours(), d.getMinutes(), d.getSeconds()].map((n) => String(n).padStart(2, "0")).join(":");
}

/** opTone colors an operation status like a release's. */
export function opTone(status: Operation["status"]): Tone {
  switch (status) {
    case "succeeded":
      return "ok";
    case "failed":
      return "bad";
    case "cancelled":
      return "idle";
    default:
      return "busy";
  }
}

/** ago is a timestamp relative to now, coarse on purpose: "3 min ago". */
export function ago(ts: string, now: Date = new Date()): string {
  const s = Math.round((now.getTime() - Date.parse(ts)) / 1000);
  if (Number.isNaN(s)) {
    return ts;
  }
  if (s < 45) {
    return "just now";
  }
  if (s < 3600) {
    return `${Math.round(s / 60)} min ago`;
  }
  if (s < 86400) {
    return `${Math.round(s / 3600)} h ago`;
  }
  const days = Math.round(s / 86400);
  return days < 60 ? `${days} d ago` : new Date(ts).toISOString().slice(0, 10);
}

/** shortSHA is a commit as git shows it abbreviated. */
export function shortSHA(sha: string): string {
  return sha.slice(0, 12);
}

/** bytes is a memory limit in the largest whole binary unit. */
export function bytes(n: number): string {
  for (const [size, unit] of [[1 << 30, "GiB"], [1 << 20, "MiB"], [1 << 10, "KiB"]] as const) {
    if (n >= size && n % size === 0) {
      return `${n / size} ${unit}`;
    }
  }
  return n >= 1 << 20 ? `${(n / (1 << 20)).toFixed(1)} MiB` : `${n} B`;
}

export type Tone = "ok" | "busy" | "bad" | "idle";

/** tone colors a deployment status: serving, in progress, failed, or past. */
export function tone(status: Release["status"]): Tone {
  switch (status) {
    case "active":
      return "ok";
    case "failed":
      return "bad";
    case "superseded":
    case "cancelled":
      return "idle";
    default:
      return "busy";
  }
}

/** statusLabel is a status in words. */
export function statusLabel(status: Release["status"]): string {
  return status.replace("_", " ");
}
