// The few Node APIs the tests use, so tsc checks them without @types/node.

// The Playwright tests read what e2e/setup.mjs started from the environment.
declare const process: { env: Record<string, string | undefined> };
declare module "node:test" {
  export function test(name: string, fn: () => void | Promise<void>): Promise<void>;
}

declare module "node:assert/strict" {
  const assert: {
    equal(actual: unknown, expected: unknown, message?: string): void;
    deepEqual(actual: unknown, expected: unknown, message?: string): void;
    ok(value: unknown, message?: string): asserts value;
    match(value: string, regexp: RegExp, message?: string): void;
    throws(fn: () => unknown, expected?: unknown): void;
    rejects(promise: Promise<unknown> | (() => Promise<unknown>), expected?: unknown): Promise<void>;
  };
  export default assert;
}
