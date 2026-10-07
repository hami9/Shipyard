import { expect, test, type Page } from "@playwright/test";
import { accessible, signIn, url, workerStandIn } from "./helpers.ts";

// The UI's flows against the real API (P6.6b). The tests share the seeded
// database and run in order; each leaves what the next expects.
test.describe.configure({ mode: "serial" });

async function opPage(page: Page, status: RegExp): Promise<void> {
  await expect(page).toHaveURL(/\/operations\/[0-9a-f-]{36}$/);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(status);
}

test("an app: create, refuse a bad field, change settings, delete", async ({ page }) => {
  await signIn(page, "admin");
  await page.getByRole("link", { name: "New app" }).click();
  await page.getByLabel("Name", { exact: true }).fill("web");
  await page.getByLabel("GitHub repository", { exact: true }).fill("acme/shop");
  await page.getByLabel("Port", { exact: true }).fill("8080");
  await page.getByRole("button", { name: "Create app" }).click();
  await expect(page.getByRole("alert")).toHaveText("an app with this slug already exists");

  await page.getByLabel("Name", { exact: true }).fill("shop");
  await page.getByLabel("Branch", { exact: true }).fill("-bad");
  await page.getByRole("button", { name: "Create app" }).click();
  await expect(page.getByText("must not start with '-'")).toBeVisible();
  await accessible(page); // a form showing its errors

  await page.getByLabel("Branch", { exact: true }).fill("main");
  await page.getByRole("button", { name: "Create app" }).click();
  await expect(page).toHaveURL(/\/apps\/shop$/);
  await expect(page.getByRole("heading", { level: 1, name: "shop" })).toBeVisible();

  await page.getByRole("link", { name: "Settings" }).click();
  await page.getByLabel("Port", { exact: true }).fill("9090");
  await page.getByRole("button", { name: "Save" }).click();
  await expect(page.getByRole("status")).toHaveText("Saved. The next deploy uses the new settings.");
  await page.getByRole("button", { name: "Save" }).click();
  await expect(page.getByRole("status")).toHaveText("Nothing changed.");

  const del = page.getByRole("button", { name: "Delete shop" });
  await page.getByLabel(/to confirm/).fill("Shop");
  await expect(del).toBeDisabled();
  await page.getByLabel(/to confirm/).fill("shop");
  await del.click();
  await opPage(page, /^delete/);
});

test("deploy the branch head, and follow it", async ({ page }) => {
  await signIn(page, "deploy", "/apps/docs");
  await page.getByRole("button", { name: "Deploy" }).click();
  await page.getByLabel(/Commit/).fill("abc123");
  await page.locator("form.confirm").getByRole("button", { name: "Deploy" }).click();
  await expect(page.getByRole("alert")).toContainText("A full commit SHA");
  await accessible(page);
  await page.getByLabel(/Commit/).fill("");
  await page.locator("form.confirm").getByRole("button", { name: "Deploy" }).click();
  await opPage(page, /^deploy/);
  await expect(page.getByRole("status")).toContainText("Live");
});

test("roll back to an earlier release", async ({ page }) => {
  await signIn(page, "deploy", "/apps/web");
  const rows = page.locator("table.releases tbody tr");
  await expect(rows).toHaveCount(20);
  // Only releases that served before: not the active one, not the failed one.
  await expect(rows.nth(0).getByRole("button", { name: "Roll back" })).toHaveCount(0);
  await expect(rows.nth(1).getByRole("button", { name: "Roll back" })).toHaveCount(0);
  await rows.nth(2).getByRole("button", { name: "Roll back" }).click();
  await expect(page.getByRole("dialog")).toContainText("nothing is rebuilt");
  await accessible(page);
  await page.getByRole("dialog").getByRole("button", { name: "Roll back" }).click();
  await expect(page.getByRole("status").first()).toContainText("Rollback queued");
  await page.getByRole("link", { name: "Follow it" }).click();
  await opPage(page, /^rollback/);

  await page.goto(url + "/apps/web");
  await page.getByRole("button", { name: "Older releases" }).click();
  await expect(rows).toHaveCount(25);
});

test("environment: values are written, never shown", async ({ page }) => {
  const value = "e2e-secret-value-71c9";
  await signIn(page, "admin", "/apps/web/env");
  await page.getByLabel("Key", { exact: true }).fill("1BAD");
  await page.getByLabel("Value", { exact: true }).fill("x");
  await page.getByRole("button", { name: "Set", exact: true }).click();
  await expect(page.getByText("a letter or _, then letters")).toBeVisible();
  await accessible(page);

  await page.getByLabel("Key", { exact: true }).fill("API_TOKEN");
  await page.getByLabel("Value", { exact: true }).fill(value);
  const put = page.waitForResponse((r) => r.request().method() === "PUT" && r.url().includes("/env/API_TOKEN"));
  await page.getByRole("button", { name: "Set", exact: true }).click();
  expect(await (await put).text()).not.toContain(value);
  await expect(page.getByRole("status")).toContainText("API_TOKEN set");
  await expect(page.getByRole("cell", { name: "API_TOKEN" })).toBeVisible();
  await expect(page.getByLabel("Value", { exact: true })).toHaveValue("");
  expect(await page.content()).not.toContain(value);

  await page.getByRole("row", { name: /API_TOKEN/ }).getByRole("button", { name: "Remove" }).click();
  await page.getByRole("button", { name: "Remove API_TOKEN" }).click();
  await expect(page.getByRole("status")).toContainText("API_TOKEN removed");
  await expect(page.getByRole("cell", { name: "API_TOKEN" })).toHaveCount(0);
});

test("domains: add, refuse, remove", async ({ page }) => {
  await signIn(page, "admin", "/apps/web/domains");
  await page.getByLabel("Hostname", { exact: true }).fill("bad host");
  await page.getByRole("button", { name: "Add" }).click();
  await expect(page.getByText("must be a fully qualified domain name")).toBeVisible();
  await accessible(page);
  await page.getByLabel("Hostname", { exact: true }).fill("Shop.Example.com");
  await page.getByRole("button", { name: "Add" }).click();
  await expect(page.getByRole("link", { name: "shop.example.com" })).toBeVisible();

  await page.goto(url + "/apps/docs/domains");
  await page.getByLabel("Hostname", { exact: true }).fill("shop.example.com");
  await page.getByRole("button", { name: "Add" }).click();
  await expect(page.getByRole("alert")).toHaveText("this hostname is already used by an app");

  await page.goto(url + "/apps/web/domains");
  await page.getByRole("row", { name: /shop\.example\.com/ }).getByRole("button", { name: "Remove" }).click();
  await page.getByRole("button", { name: "Remove shop.example.com" }).click();
  await expect(page.getByText("No domains yet.")).toBeVisible();
});

test("logs: the tail, live lines, and the end", async ({ page }) => {
  await signIn(page, "read", "/apps/web/logs");
  if (!workerStandIn) {
    await expect(page.getByRole("alert")).toHaveText("logs are unavailable: the worker is not running");
    await expect(page.getByText("systemctl status shipyard-worker")).toBeVisible();
    await expect(page.getByRole("button", { name: "Reconnect logs" })).toBeVisible();
    return;
  }
  await expect(page.getByText("live line 2")).toBeVisible();
  await expect(page.locator(".log li.stderr")).toHaveText(/warning: slow request/);
  await expect(page.getByRole("status")).toHaveText("The stream ended: the container stopped.");
  await page.getByLabel("Follow").uncheck();
  await expect(page.getByRole("status")).toHaveText("The stream ended: end of the requested lines.");
  await accessible(page);
});

test("a read token sees, and changes nothing", async ({ page }) => {
  await signIn(page, "read", "/apps/web");
  await expect(page.locator("table.releases tbody tr").first()).toBeVisible();
  await expect(page.getByRole("button", { name: "Deploy" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Roll back" })).toHaveCount(0);
  await expect(page.getByRole("link", { name: "Settings" })).toHaveCount(0);
  await page.goto(url + "/");
  await expect(page.getByRole("link", { name: "New app" })).toHaveCount(0);
  for (const path of ["/apps/web/env", "/apps/web/domains"]) {
    await page.goto(url + path);
    await expect(page.locator("form.panel")).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Remove" })).toHaveCount(0);
  }
  await page.goto(url + "/tokens");
  await expect(page.getByText("needs the admin scope")).toBeVisible();
});
