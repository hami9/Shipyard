// The UI's pages, as paths. Caddy answers any path that is not a file with
// index.html (ADR-0016), so these survive a reload and can be bookmarked.

export type Route =
  | { page: "apps" }
  | { page: "app"; slug: string }
  | { page: "logs"; slug: string }
  | { page: "env"; slug: string }
  | { page: "domains"; slug: string }
  | { page: "settings"; slug: string }
  // Not under /apps/: "new" is a valid slug.
  | { page: "new" }
  | { page: "tokens" }
  | { page: "operation"; id: string }
  | { page: "missing"; path: string };

// Mirror the API's rules (api/openapi.json, Slug and ID).
const slugRE = /^[a-z]([a-z0-9-]{0,38}[a-z0-9])?$/;
const idRE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

export function parseRoute(path: string): Route {
  const missing: Route = { page: "missing", path };
  let parts: string[];
  try {
    parts = path.split("/").filter((p) => p !== "").map(decodeURIComponent);
  } catch {
    return missing;
  }
  const [first, second, third] = parts;
  switch (parts.length) {
    case 0:
      return { page: "apps" };
    case 1:
      switch (first) {
        case "apps":
          return { page: "apps" };
        case "new":
        case "tokens":
          return { page: first };
      }
      return missing;
    case 2:
      if (first === "apps" && slugRE.test(second ?? "")) {
        return { page: "app", slug: second ?? "" };
      }
      if (first === "operations" && idRE.test(second ?? "")) {
        return { page: "operation", id: second ?? "" };
      }
      return missing;
    case 3:
      if (first !== "apps" || !slugRE.test(second ?? "")) {
        return missing;
      }
      switch (third) {
        case "logs":
        case "env":
        case "domains":
        case "settings":
          return { page: third, slug: second ?? "" };
      }
      return missing;
  }
  return missing;
}

export function href(r: Route): string {
  switch (r.page) {
    case "apps":
      return "/";
    case "app":
      return `/apps/${encodeURIComponent(r.slug)}`;
    case "logs":
    case "env":
    case "domains":
    case "settings":
      return `/apps/${encodeURIComponent(r.slug)}/${r.page}`;
    case "new":
    case "tokens":
      return `/${r.page}`;
    case "operation":
      return `/operations/${r.id}`;
    case "missing":
      return r.path;
  }
}
