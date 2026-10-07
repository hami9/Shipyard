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
  { path: "/apps/web/settings", heading: "web settings" },
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
      await page.getByRole("link", { name: "events" }).first().click();
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
