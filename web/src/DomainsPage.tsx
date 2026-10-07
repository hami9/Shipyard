import { useRef, useState, type FormEvent } from "react";
import { ConfirmButton, focusHeading } from "./a11y.tsx";
import type { Client } from "./api/client.ts";
import type { Scope } from "./api/schema.ts";
import { ago } from "./format.ts";
import { canChange, explain, focusFirstInvalid, type Explained } from "./forms.ts";
import { AppFrame } from "./AppFrame.tsx";
import { CopyButton } from "./Copy.tsx";
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
    <AppFrame slug={slug} section="domains" scopes={scopes}>
      {domains.error && <p className="error" role="alert">{domains.error}</p>}
      {notice && <p className="notice" role="status">{notice}</p>}
      {error && <p className="error" role="alert">{error}</p>}
      {domains.data && domains.data.domains.length === 0 && <p className="empty">No domains yet.</p>}
      {domains.data && domains.data.domains.length > 0 && (
        <table>
          <thead>
            <tr>
              <th>Hostname</th>
              <th>Serving</th>
              <th>DNS</th>
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
                <td>{d.deployment_id ? <>release <code title={d.deployment_id}>{d.deployment_id.slice(0, 8)}</code></> : <span className="hint">waits for a release</span>}</td>
                <td>
                  {d.dns_checked_at ? (
                    <>
                      <span className="badge ok">checked</span> <span className="hint small">{ago(d.dns_checked_at)}</span>
                    </>
                  ) : (
                    <span className="badge idle">not checked</span>
                  )}
                </td>
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
    </AppFrame>
  );
}

/**
 * DnsHelp is the record to create before adding a hostname. The UI is
 * served on the API's hostname (ADR-0016), which already points at this
 * server, so a CNAME to it is right for any subdomain. An apex cannot be a
 * CNAME; it needs A/AAAA records with the same addresses. The API checks
 * every A/AAAA record against the server's public addresses, and names
 * them when one is wrong.
 */
function DnsHelp({ hostname }: { hostname: string }) {
  const target = window.location.hostname;
  return (
    <div className="dns">
      <p className="hint">First, in your DNS provider, point the hostname at this server:</p>
      <table className="dns-records">
        <thead>
          <tr>
            <th>Type</th>
            <th>Name</th>
            <th>Value</th>
          </tr>
        </thead>
        <tbody>
          <tr>
            <td>CNAME</td>
            <td>
              <code>{hostname}</code>
            </td>
            <td>
              <span className="copyable">
                <code>{target}</code>
                <CopyButton value={target} label="the CNAME value" />
              </span>
            </td>
          </tr>
        </tbody>
      </table>
      <p className="hint">
        For a bare domain (<code>example.com</code>), which cannot be a CNAME, add A (and AAAA) records with the addresses{" "}
        <code>{target}</code> has. Adding checks the records; a certificate follows within a minute.
      </p>
    </div>
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
      <DnsHelp hostname={hostname.trim() || "www.example.com"} />
      <div className="actions">
        <button type="submit" disabled={busy || hostname.trim() === ""}>
          {busy ? "Checking DNS…" : "Add"}
        </button>
      </div>
      {problems.message && <p className="error" role="alert">{problems.message}</p>}
    </form>
  );
}
