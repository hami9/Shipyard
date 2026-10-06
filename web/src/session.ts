// The signed-in session: the API token, kept in sessionStorage (the
// owner's choice, ADR-0016), so it survives a reload and goes when the tab
// closes. It is only ever sent as a bearer header, never as a cookie, so
// the API needs no CSRF defense.

import { ApiError, Client } from "./api/client.ts";
import type { Whoami } from "./api/schema.ts";

const key = "shipyard.token";

/** The storage the token lives in; tests pass their own. */
export type TokenStore = Pick<Storage, "getItem" | "setItem" | "removeItem">;

// Storage can be missing or throw (private modes, blocked site data);
// then the session lasts as long as the page.
function store(): TokenStore | undefined {
  try {
    return globalThis.sessionStorage;
  } catch {
    return undefined;
  }
}

export function savedToken(s: TokenStore | undefined = store()): string | undefined {
  try {
    return s?.getItem(key) ?? undefined;
  } catch {
    return undefined;
  }
}

export function saveToken(token: string, s: TokenStore | undefined = store()): void {
  try {
    s?.setItem(key, token);
  } catch {
    // Kept in memory only.
  }
}

export function forgetToken(s: TokenStore | undefined = store()): void {
  try {
    s?.removeItem(key);
  } catch {
    // Nothing to remove.
  }
}

// The API is on this page's own origin (ADR-0016), so requests are
// same-origin: no CORS, and the token goes only where the page came from.
export function newClient(token: string, onUnauthorized?: () => void): Client {
  return new Client(onUnauthorized ? { token, onUnauthorized } : { token });
}

export interface Session {
  token: string;
  whoami: Whoami;
}

/** signIn checks a pasted token against the API. It returns the session, or a message for the login form. */
export async function signIn(raw: string, newClient: (token: string) => Client): Promise<Session | string> {
  const token = raw.trim();
  if (!/^shp_[A-Za-z0-9_-]+$/.test(token)) {
    return "That is not a Shipyard token: it starts with shp_.";
  }
  try {
    const whoami = await newClient(token).call("whoami");
    return { token, whoami };
  } catch (err) {
    if (err instanceof ApiError) {
      if (err.status === 401) {
        return "The token is invalid, expired, or revoked.";
      }
      if (err.status === 429) {
        return `Too many attempts. Try again in ${err.retryAfter ?? 60} seconds.`;
      }
      return `The API answered ${err.status}: ${err.message}`;
    }
    return "The API is not reachable.";
  }
}

/** expiresIn describes a token's remaining lifetime for the header. */
export function expiresIn(expiresAt: string | null, now: Date = new Date()): string {
  if (expiresAt === null) {
    return "never expires";
  }
  const days = Math.floor((Date.parse(expiresAt) - now.getTime()) / 86_400_000);
  if (days < 0) {
    return "expired";
  }
  if (days === 0) {
    return "expires today";
  }
  return days === 1 ? "expires tomorrow" : `expires in ${days} days`;
}
