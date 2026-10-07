import { useRef, useState, type FormEvent } from "react";
import { ConfirmButton, focusHeading } from "./a11y.tsx";
import type { Client } from "./api/client.ts";
import type { Scope } from "./api/schema.ts";
import { ago } from "./format.ts";
import { canChange, explain, focusFirstInvalid, type Explained } from "./forms.ts";
import { Link } from "./nav.tsx";
import { useApi } from "./useApi.ts";

interface Props {
  client: Client;
  slug: string;
  scopes: Scope[];
}

/**
 * DomainsPage lists an app's hostnames and adds or removes them. The API
 * checks the DNS first (every A/AAAA record must point at the server), and
 * Caddy picks a change up within a minute, getting its certificate then.
 */
export function DomainsPage({ client, slug, scopes }: Props) {
  const domains = useApi(() => client.call("listDomains", { path: { app: slug } }), [client, slug]);
  const admin = canChange(scopes);
  const [notice, setNotice] = useState<string | undefined>();
  const [error, setError] = useState<string | undefined>();

  async function remove(hostname: string) {
    setError(undefined);
    try {
      await client.call("removeDomain", { path: { app: slug, hostname } });
      setNotice(`${hostname} removed. Caddy stops serving it within a minute.`);
      domains.reload();
    } catch (err) {
      setError(explain(err).message);
    } finally {
      focusHeading();
    }
  }

  return (
    <section>
      <p className="crumbs">
        <Link to={{ page: "apps" }}>Apps</Link> / <Link to={{ page: "app", slug }}>{slug}</Link> / domains
      </p>
      <div className="title">
        <h1>Domains</h1>
      </div>
      {domains.error && <p className="error" role="alert">{domains.error}</p>}
      {notice && <p className="notice" role="status">{notice}</p>}
      {error && <p className="error" role="alert">{error}</p>}
      {domains.data && domains.data.domains.length === 0 && <p className="hint">No domains yet.</p>}
      {domains.data && domains.data.domains.length > 0 && (
        <table>
          <thead>
            <tr>
              <th>Hostname</th>
              <th>Serving</th>
              <th>DNS checked</th>
              <th>
                <span className="sr-only">Actions</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {domains.data.domains.map((d) => (
              <tr key={d.hostname}>
                <td>
                  <a href={`https://${d.hostname}/`} target="_blank" rel="noopener noreferrer">
                    {d.hostname}
                  </a>
                </td>
                <td>{d.deployment_id ? <code title={d.deployment_id}>{d.deployment_id.slice(0, 8)}</code> : "no release yet"}</td>
                <td>{d.dns_checked_at ? ago(d.dns_checked_at) : "not checked"}</td>
                <td>
                  {admin && (
                    <ConfirmButton label="Remove" confirmLabel={`Remove ${d.hostname}`} destructive onConfirm={() => void remove(d.hostname)} />
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {admin && (
        <AddForm
          client={client}
          slug={slug}
          onAdded={(h) => {
            setNotice(`${h} added. Caddy serves it within a minute, and requests its certificate then.`);
            domains.reload();
          }}
        />
      )}
    </section>
  );
}

function AddForm({ client, slug, onAdded }: { client: Client; slug: string; onAdded: (hostname: string) => void }) {
  const [hostname, setHostname] = useState("");
  const [busy, setBusy] = useState(false);
  const [problems, setProblems] = useState<Explained>({ message: "", fields: {} });
  const form = useRef<HTMLFormElement>(null);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setProblems({ message: "", fields: {} });
    try {
      const d = await client.call("addDomain", { path: { app: slug }, body: { hostname: hostname.trim() } });
      setHostname("");
      onAdded(d.hostname);
    } catch (err) {
      setProblems(explain(err));
      focusFirstInvalid(form.current);
    } finally {
      setBusy(false);
    }
  }

  const fieldError = problems.fields["hostname"];
  return (
    <form className="panel" onSubmit={submit} ref={form} noValidate>
      <h2>Add a domain</h2>
      <label htmlFor="hostname">Hostname</label>
      <input
        id="hostname"
        value={hostname}
        onChange={(e) => setHostname(e.target.value)}
        autoComplete="off"
        spellCheck={false}
        placeholder="www.example.com"
        aria-invalid={fieldError ? true : undefined}
        aria-describedby={fieldError ? "hostname-error" : undefined}
      />
      {fieldError && <p id="hostname-error" className="error">{fieldError}</p>}
      <p className="hint">Point its A (and AAAA) records at the server first.</p>
      <div className="actions">
        <button type="submit" disabled={busy || hostname.trim() === ""}>
          {busy ? "Checking DNS…" : "Add"}
        </button>
      </div>
      {problems.message && <p className="error" role="alert">{problems.message}</p>}
    </form>
  );
}
