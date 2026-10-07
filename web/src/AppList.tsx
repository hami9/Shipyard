import type { Client } from "./api/client.ts";
import type { Scope } from "./api/schema.ts";
import { ago } from "./format.ts";
import { canChange } from "./forms.ts";
import { Link } from "./nav.tsx";
import { useApi } from "./useApi.ts";

export function AppList({ client, scopes }: { client: Client; scopes: Scope[] }) {
  const { data, error, loading, reload } = useApi(() => client.call("listApps"), [client]);
  return (
    <section>
      <div className="title">
        <h1>Apps</h1>
        <div className="actions">
          {canChange(scopes) && <Link to={{ page: "new" }}>New app</Link>}
          <button type="button" onClick={reload} disabled={loading}>
            Refresh
          </button>
        </div>
      </div>
      {error && <p className="error" role="alert">{error}</p>}
      {data && data.apps.length === 0 && (
        <p className="hint">
          No apps yet. Create one {canChange(scopes) ? "with New app, or " : ""}with <code>shipyard app create</code>.
        </p>
      )}
      {data && data.apps.length > 0 && (
        <table>
          <thead>
            <tr>
              <th>App</th>
              <th>Repository</th>
              <th>Branch</th>
              <th>Push deploys</th>
              <th>Created</th>
            </tr>
          </thead>
          <tbody>
            {data.apps.map((a) => (
              <tr key={a.id}>
                <td>
                  <Link to={{ page: "app", slug: a.slug }}>{a.slug}</Link>
                </td>
                <td>{a.repo}</td>
                <td>
                  <code>{a.branch}</code>
                </td>
                <td>{a.auto_deploy ? "on" : "off"}</td>
                <td title={a.created_at}>{ago(a.created_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {loading && !data && <p className="hint">Loading…</p>}
    </section>
  );
}
