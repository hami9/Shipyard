import { useEffect, useState } from "react";
import type { Client } from "./api/client.ts";
import type { App, Release } from "./api/schema.ts";
import { ago, bytes, shortSHA, statusLabel, tone } from "./format.ts";
import { Link } from "./nav.tsx";
import { message, useApi } from "./useApi.ts";

export function AppDetail({ client, slug }: { client: Client; slug: string }) {
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
      {app.data && <Releases client={client} slug={slug} />}
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
function Releases({ client, slug }: { client: Client; slug: string }) {
  const first = useApi(
    () => client.call("listReleases", { path: { app: slug }, query: { limit: pageSize } }),
    [client, slug],
  );
  const [more, setMore] = useState<Release[]>([]);
  const [next, setNext] = useState<string | undefined>();
  const [error, setError] = useState<string | undefined>();
  const [busy, setBusy] = useState(false);

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
                  <code title={r.id}>{r.id.slice(0, 8)}</code>
                  {r.kind === "rollback" && r.rollback_of && (
                    <div className="hint">rollback to {r.rollback_of.slice(0, 8)}</div>
                  )}
                </td>
                <td>{r.env_revision === 0 ? "none" : `revision ${r.env_revision}`}</td>
                <td title={r.created_at}>{ago(r.created_at)}</td>
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
