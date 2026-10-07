// Token management rules (ADR-0011).

import type { Token, Whoami } from "./api/schema.ts";
import type { Tone } from "./format.ts";

/** The grace periods offered for a rotation: how long the old token keeps working (at most 7 days). */
export const graces: { label: string; seconds: number }[] = [
  { label: "End it now", seconds: 0 },
  { label: "Keep it 1 hour", seconds: 3600 },
  { label: "Keep it 24 hours", seconds: 86400 },
  { label: "Keep it 7 days", seconds: 604800 },
];

/** whoamiOf is the session's view of a token the API just made. */
export function whoamiOf(t: Token): Whoami {
  return { token: t.prefix, name: t.name, scopes: t.scopes, expires_at: t.expires_at };
}

export function tokenTone(status: Token["status"]): Tone {
  return status === "active" ? "ok" : status === "revoked" ? "bad" : "idle";
}
