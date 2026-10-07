import { useEffect, useState } from "react";
import type { Client } from "./api/client.ts";
import type { App, ReleaseList, Release, Scope, ServerStatus } from "./api/schema.ts";
import { AppFrame } from "./AppFrame.tsx";
import { ago, bytes, shortSHA, statusLabel, tone } from "./format.ts";
import { Link } from "./nav.tsx";
import { Deploy } from "./Deploy.tsx";
import { canDeploy } from "./forms.ts";
import { Rollback } from "./Rollback.tsx";
import { canRollBack } from "./rollbackRules.ts";
import { health } from "./status.ts";
import { filterReleases, summarize, type ReleaseFilter } from "./summary.ts";
import { message, useApi, type Loaded } from "./useApi.ts";

interface Props {
  client: Client;
  slug: string;
  /** The token's scopes: rolling back needs deploy. */
  scopes: Scope[];
}

const pageSize = 20;

export function AppDetail({ client, slug, scopes, status }: Props & { status: ServerStatus | undefined }) {
  const app = useApi(() => client.call("getApp", { path: { app: slug } }), [client, slug]);
  // The newest page of releases feeds both the summary and the history.
  const first = useApi(
    () => client.call("listReleases", { path: { app: slug }, query: { limit: pageSize } }),
    [client, slug],
  );
  if (app.error) {
    return (
      <section>
        <p className="crumbs">
          <Link to={{ page: "apps" }}>Apps</Link> / {slug}
        </p>
        <p className="error" role="alert">
          {app.error === "Not found." ? `There is no app named ${slug}.` : app.error}
        </p>
      </section>
    );
  }
  return (
    <AppFrame
      slug={slug}
      section="app"
      scopes={scopes}
      aside={app.data && canDeploy(scopes) ? <Deploy client={client} slug={slug} branch={app.data.branch} /> : undefined}
    >
      {app.loading && !app.data && <p className="hint">Loading…</p>}
      {app.data && <Summary app={app.data} releases={first} status={status} />}
      {app.data && <Releases client={client} slug={slug} scopes={scopes} first={first} />}
      {app.data && <Configuration app={app.data} />}
    </AppFrame>
  );
}

/**
 * Summary answers "is it up, and did the last change work?": the release
 * serving traffic, and the newest deploy with its outcome, so a failure is
 * the first thing seen rather than a row in the history.
 */
function Summary({ app, releases, status }: { app: App; releases: Loaded<ReleaseList>; status: ServerStatus | undefined }) {
  const s = releases.data ? summarize(releases.data.deployments) : undefined;
  const h = health(status, app.slug);
  return (
    <div className="cards" aria-label="Status">
      <div className="card">
        <h2 className="card-label">Serving</h2>
        {!s && <p className="hint">Loading…</p>}
        {s && !s.serving && <p className="card-value">Nothing yet</p>}
        {s?.serving && (
          <>
            <p className="card-value">
              <span className="badge ok">active</span> <code title={s.serving.commit}>{shortSHA(s.serving.commit)}</code>
            </p>
            <p className="hint">
              since {ago(s.serving.active_at ?? s.serving.created_at)}
              {s.serving.kind === "rollback" ? " · a rollback" : ""}
            </p>
          </>
        )}
      </div>
      <div className={h?.tone === "bad" ? "card bad" : "card"}>
        <h2 className="card-label">Health</h2>
        {!h && <p className="hint">Loading…</p>}
        {h && (
          <>
            <p className="card-value">
              <span className={`badge ${h.tone}`}>{h.label}</span>
            </p>
            <p className="hint">
              {h.detail}
              {status?.checked_at ? ` Checked ${ago(status.checked_at)}.` : ""}
            </p>
          </>
        )}
      </div>
      <div className={s?.latest?.status === "failed" ? "card bad" : "card"}>
        <h2 className="card-label">Latest deploy</h2>
        {s && !s.latest && <p className="card-value">None yet</p>}
        {s?.latest && (
          <>
            <p className="card-value">
              <span className={`badge ${tone(s.latest.status)}`}>{statusLabel(s.latest.status)}</span>{" "}
              <span className="hint">{ago(s.latest.created_at)}</span>
            </p>
            {s.latest.failure_reason && <p className="reason">{s.latest.failure_reason}</p>}
            {s.latest.status === "failed" && s.serving && (
              <p className="hint">Traffic stayed on the release serving before it.</p>
            )}
            <Link to={{ page: "operation", id: s.latest.operation_id }} className="small">
              View deployment details
            </Link>
          </>
        )}
      </div>
      <div className="card">
        <h2 className="card-label">Source</h2>
        <p className="card-value">
          <code>{app.repo}</code>
        </p>
        <p className="hint">
          branch <code>{app.branch}</code> · push deploys {app.auto_deploy ? "on" : "off"}
        </p>
      </div>
    </div>
  );
}

function Configuration({ app }: { app: App }) {
  const rows: [string, string][] = [
    ["Build", `${app.dockerfile_path} in ${app.build_context}`],
    ["Port", String(app.port)],
    ["Health check", `GET ${app.health_path}, within ${app.health_timeout}`],
    ["Limits", `${app.cpu_limit} CPU, ${bytes(app.memory_limit)}`],
    ["Stop timeout", app.stop_timeout],
  ];
  if (app.github_installation_id !== null) {
    rows.push(["GitHub App installation", String(app.github_installation_id)]);
  }
  return (
    <>
      <div className="title">
        <h2>Configuration</h2>
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

const filters: { value: ReleaseFilter; label: string }[] = [
  { value: "all", label: "All" },
  { value: "failed", label: "Failed" },
  { value: "rollbacks", label: "Rollbacks" },
];

/** Releases is the app's deployments, newest first, a page at a time. */
function Releases({
  client,
  slug,
  scopes,
  first,
}: Props & { first: Loaded<ReleaseList> }) {
  const [more, setMore] = useState<Release[]>([]);
  const [next, setNext] = useState<string | undefined>();
  const [error, setError] = useState<string | undefined>();
  const [busy, setBusy] = useState(false);
  const [target, setTarget] = useState<Release | undefined>();
  const [queued, setQueued] = useState<{ text: string; op: string } | undefined>();
  const [filter, setFilter] = useState<ReleaseFilter>("all");

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

  const all = [...(first.data?.deployments ?? []), ...more];
  const releases = filterReleases(all, filter);
  return (
    <>
      <div className="title">
        <h2>Releases</h2>
        <div className="actions">
          <div className="segmented" role="group" aria-label="Show">
            {filters.map((f) => (
              <button key={f.value} type="button" className="small" aria-pressed={filter === f.value} onClick={() => setFilter(f.value)}>
                {f.label}
              </button>
            ))}
          </div>
          <button type="button" className="small" onClick={first.reload} disabled={first.loading}>
            Refresh
          </button>
        </div>
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
      {first.data && all.length === 0 && (
        <p className="empty">
          Nothing deployed yet. {canDeploy(scopes) ? "Deploy it with the button above, or" : "Deploy it"} with{" "}
          <code>shipyard deploy {slug}</code>.
        </p>
      )}
      {all.length > 0 && releases.length === 0 && (
        <p className="empty">No {filter === "failed" ? "failed releases" : "rollbacks"} among the {all.length} loaded.</p>
      )}
      {releases.length > 0 && (
        <table className="releases">
          <thead>
            <tr>
              <th>Status</th>
              <th>Commit</th>
              <th>Created</th>
              <th>Config</th>
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
                  {r.kind === "rollback" && r.rollback_of && (
                    <div className="hint small">rollback to {r.rollback_of.slice(0, 8)}</div>
                  )}
                  {r.failure_reason && <div className="reason">{r.failure_reason}</div>}
                </td>
                <td>
                  <code title={r.commit}>{shortSHA(r.commit)}</code>
                  <div className="hint small">
                    release <code title={r.id}>{r.id.slice(0, 8)}</code>
                  </div>
                </td>
                <td title={r.created_at}>{ago(r.created_at)}</td>
                <td>{r.env_revision === 0 ? "none" : `revision ${r.env_revision}`}</td>
                <td>
                  <div className="row-actions">
                    <Link to={{ page: "operation", id: r.operation_id }} className="small">
                      Details
                    </Link>
                    {canRollBack(r, scopes) && (
                      <button
                        type="button"
                        className="small"
                        onClick={() => {
                          setQueued(undefined);
                          setTarget(r);
                        }}
                        disabled={target !== undefined}
                      >
                        Roll back
                      </button>
                    )}
                  </div>
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
