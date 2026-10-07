import type { Client } from "./api/client.ts";
import type { App, Release, Scope } from "./api/schema.ts";
import { ago, statusLabel, tone } from "./format.ts";
import { canChange } from "./forms.ts";
import { Link } from "./nav.tsx";
import { useApi } from "./useApi.ts";

/**
 * AppList is every app with its newest deploy and that deploy's outcome, so
 * the one that needs attention stands out. One small request per app: a
 * single server has few apps.
 */
export function AppList({ client, scopes }: { client: Client; scopes: Scope[] }) {
  const { data, error, loading, reload } = useApi(async () => {
    const { apps } = await client.call("listApps");
    const latest = await Promise.all(
      apps.map((a) =>
        client.call("listReleases", { path: { app: a.slug }, query: { limit: 1 } }).then(
          (l) => l.deployments[0],
          () => undefined, // one app's history failing must not hide the list
        ),
      ),
    );
    return apps.map((app, i): [App, Release | undefined] => [app, latest[i]]);
  }, [client]);
  return (
    <section>
      <div className="title">
        <h1>Apps</h1>
        <div className="actions">
          <button type="button" onClick={reload} disabled={loading}>
            Refresh
          </button>
          {canChange(scopes) && (
            <Link to={{ page: "new" }} className="button primary">
              New app
            </Link>
          )}
        </div>
      </div>
      {error && <p className="error" role="alert">{error}</p>}
      {data && data.length === 0 && (
        <p className="empty">
          No apps yet. Create one {canChange(scopes) ? "with New app, or " : ""}with <code>shipyard app create</code>.
        </p>
      )}
      {data && data.length > 0 && (
        <table>
          <thead>
            <tr>
              <th>App</th>
              <th>Latest deploy</th>
              <th>Repository</th>
              <th>Push deploys</th>
            </tr>
          </thead>
          <tbody>
            {data.map(([a, r]) => (
              <tr key={a.id}>
                <td>
                  <Link to={{ page: "app", slug: a.slug }} className="strong">
                    {a.slug}
                  </Link>
                </td>
                <td>
                  {r ? (
                    <>
                      <span className={`badge ${tone(r.status)}`}>{statusLabel(r.status)}</span>{" "}
                      <span className="hint small" title={r.created_at}>
                        {ago(r.created_at)}
                      </span>
                    </>
                  ) : (
                    <span className="hint">never deployed</span>
                  )}
                </td>
                <td>
                  {a.repo} <span className="hint">@</span> <code>{a.branch}</code>
                </td>
                <td>{a.auto_deploy ? "on" : "off"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {loading && !data && <p className="hint">Loading…</p>}
    </section>
  );
}
