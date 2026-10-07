// Code generated from api/openapi.json by internal/openapits (make web-types). DO NOT EDIT.
// The API's schemas, and its operations by operationId. The client is web/src/api/client.ts.

export type ID = string;

/** A Go duration, e.g. "90s" or "1m30s" */
export type Duration = string;

export type Timestamp = string;

export type Scope = "read" | "deploy" | "admin";

export interface Problem {
  type: string;
  title: string;
  status: number;
  detail?: string;
  errors?: FieldError[];
}

export interface FieldError {
  field: string;
  detail: string;
}

export interface Health {
  status: "ok";
}

export interface Ready {
  status: "ready";
}

export interface ServerStatus {
  /** Whether the worker answered on its socket */
  worker: "up" | "down";
  /** The worker's last health check; null before its first, or when it is down */
  checked_at: string | null;
  /** Running apps at that check; empty when the worker is down */
  apps: AppHealth[];
  /** SHIPYARD_PUBLIC_IPS: where A/AAAA records must point */
  public_ips: string[];
}

export interface AppHealth {
  app: Slug;
  /** Its container is running */
  running: boolean;
  /** Running, and one GET of its health path passed */
  healthy: boolean;
}

export interface Whoami {
  /** The display prefix */
  token: string;
  name: string;
  scopes: Scope[];
  expires_at: string | null;
}

export interface Token {
  prefix: string;
  name: string;
  scopes: Scope[];
  status: "active" | "expired" | "revoked";
  expires_at: string | null;
  last_used_at: string | null;
  revoked_at: string | null;
  created_at: Timestamp;
}

export interface TokenList {
  tokens: Token[];
}

export interface RotateRequest {
  /** How long the old token keeps working */
  grace_seconds?: number;
}

export interface RotateResponse {
  /** The new token: shown only here */
  token: string;
  new: Token;
  old: Token;
}

export interface App {
  id: ID;
  slug: Slug;
  repo: Repo;
  branch: string;
  dockerfile_path: string;
  build_context: string;
  port: number;
  health_path: string;
  health_timeout: Duration;
  cpu_limit: number;
  /** Bytes */
  memory_limit: number;
  stop_timeout: Duration;
  auto_deploy: boolean;
  github_installation_id: number | null;
  created_at: Timestamp;
  updated_at: Timestamp;
}

export interface AppList {
  apps: App[];
}

export type Slug = string;

/** A GitHub repository as owner/name */
export type Repo = string;

/** The settings that can change. In a PATCH, absent fields keep their values. */
export interface AppSettings {
  branch?: string;
  /** Relative to the checkout */
  dockerfile_path?: string;
  /** Relative to the checkout */
  build_context?: string;
  /** The port the app listens on inside its container */
  port?: number;
  health_path?: string;
  /** 1s to 30m; default 60s */
  health_timeout?: Duration;
  cpu_limit?: number;
  /** Bytes */
  memory_limit?: number;
  /** 0s to 10m; default 10s */
  stop_timeout?: Duration;
  /** Deploy on a GitHub push to the branch */
  auto_deploy?: boolean;
  /** Fetch through this GitHub App installation (private repositories) */
  github_installation_id?: number;
}

export interface AppCreate {
  slug: Slug;
  repo: Repo;
  branch: string;
  dockerfile_path?: string;
  build_context?: string;
  port: number;
  health_path?: string;
  health_timeout?: Duration;
  cpu_limit?: number;
  memory_limit?: number;
  stop_timeout?: Duration;
  auto_deploy?: boolean;
  github_installation_id?: number;
}

export type EnvKey = string;

export interface Env {
  /** 0: no environment yet */
  revision: number;
  vars: {
    key: EnvKey;
    secret: boolean;
  }[];
}

export interface EnvSet {
  /** At most 64 KiB, no NUL character */
  value: string;
  secret?: boolean;
}

export interface Domain {
  hostname: string;
  /** null: no release serves it yet */
  deployment_id: string | null;
  dns_checked_at: string | null;
  created_at: Timestamp;
}

export interface DomainList {
  domains: Domain[];
}

export interface DomainAdd {
  hostname: string;
}

export interface DeployRequest {
  /** A full commit SHA on the branch; absent: the branch head */
  ref?: string;
}

export interface RollbackRequest {
  to: ID;
  /** Run it with today's environment */
  with_current_config?: boolean;
  /** Run it with the environment it ran with */
  with_old_config?: boolean;
}

export interface Operation {
  id: ID;
  app_id: ID;
  kind: "deploy" | "rollback" | "delete";
  status: "queued" | "running" | "succeeded" | "failed" | "cancelled";
  /** The step reached, e.g. build or health */
  phase?: string;
  payload: Record<string, unknown>;
  idempotency_key: string;
  attempt: number;
  max_attempts: number;
  last_error?: string;
  created_at: Timestamp;
  finished_at?: Timestamp;
}

export interface Admitted {
  operation: Operation;
  created: boolean;
  /** Queued operations this one replaced */
  superseded: ID[];
}

export interface Release {
  id: ID;
  operation_id: ID;
  kind: "build" | "rollback";
  /** A rollback's source deployment */
  rollback_of?: ID;
  status: "queued" | "building" | "starting" | "health_checking" | "switching" | "active" | "superseded" | "failed" | "cancelled";
  commit: string;
  image_id?: string;
  /** 0: no environment */
  env_revision: number;
  failure_reason?: string;
  created_at: Timestamp;
  active_at?: Timestamp;
  ended_at?: Timestamp;
}

export interface ReleaseList {
  deployments: Release[];
  /** The cursor for the next page (?before=); absent on a short page */
  next?: ID;
}

export interface OperationEvent {
  /** Gapless per operation; also the SSE id */
  seq: number;
  ts: Timestamp;
  level: "debug" | "info" | "warn" | "error";
  message: string;
}

export interface LogLine {
  ts: Timestamp;
  /** stdout or stderr */
  stream: string;
  line: string;
}

export interface LogEnd {
  reason: string;
}

/** Every operation: its request (path, query, header, body), its success response, and a stream's events. */
export interface Operations {
  /** Liveness: the process serves HTTP */
  getHealth: {
    request: {};
    response: Health;
    events: never;
  };
  /** Readiness: PostgreSQL answers */
  getReady: {
    request: {};
    response: Ready;
    events: never;
  };
  /** The worker, the apps' health, and the server's addresses */
  getStatus: {
    request: {};
    response: ServerStatus;
    events: never;
  };
  /** Describe the calling token */
  whoami: {
    request: {};
    response: Whoami;
    events: never;
  };
  /** List every token, newest first */
  listTokens: {
    request: {};
    response: TokenList;
    events: never;
  };
  /** Revoke a token */
  revokeToken: {
    request: { path: { prefix: string } };
    response: Token;
    events: never;
  };
  /** Replace the calling token */
  rotateToken: {
    request: { body?: RotateRequest };
    response: RotateResponse;
    events: never;
  };
  /** List apps */
  listApps: {
    request: {};
    response: AppList;
    events: never;
  };
  /** Create an app */
  createApp: {
    request: { body: AppCreate };
    response: App;
    events: never;
  };
  /** Show an app */
  getApp: {
    request: { path: { app: string } };
    response: App;
    events: never;
  };
  /** Queue the app's deletion */
  deleteApp: {
    request: { path: { app: string }; header?: { "Idempotency-Key"?: string } };
    response: Admitted;
    events: never;
  };
  /** Change an app's settings */
  updateApp: {
    request: { path: { app: string }; body: AppSettings };
    response: App;
    events: never;
  };
  /** List environment keys, never values */
  listEnv: {
    request: { path: { app: string } };
    response: Env;
    events: never;
  };
  /** Set a variable */
  setEnv: {
    request: { path: { app: string; key: EnvKey }; body: EnvSet };
    response: Env;
    events: never;
  };
  /** Remove a variable */
  unsetEnv: {
    request: { path: { app: string; key: EnvKey } };
    response: Env;
    events: never;
  };
  /** List an app's hostnames */
  listDomains: {
    request: { path: { app: string } };
    response: DomainList;
    events: never;
  };
  /** Add a hostname */
  addDomain: {
    request: { path: { app: string }; body: DomainAdd };
    response: Domain;
    events: never;
  };
  /** Remove a hostname */
  removeDomain: {
    request: { path: { app: string; hostname: string } };
    response: void;
    events: never;
  };
  /** Stream the running release's output */
  streamLogs: {
    request: { path: { app: string }; query?: { tail?: number; follow?: boolean } };
    response: never;
    events: { message: LogLine; end: LogEnd };
  };
  /** List an app's deployments, newest first */
  listReleases: {
    request: { path: { app: string }; query?: { limit?: number; before?: ID } };
    response: ReleaseList;
    events: never;
  };
  /** Queue a deploy */
  deploy: {
    request: { path: { app: string }; header?: { "Idempotency-Key"?: string }; body?: DeployRequest };
    response: Admitted;
    events: never;
  };
  /** Queue a rollback to an earlier release */
  rollback: {
    request: { path: { app: string }; header?: { "Idempotency-Key"?: string }; body: RollbackRequest };
    response: Admitted;
    events: never;
  };
  /** Show an operation */
  getOperation: {
    request: { path: { id: ID } };
    response: Operation;
    events: never;
  };
  /** Stream an operation's events */
  streamEvents: {
    request: { path: { id: ID }; header?: { "Last-Event-ID"?: number } };
    response: never;
    events: { message: OperationEvent; end: Operation };
  };
}

/** Each operation's method and path template, and whether it answers with an event stream. */
export const operations = {
  getHealth: { method: "GET", path: "/healthz", stream: false },
  getReady: { method: "GET", path: "/readyz", stream: false },
  getStatus: { method: "GET", path: "/v1/status", stream: false },
  whoami: { method: "GET", path: "/v1/whoami", stream: false },
  listTokens: { method: "GET", path: "/v1/tokens", stream: false },
  revokeToken: { method: "DELETE", path: "/v1/tokens/{prefix}", stream: false },
  rotateToken: { method: "POST", path: "/v1/tokens/self/rotate", stream: false },
  listApps: { method: "GET", path: "/v1/apps", stream: false },
  createApp: { method: "POST", path: "/v1/apps", stream: false },
  getApp: { method: "GET", path: "/v1/apps/{app}", stream: false },
  deleteApp: { method: "DELETE", path: "/v1/apps/{app}", stream: false },
  updateApp: { method: "PATCH", path: "/v1/apps/{app}", stream: false },
  listEnv: { method: "GET", path: "/v1/apps/{app}/env", stream: false },
  setEnv: { method: "PUT", path: "/v1/apps/{app}/env/{key}", stream: false },
  unsetEnv: { method: "DELETE", path: "/v1/apps/{app}/env/{key}", stream: false },
  listDomains: { method: "GET", path: "/v1/apps/{app}/domains", stream: false },
  addDomain: { method: "POST", path: "/v1/apps/{app}/domains", stream: false },
  removeDomain: { method: "DELETE", path: "/v1/apps/{app}/domains/{hostname}", stream: false },
  streamLogs: { method: "GET", path: "/v1/apps/{app}/logs", stream: true },
  listReleases: { method: "GET", path: "/v1/apps/{app}/deployments", stream: false },
  deploy: { method: "POST", path: "/v1/apps/{app}/deployments", stream: false },
  rollback: { method: "POST", path: "/v1/apps/{app}/rollbacks", stream: false },
  getOperation: { method: "GET", path: "/v1/operations/{id}", stream: false },
  streamEvents: { method: "GET", path: "/v1/operations/{id}/events", stream: true },
} as const satisfies { [K in keyof Operations]: { method: string; path: string; stream: boolean } };
