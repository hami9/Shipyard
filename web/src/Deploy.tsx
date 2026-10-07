import { useState, type FormEvent } from "react";
import type { Client } from "./api/client.ts";
import { explain } from "./forms.ts";
import { navigate } from "./nav.tsx";

const shaRE = /^([0-9a-f]{40}|[0-9a-f]{64})$/;

/**
 * Deploy queues a deploy of the branch head, or of a commit on the branch
 * (invariant 7: the worker checks it), and opens the operation to follow it.
 * One Idempotency-Key per opened panel, so a double click queues it once.
 */
export function Deploy({ client, slug, branch }: { client: Client; slug: string; branch: string }) {
  const [open, setOpen] = useState(false);
  const [ref, setRef] = useState("");
  const [key, setKey] = useState(() => crypto.randomUUID());
  const [error, setError] = useState<string | undefined>();
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    const sha = ref.trim().toLowerCase();
    if (sha !== "" && !shaRE.test(sha)) {
      setError("A full commit SHA: 40 (or 64) hex characters. Leave it empty for the branch head.");
      return;
    }
    setBusy(true);
    setError(undefined);
    try {
      const res = await client.call("deploy", {
        path: { app: slug },
        header: { "Idempotency-Key": `${key}-${sha || "head"}` },
        ...(sha ? { body: { ref: sha } } : {}),
      });
      navigate({ page: "operation", id: res.operation.id });
    } catch (err) {
      setError(explain(err).fields["ref"] ?? explain(err).message);
      setBusy(false);
    }
  }

  if (!open) {
    return (
      <button
        type="button"
        onClick={() => {
          setKey(crypto.randomUUID());
          setOpen(true);
        }}
      >
        Deploy
      </button>
    );
  }
  return (
    <form className="confirm" onSubmit={submit}>
      <h3>Deploy {slug}</h3>
      <label htmlFor="deploy-ref">
        Commit <span className="hint">(empty: the head of {branch})</span>
      </label>
      <input id="deploy-ref" value={ref} onChange={(e) => setRef(e.target.value)} autoComplete="off" spellCheck={false} placeholder="full SHA, optional" />
      <div className="actions">
        <button type="submit" disabled={busy}>
          {busy ? "Queuing…" : "Deploy"}
        </button>
        <button type="button" onClick={() => setOpen(false)} disabled={busy}>
          Cancel
        </button>
      </div>
      {error && <p className="error" role="alert">{error}</p>}
    </form>
  );
}
