import { expect, test } from "@playwright/test";
import { accessible, signIn, url } from "./helpers.ts";

// Token management (P6.5c) on tokens only these tests use: rotate, spare and
// doomed, which they end (test/uiseed).
test.describe.configure({ mode: "serial" });

test("rotate: the page moves to the new token, shown once", async ({ page }) => {
  await signIn(page, "rotate", "/tokens");
  await page.getByLabel("The old token:").selectOption({ label: "Keep it 1 hour" });
  await page.getByRole("button", { name: "Rotate" }).click();
  await expect(page.getByRole("status")).toContainText("The old token works until");
  const fresh = await page.locator(".secret code").textContent();
  expect(fresh).toMatch(/^shp_[A-Za-z0-9_-]{40,}$/);
  expect(await page.evaluate(() => sessionStorage.getItem("shipyard.token"))).toBe(fresh);
  await accessible(page);

  await page.reload();
  await expect(page.getByRole("button", { name: "Sign out" })).toBeVisible();
  await expect(page.locator(".secret")).toHaveCount(0);

  await page.getByRole("button", { name: "Rotate" }).click(); // ending the old one now
  await expect(page.getByRole("status")).toContainText("The old token no longer works.");
  await page.goto(url + "/apps/web");
  await expect(page.getByRole("heading", { level: 1, name: "web" })).toBeVisible();
});

test("revoke another token, then this session's own", async ({ page }) => {
  await signIn(page, "doomed", "/tokens");
  const row = page.getByRole("row", { name: /ui-spare/ });
  await row.getByRole("button", { name: "Revoke" }).click();
  await row.getByRole("button", { name: "Revoke" }).click();
  await expect(row.getByText("revoked")).toBeVisible();
  await expect(page.getByRole("button", { name: "Sign out" })).toBeVisible();

  const self = page.getByRole("row", { name: /this session/ });
  await self.getByRole("button", { name: "Revoke" }).click();
  await self.getByRole("button", { name: "Revoke and sign out" }).click();
  await expect(page.getByRole("alert")).toHaveText("You revoked the token this page used.");
  expect(await page.evaluate(() => sessionStorage.getItem("shipyard.token"))).toBeNull();
});
