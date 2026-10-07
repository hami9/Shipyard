// Checks a form makes before it asks the API, and the API's answers in the
// form's terms. The API checks everything again; these only save a round trip.

import { ApiError } from "./api/client.ts";
import type { Scope } from "./api/schema.ts";
import { message } from "./useApi.ts";

/** canChange: setting configuration and domains needs the admin scope (api/openapi.json). */
export function canChange(scopes: Scope[]): boolean {
  return scopes.includes("admin");
}

/** canDeploy: deploys and rollbacks need the deploy scope, which admin includes. */
export function canDeploy(scopes: Scope[]): boolean {
  return scopes.some((s) => s === "deploy" || s === "admin");
}

// The API's rules (api/openapi.json EnvKey; internal/secrets MaxValueSize).
const keyRE = /^[A-Za-z_][A-Za-z0-9_]{0,254}$/;
export const maxValueBytes = 64 << 10;

export function keyProblem(key: string): string | undefined {
  return keyRE.test(key) ? undefined : "a letter or _, then letters, digits or _ (at most 255)";
}

export function valueProblem(value: string): string | undefined {
  if (value.includes("\0")) {
    return "must not contain a NUL character";
  }
  if (new TextEncoder().encode(value).length > maxValueBytes) {
    return "must be at most 64 KiB";
  }
  return undefined;
}

export interface Explained {
  /** For the form as a whole; "" when the fields say it all. */
  message: string;
  /** By field name, as the API names them (422 errors[]). */
  fields: Record<string, string>;
}

/** explain turns an error into what a form shows: per-field details from a 422, otherwise one message. */
export function explain(err: unknown): Explained {
  if (err instanceof ApiError && err.status === 422 && err.problem?.errors?.length) {
    const fields: Record<string, string> = {};
    for (const e of err.problem.errors) {
      fields[e.field] = fields[e.field] ? `${fields[e.field]}; ${e.detail}` : e.detail;
    }
    return { message: "", fields };
  }
  return { message: message(err), fields: {} };
}
