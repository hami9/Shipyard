import { useEffect, useRef, useState } from "react";
import type { Client } from "./api/client.ts";
import { title } from "./routes.ts";
import { AppDetail } from "./AppDetail.tsx";
import { AppSettingsPage, NewApp } from "./AppEditor.tsx";
import { AppList } from "./AppList.tsx";
import { LogoTile } from "./Brand.tsx";
import { DomainsPage } from "./DomainsPage.tsx";
import { EnvPage } from "./EnvPage.tsx";
import { Logs } from "./Logs.tsx";
import { Link, useRoute } from "./nav.tsx";
import { OperationView } from "./OperationView.tsx";
import { expiresIn, type Session } from "./session.ts";
import { TokensPage } from "./TokensPage.tsx";

interface Props {
  session: Session;
  client: Client;
  onSignOut: (why?: string) => void;
  /** The page now uses another token (a rotation). */
  onSession: (s: Session) => void;
}

// The signed-in frame and its pages.
export function Shell({ session, client, onSignOut, onSession }: Props) {
  const { whoami } = session;
  const route = useRoute();
  const main = useRef<HTMLElement>(null);
  const first = useRef(true);
  const [announced, setAnnounced] = useState("");

  // A page change without a reload: retitle the document, move focus to the
  // new content (the old link is gone), and say where we are, as a full
  // page load would (WCAG 2.4.2, 2.4.3).
  const pageTitle = title(route);
  useEffect(() => {
    document.title = pageTitle;
    if (first.current) {
      first.current = false;
      return;
    }
    main.current?.focus();
    setAnnounced(pageTitle);
  }, [pageTitle]);

  return (
    <div className="shell">
      <a className="skip" href="#main">
        Skip to content
      </a>
      <p className="sr-only" aria-live="polite" aria-atomic="true">
        {announced}
      </p>
      <header>
        <Link to={{ page: "apps" }} className="brand">
          <LogoTile />
          Shipyard
        </Link>
        <nav className="topnav" aria-label="Main">
          <Link to={{ page: "apps" }} current={route.page !== "tokens"}>
            Apps
          </Link>
          <Link to={{ page: "tokens" }} current={route.page === "tokens"}>
            Tokens
          </Link>
        </nav>
        <span className="who" title={`token ${whoami.token}`}>
          <strong>{whoami.name}</strong> · {whoami.scopes.join(", ")} · {expiresIn(whoami.expires_at)}
        </span>
        <button type="button" className="small" onClick={() => onSignOut()}>
          Sign out
        </button>
      </header>
      <main id="main" ref={main} tabIndex={-1}>
        {route.page === "apps" && <AppList client={client} scopes={whoami.scopes} />}
        {route.page === "new" && <NewApp client={client} scopes={whoami.scopes} />}
        {route.page === "settings" && (
          <AppSettingsPage key={route.slug} client={client} slug={route.slug} scopes={whoami.scopes} />
        )}
        {route.page === "app" && (
          <AppDetail key={route.slug} client={client} slug={route.slug} scopes={whoami.scopes} />
        )}
        {route.page === "logs" && <Logs key={route.slug} client={client} slug={route.slug} />}
        {route.page === "env" && <EnvPage key={route.slug} client={client} slug={route.slug} scopes={whoami.scopes} />}
        {route.page === "domains" && (
          <DomainsPage key={route.slug} client={client} slug={route.slug} scopes={whoami.scopes} />
        )}
        {route.page === "operation" && <OperationView key={route.id} client={client} id={route.id} />}
        {route.page === "tokens" && <TokensPage client={client} session={session} onSession={onSession} onSignOut={onSignOut} />}
        {route.page === "missing" && (
          <p>
            There is no page at <code>{route.path}</code>. <Link to={{ page: "apps" }}>Back to the apps</Link>.
          </p>
        )}
      </main>
    </div>
  );
}
