import { useEffect, useState } from "react";
import type { Client } from "./api/client.ts";
import type { App, Release, Scope } from "./api/schema.ts";
import { ago, bytes, shortSHA, statusLabel, tone } from "./format.ts";
import { Link } from "./nav.tsx";
import { Rollback } from "./Rollback.tsx";
import { canRollBack } from "./rollbackRules.ts";
import { message, useApi } from "./useApi.ts";

interface Props {
  client: Client;
  slug: string;
  /** The token's scopes: rolling back needs deploy. */
  scopes: Scope[];
}

export function AppDetail({ client, slug, scopes }: Props) {
  const app = useApi(() => client.call("getApp", { path: { app: slug } }), [client, slug]);
  return (
    <section>
      <p className="crumbs">
        <Link to={{ page: "apps" }}>Apps</Link> / {slug}
      </p>
      {app.error && (
        <p className="error" role="alert">
          {app.error === "Not found." ? `There is no app named ${slug}.` : app.error}
        </p>
      )}
      {app.data && <Settings app={app.data} />}
      {app.data && <Releases client={client} slug={slug} scopes={scopes} />}
      {app.loading && !app.data && <p className="hint">Loading…</p>}
    </section>
  );
}

function Settings({ app }: { app: App }) {
  const rows: [string, string][] = [
    ["Repository", `${app.repo} @ ${app.branch}`],
    ["Push deploys", app.auto_deploy ? "on" : "off"],
    ["Build", `${app.dockerfile_path} in ${app.build_context}`],
    ["Port", String(app.port)],
    ["Health check", `GET ${app.health_path}, ${app.health_timeout}`],
    ["Limits", `${app.cpu_limit} CPU, ${bytes(app.memory_limit)}, stop timeout ${app.stop_timeout}`],
  ];
  if (app.github_installation_id !== null) {
    rows.push(["GitHub App installation", String(app.github_installation_id)]);
  }
  return (
    <>
      <div className="title">
        <h1>{app.slug}</h1>
        <nav className="actions" aria-label={`${app.slug} pages`}>
          <Link to={{ page: "env", slug: app.slug }}>Environment</Link>
          <Link to={{ page: "domains", slug: app.slug }}>Domains</Link>
          <Link to={{ page: "logs", slug: app.slug }}>Logs</Link>
        </nav>
      </div>
      <dl className="settings">
        {rows.map(([k, v]) => (
          <div key={k}>
            <dt>{k}</dt>
            <dd>{v}</dd>
          </div>
        ))}
      </dl>
    </>
  );
}

const pageSize = 20;

/** Releases is the app's deployments, newest first, a page at a time. */
function Releases({ client, slug, scopes }: Props) {
  const first = useApi(
    () => client.call("listReleases", { path: { app: slug }, query: { limit: pageSize } }),
    [client, slug],
  );
  const [more, setMore] = useState<Release[]>([]);
  const [next, setNext] = useState<string | undefined>();
  const [error, setError] = useState<string | undefined>();
  const [busy, setBusy] = useState(false);
  const [target, setTarget] = useState<Release | undefined>();
  const [queued, setQueued] = useState<{ text: string; op: string } | undefined>();

  // A new first page (a refresh) starts the list over.
  useEffect(() => {
    setMore([]);
    setNext(first.data?.next);
  }, [first.data]);

  async function loadMore() {
    if (!next) {
      return;
    }
    setBusy(true);
    setError(undefined);
    try {
      const page = await client.call("listReleases", { path: { app: slug }, query: { limit: pageSize, before: next } });
      setMore((m) => [...m, ...page.deployments]);
      setNext(page.next);
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  const releases = [...(first.data?.deployments ?? []), ...more];
  return (
    <>
      <div className="title">
        <h2>Releases</h2>
        <button type="button" onClick={first.reload} disabled={first.loading}>
          Refresh
        </button>
      </div>
      {first.error && <p className="error" role="alert">{first.error}</p>}
      {queued && (
        <p className="notice" role="status">
          {queued.text} <Link to={{ page: "operation", id: queued.op }}>Follow it</Link>
        </p>
      )}
      {target && (
        <Rollback
          client={client}
          slug={slug}
          target={target}
          onCancel={() => setTarget(undefined)}
          onDone={(a) => {
            setTarget(undefined);
            const replaced =
              a.superseded.length > 0
                ? ` It replaced ${a.superseded.length === 1 ? "an operation" : `${a.superseded.length} operations`} still waiting in the queue.`
                : "";
            setQueued({
              op: a.operation.id,
              text: a.created
                ? `Rollback queued: operation ${a.operation.id.slice(0, 8)} (${a.operation.status}).${replaced} The new release appears here once the worker starts it.`
                : `That rollback was already queued: operation ${a.operation.id.slice(0, 8)} (${a.operation.status}).`,
            });
            first.reload();
          }}
        />
      )}
      {first.data && releases.length === 0 && (
        <p className="hint">
          Nothing deployed yet. Deploy with <code>shipyard deploy {slug}</code>.
        </p>
      )}
      {releases.length > 0 && (
        <table className="releases">
          <thead>
            <tr>
              <th>Status</th>
              <th>Commit</th>
              <th>Release</th>
              <th>Config</th>
              <th>Created</th>
              <th>
                <span className="sr-only">Actions</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {releases.map((r) => (
              <tr key={r.id}>
                <td>
                  <span className={`badge ${tone(r.status)}`}>{statusLabel(r.status)}</span>
                  {r.failure_reason && <div className="reason">{r.failure_reason}</div>}
                </td>
                <td>
                  <code title={r.commit}>{shortSHA(r.commit)}</code>
                </td>
                <td>
                  <code title={r.id}>{r.id.slice(0, 8)}</code>{" "}
                  <Link to={{ page: "operation", id: r.operation_id }} className="small">
                    events
                  </Link>
                  {r.kind === "rollback" && r.rollback_of && (
                    <div className="hint">rollback to {r.rollback_of.slice(0, 8)}</div>
                  )}
                </td>
                <td>{r.env_revision === 0 ? "none" : `revision ${r.env_revision}`}</td>
                <td title={r.created_at}>{ago(r.created_at)}</td>
                <td>
                  {canRollBack(r, scopes) && (
                    <button
                      type="button"
                      onClick={() => {
                        setQueued(undefined);
                        setTarget(r);
                      }}
                      disabled={target !== undefined}
                    >
                      Roll back
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {error && <p className="error" role="alert">{error}</p>}
      {next && (
        <button type="button" onClick={loadMore} disabled={busy}>
          {busy ? "Loading…" : "Older releases"}
        </button>
      )}
    </>
  );
}
