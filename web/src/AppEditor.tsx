import { useRef, useState, type FormEvent, type ReactNode } from "react";
import type { Client } from "./api/client.ts";
import type { App, Scope } from "./api/schema.ts";
import { createBody, updateBody, valuesOf, type AppValues } from "./appForm.ts";
import { canChange, explain, focusFirstInvalid } from "./forms.ts";
import { AppFrame } from "./AppFrame.tsx";
import { Link, navigate } from "./nav.tsx";
import { useApi } from "./useApi.ts";

type Fields = Record<string, string>;

/** NewApp creates an app (admin), then opens its page. */
export function NewApp({ client, scopes }: { client: Client; scopes: Scope[] }) {
  const [v, setV] = useState<AppValues>(() => valuesOf());
  const [fields, setFields] = useState<Fields>({});
  const [error, setError] = useState<string | undefined>();
  const [busy, setBusy] = useState(false);
  const form = useRef<HTMLFormElement>(null);

  async function submit(e: FormEvent) {
    e.preventDefault();
    const { body, fields: f } = createBody(v);
    setFields(f);
    setError(undefined);
    if (!body) {
      focusFirstInvalid(form.current);
      return;
    }
    setBusy(true);
    try {
      const app = await client.call("createApp", { body });
      navigate({ page: "app", slug: app.slug });
    } catch (err) {
      const x = explain(err);
      setFields(x.fields);
      setError(x.message || undefined);
      focusFirstInvalid(form.current);
    } finally {
      setBusy(false);
    }
  }

  if (!canChange(scopes)) {
    return <p>Creating an app needs a token with the admin scope.</p>;
  }
  return (
    <section>
      <p className="crumbs">
        <Link to={{ page: "apps" }}>Apps</Link> / new
      </p>
      <form className="panel wide" onSubmit={submit} ref={form} noValidate>
        <h1>New app</h1>
        <Field id="slug" label="Name" hint="a-z, 0-9 and -, starting with a letter; it names the app in URLs and the CLI" error={fields["slug"]}>
          <input id="slug" value={v.slug} onChange={(e) => setV({ ...v, slug: e.target.value })} autoComplete="off" spellCheck={false} placeholder="web" {...fieldProps("slug", fields["slug"], true)} />
        </Field>
        <Field id="repo" label="GitHub repository" hint="owner/name" error={fields["repo"]}>
          <input id="repo" value={v.repo} onChange={(e) => setV({ ...v, repo: e.target.value })} autoComplete="off" spellCheck={false} placeholder="acme/web" {...fieldProps("repo", fields["repo"], true)} />
        </Field>
        <SettingsFields v={v} setV={setV} fields={fields} />
        <div className="actions">
          <button type="submit" disabled={busy}>
            {busy ? "Creating…" : "Create app"}
          </button>
        </div>
        {error && <p className="error" role="alert">{error}</p>}
      </form>
    </section>
  );
}

/** AppSettingsPage changes an app's settings (admin), and deletes it at the bottom. */
export function AppSettingsPage({ client, slug, scopes }: { client: Client; slug: string; scopes: Scope[] }) {
  const app = useApi(() => client.call("getApp", { path: { app: slug } }), [client, slug]);
  return (
    <AppFrame slug={slug} section="settings" scopes={scopes}>
      {app.error && <p className="error" role="alert">{app.error === "Not found." ? `There is no app named ${slug}.` : app.error}</p>}
      {app.data && !canChange(scopes) && <p>Changing an app needs a token with the admin scope.</p>}
      {app.data && canChange(scopes) && (
        <>
          {/* Not keyed by updated_at: a remount after saving would drop its "Saved" message. */}
          <EditForm client={client} app={app.data} onSaved={app.reload} />
          <DangerZone client={client} app={app.data} />
        </>
      )}
    </AppFrame>
  );
}

function EditForm({ client, app, onSaved }: { client: Client; app: App; onSaved: () => void }) {
  const [v, setV] = useState<AppValues>(() => valuesOf(app));
  const [fields, setFields] = useState<Fields>({});
  const [message, setMessage] = useState<{ ok: boolean; text: string } | undefined>();
  const [busy, setBusy] = useState(false);
  const form = useRef<HTMLFormElement>(null);

  async function submit(e: FormEvent) {
    e.preventDefault();
    const { body, fields: f } = updateBody(v, app);
    setFields(f);
    setMessage(undefined);
    if (!body) {
      focusFirstInvalid(form.current);
      return;
    }
    if (Object.keys(body).length === 0) {
      setMessage({ ok: true, text: "Nothing changed." });
      return;
    }
    setBusy(true);
    try {
      await client.call("updateApp", { path: { app: app.slug }, body });
      setMessage({ ok: true, text: "Saved. The next deploy uses the new settings." });
      onSaved();
    } catch (err) {
      const x = explain(err);
      setFields(x.fields);
      setMessage(x.message ? { ok: false, text: x.message } : undefined);
      focusFirstInvalid(form.current);
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="panel wide" onSubmit={submit} ref={form} noValidate aria-labelledby="edit-title">
      <h2 id="edit-title">Deploy settings</h2>
      <p className="hint">
        Repository <code>{app.repo}</code>; to use another, create a new app.
      </p>
      <SettingsFields v={v} setV={setV} fields={fields} />
      <div className="actions">
        <button type="submit" disabled={busy}>
          {busy ? "Saving…" : "Save"}
        </button>
      </div>
      {message && (
        <p className={message.ok ? "notice" : "error"} role={message.ok ? "status" : "alert"}>
          {message.text}
        </p>
      )}
    </form>
  );
}

/** DangerZone deletes the app, once its name is typed, and follows the delete operation. */
function DangerZone({ client, app }: { client: Client; app: App }) {
  const [typed, setTyped] = useState("");
  const [key] = useState(() => crypto.randomUUID());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | undefined>();

  async function remove() {
    setBusy(true);
    setError(undefined);
    try {
      const res = await client.call("deleteApp", { path: { app: app.slug }, header: { "Idempotency-Key": key } });
      navigate({ page: "operation", id: res.operation.id });
    } catch (err) {
      setError(explain(err).message);
      setBusy(false);
    }
  }

  return (
    <div className="panel wide danger">
      <h2>Delete this app</h2>
      <p>
        This takes <strong>{app.slug}</strong> offline and removes its containers, network, images, domains, configuration and
        history. It cannot be undone.
      </p>
      <label htmlFor="confirm-name">
        Type <code>{app.slug}</code> to confirm
      </label>
      <input id="confirm-name" value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" spellCheck={false} />
      <div className="actions">
        <button type="button" className="destructive" disabled={busy || typed !== app.slug} onClick={() => void remove()}>
          {busy ? "Deleting…" : `Delete ${app.slug}`}
        </button>
      </div>
      {error && <p className="error" role="alert">{error}</p>}
    </div>
  );
}

function SettingsFields({ v, setV, fields }: { v: AppValues; setV: (v: AppValues) => void; fields: Fields }) {
  const hinted = new Set(["branch", "port", "health_path", "github_installation_id"]);
  const text = (k: Exclude<keyof AppValues, "auto_deploy">, extra: { placeholder?: string; inputMode?: "numeric" | "decimal" } = {}) => (
    <input
      id={k}
      value={v[k]}
      onChange={(e) => setV({ ...v, [k]: e.target.value })}
      autoComplete="off"
      spellCheck={false}
      {...extra}
      // The API names memory in bytes; the form, in MiB.
      {...fieldProps(k, fields[k === "memory_mib" ? "memory_limit" : k], hinted.has(k))}
    />
  );
  // An error inside the folded section opens it, so it is never hidden.
  const advanced = ["dockerfile_path", "build_context", "cpu_limit", "memory_limit", "health_timeout", "stop_timeout", "github_installation_id"];
  const open = advanced.some((k) => fields[k]);
  return (
    <>
      <Field id="branch" label="Branch" hint="deploys come from this branch" error={fields["branch"]}>
        {text("branch")}
      </Field>
      <Field id="port" label="Port" hint="the port the app listens on in its container" error={fields["port"]}>
        {text("port", { inputMode: "numeric", placeholder: "8080" })}
      </Field>
      <Field id="health_path" label="Health check path" hint="must answer 2xx before the release takes traffic" error={fields["health_path"]}>
        {text("health_path")}
      </Field>
      <label className="check">
        <input type="checkbox" checked={v.auto_deploy} onChange={(e) => setV({ ...v, auto_deploy: e.target.checked })} /> Deploy on
        every push to the branch
      </label>
      <details open={open || undefined}>
        <summary>Build, limits and timeouts</summary>
        <div className="grid2">
          <Field id="dockerfile_path" label="Dockerfile" error={fields["dockerfile_path"]}>
            {text("dockerfile_path")}
          </Field>
          <Field id="build_context" label="Build context" error={fields["build_context"]}>
            {text("build_context")}
          </Field>
          <Field id="cpu_limit" label="CPUs" error={fields["cpu_limit"]}>
            {text("cpu_limit", { inputMode: "decimal" })}
          </Field>
          <Field id="memory_mib" label="Memory (MiB)" error={fields["memory_limit"]}>
            {text("memory_mib", { inputMode: "numeric" })}
          </Field>
          <Field id="health_timeout" label="Health timeout" error={fields["health_timeout"]}>
            {text("health_timeout")}
          </Field>
          <Field id="stop_timeout" label="Stop timeout" error={fields["stop_timeout"]}>
            {text("stop_timeout")}
          </Field>
          <Field id="github_installation_id" label="GitHub App installation" hint="for a private repository" error={fields["github_installation_id"]}>
            {text("github_installation_id", { inputMode: "numeric" })}
          </Field>
        </div>
      </details>
    </>
  );
}

// Field labels an input and ties its hint or error to it: the input gets
// aria-describedby `${id}-help` and aria-invalid from fieldProps.
function Field({ id, label, hint, error, children }: { id: string; label: string; hint?: string; error?: string | undefined; children: ReactNode }) {
  return (
    <div className={error ? "field invalid" : "field"}>
      <label htmlFor={id}>{label}</label>
      {children}
      {error ? (
        <p id={`${id}-help`} className="error">
          {error}
        </p>
      ) : (
        hint && (
          <p id={`${id}-help`} className="hint">
            {hint}
          </p>
        )
      )}
    </div>
  );
}

/** fieldProps are an input's accessibility attributes, matching Field's. */
function fieldProps(id: string, error: string | undefined, hasHint: boolean) {
  return {
    "aria-invalid": error ? true : undefined,
    "aria-describedby": error || hasHint ? `${id}-help` : undefined,
  };
}
