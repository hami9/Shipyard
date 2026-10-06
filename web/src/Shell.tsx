import type { Client } from "./api/client.ts";
import { AppDetail } from "./AppDetail.tsx";
import { AppList } from "./AppList.tsx";
import { Link, useRoute } from "./nav.tsx";
import { expiresIn, type Session } from "./session.ts";

interface Props {
  session: Session;
  client: Client;
  onSignOut: () => void;
}

// The signed-in frame and its pages. Logs, environment, and domains come
// with P6.4 and P6.5.
export function Shell({ session, client, onSignOut }: Props) {
  const { whoami } = session;
  const route = useRoute();
  return (
    <div className="shell">
      <header>
        <Link to={{ page: "apps" }} className="brand">
          Shipyard
        </Link>
        <span className="who" title={`token ${whoami.token}`}>
          {whoami.name} · {whoami.scopes.join(", ")} · {expiresIn(whoami.expires_at)}
        </span>
        <button type="button" onClick={onSignOut}>
          Sign out
        </button>
      </header>
      <main>
        {route.page === "apps" && <AppList client={client} />}
        {route.page === "app" && (
          <AppDetail key={route.slug} client={client} slug={route.slug} scopes={whoami.scopes} />
        )}
        {route.page === "missing" && (
          <p>
            There is no page at <code>{route.path}</code>. <Link to={{ page: "apps" }}>Back to the apps</Link>.
          </p>
        )}
      </main>
    </div>
  );
}
