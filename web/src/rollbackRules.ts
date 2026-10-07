// Rollback rules the page applies before and after asking the API
// (ARCHITECTURE §5, Rollback; internal/api/rollbacks.go).

import { ApiError } from "./api/client.ts";
import type { Release, Scope } from "./api/schema.ts";
import { canDeploy } from "./forms.ts";

/** canRollBack: only a release that served before is a target, and only with the deploy scope. */
export function canRollBack(r: Release, scopes: Scope[]): boolean {
  return r.status === "superseded" && canDeploy(scopes);
}

export type ConfigChoice = "current" | "old";

/**
 * needsConfigChoice: the API refused (409) because secrets changed since the
 * target ran, and asks for with_current_config or with_old_config. Its
 * message names both fields; the page then offers the choice.
 */
export function needsConfigChoice(err: unknown): boolean {
  return err instanceof ApiError && err.status === 409 && (err.problem?.detail ?? "").includes("with_current_config");
}

/** rollbackBody is the request for a target and, after a 409, the operator's choice. */
export function rollbackBody(to: string, choice?: ConfigChoice) {
  return {
    to,
    ...(choice === "current" ? { with_current_config: true } : {}),
    ...(choice === "old" ? { with_old_config: true } : {}),
  };
}
