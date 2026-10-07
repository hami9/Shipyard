import { useEffect, useState } from "react";
import type { Client } from "./api/client.ts";
import type { Token } from "./api/schema.ts";
import { ago } from "./format.ts";
import { canChange, explain } from "./forms.ts";
import { Link } from "./nav.tsx";
import { expiresIn, type Session } from "./session.ts";
import { graces, tokenTone, whoamiOf } from "./tokens.ts";
import { useApi } from "./useApi.ts";

interface Props {
  client: Client;
  session: Session;
  /** The page now uses another token: after a rotation. */
  onSession: (s: Session) => void;
  onSignOut: (why: string) => void;
}

/**
 * TokensPage rotates the signed-in token (any scope) and, with the admin
 * scope, lists every token and revokes them (ADR-0011). Hashes are never
 * shown: the API has none to give.
 */
export function TokensPage({ client, session, onSession, onSignOut }: Props) {
  const admin = canChange(session.whoami.scopes);
  return (
    <section>
      <p className="crumbs">
        <Link to={{ page: "apps" }}>Apps</Link> / tokens
      </p>
      <div className="title">
        <h1>Tokens</h1>
      </div>
      <Rotate client={client} session={session} onSession={onSession} />
      {admin ? (
        <TokenList client={client} self={session.whoami.token} onSignOut={onSignOut} />
      ) : (
        <p className="hint">Listing and revoking tokens needs the admin scope.</p>
      )}
    </section>
  );
}

function Rotate({ client, session, onSession }: { client: Client; session: Session; onSession: (s: Session) => void }) {
  const [grace, setGrace] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | undefined>();
  // The new token, shown once so it can be copied to the CLI; gone when the page goes.
  const [fresh, setFresh] = useState<{ token: string; old: Token } | undefined>();
  const [copied, setCopied] = useState(false);
  const { whoami } = session;

  async function rotate() {
    setBusy(true);
    setError(undefined);
    try {
      const res = await client.call("rotateToken", { body: { grace_seconds: grace } });
      setFresh({ token: res.token, old: res.old });
      setCopied(false);
      onSession({ token: res.token, whoami: whoamiOf(res.new) });
    } catch (err) {
      setError(explain(err).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="panel wide">
      <h2>This session's token</h2>
      <p>
        <code>{whoami.token}…</code> {whoami.name} · {whoami.scopes.join(", ")} · {expiresIn(whoami.expires_at)}
      </p>
      {fresh ? (
        <>
          <p className="notice" role="status">
            Rotated. This page now uses the new token below. It is shown only now: if the CLI used the old one, run{" "}
            <code>shipyard login</code> with it.{" "}
            {fresh.old.revoked_at
              ? "The old token no longer works."
              : `The old token works until ${new Date(fresh.old.expires_at ?? "").toLocaleString()}.`}
          </p>
          <div className="secret">
            <code>{fresh.token}</code>
            <button
              type="button"
              onClick={() => {
                void navigator.clipboard?.writeText(fresh.token).then(() => setCopied(true));
              }}
            >
              {copied ? "Copied" : "Copy"}
            </button>
            <button type="button" onClick={() => setFresh(undefined)}>
              Hide
            </button>
          </div>
        </>
      ) : (
        <>
          <p className="hint">A rotation gives this token's user, name, scopes and lifetime to a new one, and ends the old one.</p>
          <div className="actions">
            <label>
              The old token:{" "}
              <select value={grace} onChange={(e) => setGrace(Number(e.target.value))}>
                {graces.map((g) => (
                  <option key={g.seconds} value={g.seconds}>
                    {g.label}
                  </option>
                ))}
              </select>
            </label>
            <button type="button" onClick={() => void rotate()} disabled={busy}>
              {busy ? "Rotating…" : "Rotate"}
            </button>
          </div>
        </>
      )}
      {error && <p className="error" role="alert">{error}</p>}
    </div>
  );
}

function TokenList({ client, self, onSignOut }: { client: Client; self: string; onSignOut: (why: string) => void }) {
  const list = useApi(() => client.call("listTokens"), [client]);
  const [confirm, setConfirm] = useState<string | undefined>();
  const [error, setError] = useState<string | undefined>();
  const [notice, setNotice] = useState<string | undefined>();
  useEffect(() => setConfirm(undefined), [list.data]);

  async function revoke(prefix: string) {
    setError(undefined);
    try {
      await client.call("revokeToken", { path: { prefix } });
      if (prefix === self) {
        onSignOut("You revoked the token this page used.");
        return;
      }
      setNotice(`${prefix} revoked.`);
      list.reload();
    } catch (err) {
      setError(explain(err).message);
    }
  }

  return (
    <>
      <div className="title">
        <h2>All tokens</h2>
        <button type="button" onClick={list.reload} disabled={list.loading}>
          Refresh
        </button>
      </div>
      <p className="hint">
        New tokens are made on the server: <code>shipyard-api token create</code>.
      </p>
      {list.error && <p className="error" role="alert">{list.error}</p>}
      {notice && <p className="notice" role="status">{notice}</p>}
      {error && <p className="error" role="alert">{error}</p>}
      {list.data && (
        <table>
          <thead>
            <tr>
              <th>Token</th>
              <th>Name</th>
              <th>Scopes</th>
              <th>Status</th>
              <th>Expires</th>
              <th>Last used</th>
              <th>
                <span className="sr-only">Actions</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {list.data.tokens.map((t) => (
              <tr key={t.prefix}>
                <td>
                  <code>{t.prefix}…</code>
                  {t.prefix === self && <div className="hint">this session</div>}
                </td>
                <td>{t.name}</td>
                <td>{t.scopes.join(", ")}</td>
                <td>
                  <span className={`badge ${tokenTone(t.status)}`}>{t.status}</span>
                </td>
                <td>{t.expires_at ? new Date(t.expires_at).toLocaleDateString() : "never"}</td>
                <td>{t.last_used_at ? ago(t.last_used_at) : "never"}</td>
                <td>
                  {t.status === "active" &&
                    (confirm === t.prefix ? (
                      <span className="actions">
                        <button type="button" className="destructive" onClick={() => void revoke(t.prefix)}>
                          {t.prefix === self ? "Revoke and sign out" : "Revoke"}
                        </button>
                        <button type="button" onClick={() => setConfirm(undefined)}>
                          Cancel
                        </button>
                      </span>
                    ) : (
                      <button type="button" onClick={() => setConfirm(t.prefix)}>
                        Revoke
                      </button>
                    ))}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  );
}
