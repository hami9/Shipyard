import type { Client } from "./api/client.ts";
import { expiresIn, type Session } from "./session.ts";

interface Props {
  session: Session;
  client: Client;
  onSignOut: () => void;
}

// The signed-in frame. The pages inside it (apps, releases, logs,
// environment, domains) arrive with P6.3–P6.5 and use client.
export function Shell({ session, onSignOut }: Props) {
  const { whoami } = session;
  return (
    <div className="shell">
      <header>
        <strong>Shipyard</strong>
        <span className="who" title={`token ${whoami.token}`}>
          {whoami.name} · {whoami.scopes.join(", ")} · {expiresIn(whoami.expires_at)}
        </span>
        <button type="button" onClick={onSignOut}>
          Sign out
        </button>
      </header>
      <main>
        <p>Signed in. Apps, releases, and logs come next (roadmap P6.3).</p>
      </main>
    </div>
  );
}
