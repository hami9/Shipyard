import { expect, test, type Page } from "@playwright/test";
import { signIn, url } from "./helpers.ts";

// The manual accessibility pass (P6.6c), as tests: what axe cannot see,
// done with the keyboard alone.

const focused = (page: Page) =>
  page.evaluate(() => {
    const el = document.activeElement as HTMLElement | null;
    return { tag: el?.tagName.toLowerCase(), id: el?.id ?? "", text: el?.textContent?.trim() ?? "" };
  });

test("titles, the skip link, and focus after a page change", async ({ page }) => {
  await page.goto(url + "/");
  await expect(page).toHaveTitle("Sign in · Shipyard");
  expect((await focused(page)).id).toBe("token"); // the login starts in its field
  await signIn(page, "read");
  await expect(page).toHaveTitle("Apps · Shipyard");

  await page.keyboard.press("Tab");
  expect((await focused(page)).text).toBe("Skip to content");
  await expect(page.getByRole("link", { name: "Skip to content" })).toBeVisible();
  await page.keyboard.press("Enter");
  expect((await focused(page)).id).toBe("main");

  // A link followed by keyboard: new title, focus on the new content, and
  // the change announced.
  await page.getByRole("link", { name: "web", exact: true }).focus();
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/\/apps\/web$/);
  await expect(page).toHaveTitle("web · Shipyard");
  expect((await focused(page)).id).toBe("main");
  await expect(page.locator("[aria-live=polite][aria-atomic=true]")).toHaveText("web · Shipyard");
  await page.goBack();
  await expect(page).toHaveTitle("Apps · Shipyard");
});

test("a form with errors puts focus on the first, and describes it", async ({ page }) => {
  await signIn(page, "admin", "/new");
  await page.getByLabel("Name", { exact: true }).focus();
  await page.keyboard.press("Enter"); // submit, empty
  await expect(page.locator("#slug")).toBeFocused();
  await expect(page.locator("#slug")).toHaveAttribute("aria-invalid", "true");
  await expect(page.locator("#slug")).toHaveAccessibleDescription("is required");
  await expect(page.locator("#branch")).toHaveAccessibleDescription("deploys come from this branch");
});

test("confirmations take focus, Escape cancels, focus comes back", async ({ page }) => {
  await signIn(page, "admin", "/apps/docs/domains");
  await page.getByLabel("Hostname", { exact: true }).fill("a11y.example.com");
  await page.keyboard.press("Enter");
  const remove = page.getByRole("row", { name: /a11y\.example\.com/ }).getByRole("button", { name: "Remove" });
  await remove.focus();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("button", { name: "Cancel" })).toBeFocused(); // the safe choice
  await page.keyboard.press("Escape");
  await expect(remove).toBeFocused();
  await page.keyboard.press("Enter");
  await page.getByRole("button", { name: "Remove a11y.example.com" }).click();
  await expect(page.getByText("No domains yet.")).toBeVisible();
  await expect(page.getByRole("heading", { level: 1, name: "Domains" })).toBeFocused();
});

test("the rollback and deploy panels behave as dialogs", async ({ page }) => {
  await signIn(page, "deploy", "/apps/web");
  const rollback = page.locator("table.releases tbody tr").nth(3).getByRole("button", { name: "Roll back" });
  await rollback.focus();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("dialog")).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(rollback).toBeFocused();

  await page.getByRole("button", { name: "Deploy" }).focus();
  await page.keyboard.press("Enter");
  await expect(page.getByLabel(/Commit/)).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("button", { name: "Deploy" })).toBeFocused();
});

test("an operation's events are a focusable, labelled log", async ({ page }) => {
  await signIn(page, "read", "/apps/web");
  await page.getByRole("link", { name: "Details", exact: true }).first().click();
  const log = page.getByRole("log", { name: "Events" });
  await expect(log).toBeVisible();
  await expect(log).toHaveAttribute("tabindex", "0");
  await expect(log).toHaveAttribute("aria-live", "polite");
});
