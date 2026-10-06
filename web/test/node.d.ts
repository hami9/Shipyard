// The few Node test APIs the tests use, so tsc checks them without
// @types/node (ADR-0015: typescript is the only npm dependency).
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
