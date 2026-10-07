import { useRef, useState, type FormEvent } from "react";
import { ConfirmButton, focusHeading } from "./a11y.tsx";
import type { Client } from "./api/client.ts";
import type { Scope } from "./api/schema.ts";
import { canChange, explain, focusFirstInvalid, keyProblem, valueProblem, type Explained } from "./forms.ts";
import { AppFrame } from "./AppFrame.tsx";
import { useApi } from "./useApi.ts";

interface Props {
  client: Client;
  slug: string;
  scopes: Scope[];
}

/**
 * EnvPage lists an app's configuration keys and changes them. Values are
 * write-only (ADR-0005): the API returns keys, never values, and this page
 * drops a value from memory once it is sent. A change makes a new revision,
 * which the next deploy uses.
 */
export function EnvPage({ client, slug, scopes }: Props) {
  const env = useApi(() => client.call("listEnv", { path: { app: slug } }), [client, slug]);
  const admin = canChange(scopes);
  const [notice, setNotice] = useState<string | undefined>();
  const [error, setError] = useState<string | undefined>();

  async function unset(key: string) {
    setError(undefined);
    try {
      const res = await client.call("unsetEnv", { path: { app: slug, key } });
      setNotice(`${key} removed: revision ${res.revision}. The next deploy uses it.`);
      env.reload();
    } catch (err) {
      setError(explain(err).message || "The key could not be removed.");
    } finally {
      focusHeading();
    }
  }

  return (
    <AppFrame slug={slug} section="env" scopes={scopes} aside={env.data && <span className="hint">revision {env.data.revision}</span>}>
      <p className="hint">Values are never shown, here or anywhere: only their keys. A change takes effect at the next deploy.</p>
      {env.error && <p className="error" role="alert">{env.error}</p>}
      {notice && <p className="notice" role="status">{notice}</p>}
      {error && <p className="error" role="alert">{error}</p>}
      {env.data && env.data.vars.length === 0 && <p className="hint">No variables yet.</p>}
      {env.data && env.data.vars.length > 0 && (
        <table>
          <thead>
            <tr>
              <th>Key</th>
              <th>Kind</th>
              <th>
                <span className="sr-only">Actions</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {env.data.vars.map((v) => (
              <tr key={v.key}>
                <td>
                  <code>{v.key}</code>
                </td>
                <td>{v.secret ? "secret" : "plain"}</td>
                <td>
                  {admin && <ConfirmButton label="Remove" confirmLabel={`Remove ${v.key}`} destructive onConfirm={() => void unset(v.key)} />}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {admin && (
        <SetForm
          client={client}
          slug={slug}
          onSet={(key, revision) => {
            setNotice(`${key} set: revision ${revision}. The next deploy uses it.`);
            env.reload();
          }}
        />
      )}
    </AppFrame>
  );
}

function SetForm({ client, slug, onSet }: { client: Client; slug: string; onSet: (key: string, revision: number) => void }) {
  const [key, setKey] = useState("");
  const [value, setValue] = useState("");
  const [secret, setSecret] = useState(true);
  const [busy, setBusy] = useState(false);
  const [problems, setProblems] = useState<Explained>({ message: "", fields: {} });
  const form = useRef<HTMLFormElement>(null);

  async function submit(e: FormEvent) {
    e.preventDefault();
    const fields: Record<string, string> = {};
    const kp = keyProblem(key);
    const vp = valueProblem(value);
    if (kp) {
      fields["key"] = kp;
    }
    if (vp) {
      fields["value"] = vp;
    }
    setProblems({ message: "", fields });
    if (kp || vp) {
      focusFirstInvalid(form.current);
      return;
    }
    setBusy(true);
    try {
      const res = await client.call("setEnv", { path: { app: slug, key }, body: { value, secret } });
      setKey("");
      setValue(""); // sent: not kept in the page
      onSet(key, res.revision);
    } catch (err) {
      setProblems(explain(err));
      focusFirstInvalid(form.current);
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="panel" onSubmit={submit} ref={form} noValidate>
      <h2>Set a variable</h2>
      <label htmlFor="env-key">Key</label>
      <input
        id="env-key"
        value={key}
        onChange={(e) => setKey(e.target.value)}
        autoComplete="off"
        spellCheck={false}
        placeholder="DATABASE_URL"
        aria-invalid={problems.fields["key"] ? true : undefined}
        aria-describedby={problems.fields["key"] ? "env-key-error" : undefined}
      />
      {problems.fields["key"] && <p id="env-key-error" className="error">{problems.fields["key"]}</p>}
      <label htmlFor="env-value">Value</label>
      {/* A password field for secrets: not shown, not autofilled, not offered for saving. */}
      <input
        id="env-value"
        type={secret ? "password" : "text"}
        value={value}
        onChange={(e) => setValue(e.target.value)}
        autoComplete="new-password"
        spellCheck={false}
        aria-invalid={problems.fields["value"] ? true : undefined}
        aria-describedby={problems.fields["value"] ? "env-value-error" : undefined}
      />
      {problems.fields["value"] && <p id="env-value-error" className="error">{problems.fields["value"]}</p>}
      <label className="check">
        <input type="checkbox" checked={secret} onChange={(e) => setSecret(e.target.checked)} /> Secret: encrypted, and
        redacted from logs
      </label>
      <div className="actions">
        <button type="submit" disabled={busy || key === ""}>
          {busy ? "Saving…" : "Set"}
        </button>
      </div>
      {problems.message && <p className="error" role="alert">{problems.message}</p>}
    </form>
  );
}
