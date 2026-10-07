// The app form's values, as typed, and the requests they become. The API
// checks every field again and names a bad one by the same name, so the
// page checks only what it must to build a request: numbers and durations.

import type { App, AppCreate, AppSettings } from "./api/schema.ts";

export interface AppValues {
  slug: string;
  repo: string;
  branch: string;
  port: string;
  dockerfile_path: string;
  build_context: string;
  health_path: string;
  health_timeout: string;
  cpu_limit: string;
  /** In MiB; the API takes bytes. */
  memory_mib: string;
  stop_timeout: string;
  auto_deploy: boolean;
  github_installation_id: string;
}

const MiB = 1 << 20;

/** valuesOf is an app's settings as form values, or a new app's defaults (the schema's). */
export function valuesOf(app?: App): AppValues {
  if (!app) {
    return {
      slug: "", repo: "", branch: "main", port: "", dockerfile_path: "Dockerfile", build_context: ".", health_path: "/",
      health_timeout: "60s", cpu_limit: "1", memory_mib: "512", stop_timeout: "10s", auto_deploy: false, github_installation_id: "",
    };
  }
  return {
    slug: app.slug, repo: app.repo, branch: app.branch, port: String(app.port), dockerfile_path: app.dockerfile_path,
    build_context: app.build_context, health_path: app.health_path, health_timeout: app.health_timeout,
    cpu_limit: String(app.cpu_limit), memory_mib: String(app.memory_limit / MiB), stop_timeout: app.stop_timeout,
    auto_deploy: app.auto_deploy, github_installation_id: app.github_installation_id === null ? "" : String(app.github_installation_id),
  };
}

/** durationMs parses a Go duration ("90s", "1m30s", "500ms"); undefined if it is not one. */
export function durationMs(s: string): number | undefined {
  const t = s.trim();
  if (t === "0") {
    return 0;
  }
  const re = /(\d+(?:\.\d+)?)(ms|h|m|s)/y;
  let total = 0;
  let at = 0;
  while (at < t.length) {
    re.lastIndex = at;
    const m = re.exec(t);
    if (!m) {
      return undefined;
    }
    total += Number(m[1]) * { ms: 1, s: 1000, m: 60_000, h: 3_600_000 }[m[2] as "ms" | "s" | "m" | "h"];
    at = re.lastIndex;
  }
  return t === "" ? undefined : total;
}

type Fields = Record<string, string>;

// settings reads the shared fields; with base, only those that differ from it.
function settings(v: AppValues, fields: Fields, base?: App): AppSettings {
  const out: AppSettings = {};
  const text = (k: "branch" | "dockerfile_path" | "build_context" | "health_path") => {
    const s = v[k].trim();
    if (s !== "" && s !== base?.[k]) {
      out[k] = s;
    }
  };
  text("branch");
  text("dockerfile_path");
  text("build_context");
  text("health_path");

  const port = Number(v.port);
  if (!/^\d+$/.test(v.port.trim()) || port < 1 || port > 65535) {
    fields["port"] = "a number from 1 to 65535";
  } else if (port !== base?.port) {
    out.port = port;
  }
  for (const k of ["health_timeout", "stop_timeout"] as const) {
    const s = v[k].trim();
    const ms = durationMs(s);
    if (ms === undefined) {
      fields[k] = "a duration such as 30s, 2m or 1m30s";
    } else if (base === undefined || ms !== durationMs(base[k])) {
      out[k] = s;
    }
  }
  const cpu = Number(v.cpu_limit);
  if (v.cpu_limit.trim() === "" || !(cpu > 0)) {
    fields["cpu_limit"] = "a number of CPUs above 0, such as 0.5 or 2";
  } else if (cpu !== base?.cpu_limit) {
    out.cpu_limit = cpu;
  }
  const mib = Number(v.memory_mib);
  if (!/^\d+$/.test(v.memory_mib.trim()) || mib < 6) {
    fields["memory_limit"] = "a whole number of MiB, at least 6";
  } else if (mib * MiB !== base?.memory_limit) {
    out.memory_limit = mib * MiB;
  }
  if (v.auto_deploy !== (base?.auto_deploy ?? false) || base === undefined) {
    out.auto_deploy = v.auto_deploy;
  }
  const gh = v.github_installation_id.trim();
  if (gh !== "") {
    if (!/^\d+$/.test(gh) || Number(gh) < 1) {
      fields["github_installation_id"] = "a positive number, or empty";
    } else if (Number(gh) !== base?.github_installation_id) {
      out.github_installation_id = Number(gh);
    }
  }
  return out;
}

/** createBody is the request for a new app, or the fields to fix first. */
export function createBody(v: AppValues): { body?: AppCreate; fields: Fields } {
  const fields: Fields = {};
  const s = settings(v, fields);
  for (const k of ["slug", "repo", "branch"] as const) {
    if (v[k].trim() === "") {
      fields[k] = "is required";
    }
  }
  if (Object.keys(fields).length > 0 || s.port === undefined) {
    return { fields };
  }
  return { body: { ...s, slug: v.slug.trim(), repo: v.repo.trim(), branch: v.branch.trim(), port: s.port }, fields };
}

/** updateBody is the change to an app: only what differs, so a PATCH keeps the rest. */
export function updateBody(v: AppValues, app: App): { body?: AppSettings; fields: Fields } {
  const fields: Fields = {};
  const body = settings(v, fields, app);
  return Object.keys(fields).length > 0 ? { fields } : { body, fields };
}
