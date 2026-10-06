// The UI's pages, as paths. Caddy answers any path that is not a file with
// index.html (ADR-0016), so these survive a reload and can be bookmarked.

export type Route =
  | { page: "apps" }
  | { page: "app"; slug: string }
  | { page: "missing"; path: string };

// Mirrors the API's slug rule (api/openapi.json, Slug).
const slugRE = /^[a-z]([a-z0-9-]{0,38}[a-z0-9])?$/;

export function parseRoute(path: string): Route {
  const parts = path.split("/").filter((p) => p !== "");
  if (parts.length === 0 || (parts.length === 1 && parts[0] === "apps")) {
    return { page: "apps" };
  }
  if (parts.length === 2 && parts[0] === "apps") {
    let slug: string;
    try {
      slug = decodeURIComponent(parts[1] ?? "");
    } catch {
      return { page: "missing", path };
    }
    if (slugRE.test(slug)) {
      return { page: "app", slug };
    }
  }
  return { page: "missing", path };
}

export function href(r: Route): string {
  switch (r.page) {
    case "apps":
      return "/";
    case "app":
      return `/apps/${encodeURIComponent(r.slug)}`;
    case "missing":
      return r.path;
  }
}
