// An operation's steps, as the worker records them (internal/app: deploy.go
// and delete.go persist each phase before its side effect, invariant 3).

import type { Operation } from "./api/schema.ts";

export type StepState = "done" | "current" | "failed" | "todo";

export interface Step {
  phase: string;
  label: string;
  state: StepState;
}

const plans: Record<Operation["kind"], [string, string][]> = {
  deploy: [
    ["fetch", "Fetch"],
    ["build", "Build"],
    ["start", "Start"],
    ["health", "Health check"],
    ["switch", "Switch traffic"],
    ["activate", "Activate"],
  ],
  // A rollback starts the retained image: nothing is fetched or built.
  rollback: [
    ["start", "Start"],
    ["health", "Health check"],
    ["switch", "Switch traffic"],
    ["activate", "Activate"],
  ],
  delete: [
    ["release", "Take offline"],
    ["containers", "Containers"],
    ["network", "Network"],
    ["images", "Images"],
    ["remove", "Remove"],
  ],
};

/**
 * steps lays an operation's plan out against the phase it reached. A phase
 * the plan does not know (a newer worker) leaves every step undecided rather
 * than guessing.
 */
export function steps(op: Pick<Operation, "kind" | "status"> & { phase?: string | undefined }): Step[] {
  const plan = plans[op.kind] ?? [];
  const at = op.phase ? plan.findIndex(([p]) => p === op.phase) : -1;
  return plan.map(([phase, label], i) => {
    let state: StepState = "todo";
    if (op.status === "succeeded") {
      state = "done";
    } else if (at >= 0 && i < at) {
      state = "done";
    } else if (at >= 0 && i === at) {
      state = op.status === "failed" ? "failed" : op.status === "running" ? "current" : "todo";
    }
    return { phase, label, state };
  });
}

// The worker's failure reasons start with their step (internal/app/deploy.go).
const prefixes: [string, string][] = [
  ["fetch:", "fetch"],
  ["build:", "build"],
  ["start container:", "start"],
  ["health check:", "health"],
  ["switch traffic:", "switch"],
];

/** failedPhase is where a failure happened: the recorded phase, else the one its reason names. */
export function failedPhase(phase: string | undefined, reason: string | undefined): string | undefined {
  if (phase) {
    return phase;
  }
  return prefixes.find(([p]) => reason?.startsWith(p))?.[1];
}

/** advice is what to look at after a failure in a phase. */
export function advice(phase: string | undefined): string | undefined {
  switch (phase) {
    case "fetch":
      return "The commit could not be fetched. Check that it is on the app's branch, and that the server can reach the repository (a private one needs the GitHub App installation in Settings).";
    case "build":
      return "The image did not build. The build's output is in the events below; check the Dockerfile path and build context in Settings.";
    case "start":
      return "The container did not start. Check the app's logs, its port, and its memory limit in Settings.";
    case "health":
      return "The new release never passed its health check. Check that the app answers 2xx on its health path and port, within the health timeout (Settings), and look at its logs.";
    case "switch":
    case "activate":
      return "Traffic could not be switched to the new release. Check the worker and Caddy on the server; the previous release kept serving.";
  }
  return undefined;
}

/**
 * logAdvice says why logs can fail and what to do. They come from the
 * worker, which reads the container (the API never touches Docker,
 * invariant 1): a 503 means the API could not reach the worker at all.
 */
export function logAdvice(status: number | undefined, slug: string): string {
  switch (status) {
    case 503:
      return "Logs come from shipyard-worker, the service that runs deploys, and it is not answering. On the server, check it with: systemctl status shipyard-worker. Deploys wait in the queue until it is back. Then choose Reconnect logs.";
    case 502:
      return "The worker answered, but could not read the container's logs. The release may have just stopped: check its status on the overview, then choose Reconnect logs.";
    case 404:
      return `Nothing of ${slug} is running to read logs from. Deploy it, or roll back to an earlier release, from its overview.`;
  }
  return "Choose Reconnect logs to try again.";
}
