import { AxeBuilder } from "@axe-core/playwright";
import { expect, type Page } from "@playwright/test";

// What e2e/setup.mjs started, from the environment.
export const url = env("SHIPYARD_UI_URL");
export const workerStandIn = process.env["SHIPYARD_UI_WORKER"] === "stand-in";

function env(name: string): string {
  const v = process.env[name];
  if (!v) {
    throw new Error(`${name} is not set: run the tests with npx playwright test (e2e/setup.mjs sets it)`);
  }
  return v;
}

/**
 * The seeded tokens (test/uiseed): admin, deploy and read for every test;
 * rotate (deploy), spare (read) and doomed (admin) only for tokens.spec.ts,
 * which ends them.
 */
export type TokenName = "admin" | "deploy" | "read" | "rotate" | "spare" | "doomed";

export function token(name: TokenName): string {
  return env(`SHIPYARD_UI_TOKEN_${name.toUpperCase()}`);
}

/** signIn opens path and signs in with that token, through the login form. */
export async function signIn(page: Page, name: TokenName, path = "/"): Promise<void> {
  await page.goto(url + path);
  await page.getByLabel("API token").fill(token(name));
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("button", { name: "Sign out" })).toBeVisible();
}

/**
 * accessible fails on any WCAG 2.2 A or AA violation axe finds on the page
 * as it is now (the owner's choice, 2026-10-07).
 */
export async function accessible(page: Page): Promise<void> {
  const r = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"]).analyze();
  const found = r.violations.map((v) => `${v.id} (${v.impact}): ${v.help}\n    ${v.nodes.map((n) => n.target.join(" ")).join("\n    ")}`);
  expect(found, `axe on ${page.url()}`).toEqual([]);
}

/** cspClean collects Content-Security-Policy violations, which the console reports. */
export function cspViolations(page: Page): string[] {
  const seen: string[] = [];
  page.on("console", (m) => {
    if (m.type() === "error" && m.text().includes("Content Security Policy")) {
      seen.push(m.text());
    }
  });
  return seen;
}
