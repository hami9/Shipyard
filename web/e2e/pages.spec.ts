import { expect, test } from "@playwright/test";
import { accessible, cspViolations, signIn, url } from "./helpers.ts";

// Every page, as an admin sees it, passes axe's WCAG 2.2 AA rules in both
// color schemes and runs under Caddy's CSP without a violation (P6.6a).

const pages: { path: string; heading: string | RegExp }[] = [
  { path: "/", heading: "Apps" },
  { path: "/apps/web", heading: "web" },
  { path: "/apps/web/env", heading: "Environment" },
  { path: "/apps/web/domains", heading: "Domains" },
  { path: "/apps/web/logs", heading: "Logs" },
  { path: "/apps/web/settings", heading: "Settings" },
  { path: "/new", heading: "New app" },
  { path: "/tokens", heading: "Tokens" },
  { path: "/nowhere", heading: /./ },
];

for (const scheme of ["light", "dark"] as const) {
  test.describe(`${scheme} scheme`, () => {
    test.use({ colorScheme: scheme });

    test("the login page", async ({ page }) => {
      const csp = cspViolations(page);
      await page.goto(url + "/");
      await expect(page.getByRole("heading", { name: "Shipyard" })).toBeVisible();
      await accessible(page);
      expect(csp).toEqual([]);
    });

    test("every signed-in page", async ({ page }) => {
      const csp = cspViolations(page);
      await signIn(page, "admin");
      for (const p of pages) {
        await page.goto(url + p.path);
        if (p.path === "/nowhere") {
          await expect(page.getByText("There is no page at")).toBeVisible();
        } else {
          await expect(page.getByRole("heading", { level: 1, name: p.heading })).toBeVisible();
        }
        await page.waitForLoadState("networkidle");
        await accessible(page);
      }
      // An operation page, reached from a release.
      await page.goto(url + "/apps/web");
      await page.getByRole("link", { name: "Details", exact: true }).first().click();
      await expect(page.getByText("seeded release 25")).toBeVisible();
      await accessible(page);
      expect(csp).toEqual([]);
    });
  });
}

test("a wrong token is refused, and a good one is kept across a reload", async ({ page }) => {
  await page.goto(url + "/");
  await page.getByLabel("API token").fill("shp_not_a_real_token");
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("alert")).toHaveText("The token is invalid, expired, or revoked.");

  await signIn(page, "read", "/apps/web");
  await expect(page).toHaveURL(/\/apps\/web$/);
  await page.reload();
  await expect(page.getByRole("heading", { level: 1, name: "web" })).toBeVisible();
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByLabel("API token")).toBeVisible();
  await page.reload();
  await expect(page.getByLabel("API token")).toBeVisible();
});

test("an app's pages are tabs, and the history filters", async ({ page }) => {
  await signIn(page, "read", "/apps/web");
  const tabs = page.getByRole("navigation", { name: "web pages" });
  await expect(tabs.getByRole("link", { name: "Overview" })).toHaveAttribute("aria-current", "page");
  await expect(tabs.getByRole("link", { name: "Settings" })).toHaveCount(0); // read cannot change them
  await expect(page.getByRole("heading", { name: "Latest deploy" })).toBeVisible();

  await page.getByRole("button", { name: "Failed", exact: true }).click();
  const rows = page.locator("table.releases tbody tr");
  await expect(rows).not.toHaveCount(0);
  for (const row of await rows.all()) {
    await expect(row.locator(".badge")).toHaveText("failed");
  }
  await page.getByRole("button", { name: "All", exact: true }).click();
  await expect(rows).toHaveCount(20);

  await tabs.getByRole("link", { name: "Domains" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Domains" })).toBeVisible();
  await expect(tabs.getByRole("link", { name: "Domains" })).toHaveAttribute("aria-current", "page");
  await tabs.getByRole("link", { name: "Overview" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "web" })).toBeVisible();
});

test("a failed release explains itself on its page", async ({ page }) => {
  await signIn(page, "read", "/apps/web");
  await page.getByRole("button", { name: "Failed", exact: true }).click();
  await page.locator("table.releases tbody tr").first().getByRole("link", { name: "Details", exact: true }).click();
  await expect(page.getByRole("heading", { name: "This release failed" })).toBeVisible();
  await expect(page.locator(".reason-text")).toContainText("health check did not pass");
  await expect(page.getByText("never passed its health check")).toBeVisible();
  // The seeded operation recorded no phase: the reason names the step.
  await expect(page.getByRole("list", { name: "Steps" }).locator("li.failed")).toHaveText(/Health check/);
  await accessible(page);
});
