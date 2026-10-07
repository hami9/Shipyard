// The server's status (GET /v1/status) as the pages show it.

import type { ServerStatus } from "./api/schema.ts";
import type { Tone } from "./format.ts";

export interface Health {
  label: string;
  tone: Tone;
  detail: string;
}

/**
 * health is one app's state at the worker's last check. It is "unknown"
 * whenever the check cannot speak for it: the worker is down, has not
 * checked yet, or did not see the app running a release.
 */
export function health(st: ServerStatus | undefined, slug: string): Health | undefined {
  if (!st) {
    return undefined;
  }
  if (st.worker === "down") {
    return { label: "unknown", tone: "idle", detail: "The worker is not answering, so nothing checks the app." };
  }
  if (!st.checked_at) {
    return { label: "unknown", tone: "idle", detail: "The worker has not checked the apps yet." };
  }
  const a = st.apps.find((x) => x.app === slug);
  if (!a) {
    return { label: "not running", tone: "idle", detail: "No release of it was running at the last check." };
  }
  if (!a.running) {
    return { label: "down", tone: "bad", detail: "Its container was not running at the last check." };
  }
  if (!a.healthy) {
    return { label: "unhealthy", tone: "bad", detail: "It runs, but its health check failed at the last check." };
  }
  return { label: "healthy", tone: "ok", detail: "It answered its health check at the last check." };
}
