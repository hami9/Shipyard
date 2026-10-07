// What an app's releases say about it, for the overview.

import type { Release } from "./api/schema.ts";

export interface Summary {
  /** The release taking traffic, if it is among those given. */
  serving: Release | undefined;
  /** The newest deploy or rollback, whatever its outcome. */
  latest: Release | undefined;
}

/** summarize reads releases newest first, as the API lists them. */
export function summarize(releases: Release[]): Summary {
  return {
    serving: releases.find((r) => r.status === "active"),
    latest: releases[0],
  };
}

export type ReleaseFilter = "all" | "failed" | "rollbacks";

export function filterReleases(releases: Release[], f: ReleaseFilter): Release[] {
  switch (f) {
    case "failed":
      return releases.filter((r) => r.status === "failed");
    case "rollbacks":
      return releases.filter((r) => r.kind === "rollback");
    default:
      return releases;
  }
}
