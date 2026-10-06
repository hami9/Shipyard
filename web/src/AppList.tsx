import type { Client } from "./api/client.ts";
import { ago } from "./format.ts";
import { Link } from "./nav.tsx";
import { useApi } from "./useApi.ts";

export function AppList({ client }: { client: Client }) {
  const { data, error, loading, reload } = useApi(() => client.call("listApps"), [client]);
  return (
    <section>
      <div className="title">
        <h1>Apps</h1>
        <button type="button" onClick={reload} disabled={loading}>
          Refresh
        </button>
      </div>
      {error && <p className="error" role="alert">{error}</p>}
      {data && data.apps.length === 0 && (
        <p className="hint">
          No apps yet. Create one with <code>shipyard app create</code>.
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
